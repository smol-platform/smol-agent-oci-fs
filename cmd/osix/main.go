package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/zctaylor/agent-oci-fs/internal/osix"
)

const usage = `osix is a local prototype for OCI Agent State Images.

Usage:
  osix init BASE --name NAME --state REF --mount DIR
  osix snapshot DIR [--message MSG] [--tag TAG] [--also-tag TAG]
  osix restore REF DIR [--force]
  osix mount REF DIR [--force]
  osix diff REF_A REF_B
  osix fork SOURCE_REF TARGET_TAG
  osix show REF
  osix refs

Refs are local tags in .osix/refs or immutable sha256:... manifest digests.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "osix: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		fmt.Print(usage)
		return nil
	}

	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "snapshot":
		return runSnapshot(args[1:])
	case "restore":
		return runRestore(args[1:], false)
	case "mount":
		return runRestore(args[1:], true)
	case "diff":
		return runDiff(args[1:])
	case "fork":
		return runFork(args[1:])
	case "show":
		return runShow(args[1:])
	case "refs":
		return runRefs(args[1:])
	default:
		return fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
}

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	name := fs.String("name", "", "agent name")
	state := fs.String("state", "local/osix-state", "state image reference")
	mount := fs.String("mount", "./agentfs", "local writable mount directory")
	branch := fs.String("branch", "main", "default branch")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, nil); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix init BASE --name NAME --state REF --mount DIR")
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	cfg, err := osix.Init(".", osix.InitOptions{
		Base:          fs.Arg(0),
		Name:          *name,
		StateRef:      *state,
		Mount:         *mount,
		DefaultBranch: *branch,
	})
	if err != nil {
		return err
	}
	fmt.Printf("initialized OSIx workspace\n")
	fmt.Printf("  agent:  %s\n", cfg.Name)
	fmt.Printf("  base:   %s\n", cfg.Base)
	fmt.Printf("  state:  %s\n", cfg.StateRef)
	fmt.Printf("  mount:  %s\n", cfg.Mount)
	return nil
}

func runSnapshot(args []string) error {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	msg := fs.String("message", "", "snapshot message")
	tag := fs.String("tag", "", "tag to write")
	alsoTag := fs.String("also-tag", "", "additional tag to write")
	push := fs.Bool("push", false, "accepted for compatibility; local prototype does not push")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"push": true}); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("usage: osix snapshot DIR [--message MSG] [--tag TAG] [--also-tag TAG]")
	}
	if *push {
		fmt.Fprintln(os.Stderr, "osix: --push requested; local prototype stored snapshot in .osix only")
	}
	result, err := osix.Snapshot(".", fs.Arg(0), osix.SnapshotOptions{
		Message: *msg,
		Tag:     *tag,
		AlsoTag: *alsoTag,
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", result.ManifestDigest)
	for _, tag := range result.Tags {
		fmt.Printf("tagged %s -> %s\n", tag, result.ManifestDigest)
	}
	return nil
}

func runRestore(args []string, mountAlias bool) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	force := fs.Bool("force", false, "overwrite an existing non-empty target")
	fs.SetOutput(os.Stderr)
	if err := parseInterspersed(fs, args, map[string]bool{"force": true}); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		if mountAlias {
			return fmt.Errorf("usage: osix mount REF DIR [--force]")
		}
		return fmt.Errorf("usage: osix restore REF DIR [--force]")
	}
	if err := osix.Restore(".", fs.Arg(0), fs.Arg(1), osix.RestoreOptions{Force: *force}); err != nil {
		return err
	}
	if mountAlias {
		fmt.Printf("mounted local snapshot copy %s at %s\n", fs.Arg(0), fs.Arg(1))
	} else {
		fmt.Printf("restored %s to %s\n", fs.Arg(0), fs.Arg(1))
	}
	return nil
}

func runDiff(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: osix diff REF_A REF_B")
	}
	changes, err := osix.Diff(".", args[0], args[1])
	if err != nil {
		return err
	}
	for _, change := range changes {
		fmt.Printf("%s %s\n", change.Kind, change.Path)
	}
	return nil
}

func runFork(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: osix fork SOURCE_REF TARGET_TAG")
	}
	digest, err := osix.Fork(".", args[0], args[1])
	if err != nil {
		return err
	}
	fmt.Printf("tagged %s -> %s\n", args[1], digest)
	return nil
}

func runShow(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: osix show REF")
	}
	text, err := osix.Show(".", args[0])
	if err != nil {
		return err
	}
	fmt.Print(text)
	return nil
}

func runRefs(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("usage: osix refs")
	}
	refs, err := osix.Refs(".")
	if err != nil {
		return err
	}
	for _, ref := range refs {
		fmt.Printf("%s %s\n", ref.Name, ref.Digest)
	}
	return nil
}

func parseInterspersed(fs *flag.FlagSet, args []string, boolFlags map[string]bool) error {
	var flags []string
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			flags = append(flags, arg)
			name := strings.TrimPrefix(arg, "--")
			if before, _, ok := strings.Cut(name, "="); ok {
				name = before
			}
			if !boolFlags[name] && !strings.Contains(arg, "=") {
				if i+1 >= len(args) {
					return fmt.Errorf("flag %s requires a value", arg)
				}
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, arg)
	}
	return fs.Parse(append(flags, positional...))
}
