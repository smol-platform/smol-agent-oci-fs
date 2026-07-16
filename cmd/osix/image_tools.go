package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/smol-platform/smol-agent-oci-fs/internal/osix"
	"golang.org/x/term"
)

const browsePreviewLimit = 64 * 1024

func runExtract(args []string) error {
	fs := flag.NewFlagSet("extract", flag.ContinueOnError)
	force := fs.Bool("force", false, "replace an existing destination")
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"force": true}); err != nil {
		return err
	}
	if fs.NArg() != 3 {
		return fmt.Errorf("usage: osix extract REF PATH DEST [--force] [--decrypt IDENTITIES]")
	}
	ref, err := localImageRef(fs.Arg(0), true)
	if err != nil {
		return err
	}
	result, err := osix.ExtractSnapshotPath(".", ref, fs.Arg(1), fs.Arg(2), osix.ExtractOptions{
		Force:   *force,
		Decrypt: *decrypt,
	})
	if err != nil {
		return err
	}
	fmt.Printf("extracted %s:%s to %s (%d files, %d bytes)\n",
		result.SourceDigest, displaySnapshotPath(result.SourcePath), result.Destination, result.Files, result.Bytes)
	return nil
}

func runBrowse(args []string) error {
	fs := flag.NewFlagSet("browse", flag.ContinueOnError)
	plain := fs.Bool("plain", false, "print one directory listing and exit")
	jsonOutput := fs.Bool("json", false, "print one directory listing as JSON and exit")
	decrypt := fs.String("decrypt", "", "decrypt identities or KMS recipients, comma-separated")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"plain": true, "json": true}); err != nil {
		return err
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		return fmt.Errorf("usage: osix browse REF [PATH] [--plain|--json] [--decrypt IDENTITIES]")
	}
	if *plain && *jsonOutput {
		return fmt.Errorf("--plain and --json are mutually exclusive")
	}
	ref, err := localImageRef(fs.Arg(0), true)
	if err != nil {
		return err
	}
	dir := ""
	if fs.NArg() == 2 {
		dir = fs.Arg(1)
	}
	if *plain || *jsonOutput || !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		_, entries, err := osix.ListSnapshotDirectory(".", ref, dir)
		if err != nil {
			return err
		}
		if *jsonOutput {
			encoder := json.NewEncoder(os.Stdout)
			encoder.SetIndent("", "  ")
			return encoder.Encode(entries)
		}
		printSnapshotEntries(entries)
		return nil
	}
	return browseSnapshotInteractive(ref, dir, *decrypt)
}

func localImageRef(ref string, lazy bool) (string, error) {
	if !osix.IsRegistryReference(ref) {
		return ref, nil
	}
	return osix.PullSnapshotWithOptions(".", ref, "", osix.PullOptions{Lazy: lazy})
}

func printSnapshotEntries(entries []osix.TreeEntry) {
	for _, entry := range entries {
		name := entry.Path
		size := "-"
		switch entry.Type {
		case "dir":
			name += "/"
		case "file":
			size = humanBytes(entry.Size)
		case "symlink":
			name += " -> " + entry.Linkname
		}
		fmt.Printf("%-7s %8s  %s\n", entry.Type, size, name)
	}
}

func browseSnapshotInteractive(ref, startDir, decrypt string) error {
	if _, _, err := osix.ListSnapshotDirectory(".", ref, startDir); err != nil {
		return err
	}
	state, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	defer term.Restore(int(os.Stdin.Fd()), state)
	defer fmt.Fprint(os.Stdout, "\x1b[?25h\r\n")
	fmt.Fprint(os.Stdout, "\x1b[?25l")

	dir := cleanBrowsePath(startDir)
	selected := 0
	status := ""
	for {
		_, entries, err := osix.ListSnapshotDirectory(".", ref, dir)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			selected = 0
		} else if selected >= len(entries) {
			selected = len(entries) - 1
		}
		renderSnapshotBrowser(ref, dir, entries, selected, status)
		status = ""
		key, err := readBrowseKey()
		if err != nil {
			return err
		}
		switch key {
		case "q", "ctrl-c":
			return nil
		case "j", "down":
			if selected+1 < len(entries) {
				selected++
			}
		case "k", "up":
			if selected > 0 {
				selected--
			}
		case "g":
			selected = 0
		case "G":
			if len(entries) > 0 {
				selected = len(entries) - 1
			}
		case "h", "left", "backspace":
			if dir != "" {
				dir = browseParent(dir)
				selected = 0
			}
		case "enter", "l", "right", "p":
			if len(entries) == 0 {
				continue
			}
			entry := entries[selected]
			if entry.Type == "dir" && key != "p" {
				dir = entry.Path
				selected = 0
				continue
			}
			if err := previewSnapshotEntry(ref, entry, decrypt); err != nil {
				status = err.Error()
			}
		case "e":
			if len(entries) == 0 {
				continue
			}
			entry := entries[selected]
			destination := filepath.Base(filepath.FromSlash(entry.Path))
			result, err := osix.ExtractSnapshotPath(".", ref, entry.Path, destination, osix.ExtractOptions{Decrypt: decrypt})
			if err != nil {
				status = err.Error()
			} else {
				status = fmt.Sprintf("extracted to %s (%d files)", result.Destination, result.Files)
			}
		case "?":
			renderBrowseHelp()
			if _, err := readBrowseKey(); err != nil {
				return err
			}
		}
	}
}

