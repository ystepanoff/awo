# Configuration reference

`awo init` writes `awo.config.json` in the project root. Any field
you omit keeps its default value. AWO loads the file with
`config.LoadOrDefault`, which means missing fields are merged from
`config.Default()` rather than left zero — so `{"branchPrefix": "awo-foo"}`
is a complete, valid override.

To inspect the effective config:

```sh
awo config print
```

That command prints the merged result (file values layered onto
defaults), which is the source of truth for what AWO will actually
do at runtime.

AWO runs every agent in **non-interactive** mode. The CLIs cannot
pause for human approval mid-run; they either have pre-granted
permission for the operation or AWO records the run as a
`permission_required` failure. Configuration choices that follow
should be read with that constraint in mind.

## Full schema

```json
{
  "worktreeBaseDir": ".awo/worktrees",
  "branchPrefix": "awo",
  "artifactDir": ".awo/runs",
  "defaultVerifyCommands": [],
  "agents": {
    "claude": {
      "enabled": true,
      "command": "claude",
      "timeoutSeconds": 1800
    },
    "codex": {
      "enabled": true,
      "command": "codex",
      "timeoutSeconds": 1800
    }
  },
  "safety": {
    "maxChangedFiles": 50,
    "maxIterations": 1,
    "protectedPaths": [
      "auth/**",
      "payments/**",
      "migrations/**",
      "infra/**",
      ".github/workflows/**",
      "**/.env*",
      "**/*secret*",
      "**/*credential*",
      "**/*permission*"
    ],
    "requireConfirmationForProtectedPaths": true,
    "redactLogs": true
  }
}
```

## Field reference

### Top level

| Field | Type | Default | Notes |
| ----- | ---- | ------- | ----- |
| `worktreeBaseDir` | string | `.awo/worktrees` | Where AWO carves worktrees. **All worktree deletions are bounded to this path.** Anything outside is refused. |
| `branchPrefix` | string | `awo` | Every AWO branch starts with this prefix. Validation requires it to start with `"awo"` so AWO can never operate on a branch that wasn't its own. No whitespace allowed. |
| `artifactDir` | string | `.awo/runs` | Where `run.json`, `proof-pack.md`, etc. land. Per-run subdirectories are created with deterministic ids. |
| `defaultVerifyCommands` | `[]string` | `[]` | Shell commands run when `--verify` is not passed. **These are full shell commands; treat them like CI invocations.** Each entry runs in sequence; first non-zero exit fails the run. |

### `agents.claude`

| Field | Type | Default | Notes |
| ----- | ---- | ------- | ----- |
| `enabled` | bool | `true` | Disable to make `awo doctor` skip the Claude check and `awo run --agent claude` refuse. |
| `command` | string | `claude` | Override if your binary lives at a different name. |
| `writerArgs` | `[]string` | `["-p", "--permission-mode", "acceptEdits"]` | Argv used when Claude plays the writer / competitor roles. `-p` puts the CLI in non-interactive mode (prompt arrives on stdin or wherever `{{prompt}}` appears). `--permission-mode acceptEdits` auto-accepts file writes inside the cwd; AWO's cwd is always an isolated worktree, so this is the intended trust boundary. It does **not** bypass permissions for bash or for paths outside cwd. |
| `reviewerArgs` | `[]string` | `["-p", "--permission-mode", "plan"]` | Argv used when Claude plays the reviewer role. `plan` mode is read-only by design. |
| `competitorArgs` | `[]string` | (falls back to `writerArgs`) | Override only if the competitor needs different flags than the writer. |
| `args` | `[]string` | _legacy_ | Pre-per-role config. Still honored: when no per-role list is set, `args` applies to every role. New configs should use `writerArgs` / `reviewerArgs`. |
| `timeoutSeconds` | int | `1800` | Hard timeout per invocation. `0` falls back to `1800` rather than "no timeout" — AWO never lets an agent block forever in non-interactive mode. |

### `agents.codex`

| Field | Type | Default | Notes |
| ----- | ---- | ------- | ----- |
| `enabled` | bool | `true` | Disable to skip Codex entirely. |
| `command` | string | `codex` | Override if needed. |
| `writerArgs` | `[]string` | `["exec", "--sandbox", "workspace-write", "--ask-for-approval", "never"]` | Writer / competitor argv. `--ask-for-approval never` is required for non-interactive runs; `--sandbox workspace-write` lets the agent edit inside the worktree without escaping it. |
| `reviewerArgs` | `[]string` | `["exec", "--sandbox", "read-only", "--ask-for-approval", "never"]` | Reviewer argv. `read-only` keeps the reviewer from mutating the worktree. |
| `competitorArgs` | `[]string` | (falls back to `writerArgs`) | Same role-fallback rules as Claude. |
| `args` | `[]string` | _legacy_ | Pre-per-role config. When set with no per-role list, AWO appends the legacy `sandbox` / `approvalMode` fields and uses the result for every role. |
| `sandbox` | string | _legacy_ | Used only with the legacy `args` form; new configs should bake the sandbox flag straight into per-role args. |
| `approvalMode` | string | _legacy_ | Used only with the legacy `args` form; new configs should bake `--ask-for-approval` straight into per-role args. |
| `timeoutSeconds` | int | `1800` | Same semantics as Claude. |

