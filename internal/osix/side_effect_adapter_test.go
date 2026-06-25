package osix

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckSideEffectUsesReplayPolicies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"github.create_issue","idempotencyKey":"create-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"github:acme/repo/issues/1","replayPolicy":"mock-by-default"}`+"\n"+
			`{"turn":2,"tool":"gmail.send","idempotencyKey":"mail-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"gmail:message/1","replayPolicy":"never-replay"}`+"\n"+
			`{"turn":3,"tool":"stripe.refund","idempotencyKey":"refund-1","requestDigest":"sha256:req3","responseDigest":"sha256:resp3","externalResource":"stripe:refund/re_1","replayPolicy":"idempotent-retry-allowed"}`+"\n")

	mock, err := CheckSideEffect(root, SideEffectCheck{Tool: "github.create_issue", ExternalResource: "github:acme/repo/issues/1", Operation: "write"})
	if err != nil {
		t.Fatal(err)
	}
	if mock.Action != SideEffectActionMock || mock.ReplayPolicy != "mock-by-default" {
		t.Fatalf("unexpected mock decision: %#v", mock)
	}
	deny, err := CheckSideEffect(root, SideEffectCheck{Tool: "gmail.send", ExternalResource: "gmail:message/1", Operation: "write"})
	if err != nil {
		t.Fatal(err)
	}
	if deny.Action != SideEffectActionDeny || deny.ReplayPolicy != "never-replay" {
		t.Fatalf("unexpected deny decision: %#v", deny)
	}
	retry, err := CheckSideEffect(root, SideEffectCheck{Tool: "stripe.refund", ExternalResource: "stripe:refund/re_1", Operation: "write", IdempotencyKey: "refund-1"})
	if err != nil {
		t.Fatal(err)
	}
	if retry.Action != SideEffectActionAllow || retry.ReplayPolicy != "idempotent-retry-allowed" {
		t.Fatalf("unexpected retry decision: %#v", retry)
	}
	approval, err := CheckSideEffect(root, SideEffectCheck{Tool: "stripe.refund", ExternalResource: "stripe:refund/re_1", Operation: "write", IdempotencyKey: "wrong"})
	if err != nil {
		t.Fatal(err)
	}
	if approval.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected approval decision: %#v", approval)
	}
	read, err := CheckSideEffect(root, SideEffectCheck{Tool: "gmail.send", ExternalResource: "gmail:message/1", Operation: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if read.Action != SideEffectActionAllow {
		t.Fatalf("unexpected read decision: %#v", read)
	}
}

func TestCheckSideEffectAllowsNormalRuntime(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"), `{"turn":1,"tool":"github.create_issue","idempotencyKey":"create-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"github:acme/repo/issues/1","replayPolicy":"never-replay"}`+"\n")
	decision, err := CheckSideEffect(root, SideEffectCheck{Tool: "github.create_issue", ExternalResource: "github:acme/repo/issues/1", Operation: "write"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != SideEffectActionAllow || decision.ReplayMode != "normal" {
		t.Fatalf("unexpected normal decision: %#v", decision)
	}
}

func TestCheckSideEffectRequiresApprovalForUntrackedRestoredWrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".osix-replay-policy.json"), []byte(`{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	decision, err := CheckSideEffect(root, SideEffectCheck{Tool: "github.create_issue", ExternalResource: "github:acme/repo/issues/2", Operation: "write"})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != SideEffectActionRequireApproval || decision.MatchedEntry != nil {
		t.Fatalf("unexpected untracked decision: %#v", decision)
	}
}
