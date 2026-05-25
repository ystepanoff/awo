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
      "args": [],
      "timeoutSeconds": 1800
    },
    "codex": {
      "enabled": true,
      "command": "codex",
      "args": [],
      "timeoutSeconds": 1800,
      "sandbox": "workspace-write",
      "approvalMode": "on-request"
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
| `args` | `[]string` | `["-p", "--permission-mode", "acceptEdits"]` (when unset) | Replaces — does not append to — the defaults. `-p` puts the CLI in non-interactive mode so it reads the prompt from stdin. `--permission-mode acceptEdits` auto-accepts file writes inside the cwd; AWO's cwd is always an isolated worktree under `.awo/worktrees/`, so this is the intended trust boundary. It does **not** bypass permissions for bash or for paths outside cwd. **CLI versions vary**: if a future Claude release renames a flag, change this rather than waiting for an AWO update. See the note below if you need to override these defaults. |
| `timeoutSeconds` | int | `1800` | Hard timeout per invocation. `0` disables. |

### `agents.codex`

| Field | Type | Default | Notes |
| ----- | ---- | ------- | ----- |
| `enabled` | bool | `true` | Disable to skip Codex entirely. |
| `command` | string | `codex` | Override if needed. |
| `args` | `[]string` | `[]` | Same as Claude — **here on purpose because the Codex CLI's flag set evolves.** Pin to a version-appropriate set. |
| `timeoutSeconds` | int | `1800` | Same semantics as Claude. |
| `sandbox` | string | `workspace-write` | Passed to `codex --sandbox`. Stricter values reduce blast radius further. |
| `approvalMode` | string | `on-request` | Passed to `codex --approval-mode`. AWO inherits whatever the CLI supports. |

The reason `command` and `args` are config-driven instead of
hard-coded: the Claude and Codex CLIs each have their own release
cadence, and forcing AWO to match them in lock-step would mean every
upstream rename ships as an AWO bug. Leaving these fields in your
config means a CLI bump becomes a one-line edit on your machine.

#### Customizing Claude's permission mode

The default args (`-p --permission-mode acceptEdits`) make the CLI
work inside an isolated worktree without a human at the keyboard.
You may want to change them in two situations:

- **Older Claude versions that don't accept `--permission-mode`.**
  Drop the flag and pass whichever non-interactive mode your version
  supports — for example `["-p"]` alone, or `["-p", "--allowedTools",
  "Edit"]`. If runs report `changed files: 0` and the agent log says
  permission was denied, this is the knob that needs tuning.
- **Tighter sandboxing.** Replace `acceptEdits` with `default` /
  `dontAsk` if you want every write to require explicit allowlisting
  via `--allowedTools`. Note that `bypassPermissions` and
  `--dangerously-skip-permissions` are out of scope: AWO's worktree
  isolation is the safety boundary, not the CLI's permission flag,
  but auto-bypassing bash defeats the spirit of leaving verification
  as the only operator-controlled shell entry point.

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
      "sandbox": "read-only",
      "approvalMode": "never"
    }
  }
}
```

(Use only with tasks that don't actually need to write files; this
will make the writer role useless.)

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

A validation failure aborts the command before AWO touches any
files — your worktrees and artifact dirs are safe from a half-applied
config change.
