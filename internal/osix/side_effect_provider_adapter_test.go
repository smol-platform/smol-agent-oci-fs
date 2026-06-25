package osix

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestGitHubIssueAdapterEnforcesReplayPolicies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"github.issue.create","idempotencyKey":"create-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"github:acme/repo/issues","replayPolicy":"mock-by-default"}`+"\n"+
			`{"turn":2,"tool":"github.issue.comment","idempotencyKey":"comment-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"github:acme/repo/issues/7/comments","replayPolicy":"idempotent-retry-allowed"}`+"\n"+
			`{"turn":3,"tool":"github.issue.comment","idempotencyKey":"comment-2","requestDigest":"sha256:req3","responseDigest":"sha256:resp3","externalResource":"github:acme/repo/issues/8/comments","replayPolicy":"never-replay"}`+"\n")
	adapter, err := NewGitHubIssueAdapter("acme/repo")
	if err != nil {
		t.Fatal(err)
	}

	read, err := adapter.CheckReadIssue(root, 7)
	if err != nil {
		t.Fatal(err)
	}
	if read.Action != SideEffectActionAllow {
		t.Fatalf("unexpected read decision: %#v", read)
	}

	create, err := adapter.CheckCreateIssue(root, "create-1")
	if err != nil {
		t.Fatal(err)
	}
	if create.Action != SideEffectActionMock || create.ReplayPolicy != "mock-by-default" {
		t.Fatalf("unexpected create decision: %#v", create)
	}

	comment, err := adapter.CheckCommentIssue(root, 7, "comment-1")
	if err != nil {
		t.Fatal(err)
	}
	if comment.Action != SideEffectActionAllow || comment.ReplayPolicy != "idempotent-retry-allowed" {
		t.Fatalf("unexpected comment decision: %#v", comment)
	}

	untracked, err := adapter.CheckCommentIssue(root, 9, "comment-9")
	if err == nil {
		t.Fatal("expected untracked comment to require approval")
	}
	var blocked SideEffectBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if untracked.Action != SideEffectActionRequireApproval || blocked.Decision.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected untracked decision: %#v / %#v", untracked, blocked.Decision)
	}

	denied, err := adapter.CheckCommentIssue(root, 8, "comment-2")
	if err == nil {
		t.Fatal("expected denied comment to fail")
	}
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if denied.Action != SideEffectActionDeny || blocked.Decision.Action != SideEffectActionDeny {
		t.Fatalf("unexpected deny decision: %#v / %#v", denied, blocked.Decision)
	}
}

func TestGitHubIssueAdapterRequiresOwnerRepo(t *testing.T) {
	if _, err := NewGitHubIssueAdapter("repo-only"); err == nil {
		t.Fatal("expected OWNER/REPO validation error")
	}
}

func TestGmailAdapterEnforcesReplayPolicies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"gmail.message.send","idempotencyKey":"send-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"gmail:me@example.com/send","replayPolicy":"idempotent-retry-allowed"}`+"\n"+
			`{"turn":2,"tool":"gmail.draft.create","idempotencyKey":"draft-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"gmail:me@example.com/drafts","replayPolicy":"mock-by-default"}`+"\n"+
			`{"turn":3,"tool":"gmail.message.modify_labels","idempotencyKey":"labels-1","requestDigest":"sha256:req3","responseDigest":"sha256:resp3","externalResource":"gmail:me@example.com/messages/msg-1/labels","replayPolicy":"never-replay"}`+"\n")
	adapter, err := NewGmailAdapter("me@example.com")
	if err != nil {
		t.Fatal(err)
	}

	read, err := adapter.CheckReadMessage(root, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if read.Action != SideEffectActionAllow {
		t.Fatalf("unexpected read decision: %#v", read)
	}

	send, err := adapter.CheckSendMessage(root, "send-1")
	if err != nil {
		t.Fatal(err)
	}
	if send.Action != SideEffectActionAllow || send.ReplayPolicy != "idempotent-retry-allowed" {
		t.Fatalf("unexpected send decision: %#v", send)
	}

	draft, err := adapter.CheckCreateDraft(root, "draft-1")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Action != SideEffectActionMock || draft.ReplayPolicy != "mock-by-default" {
		t.Fatalf("unexpected draft decision: %#v", draft)
	}

	labels, err := adapter.CheckModifyMessageLabels(root, "msg-1", "labels-1")
	if err == nil {
		t.Fatal("expected label mutation to be blocked")
	}
	var blocked SideEffectBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if labels.Action != SideEffectActionDeny || blocked.Decision.Action != SideEffectActionDeny {
		t.Fatalf("unexpected label decision: %#v / %#v", labels, blocked.Decision)
	}

	untracked, err := adapter.CheckSendMessage(root, "send-2")
	if err == nil {
		t.Fatal("expected untracked send to require approval")
	}
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if untracked.Action != SideEffectActionRequireApproval || blocked.Decision.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected untracked send decision: %#v / %#v", untracked, blocked.Decision)
	}
}

