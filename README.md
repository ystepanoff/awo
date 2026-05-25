<p align="center">
  <img src="docs/images/awo-logo.png" alt="AWO logo" width="240">
</p>

# AWO — Agent Worktree Orchestrator

AWO is a local Go CLI that coordinates Claude Code and Codex across isolated
git worktrees, runs deterministic verification commands against the result,
and produces a structured artifact bundle (`run.json`, `proof-pack.md`,
`diff.patch`, agent stdout/stderr) that a human reviews before merging.

AWO does not commit, push, merge, or open pull requests on your behalf.

## What AWO is

A small, opinionated wrapper around two existing CLI tools — `claude` and
`codex` — that:

- Carves out an isolated git worktree per agent so agent edits never
  touch your working tree directly.
- Runs the agents you choose against the same task, in one of three modes
  (`single`, `writer-reviewer`, `competitive`).
- Runs the verification commands *you* configure (e.g. `go test ./...`)
  inside the worktree, and treats the **exit code as the only trusted
  signal** of success.
- Writes a deterministic artifact bundle under `.awo/runs/<run-id>/` so
  the run is auditable after the fact.
- Hands the result back to you as a candidate change to review,
  commit, push, and PR — manually.

## Why isolated worktrees matter

Letting an agent edit your live working tree is high-blast-radius:
mid-run failures leave you with a half-applied change, parallel
processes (your editor, your dev server) see partial state, and there
is no clean "undo" if the agent goes off the rails.

`git worktree` lets each agent run in its own checkout of your repo on
its own branch, sharing the same `.git` directory. AWO uses that to:

- Run multiple agents in parallel (competitive mode) without them
  stepping on each other.
- Bound the blast radius: AWO will only ever delete paths under
  `.awo/worktrees/`, never your real source tree.
- Capture an exact diff per agent for review.

## Why Claude + Codex as separate backends

Claude Code and Codex have meaningfully different strengths, prompt
ergonomics, and failure modes. Forcing one to ape the other loses
information — you want the *real* output of each.

- **Single mode** lets you pick the better tool for the task at hand.
- **Writer-reviewer mode** uses one as the writer and the other as the
  reviewer, which surfaces blind spots that a same-model review would
  not catch.
- **Competitive mode** runs both on the same task and ranks them with
  a deterministic, explainable scoring function — never with an LLM
  judge.

The two backends are wired through small adapter layers so adding a
third (or swapping CLI versions) is a config change, not a code change.

## Installation from source

Requires Go 1.22+.

```sh
# install into $GOBIN
go install github.com/awo-dev/awo/cmd/awo@latest

# or build a local binary
git clone https://github.com/ystepanoff/awo.git
cd awo
go build ./cmd/awo
```

The `awo` binary is the only thing AWO ships — no daemon, no service,
no remote dependencies.

## Prerequisites

- `git` on `$PATH`
- Go 1.22+ (only for building from source)
- The Claude CLI (`claude`) installed and authenticated:
  <https://docs.anthropic.com/en/docs/claude-code>
- The Codex CLI (`codex`) installed and authenticated:
  <https://github.com/openai/codex>

`awo doctor` checks all four and prints what's missing or
unauthenticated.

## Quick start

```sh
# Inside your project's git repo:
awo init                                  # scaffold .awo/, awo.config.json, CLAUDE.md, AGENTS.md
awo doctor                                # confirm git/go/claude/codex are reachable
awo run "add tests for calculator" \
    --mode single \
    --agent claude \
    --verify "go test ./..."
```

Want a safe sandbox before pointing AWO at your real code?
`awo examples create-fixture` materializes a tiny self-contained Go
module under `.awo/fixtures/sample-go-app/` (its own git repo) so you
can dogfood every mode without risk.

## Single mode

One agent does the work end-to-end inside a writer worktree.
Verification runs in the same worktree.

```sh
awo run "fix the off-by-one in pagination" \
    --mode single \
    --agent claude \
    --verify "go test ./..."

awo run "fix the off-by-one in pagination" \
    --mode single \
    --agent codex \
    --verify "go test ./..."
```

Use this when you've already decided which agent is best suited to the
task.

## Writer-reviewer mode

A primary agent writes the change in a writer worktree; a different
agent reviews the writer's diff in a separate read-only worktree
carved from the same base. The reviewer's findings are surfaced in the
proof pack, but **the reviewer cannot modify the writer's worktree** —
any files it touches in its own worktree become a warning, not a
patch.

```sh
awo run "fix checkout validation" \
    --mode writer-reviewer \
    --primary claude \
    --reviewer codex \
    --verify "go test ./..."
```

Use this when you want a different model's eyes on the change before
you spend your own attention on it.

## Competitive mode

Two agents attempt the same task in parallel in independent worktrees.
AWO runs verification against each, scores them with a deterministic
function (verification status, diff size, test files added, protected
paths touched), and surfaces the comparison.

```sh
awo run "migrate date utility usage" \
    --mode competitive \
    --competitors claude,codex \
    --verify "go test ./..."
```

There is **no LLM judge**. The scoring is intentionally
explainable — the proof pack lists the inputs to every score so you can
disagree with the ranking on the spot.

## Safety model

AWO's safety stance is captured in three rules:

1. **Verification command exit codes are the only trusted signal of
   success.** Agent self-reports ("I ran the tests and they passed")
   are persisted as advisory metadata only.
