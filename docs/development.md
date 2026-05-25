# Development

AWO is a single Go binary with no runtime dependencies beyond `git`,
`claude`, and `codex` on `$PATH`. There is no daemon, no service, no
remote backend.

## Build

```sh
git clone https://github.com/ystepanoff/awo.git
cd awo
go build ./cmd/awo
./awo --help
```

To install into `$GOBIN`:

```sh
go install ./cmd/awo
```

Requires Go 1.22+.

## Test

```sh
go test ./...
```

The suite is fully offline:

- No real `claude` or `codex` invocations. The agent layer is
  mocked at the `execx` boundary in tests.
- No real network calls.
- No host-repo mutation. `t.TempDir()` is used everywhere a test
  needs a worktree-shaped fixture.

If you add a test that shells out to a real CLI or touches the host
repo, that's a bug — the rest of the suite would become flaky in CI
or unsafe in someone's working tree.

## Package layout (developer's view)

| Package | Responsibility | Don't put |
| ------- | -------------- | --------- |
| `cmd/awo` | Binary entry point only | Anything beyond cobra wiring |
| `internal/cli` | Cobra commands, flag parsing, user-visible output | Subprocess execution, scoring, file IO that isn't UX |
| `internal/config` | `AwoConfig`, `Default()`, `Load()`, `Validate()` | Anything that does IO outside `Load`/`Save` |
| `internal/domain` | Cross-cutting types (`AgentKind`, `Recommendation`, `RunReport`) | Behavior — domain types are pure data |
| `internal/gitx` | Git wrappers (worktree create/list/remove, status, diff) | Anything that isn't a thin shell over `git` |
| `internal/execx` | Subprocess execution with timeouts and captured I/O | Agent-specific logic |
| `internal/agents` | Adapters for `claude` and `codex`, prompt templates, JSON parsing | Orchestration |
| `internal/orchestrator` | The three modes; verification; deterministic scoring | Subprocess execution beyond verify |
| `internal/safety` | Path containment, protected paths, redaction | Orchestration |
| `internal/artifacts` | `.awo/runs/<run-id>/` filesystem layout | Rendering |
| `internal/reports` | Proof pack / summary / comparison rendering | Decisions |
| `internal/prhelper` | `awo pr prepare` rendering | Anything that calls `gh` |
| `internal/examples` | Fixture generator | Code with side effects on the host repo |
| `internal/runid` | Run id generation | Anything else |

The dependency direction is one-way: `cli` and `orchestrator` depend
on the lower layers, never the reverse. If you find yourself wanting
to import `cli` from `orchestrator`, that's a sign the boundary is
about to leak.

## Conventions

- **No emojis in code, comments, or output.** AWO output is read in
  CI-style logs by humans; emojis don't survive monospace tools well
  and add noise.
- **Comments explain *why*, not *what*.** Prefer expressive names
  and short functions.
- **Hard rules go in code, not just docs.** If a safety property
  needs to hold (e.g. "deletes are bounded to `.awo/worktrees`"),
  there should be a test in `internal/safety` that breaks if a
  future refactor violates it.
- **No new dependencies without justification.** AWO is small on
  purpose; every dependency is a future supply-chain concern.
- **Templates over string concatenation.** Prompts and reports use
  `text/template` with `embed.FS` so what ships is what runs.

## Inspecting artifacts during development

When you're debugging a run:

```sh
LATEST=$(ls -t .awo/runs | head -n 1)

# Machine-readable record
cat .awo/runs/$LATEST/run.json | jq

# Human reports
less .awo/runs/$LATEST/proof-pack.md
cat  .awo/runs/$LATEST/summary.md
cat  .awo/runs/$LATEST/comparison.md   # competitive only

# What the agents actually did
ls   .awo/runs/$LATEST/agents/
cat  .awo/runs/$LATEST/agents/claude-writer/prompt.md
cat  .awo/runs/$LATEST/agents/claude-writer/stdout.log

# What verification actually did
ls   .awo/runs/$LATEST/verify/
cat  .awo/runs/$LATEST/verify/000/exit
cat  .awo/runs/$LATEST/verify/000/stdout.log

# The candidate diff
cat  .awo/runs/$LATEST/diff.patch
git -C .awo/worktrees/<id> diff
git -C .awo/worktrees/<id> log --oneline
```

`run.json` is the canonical record — every other rendered file is
derived from it. When `proof-pack.md` and `run.json` disagree, treat
`run.json` as authoritative and file a bug against the renderer.

## Running against the bundled fixture

The fastest dev loop is to dogfood the fixture rather than your real
repo:

```sh
go build ./cmd/awo
./awo examples create-fixture
cd .awo/fixtures/sample-go-app
../../awo init
../../awo run "add an edge-case test for Divide" \
    --mode single --agent claude \
    --verify "go test ./..."
```

The fixture is its own git repo with one commit, so any branches /
worktrees AWO creates while you're iterating on it stay inside the
fixture. You can wipe and recreate it (`awo examples create-fixture
--force`) any time the state gets confusing.

## A note on shell commands

Several places in AWO accept caller-supplied shell strings:
`--verify`, `cfg.DefaultVerifyCommands`, the agent CLI commands.
**These are not parsed, not sandboxed, not filtered.** If you're
adding a feature that takes another shell string from the user,
follow the same convention — but be very deliberate about *what
input source* feeds it. The tests in
`internal/orchestrator/verify_test.go` and `internal/agents` exist
in part to make sure no other code path quietly starts shelling out
based on agent-provided input. If your change adds one, that's a
review blocker; agents must not be able to influence the shell
command line.

## Releasing

There is no release process yet. Tag pushes to `main` are how you
ship today. A signing/attestation pass over `run.json` is on the
roadmap but explicitly not implemented — don't ship artifacts as if
they were trusted across machines.

## Filing bugs

Open an issue with:

- The output of `awo doctor`.
- The contents of `awo.config.json` (after redacting anything
  sensitive).
- The relevant run's `run.json`, `proof-pack.md`, and the
  `agents/<agent>-<role>/stderr.log`.
- A description of what you expected and what you got.

If your bug is "AWO did something to a file outside its sandbox",
that's a security issue — please flag it in the issue title.
