# `run.json` schema

Every AWO run produces a `run.json` at
`<repoRoot>/.awo/runs/<run-id>/run.json`. It is the **canonical record**
of the run — every other rendered file (`proof-pack.md`, `summary.md`,
`comparison.md`) is derived from it. When `proof-pack.md` and
`run.json` disagree, treat `run.json` as authoritative and file a bug
against the renderer.

This document describes the shape that ships in v0.1.0 and what each
field means. Field names use lowerCamelCase. Times are RFC 3339 UTC.
Optional fields are omitted when empty.

## Top level: `RunReport`

| Field | Type | Notes |
| ----- | ---- | ----- |
| `runId` | string | Globally unique short id. Also the artifact directory name. |
| `spec` | object | What the run was asked to do. See [`RunSpec`](#runspec). |
| `status` | string enum | One of `created`, `preparing`, `running`, `verifying`, `completed`, `failed`, `cancelled`. |
| `startedAt` | RFC3339 | When orchestration began. |
| `finishedAt` | RFC3339 | When orchestration ended. Omitted if the run is still in flight. |
| `agentResults` | array | One entry per agent invocation. See [`AgentRunResult`](#agentrunresult). |
| `verificationResults` | array | One entry per verification command. See [`VerificationResult`](#verificationresult). |
| `safety` | object | Safety analysis. See [`SafetyReport`](#safetyreport). Omitted if not yet computed. |
| `recommendation` | string enum | The orchestrator's verdict. See [Recommendation ladder](#recommendation-ladder). |
| `proofPackPath` | string | Filesystem path to `proof-pack.md` for this run. |
| `warnings` | array of string | Human-readable orchestration-level warnings. Advisory. |

### `RunSpec`

```json
{
  "task": "fix checkout validation",
  "mode": "writer-reviewer",
  "primary": "claude",
  "reviewer": "codex",
  "verifyCommands": ["go test ./..."],
  "baseBranch": "",
  "keepWorktrees": false,
  "dryRun": false,
  "maxChangedFiles": 0
}
```

| Field | Type | Notes |
| ----- | ---- | ----- |
| `task` | string | The natural-language task passed to `awo run`. |
| `mode` | string enum | `single` \| `writer-reviewer` \| `competitive`. |
| `agent` | string | Set in `single` mode; `claude` or `codex`. |
| `primary` | string | Set in `writer-reviewer` mode. |
| `reviewer` | string | Set in `writer-reviewer` mode. |
| `competitors` | array of string | Set in `competitive` mode. |
| `verifyCommands` | array of string | Resolved verification commands (after fallback to defaults). |
| `baseBranch` | string | Optional override; empty means `HEAD`. |
| `keepWorktrees` | bool | True when `--keep-worktrees` was passed. |
| `dryRun` | bool | True when `--dry-run` was passed. |
| `maxChangedFiles` | int | Per-run override of `safety.maxChangedFiles`. `0` means use the config value. |

### `AgentRunResult`

```json
{
  "agent": "claude",
  "role": "writer",
  "worktreePath": ".awo/worktrees/<run-id>/claude-writer",
  "branchName": "awo/<run-id>/claude-writer",
  "status": "ok",
  "startedAt": "2026-05-25T10:00:00Z",
  "finishedAt": "2026-05-25T10:01:23Z",
  "exitCode": 0,
  "stdoutPath": ".awo/runs/<run-id>/agents/claude-writer/stdout.log",
  "stderrPath": ".awo/runs/<run-id>/agents/claude-writer/stderr.log",
  "changedFiles": ["internal/calc/calc_test.go"],
  "parsedResult": { "summary": "...", "filesTouched": ["..."] },
  "review": null,
  "warnings": []
}
```

| Field | Type | Notes |
| ----- | ---- | ----- |
| `agent` | string enum | `claude` or `codex`. |
| `role` | string enum | `writer`, `reviewer`, or `competitor`. |
| `worktreePath` | string | Always under `<repoRoot>/.awo/worktrees/`. |
| `branchName` | string | Always begins with `cfg.branchPrefix` (default `awo`). |
| `status` | string | `ok`, `failed`, `dry-run`, `timed-out`. Derived from exit code; never from agent self-report. |
| `exitCode` | int | Process exit code. Trusted. |
| `stdoutPath` / `stderrPath` | string | Paths to captured logs. |
| `changedFiles` | array of string | Output of `git status --porcelain` in the agent's worktree. **Trusted.** |
| `parsedResult` | object | Best-effort parse of `AWO_RESULT_JSON`. **Advisory only.** |
| `review` | object | Best-effort parse of `AWO_REVIEW_JSON`. Set for reviewer-role results. **Advisory only.** |
| `warnings` | array of string | Per-agent warnings. |

### `VerificationResult`

```json
{
  "command": "go test ./...",
  "exitCode": 0,
  "startedAt": "2026-05-25T10:01:30Z",
  "finishedAt": "2026-05-25T10:01:42Z",
  "durationMillis": 12000,
  "stdoutPath": ".awo/runs/<run-id>/verify/000/stdout.log",
  "stderrPath": ".awo/runs/<run-id>/verify/000/stderr.log",
  "passed": true
}
```

| Field | Type | Notes |
| ----- | ---- | ----- |
| `command` | string | The shell string AWO ran. Comes from `--verify` or `cfg.defaultVerifyCommands`. |
| `exitCode` | int | Trusted. The only success signal. |
| `passed` | bool | `exitCode == 0`. |

### `SafetyReport`

```json
{
  "protectedHits": [
    { "path": ".github/workflows/ci.yml", "patterns": [".github/**"] }
  ],
  "changedFileCount": 1,
  "maxChangedFiles": 50,
  "exceedsMaxChanged": false
}
```

| Field | Type | Notes |
| ----- | ---- | ----- |
| `protectedHits` | array | Changed files matching `cfg.safety.protectedPaths` globs. |
| `changedFileCount` | int | Number of changed files used to compute the cap. |
| `maxChangedFiles` | int | Resolved limit for this run. |
| `exceedsMaxChanged` | bool | True when the cap was breached. |

### `ParsedAgentResult` and `ReviewFindings`

Both are **advisory**: they reflect what the agent *claimed* it did,
not what it actually did. Never use them for control-flow decisions.

`ParsedAgentResult` (from `AWO_RESULT_JSON`):

| Field | Type |
| ----- | ---- |
| `summary` | string |
| `filesTouched` | array of string |
| `selfReportedSuccess` | nullable bool |
| `notes` | array of string |

`ReviewFindings` (from `AWO_REVIEW_JSON`, reviewer role only):

| Field | Type |
| ----- | ---- |
| `blocking` | array of string |
| `nonBlocking` | array of string |
| `suggestedTests` | array of string |
| `riskSummary` | string |
| `recommendation` | string (free-form; `reject` / `needs_revision` / `accept`) |

## Recommendation ladder

`recommendation` is one of:

- `failed_verification` — at least one verification command exited
  non-zero. **Strongest signal.**
- `needs_revision` — reviewer (writer-reviewer mode) asked for changes
  or flagged blocking issues.
- `needs_human_attention` — changed files include protected paths.
- `too_large_for_auto_review` — changed-file count exceeds the cap.
- `ready_for_human_review` — nothing in the above categories. **Still
  requires human review** before merging — AWO is an auto-review
  signal, not an auto-approval one.

The ladder is implemented in `orchestrator.escalateForSafety` and
applied per-mode by `recommendSingle`, `recommendWriterReviewer`, and
the competitive scorer.

## Stability

`run.json` is the contract surface most likely to be consumed by
external tooling (CI integration, dashboards, audit pipelines). For
v0.1.0:

- **Stable:** field names listed above, enum values for `status`,
  `mode`, `role`, `agent`, `recommendation`.
- **Additive only:** new optional fields may appear in minor versions.
  Existing field names and types will not change without a major
  version bump.
- **Best-effort:** `parsedResult.notes`, `review.riskSummary`, and the
  free-form `review.recommendation` reflect agent output and are
  inherently noisy.

If you build tooling on `run.json`, tolerate unknown fields and
prefer reading `recommendation` over re-deriving it from
`verificationResults` + `safety` — the orchestrator is the source of
truth for the verdict.
