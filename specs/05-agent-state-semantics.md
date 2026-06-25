# Spec 05: Agent State Semantics

## Purpose

Define which agent state OSIx captures, excludes, and treats specially.

## Standard State Roots

OSIx agents use this default layout:

```text
/agent
  /workspace
  /memory
  /skills
  /runtime
  /logs
  /state
  /side-effects
  /secrets
  /cache
  /tmp
```

Default behavior:

| Path | Behavior |
| --- | --- |
| `/agent/workspace` | normal filesystem diff |
| `/agent/memory` | versioned, append-friendly, compactable |
| `/agent/skills` | signed versioned packages |
| `/agent/runtime` | runtime metadata and locks, no secrets |
| `/agent/logs` | included unless policy excludes or redacts |
| `/agent/state` | included |
| `/agent/side-effects` | append-only ledger |
| `/agent/secrets` | never snapshot |
| `/agent/cache` | excluded by default |
| `/agent/tmp` | excluded by default |

## Required Reproducibility State

A reproducible snapshot SHOULD include:

- workspace files
- memory database or memory export
- skill tree
- tool registry lock
- prompt and config lock
- side-effect ledger
- runtime version lock

It MUST NOT include by default:

- raw credentials
- cloud provider session tokens
- KMS plaintext keys
- browser auth cookies
- SSH private keys
- API tokens

Browser auth cookies MAY be included only with explicit opt-in policy.

## Side-Effect Ledger

Filesystem snapshots do not capture external actions such as emails, trades, cloud resources, GitHub mutations, payment calls, or calendar writes. OSIx therefore defines an append-only side-effect ledger:

```text
/agent/side-effects/ledger.jsonl
```

Each line is a JSON object:

```json
{
  "turn": 1242,
  "tool": "github.create_issue",
  "idempotencyKey": "osix-2026-06-19-...",
  "requestDigest": "sha256:...",
  "responseDigest": "sha256:...",
  "externalResource": "github:acme/repo/issues/123",
  "replayPolicy": "mock-by-default",
  "compensatingAction": "github.close_issue"
}
```

## Replay Policy

Supported replay policies:

- `mock-by-default`
- `read-only-by-default`
- `require-approval`
- `never-replay`
- `idempotent-retry-allowed`

After restore or fork, OSIx-aware tool runtimes SHOULD consult the ledger before performing external writes.

OSIx provides a generic adapter check for external tools:

```sh
osix side-effect check ./restored \
  --tool github.create_issue \
  --resource github:acme/repo/issues/123 \
  --operation write \
  --idempotency-key osix-2026-06-19-...
```

The check reads `.osix-replay-policy.json` plus
`/agent/side-effects/ledger.jsonl` and returns a JSON decision. Restored or
forked runtimes map ledger policy to one of `allow`, `mock`, `read-only`,
`require-approval`, or `deny`; write-capable adapters MUST treat
`require-approval`, `read-only`, and `deny` as blocking unless their caller has
an explicit approval workflow.

Reference adapters MAY wrap this gate with provider-specific resource names.
The Go library includes:

- `GitHubIssueAdapter`, which maps issue reads, issue creation, and issue
  comments to stable resources such as `github:OWNER/REPO/issues/123`.
- `GmailAdapter`, which maps message/thread reads, sends, drafts, and label
  mutations to stable resources such as `gmail:MAILBOX/messages/MSG_ID`.
- `GoogleCalendarAdapter`, which maps event reads, creates, updates, deletes,
  and invitation responses to stable resources such as
  `gcal:CALENDAR_ID/events/EVENT_ID`.
- `LinearAdapter`, which maps issue reads, issue creation, issue updates, and
  issue comments to stable resources such as
  `linear:WORKSPACE/issues/ENG-123`.
- `SlackAdapter`, which maps channel reads, message posts, message updates,
  message deletes, and reactions to stable resources such as
  `slack:WORKSPACE/channels/C123/messages/1700000000.000100`.

Write-capable provider adapters return a typed block error for unsafe restored
writes.

## Turn Boundaries

Agent snapshots SHOULD align with turn boundaries. A turn boundary is a point where:

- no tool call is in progress
- pending side effects have been recorded
- memory writes have been flushed
- logs have been flushed
- the runtime can resume deterministically enough for the configured policy

`osix watch --on-turn-boundary` SHOULD wait for this condition before creating a pushed snapshot.

## Memory State

v0 MAY snapshot memory as normal files. Future versions SHOULD define memory-specific indexes that allow:

- append-only ingestion
- compaction
- semantic search index rebuilds
- redaction
- migration between memory backends

Memory indexes SHOULD be stored as encrypted referrer artifacts when they expose paths, text, embeddings, or other sensitive data.
