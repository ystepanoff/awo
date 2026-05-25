# Contributing to AWO

Thanks for your interest in AWO. This document covers what you need to
hack on the codebase and the conventions a change has to satisfy before
it can land.

## Quick start

```sh
git clone https://github.com/ystepanoff/awo.git
cd awo
make build
./awo --help
```

Requires Go 1.22+. Runtime dependencies are `git`, plus whichever agent
CLIs you intend to invoke (`claude`, `codex`).

## Development loop

```sh
make test            # go test ./...
make vet             # go vet ./...
make lint            # vet + golangci-lint (or gofmt -l fallback)
make build           # ./awo
```

The full suite is offline:

- No real `claude` or `codex` invocations. Tests mock at the `execx`
  boundary.
- No real network calls.
- No host-repo mutation. Tests use `t.TempDir()`.

If you write a test that shells out to a real CLI or touches the host
repo, that's a bug — the rest of the suite would become flaky in CI or
unsafe in someone's working tree.

For an end-to-end smoke loop, use the bundled fixture:

```sh
make build
./awo examples create-fixture
cd .awo/fixtures/sample-go-app
../../awo init
../../awo run "add an edge-case test for Divide" \
    --mode single --agent claude \
    --verify "go test ./..."
```

## Package layout

| Package | Responsibility |
| ------- | -------------- |
| `cmd/awo` | Binary entry point only |
| `internal/cli` | Cobra commands, flag parsing, user-visible output |
| `internal/config` | `AwoConfig`, `Default()`, `Load()`, `Validate()` |
| `internal/domain` | Cross-cutting types (pure data, no behavior) |
| `internal/gitx` | Git wrappers (worktree create/list/remove, status, diff) |
| `internal/execx` | Subprocess execution with timeouts and captured I/O |
| `internal/agents` | Adapters for `claude`/`codex`, prompts, JSON parsing |
| `internal/orchestrator` | The three modes; verification; deterministic scoring |
| `internal/safety` | Path containment, protected paths, redaction |
| `internal/artifacts` | `.awo/runs/<run-id>/` filesystem layout |
| `internal/reports` | Proof pack / summary / comparison rendering |
| `internal/prhelper` | `awo pr prepare` rendering |
| `internal/runid` | Run id generation |

The dependency direction is one-way: `cli` and `orchestrator` depend on
the lower layers, never the reverse.

See `docs/architecture.md` and `docs/development.md` for the longer
versions.

## Hard rules a contribution must respect

These are load-bearing safety properties. They are enforced in code and
covered by tests. Don't relax them, and if your change touches them,
make sure the existing tests still hold (or add new ones).

1. **AWO never commits, merges, pushes, or fetches.** Worktrees and
   branches are created and removed; that's it.
2. **Deletion is bounded to `.awo/worktrees`.** Every removal goes
   through `gitx.RemoveWorktree`, which calls `safety.MustBeUnder`.
   Don't add an `os.Remove*` call against a caller-supplied path.
3. **Only verification commands shell out.** `execx.RunShellVerification`
   is the single entry point that uses `sh -c` / `cmd /C`. All other
   subprocess work uses `exec.CommandContext` with separate `Command`
   and `Args`. Agent-supplied strings must never reach a shell.
4. **Agent self-reports are advisory.** Changed files come from
   `git status`; verification success comes from exit codes. If your
   change starts trusting agent stdout for a control-flow decision,
   that's a review blocker.
5. **Secrets get redacted.** Captured stdout/stderr passes through
   `safety.Redact` when `cfg.Safety.RedactLogs` is true.

## Conventions

- **No emojis** in code, comments, or output.
- **Comments explain *why*, not *what*.** Prefer expressive names and
  short functions.
- **Templates over string concatenation.** Prompts and reports use
  `text/template` with `embed.FS`.
- **No new dependencies without justification.** AWO is small on
  purpose; every dependency is a future supply-chain concern.
- **Hard rules go in code, not just docs.** If a safety property needs
  to hold, there should be a test that breaks if a future refactor
  violates it (see `internal/safety` and `internal/orchestrator`).

## Submitting a change

1. Open an issue first for non-trivial changes so we can agree on
   scope before code is written.
2. Branch from `main`.
3. Run `make test`, `make vet`, and `make lint` (or `make fmt && make
   lint`) before pushing.
4. Update `CHANGELOG.md` under `## [Unreleased]` with a one-line
   summary of your change.
5. Open a PR with a focused diff. PRs that mix unrelated cleanups
   tend to get bounced back.

## Reporting security issues

See `SECURITY.md`. Please don't open public issues for security bugs.
