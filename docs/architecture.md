# Architecture

AWO is a small Go binary. It does not run as a daemon, hold open
connections, or persist state outside the project directory. Every run
is a one-shot process: parse args, plan worktrees, exec the agents,
exec verification, write artifacts, exit.

## Package layout

```
cmd/awo/                 # entry point — wires the cobra root command
internal/
  cli/                   # cobra commands (init, doctor, run, pr, examples, ...)
  config/                # AwoConfig schema, Default(), Load(), Validate()
  domain/                # cross-cutting types: AgentKind, Recommendation, RunReport
  gitx/                  # thin wrappers around `git` (worktree, status, diff)
  execx/                 # subprocess execution with timeouts and captured I/O
  agents/                # adapters for `claude` and `codex` + prompt templates
  orchestrator/          # the three modes: single, writer-reviewer, competitive
  safety/                # path containment, protected-paths matching, redaction
  artifacts/             # filesystem layout for .awo/runs/<run-id>/
  reports/               # proof-pack.md / summary.md / comparison.md rendering
  prhelper/              # `awo pr prepare` — pr-description.md rendering
  examples/              # `awo examples create-fixture` — sample-go-app generator
  runid/                 # deterministic run id generation
```

The packages above are the only seams the rest of the code talks
through. `cli` knows nothing about subprocesses; `orchestrator` knows
nothing about cobra; `safety` knows nothing about agents. That keeps
each package independently testable and keeps the trust boundaries
visible from the import graph.

## How a run flows through AWO

For `awo run "<task>" --mode <mode> ...`:

1. **Resolve config.** `config.LoadOrDefault("awo.config.json")` layers
   the user's JSON onto `config.Default()`. Missing fields keep their
   default value.
2. **Plan worktrees.** `gitx` creates one or two worktrees under
   `cfg.WorktreeBaseDir` (default `.awo/worktrees/`) with branch names
   prefixed by `cfg.BranchPrefix` (default `awo`). The base ref is the
   current `HEAD`. Worktrees are siblings — they never overlap.
3. **Render prompts.** `agents.BuildWriterPrompt` (or
   `BuildCompetitorPrompt` / `BuildReviewerPrompt`) fills in
   `templates/*.md.tmpl` with the task, worktree path, and protected
   paths. The reviewer also sees the writer's diff.
4. **Exec the agent CLI(s).** `agents` shells out to `claude` or
   `codex` with `cwd` pinned to that agent's worktree. stdout/stderr
   are streamed to disk under `agents/<agent>-<role>/`.
5. **Parse the agent JSON.** Each prompt asks for an `AWO_RESULT_JSON`
   (writer/competitor) or `AWO_REVIEW_JSON` (reviewer) trailer.
   `agents.ParseLast*` extracts the **last** matching block. The fields
   are persisted as **advisory** metadata only — never trusted to gate
   anything.
6. **Run verification.** `orchestrator/verify.go` exec's each
   `--verify` (or `cfg.DefaultVerifyCommands`) entry inside the
   candidate's worktree. **Exit code 0 = passed; anything else =
   failed.** That is the only signal AWO trusts.
7. **Score (competitive only).** `orchestrator/scoring.go` builds a
   `CandidateSnapshot` per competitor and runs `ScoreCandidate`. The
   scoring function is pure and deterministic — same inputs, same
   output, no clock, no network, no LLM.
8. **Compute the recommendation.** `safety` matches changed files
   against `cfg.Safety.ProtectedPaths`, compares the file count to
   `cfg.Safety.MaxChangedFiles`, and folds those into a
   `domain.Recommendation` (`failed_verification` →
   `needs_revision` → `needs_human_attention` →
   `too_large_for_auto_review` → `ready_for_human_review`).
9. **Render reports.** `reports` writes `run.json`, `proof-pack.md`,
   `summary.md`, and (in competitive mode) `comparison.md`. The diff
   captured from the winning worktree lands in `diff.patch`.
10. **Stop.** AWO exits with status reflecting the recommendation. It
    does not commit, push, merge, or open a PR.

## Inspecting artifacts after a run

Every run lands at `.awo/runs/<run-id>/`. To inspect the most recent:

```sh
# Latest run id
LATEST=$(ls -t .awo/runs | head -n 1)

# Quick read
cat .awo/runs/$LATEST/summary.md

# Long-form report
less .awo/runs/$LATEST/proof-pack.md

# The patch
git -C .awo/worktrees/<id> diff             # in the worktree
cat .awo/runs/$LATEST/diff.patch            # the captured copy

# Per-agent stdout/stderr
ls .awo/runs/$LATEST/agents/
cat .awo/runs/$LATEST/agents/claude-writer/stdout.log

# Per-verification command stdout/stderr/exit
cat .awo/runs/$LATEST/verify/000/exit
cat .awo/runs/$LATEST/verify/000/stdout.log
```

`run.json` is the canonical machine-readable record (a `domain.RunReport`)
and is what `awo pr prepare` consumes.

## Trust boundaries

- **Outside AWO's sandbox:** the host repo's `HEAD`, working tree,
  refs, hooks, and remotes. AWO never writes there.
- **Inside AWO's sandbox:** `cfg.WorktreeBaseDir` and `cfg.ArtifactDir`.
  Anything AWO deletes or rewrites is bounded to these.
- **Agent claims** (`tests_run`, `changed_files_intended`,
  `confidence`) cross the trust boundary into AWO. They are stored,
  shown to the human in the proof pack, and never used to *decide*
  anything. The deciding signal is the verification exit code and the
  list of files actually changed in the worktree (from `git status`).

If you are extending AWO, treat that boundary as load-bearing. New
features that move trust from "verification exit code" to "agent
claim" need an explicit, well-justified design discussion.