func TestGmailAdapterAllowsNormalRuntimeWrites(t *testing.T) {
	root := t.TempDir()
	adapter, err := NewGmailAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := adapter.CheckSendMessage(root, "send-1")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != SideEffectActionAllow || decision.ReplayMode != "normal" {
		t.Fatalf("unexpected normal Gmail decision: %#v", decision)
	}
}

func TestGmailAdapterValidatesInputs(t *testing.T) {
	if _, err := NewGmailAdapter("bad mailbox"); err == nil {
		t.Fatal("expected mailbox validation error")
	}
	adapter, err := NewGmailAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Mailbox != "default" {
		t.Fatalf("default mailbox = %q, want default", adapter.Mailbox)
	}
	if _, err := adapter.CheckReadMessage(t.TempDir(), ""); err == nil {
		t.Fatal("expected empty message ID validation error")
	}
	if _, err := adapter.CheckReadThread(t.TempDir(), ""); err == nil {
		t.Fatal("expected empty thread ID validation error")
	}
}

func TestGoogleCalendarAdapterEnforcesReplayPolicies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"google_calendar.event.create","idempotencyKey":"create-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"gcal:primary/events","replayPolicy":"idempotent-retry-allowed"}`+"\n"+
			`{"turn":2,"tool":"google_calendar.event.update","idempotencyKey":"update-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"gcal:primary/events/event-1","replayPolicy":"require-approval"}`+"\n"+
			`{"turn":3,"tool":"google_calendar.event.delete","idempotencyKey":"delete-1","requestDigest":"sha256:req3","responseDigest":"sha256:resp3","externalResource":"gcal:primary/events/event-2","replayPolicy":"never-replay"}`+"\n"+
			`{"turn":4,"tool":"google_calendar.event.respond","idempotencyKey":"respond-1","requestDigest":"sha256:req4","responseDigest":"sha256:resp4","externalResource":"gcal:primary/events/event-3/response","replayPolicy":"mock-by-default"}`+"\n")
	adapter, err := NewGoogleCalendarAdapter("")
	if err != nil {
		t.Fatal(err)
	}

	read, err := adapter.CheckReadEvent(root, "event-1")
	if err != nil {
		t.Fatal(err)
	}
	if read.Action != SideEffectActionAllow {
		t.Fatalf("unexpected read decision: %#v", read)
	}

	create, err := adapter.CheckCreateEvent(root, "create-1")
	if err != nil {
		t.Fatal(err)
	}
	if create.Action != SideEffectActionAllow || create.ReplayPolicy != "idempotent-retry-allowed" {
		t.Fatalf("unexpected create decision: %#v", create)
	}

	response, err := adapter.CheckRespondInvitation(root, "event-3", "respond-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Action != SideEffectActionMock || response.ReplayPolicy != "mock-by-default" {
		t.Fatalf("unexpected response decision: %#v", response)
	}

	update, err := adapter.CheckUpdateEvent(root, "event-1", "update-1")
	if err == nil {
		t.Fatal("expected update to require approval")
	}
	var blocked SideEffectBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if update.Action != SideEffectActionRequireApproval || blocked.Decision.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected update decision: %#v / %#v", update, blocked.Decision)
	}

	deleted, err := adapter.CheckDeleteEvent(root, "event-2", "delete-1")
	if err == nil {
		t.Fatal("expected delete to be blocked")
	}
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if deleted.Action != SideEffectActionDeny || blocked.Decision.Action != SideEffectActionDeny {
		t.Fatalf("unexpected delete decision: %#v / %#v", deleted, blocked.Decision)
	}
}

func TestGoogleCalendarAdapterAllowsNormalRuntimeWrites(t *testing.T) {
	root := t.TempDir()
	adapter, err := NewGoogleCalendarAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := adapter.CheckCreateEvent(root, "create-1")
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != SideEffectActionAllow || decision.ReplayMode != "normal" {
		t.Fatalf("unexpected normal Google Calendar decision: %#v", decision)
	}
}

