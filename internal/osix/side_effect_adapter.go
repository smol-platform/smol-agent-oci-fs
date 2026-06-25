package osix

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	SideEffectOperationRead  = "read"
	SideEffectOperationWrite = "write"

	SideEffectActionAllow           = "allow"
	SideEffectActionMock            = "mock"
	SideEffectActionReadOnly        = "read-only"
	SideEffectActionRequireApproval = "require-approval"
	SideEffectActionDeny            = "deny"
)

type SideEffectCheck struct {
	Tool             string `json:"tool"`
	ExternalResource string `json:"externalResource"`
	Operation        string `json:"operation"`
	IdempotencyKey   string `json:"idempotencyKey,omitempty"`
}

type SideEffectDecision struct {
	Action       string           `json:"action"`
	ReplayMode   string           `json:"replayMode"`
	ReplayPolicy string           `json:"replayPolicy,omitempty"`
	MatchedEntry *SideEffectEntry `json:"matchedEntry,omitempty"`
	Reason       string           `json:"reason"`
}

func CheckSideEffect(target string, check SideEffectCheck) (SideEffectDecision, error) {
	check.Operation = strings.ToLower(strings.TrimSpace(check.Operation))
	if check.Operation == "" {
		check.Operation = SideEffectOperationWrite
	}
	if check.Operation != SideEffectOperationRead && check.Operation != SideEffectOperationWrite {
		return SideEffectDecision{}, fmt.Errorf("invalid side-effect operation %q", check.Operation)
	}
	if strings.TrimSpace(check.Tool) == "" {
		return SideEffectDecision{}, fmt.Errorf("side-effect tool is required")
	}
	if strings.TrimSpace(check.ExternalResource) == "" {
		return SideEffectDecision{}, fmt.Errorf("side-effect external resource is required")
	}
	marker, err := readReplayMarker(target)
	if err != nil {
		return SideEffectDecision{}, err
	}
	entries, err := readSideEffectLedger(target)
	if err != nil {
		return SideEffectDecision{}, err
	}
	entry := matchSideEffectEntry(entries, check)
	mode := "normal"
	if marker != nil && marker.Mode != "" {
		mode = marker.Mode
	}
	if mode == "normal" {
		return SideEffectDecision{
			Action:       SideEffectActionAllow,
			ReplayMode:   mode,
			ReplayPolicy: replayPolicy(entry),
			MatchedEntry: entry,
			Reason:       "runtime is not marked as restored or forked",
		}, nil
	}
	if check.Operation == SideEffectOperationRead {
		return SideEffectDecision{
			Action:       SideEffectActionAllow,
			ReplayMode:   mode,
			ReplayPolicy: replayPolicy(entry),
			MatchedEntry: entry,
			Reason:       "read-only external operation is allowed",
		}, nil
	}
	if entry == nil {
		return SideEffectDecision{
			Action:     SideEffectActionRequireApproval,
			ReplayMode: mode,
			Reason:     "restored runtime has no matching side-effect ledger entry",
		}, nil
	}
	switch entry.ReplayPolicy {
	case "mock-by-default":
		return sideEffectDecision(SideEffectActionMock, mode, entry, "ledger policy requires mock behavior by default"), nil
	case "read-only-by-default":
		return sideEffectDecision(SideEffectActionReadOnly, mode, entry, "ledger policy allows reads but blocks writes by default"), nil
	case "require-approval":
		return sideEffectDecision(SideEffectActionRequireApproval, mode, entry, "ledger policy requires approval before replay"), nil
	case "never-replay":
		return sideEffectDecision(SideEffectActionDeny, mode, entry, "ledger policy forbids replay"), nil
	case "idempotent-retry-allowed":
		if check.IdempotencyKey != "" && check.IdempotencyKey == entry.IdempotencyKey {
			return sideEffectDecision(SideEffectActionAllow, mode, entry, "matching idempotency key allows retry"), nil
		}
		return sideEffectDecision(SideEffectActionRequireApproval, mode, entry, "idempotent retry requires the original idempotency key"), nil
	default:
		return SideEffectDecision{}, fmt.Errorf("invalid side-effect replay policy %q", entry.ReplayPolicy)
	}
}

func sideEffectDecision(action, mode string, entry *SideEffectEntry, reason string) SideEffectDecision {
	return SideEffectDecision{
		Action:       action,
		ReplayMode:   mode,
		ReplayPolicy: replayPolicy(entry),
		MatchedEntry: entry,
		Reason:       reason,
	}
}

func replayPolicy(entry *SideEffectEntry) string {
	if entry == nil {
		return ""
	}
	return entry.ReplayPolicy
}

func readReplayMarker(target string) (*ReplayMarker, error) {
	data, err := os.ReadFile(filepath.Join(target, ".osix-replay-policy.json"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marker ReplayMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, fmt.Errorf("parse replay marker: %w", err)
	}
	return &marker, nil
}

func readSideEffectLedger(root string) ([]SideEffectEntry, error) {
	path := filepath.Join(root, "agent", "side-effects", "ledger.jsonl")
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var entries []SideEffectEntry
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
			return nil, fmt.Errorf("side-effect ledger line %d: %w", lineNo, err)
		}
		if entry.Turn == 0 || entry.Tool == "" || entry.IdempotencyKey == "" || entry.RequestDigest == "" || entry.ResponseDigest == "" || entry.ExternalResource == "" || entry.ReplayPolicy == "" {
			return nil, fmt.Errorf("side-effect ledger line %d missing required fields", lineNo)
		}
		if !validReplayPolicy(entry.ReplayPolicy) {
			return nil, fmt.Errorf("side-effect ledger line %d has invalid replayPolicy %q", lineNo, entry.ReplayPolicy)
		}
		entries = append(entries, entry)
	}
	return entries, scanner.Err()
}

func matchSideEffectEntry(entries []SideEffectEntry, check SideEffectCheck) *SideEffectEntry {
	for i := range entries {
		entry := &entries[i]
		if entry.Tool != check.Tool || entry.ExternalResource != check.ExternalResource {
			continue
		}
		if check.IdempotencyKey != "" && entry.IdempotencyKey != check.IdempotencyKey {
			continue
		}
		return entry
	}
	return nil
}