2. **AWO never mutates state outside its sandbox.** Worktree deletions
   are constrained to paths under `.awo/worktrees/`. Branches outside
   `config.branchPrefix` (default `awo`) are never touched. The outer
   repo's `HEAD` and working tree are never modified by AWO.
3. **Human review is always required.** AWO has no merge button. The
   final step of every run is a recommendation to a human, who is the
   only thing that turns AWO output into a real PR.

Additional hard rules implemented in code:

- Protected paths (default: `auth/**`, `payments/**`, `migrations/**`,
  `infra/**`, `.github/workflows/**`, `**/.env*`, `**/*secret*`,
  `**/*credential*`, `**/*permission*`) escalate the recommendation to
  `needs_human_attention` whenever they are touched.
- Patches that exceed `safety.maxChangedFiles` (default 50) escalate
  to `too_large_for_auto_review`.
- Reviewer-side worktree edits in writer-reviewer mode are detected
  and surfaced as warnings, never applied.
- Agents are not allowed to inspect each other's worktrees.

See [`docs/safety.md`](docs/safety.md) for the full list.

## What AWO does not do

- It does **not** auto-merge.
- It does **not** auto-commit.
- It does **not** push to remotes.
- It does **not** open pull requests. (`awo pr prepare` writes a
  `pr-description.md` you can paste; it does not call `gh`.)
- It does **not** delete files outside `.awo/worktrees/`.
- It does **not** guarantee correctness. Agents make mistakes; tests
  miss things; the recommendation is a heuristic.
- It is **not** a replacement for human code review.

## Artifact layout

Every run writes a directory under `.awo/runs/<run-id>/`:

```
.awo/runs/20260525-094200-abc123/
├── run.json            # canonical machine-readable record (RunReport)
├── proof-pack.md       # long-form human report
├── summary.md          # short-form human summary
├── comparison.md       # competitive mode only
├── pr-description.md   # written by `awo pr prepare` (not by run)
├── diff.patch          # the diff produced by the selected candidate
├── agents/
│   └── <agent>-<role>/ # per-agent stdout, stderr, prompt, command
└── verify/
    └── 000/            # per-verification-command stdout, stderr, exit
```

Inspecting after a run:

```sh
ls .awo/runs/$(ls -t .awo/runs | head -n 1)/
cat .awo/runs/<run-id>/proof-pack.md
git -C <worktree-path> diff
```

`awo worktrees list` shows the worktrees AWO is tracking; `awo
worktrees cleanup --run-id <id>` removes them when you no longer need
them.

## Configuration reference

`awo init` writes `awo.config.json` with sensible defaults. The full
schema and recommended overrides are in
[`docs/configuration.md`](docs/configuration.md). Highlights:

| Field                                  | Default            | What it controls                                              |
| -------------------------------------- | ------------------ | ------------------------------------------------------------- |
| `branchPrefix`                         | `awo`              | All AWO branches start with this prefix; nothing else is touched. |
| `worktreeBaseDir`                      | `.awo/worktrees`   | Where worktrees live; deletions are bounded to this path.     |
| `artifactDir`                          | `.awo/runs`        | Where run artifacts are written.                              |
| `defaultVerifyCommands`                | `[]`               | Commands run when `--verify` is not passed.                   |
| `agents.claude.command` / `writerArgs` / `reviewerArgs` | `claude` / `-p --permission-mode acceptEdits` / `-p --permission-mode plan` | Per-role argv. AWO runs every agent non-interactively; if the CLI hits an approval prompt the run fails closed with `permission_required`. |
| `agents.codex.command` / `writerArgs` / `reviewerArgs` | `codex` / `exec --sandbox workspace-write --ask-for-approval never` / `exec --sandbox read-only --ask-for-approval never` | Same. AWO refuses dangerous bypasses (`bypassPermissions`, `danger-full-access`, etc.). |
| `safety.maxChangedFiles`               | `50`               | Patches above this escalate to `too_large_for_auto_review`.   |
| `safety.protectedPaths`                | (9 globs)          | Hits escalate to `needs_human_attention`.                     |
| `safety.requireConfirmationForProtectedPaths` | `true`      | Reserved for future interactive prompts.                      |

Run `awo config print` to see the effective config (file values layered
on top of defaults).

## Roadmap

Short term:

- More verification adapters beyond shell commands (lint, typecheck).
- Pluggable scoring weights for competitive mode.
- A "rerun" subcommand that resumes a failed run from artifacts.
- Iteration support (`safety.maxIterations` is currently fixed at 1).

Longer term:

- Adapters for additional agent backends (Gemini CLI, etc.).
- A small TUI for inspecting runs in place.
- A signing/attestation pass over `run.json` so artifacts can be
  trusted across machines.

Explicitly out of scope:

- LLM-as-judge scoring.
- Auto-commit, auto-push, auto-merge — ever.
- Anything that mutates state outside the configured AWO sandbox.

## Docs

- [`docs/architecture.md`](docs/architecture.md) — package layout and
  how a run flows through AWO.
- [`docs/safety.md`](docs/safety.md) — the full safety model and the
  invariants tests enforce.
- [`docs/prompts.md`](docs/prompts.md) — the prompt contract AWO uses
  with Claude and Codex.
- [`docs/examples.md`](docs/examples.md) — worked examples for every
  mode using the bundled fixture.
- [`docs/configuration.md`](docs/configuration.md) — full
  `awo.config.json` reference.
- [`docs/development.md`](docs/development.md) — how to build, test,
  and contribute.

## License

See [`LICENSE`](LICENSE).