func TestGoogleCalendarAdapterValidatesInputs(t *testing.T) {
	if _, err := NewGoogleCalendarAdapter("bad calendar"); err == nil {
		t.Fatal("expected calendar validation error")
	}
	adapter, err := NewGoogleCalendarAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.CalendarID != "primary" {
		t.Fatalf("default calendar = %q, want primary", adapter.CalendarID)
	}
	if _, err := adapter.CheckReadEvent(t.TempDir(), ""); err == nil {
		t.Fatal("expected empty read event ID validation error")
	}
	if _, err := adapter.CheckUpdateEvent(t.TempDir(), "", "update-1"); err == nil {
		t.Fatal("expected empty update event ID validation error")
	}
	if _, err := adapter.CheckDeleteEvent(t.TempDir(), "", "delete-1"); err == nil {
		t.Fatal("expected empty delete event ID validation error")
	}
	if _, err := adapter.CheckRespondInvitation(t.TempDir(), "", "respond-1"); err == nil {
		t.Fatal("expected empty response event ID validation error")
	}
}

func TestLinearAdapterEnforcesReplayPolicies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"linear.issue.create","idempotencyKey":"create-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"linear:acme/teams/ENG/issues","replayPolicy":"idempotent-retry-allowed"}`+"\n"+
			`{"turn":2,"tool":"linear.issue.update","idempotencyKey":"update-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"linear:acme/issues/ENG-123","replayPolicy":"require-approval"}`+"\n"+
			`{"turn":3,"tool":"linear.comment.create","idempotencyKey":"comment-1","requestDigest":"sha256:req3","responseDigest":"sha256:resp3","externalResource":"linear:acme/issues/ENG-456/comments","replayPolicy":"mock-by-default"}`+"\n")
	adapter, err := NewLinearAdapter("acme")
	if err != nil {
		t.Fatal(err)
	}

	read, err := adapter.CheckReadIssue(root, "ENG-123")
	if err != nil {
		t.Fatal(err)
	}
	if read.Action != SideEffectActionAllow {
		t.Fatalf("unexpected read decision: %#v", read)
	}

	create, err := adapter.CheckCreateIssue(root, "ENG", "create-1")
	if err != nil {
		t.Fatal(err)
	}
	if create.Action != SideEffectActionAllow || create.ReplayPolicy != "idempotent-retry-allowed" {
		t.Fatalf("unexpected create decision: %#v", create)
	}

	comment, err := adapter.CheckCommentIssue(root, "ENG-456", "comment-1")
	if err != nil {
		t.Fatal(err)
	}
	if comment.Action != SideEffectActionMock || comment.ReplayPolicy != "mock-by-default" {
		t.Fatalf("unexpected comment decision: %#v", comment)
	}

	update, err := adapter.CheckUpdateIssue(root, "ENG-123", "update-1")
	if err == nil {
		t.Fatal("expected update to require approval")
	}
	var blocked SideEffectBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if update.Action != SideEffectActionRequireApproval || blocked.Decision.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected update decision: %#v / %#v", update, blocked.Decision)
	}

	untracked, err := adapter.CheckCreateIssue(root, "ENG", "create-2")
	if err == nil {
		t.Fatal("expected untracked create to require approval")
	}
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if untracked.Action != SideEffectActionRequireApproval || blocked.Decision.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected untracked decision: %#v / %#v", untracked, blocked.Decision)
	}
}

func TestLinearAdapterValidatesInputs(t *testing.T) {
	if _, err := NewLinearAdapter("bad workspace"); err == nil {
		t.Fatal("expected workspace validation error")
	}
	adapter, err := NewLinearAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Workspace != "default" {
		t.Fatalf("default workspace = %q, want default", adapter.Workspace)
	}
	if _, err := adapter.CheckReadIssue(t.TempDir(), ""); err == nil {
		t.Fatal("expected empty issue ID validation error")
	}
	if _, err := adapter.CheckCreateIssue(t.TempDir(), "", "create-1"); err == nil {
		t.Fatal("expected empty team key validation error")
	}
	if _, err := adapter.CheckUpdateIssue(t.TempDir(), "", "update-1"); err == nil {
		t.Fatal("expected empty update issue ID validation error")
	}
	if _, err := adapter.CheckCommentIssue(t.TempDir(), "", "comment-1"); err == nil {
		t.Fatal("expected empty comment issue ID validation error")
	}
}

