package osix

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type PolicyOptions struct {
	SecretScan string
}

type SideEffectEntry struct {
	Turn               int64  `json:"turn"`
	Tool               string `json:"tool"`
	IdempotencyKey     string `json:"idempotencyKey"`
	RequestDigest      string `json:"requestDigest"`
	ResponseDigest     string `json:"responseDigest"`
	ExternalResource   string `json:"externalResource"`
	ReplayPolicy       string `json:"replayPolicy"`
	CompensatingAction string `json:"compensatingAction,omitempty"`
}

type ReplayMarker struct {
	Mode      string    `json:"mode"`
	CreatedAt time.Time `json:"createdAt"`
}

func ValidateAgentState(root string, opts PolicyOptions) error {
	if opts.SecretScan == "" {
		opts.SecretScan = "warn"
	}
	if err := validateSideEffectLedger(root); err != nil {
		return err
	}
	findings, err := scanSecrets(root)
	if err != nil {
		return err
	}
	if len(findings) > 0 && opts.SecretScan == "block" {
		return fmt.Errorf("secret scan blocked snapshot: %s", strings.Join(findings, "; "))
	}
	return nil
}

func validateSideEffectLedger(root string) error {
	path := filepath.Join(root, "agent", "side-effects", "ledger.jsonl")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry SideEffectEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return fmt.Errorf("side-effect ledger line %d: %w", lineNo, err)
		}
		if entry.Turn == 0 || entry.Tool == "" || entry.IdempotencyKey == "" || entry.RequestDigest == "" || entry.ResponseDigest == "" || entry.ExternalResource == "" || entry.ReplayPolicy == "" {
			return fmt.Errorf("side-effect ledger line %d missing required fields", lineNo)
		}
		if !validReplayPolicy(entry.ReplayPolicy) {
			return fmt.Errorf("side-effect ledger line %d has invalid replayPolicy %q", lineNo, entry.ReplayPolicy)
		}
	}
	return scanner.Err()
}

func validReplayPolicy(policy string) bool {
	switch policy {
	case "mock-by-default", "read-only-by-default", "require-approval", "never-replay", "idempotent-retry-allowed":
		return true
	default:
		return false
	}
}

func scanSecrets(root string) ([]string, error) {
	var findings []string
	secretPattern := regexp.MustCompile(`(?i)(api[_-]?key|secret|token|password)\s*[:=]\s*['"]?[^'"\s]+`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".env" || strings.HasSuffix(rel, "/.env") || strings.HasSuffix(rel, "/id_rsa") || strings.HasSuffix(rel, "/id_ed25519") || strings.HasPrefix(rel, "agent/secrets/") || rel == "agent/secrets" {
			findings = append(findings, rel)
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || shouldExclude(rel) {
			if d.IsDir() && shouldExclude(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if secretPattern.Match(data) {
			findings = append(findings, rel)
		}
		return nil
	})
	return findings, err
}

func writeReplayMarker(target string) error {
	marker := ReplayMarker{Mode: "require-approval", CreatedAt: time.Now().UTC().Truncate(time.Second)}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(target, ".osix-replay-policy.json")
	return os.WriteFile(path, data, 0o644)
}

func shouldRedact(path string) bool {
	path = filepath.ToSlash(path)
	return strings.HasPrefix(path, "agent/logs/") && (strings.HasSuffix(path, ".jsonl") || strings.HasSuffix(path, ".log") || strings.HasSuffix(path, ".json"))
}

func redactLog(data []byte) []byte {
	replacements := []*regexp.Regexp{
		regexp.MustCompile(`(?i)("?(api[_-]?key|secret|token|password)"?\s*[:=]\s*")([^"]+)(")`),
		regexp.MustCompile(`(?i)((api[_-]?key|secret|token|password)\s*[:=]\s*)([^\s,}]+)`),
	}
	out := data
	out = replacements[0].ReplaceAll(out, []byte(`${1}[REDACTED]${4}`))
	out = replacements[1].ReplaceAll(out, []byte(`${1}[REDACTED]`))
	return out
}