func renderSnapshotBrowser(ref, dir string, entries []osix.TreeEntry, selected int, status string) {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width < 40 {
		width = 80
	}
	if height < 10 {
		height = 24
	}
	visible := height - 6
	start := 0
	if selected >= visible {
		start = selected - visible + 1
	}
	end := start + visible
	if end > len(entries) {
		end = len(entries)
	}
	fmt.Fprint(os.Stdout, "\x1b[2J\x1b[H")
	fmt.Fprintf(os.Stdout, "OSIx  %s  %s\r\n", compactDigest(ref), displaySnapshotPath(cleanBrowsePath(dir)))
	fmt.Fprint(os.Stdout, "j/k move  enter/l open  h back  p preview  e extract  ? help  q quit\r\n\r\n")
	for i := start; i < end; i++ {
		entry := entries[i]
		cursor := "  "
		if i == selected {
			cursor = "> "
		}
		name := path.Base(entry.Path)
		kind := entry.Type
		if entry.Type == "dir" {
			name += "/"
		} else if entry.Type == "symlink" {
			name += " -> " + entry.Linkname
		}
		line := fmt.Sprintf("%s%-7s %8s  %s", cursor, kind, browseEntrySize(entry), name)
		fmt.Fprintf(os.Stdout, "%s\r\n", truncateDisplay(line, width))
	}
	for i := end - start; i < visible; i++ {
		fmt.Fprint(os.Stdout, "\r\n")
	}
	if status != "" {
		fmt.Fprintf(os.Stdout, "%s\r\n", truncateDisplay(status, width))
	} else {
		fmt.Fprintf(os.Stdout, "%d entries\r\n", len(entries))
	}
}

func previewSnapshotEntry(ref string, entry osix.TreeEntry, decrypt string) error {
	fmt.Fprint(os.Stdout, "\x1b[2J\x1b[H")
	fmt.Fprintf(os.Stdout, "%s  %s  %s\r\n\r\n", entry.Type, humanBytes(entry.Size), entry.Path)
	if entry.Type == "dir" {
		fmt.Fprint(os.Stdout, "directory\r\n")
	} else if entry.Type == "symlink" {
		fmt.Fprintf(os.Stdout, "-> %s\r\n", entry.Linkname)
	} else {
		data, err := osix.ReadSnapshotFileRange(".", ref, entry.Path, 0, browsePreviewLimit, osix.ReadFileOptions{Decrypt: decrypt})
		if err != nil {
			return err
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
			fmt.Fprint(os.Stdout, strings.ReplaceAll(hex.Dump(data), "\n", "\r\n"))
		} else {
			fmt.Fprint(os.Stdout, strings.ReplaceAll(string(data), "\n", "\r\n"))
		}
		if entry.Size > browsePreviewLimit {
			fmt.Fprintf(os.Stdout, "\r\n… preview limited to %s\r\n", humanBytes(browsePreviewLimit))
		}
	}
	fmt.Fprint(os.Stdout, "\r\npress any key to return")
	_, err := readBrowseKey()
	return err
}

func renderBrowseHelp() {
	fmt.Fprint(os.Stdout, "\x1b[2J\x1b[HOSIx image browser\r\n\r\n")
	fmt.Fprint(os.Stdout, "  j/k or arrows   move selection\r\n")
	fmt.Fprint(os.Stdout, "  enter/l/right   open directory or preview file\r\n")
	fmt.Fprint(os.Stdout, "  h/left          parent directory\r\n")
	fmt.Fprint(os.Stdout, "  p               preview selected entry\r\n")
	fmt.Fprint(os.Stdout, "  e               extract selection into the current directory\r\n")
	fmt.Fprint(os.Stdout, "  g/G             first/last entry\r\n")
	fmt.Fprint(os.Stdout, "  q               quit\r\n\r\npress any key to return")
}

func readBrowseKey() (string, error) {
	var b [1]byte
	if _, err := os.Stdin.Read(b[:]); err != nil {
		return "", err
	}
	switch b[0] {
	case 3:
		return "ctrl-c", nil
	case 13, 10:
		return "enter", nil
	case 8, 127:
		return "backspace", nil
	case 27:
		var sequence [2]byte
		if _, err := os.Stdin.Read(sequence[:1]); err != nil {
			return "escape", nil
		}
		if sequence[0] != '[' {
			return "escape", nil
		}
		if _, err := os.Stdin.Read(sequence[1:]); err != nil {
			return "escape", nil
		}
		switch sequence[1] {
		case 'A':
			return "up", nil
		case 'B':
			return "down", nil
		case 'C':
			return "right", nil
		case 'D':
			return "left", nil
		}
		return "escape", nil
	default:
		return string(b[:]), nil
	}
}

func cleanBrowsePath(value string) string {
	value = strings.TrimPrefix(filepath.ToSlash(strings.TrimSpace(value)), "/")
	if value == "." || value == "/" {
		return ""
	}
	return value
}

func browseParent(dir string) string {
	parent := path.Dir(dir)
	if parent == "." || parent == "/" {
		return ""
	}
	return parent
}

func browseEntrySize(entry osix.TreeEntry) string {
	if entry.Type != "file" {
		return "-"
	}
	return humanBytes(entry.Size)
}

func displaySnapshotPath(value string) string {
	if value == "" {
		return "/"
	}
	return "/" + value
}

func humanBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	size := float64(value)
	for _, unit := range units {
		size /= 1024
		if size < 1024 || unit == units[len(units)-1] {
			return fmt.Sprintf("%.1f %s", size, unit)
		}
	}
	return fmt.Sprintf("%d B", value)
}

func compactDigest(value string) string {
	if strings.HasPrefix(value, "sha256:") && len(value) > 19 {
		return value[:19] + "…"
	}
	return value
}

func truncateDisplay(value string, width int) string {
	if width <= 1 || len([]rune(value)) <= width {
		return value
	}
	runes := []rune(value)
	return string(runes[:width-1]) + "…"
}