func TestSlackAdapterEnforcesReplayPolicies(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ".osix-replay-policy.json"), `{"mode":"require-approval","createdAt":"2026-06-22T00:00:00Z"}`)
	mustWrite(t, filepath.Join(root, "agent", "side-effects", "ledger.jsonl"),
		`{"turn":1,"tool":"slack.message.post","idempotencyKey":"post-1","requestDigest":"sha256:req","responseDigest":"sha256:resp","externalResource":"slack:acme/channels/C123/messages","replayPolicy":"mock-by-default"}`+"\n"+
			`{"turn":2,"tool":"slack.message.update","idempotencyKey":"update-1","requestDigest":"sha256:req2","responseDigest":"sha256:resp2","externalResource":"slack:acme/channels/C123/messages/1700000000.000100","replayPolicy":"idempotent-retry-allowed"}`+"\n"+
			`{"turn":3,"tool":"slack.message.delete","idempotencyKey":"delete-1","requestDigest":"sha256:req3","responseDigest":"sha256:resp3","externalResource":"slack:acme/channels/C123/messages/1700000000.000200","replayPolicy":"never-replay"}`+"\n"+
			`{"turn":4,"tool":"slack.reaction.add","idempotencyKey":"reaction-1","requestDigest":"sha256:req4","responseDigest":"sha256:resp4","externalResource":"slack:acme/channels/C123/messages/1700000000.000300/reactions/eyes","replayPolicy":"require-approval"}`+"\n")
	adapter, err := NewSlackAdapter("acme")
	if err != nil {
		t.Fatal(err)
	}

	read, err := adapter.CheckReadChannel(root, "C123")
	if err != nil {
		t.Fatal(err)
	}
	if read.Action != SideEffectActionAllow {
		t.Fatalf("unexpected read decision: %#v", read)
	}

	post, err := adapter.CheckPostMessage(root, "C123", "post-1")
	if err != nil {
		t.Fatal(err)
	}
	if post.Action != SideEffectActionMock || post.ReplayPolicy != "mock-by-default" {
		t.Fatalf("unexpected post decision: %#v", post)
	}

	update, err := adapter.CheckUpdateMessage(root, "C123", "1700000000.000100", "update-1")
	if err != nil {
		t.Fatal(err)
	}
	if update.Action != SideEffectActionAllow || update.ReplayPolicy != "idempotent-retry-allowed" {
		t.Fatalf("unexpected update decision: %#v", update)
	}

	deleted, err := adapter.CheckDeleteMessage(root, "C123", "1700000000.000200", "delete-1")
	if err == nil {
		t.Fatal("expected delete to be blocked")
	}
	var blocked SideEffectBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if deleted.Action != SideEffectActionDeny || blocked.Decision.Action != SideEffectActionDeny {
		t.Fatalf("unexpected delete decision: %#v / %#v", deleted, blocked.Decision)
	}

	reaction, err := adapter.CheckAddReaction(root, "C123", "1700000000.000300", ":eyes:", "reaction-1")
	if err == nil {
		t.Fatal("expected reaction to require approval")
	}
	if !errors.As(err, &blocked) {
		t.Fatalf("expected SideEffectBlockedError, got %T: %v", err, err)
	}
	if reaction.Action != SideEffectActionRequireApproval || blocked.Decision.Action != SideEffectActionRequireApproval {
		t.Fatalf("unexpected reaction decision: %#v / %#v", reaction, blocked.Decision)
	}
}

func TestSlackAdapterValidatesInputs(t *testing.T) {
	if _, err := NewSlackAdapter("bad workspace"); err == nil {
		t.Fatal("expected workspace validation error")
	}
	adapter, err := NewSlackAdapter("")
	if err != nil {
		t.Fatal(err)
	}
	if adapter.Workspace != "default" {
		t.Fatalf("default workspace = %q, want default", adapter.Workspace)
	}
	if _, err := adapter.CheckReadChannel(t.TempDir(), ""); err == nil {
		t.Fatal("expected empty channel ID validation error")
	}
	if _, err := adapter.CheckPostMessage(t.TempDir(), "", "post-1"); err == nil {
		t.Fatal("expected empty post channel ID validation error")
	}
	if _, err := adapter.CheckUpdateMessage(t.TempDir(), "C123", "", "update-1"); err == nil {
		t.Fatal("expected empty message ID validation error")
	}
	if _, err := adapter.CheckDeleteMessage(t.TempDir(), "", "1700000000.000100", "delete-1"); err == nil {
		t.Fatal("expected empty delete channel ID validation error")
	}
	if _, err := adapter.CheckAddReaction(t.TempDir(), "C123", "1700000000.000100", "", "reaction-1"); err == nil {
		t.Fatal("expected empty reaction validation error")
	}
}