`{{prompt}}` is a recognized placeholder inside any args list. If
present, AWO substitutes the rendered prompt at every occurrence and
sends an empty stdin. If absent, the prompt is piped on stdin so the
CLI's non-interactive mode reads it.

The reason `command` and per-role args are config-driven instead of
hard-coded: the Claude and Codex CLIs each have their own release
cadence, and forcing AWO to match them in lock-step would mean every
upstream rename ships as an AWO bug. Leaving these fields in your
config means a CLI bump becomes a one-line edit on your machine.

#### Why no dangerous bypasses

AWO refuses to load a config that contains
`bypassPermissions`, `--dangerously-skip-permissions`,
`danger-full-access`, or `dangerously-bypass` in any args list.
The validation message points at the offending field. The reasoning:
non-interactive runs combined with unrestricted bypass make
unintended writes essentially invisible until verification (or worse,
production) trips. Worktree isolation is AWO's safety boundary;
leaning on it requires the CLI's own permission gate to stay alive.

#### Customizing Claude's permission mode

The default writer args (`-p --permission-mode acceptEdits`) make
the CLI work inside an isolated worktree without a human at the
keyboard. You may want to change them in two situations:

- **Older Claude versions that don't accept `--permission-mode`.**
  Drop the flag and pass whichever non-interactive mode your version
  supports — for example `["-p"]` alone, or `["-p", "--allowedTools",
  "Edit"]`. If runs report `changed files: 0` and the proof pack
  shows `Agent failure (permission_required)`, this is the knob that
  needs tuning.
- **Tighter sandboxing.** Replace `acceptEdits` with `default` /
  `dontAsk` if you want every write to require explicit allowlisting
  via `--allowedTools`.

Always confirm flag names against your installed `claude --help`
and `codex --help`; CLI versions evolve and AWO does not pin them.

#### What a permission failure looks like

When the agent CLI hits an interactive approval prompt AWO cannot
answer, the run is recorded with `failureKind: "permission_required"`,
the recommendation is escalated to `needs_human_attention`, and the
proof pack carries an "⚠ Agent failure (permission_required)"
section that points at the relevant `writerArgs` / `reviewerArgs`
knob to relax. AWO never silently retries with broader permissions.

Always confirm flag names against your installed `claude --help`;
CLI versions evolve and AWO does not pin them.

### `safety`

| Field | Type | Default | Notes |
| ----- | ---- | ------- | ----- |
| `maxChangedFiles` | int | `50` | Patches that touch more files than this escalate to `too_large_for_auto_review`. The patch is still produced; the recommendation tightens. |
| `maxIterations` | int | `1` | Reserved. Iteration support is on the roadmap; values >1 currently behave as 1. |
| `protectedPaths` | `[]string` | (9 globs) | Glob patterns; matches escalate to `needs_human_attention`. See [`safety.md`](safety.md) for glob semantics. |
| `requireConfirmationForProtectedPaths` | bool | `true` | Reserved for a future interactive `awo run` confirmation prompt. Currently a no-op. |
| `redactLogs` | bool | `true` | Scrub well-known secret patterns out of agent stdout/stderr before they're persisted. Best effort. |

## Common overrides

### Add a default verify command

```json
{
  "defaultVerifyCommands": ["go test ./...", "golangci-lint run"]
}
```

Every `awo run` without `--verify` runs both, in order.

### Pin Codex to stricter sandboxing

```json
{
  "agents": {
    "codex": {
      "writerArgs": [
        "exec", "--sandbox", "read-only", "--ask-for-approval", "never"
      ]
    }
  }
}
```

(Use only with tasks that don't actually need to write files; this
will make the writer role useless and runs will surface
`failureKind: "permission_required"` as soon as the agent tries to
edit anything.)

### Extend protected paths to your repo's hot spots

```json
{
  "safety": {
    "protectedPaths": [
      "auth/**", "payments/**", "migrations/**", "infra/**",
      ".github/workflows/**",
      "**/.env*", "**/*secret*", "**/*credential*", "**/*permission*",
      "src/billing/**",
      "ops/runbooks/**"
    ]
  }
}
```

(You generally want to *extend* the default list rather than replace
it — any default you drop is a default you're choosing to silence.)

### Tighten the patch-size guardrail

```json
{
  "safety": {
    "maxChangedFiles": 20
  }
}
```

## Validation

`config.Validate()` runs on every load. It enforces:

- `worktreeBaseDir` and `artifactDir` are non-empty.
- `branchPrefix` is non-empty, starts with `"awo"`, and contains no
  whitespace.
- All `defaultVerifyCommands` entries are non-empty.
- `safety.maxChangedFiles` and `safety.maxIterations` are >= 0.
- All `safety.protectedPaths` entries are non-empty.
- Both agent timeouts are >= 0.
- No agent args list contains `bypassPermissions`,
  `--dangerously-skip-permissions`, `danger-full-access`, or
  `dangerously-bypass`.

A validation failure aborts the command before AWO touches any
files — your worktrees and artifact dirs are safe from a half-applied
config change.
