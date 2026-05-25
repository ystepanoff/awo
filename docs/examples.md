# Worked examples

Every example below targets the bundled fixture so you can paste
commands verbatim. The fixture is a tiny self-contained Go module
with a deliberate edge case (a `Divide` that panics on zero), which
gives agents something concrete to reason about and gives you a
clear signal when the work is correct.

## 0. Materialize the fixture

From inside the AWO repo (or any other repo where you've installed
AWO):

```sh
awo examples create-fixture
```

This writes `.awo/fixtures/sample-go-app/`, initializes it as its
own git repo with one commit, and prints a handoff with the next
commands. It will refuse to overwrite an existing fixture unless
you pass `--force`, and even with `--force` it only overwrites
directories that contain the `.awo-fixture` marker — unrelated user
data is left alone.

```sh
cd .awo/fixtures/sample-go-app
awo init
go test ./...   # baseline should pass
```

`go test ./...` should pass before any agent run. That's the
known-good starting point: any failure after a run is the agent's,
not the fixture's.

## 1. Single mode (Claude)

```sh
awo run "add a test that exercises Divide with a zero divisor and asserts the panic" \
    --mode single \
    --agent claude \
    --verify "go test ./..."
```

What happens:

1. AWO carves a worktree under `.awo/worktrees/<run-id>-writer/`
   on a fresh `awo/<id>-writer` branch.
2. The writer prompt is rendered and Claude is invoked with `cwd`
   pinned to that worktree.
3. AWO runs `go test ./...` inside the worktree. Exit code 0 means
   the new test compiles and passes.
4. AWO writes `.awo/runs/<run-id>/{run.json, proof-pack.md, summary.md, diff.patch, agents/, verify/}`.

To see what landed:

```sh
LATEST=$(ls -t .awo/runs | head -n 1)
cat .awo/runs/$LATEST/summary.md
cat .awo/runs/$LATEST/proof-pack.md
git -C .awo/worktrees/$LATEST-writer diff
```

If you decide to keep the change, commit it from the worktree
yourself or copy `diff.patch` into your real working tree — AWO will
not do that for you.

## 2. Single mode (Codex)

Same task, different backend:

```sh
awo run "add a test that exercises Divide with a zero divisor and asserts the panic" \
    --mode single \
    --agent codex \
    --verify "go test ./..."
```

The artifact layout is identical; only `agents/codex-writer/` differs.

## 3. Writer-reviewer mode

```sh
awo run "make Divide return an error instead of panicking on zero" \
    --mode writer-reviewer \
    --primary claude \
    --reviewer codex \
    --verify "go test ./..."
```

What happens beyond single mode:

- A second worktree (`.awo/worktrees/<run-id>-reviewer/`) is created
  from the same base ref as the writer's.
- After the writer is done, AWO captures the writer's diff and
  invokes the reviewer with the diff in its prompt.
- The reviewer's `AWO_REVIEW_JSON` block feeds the recommendation
  ladder. A `needs_revision` or `reject` from the reviewer escalates
  the run to `needs_revision` — even if `go test ./...` passed.
- If the reviewer modified files in its own worktree (it shouldn't),
  those edits are surfaced as warnings in `proof-pack.md` and never
  applied to the writer's diff.

Inspect:

```sh
LATEST=$(ls -t .awo/runs | head -n 1)
cat .awo/runs/$LATEST/proof-pack.md   # includes the reviewer's findings
ls .awo/runs/$LATEST/agents/          # claude-writer/  codex-reviewer/
```

## 4. Competitive mode

```sh
awo run "make Divide return an error instead of panicking on zero" \
    --mode competitive \
    --competitors claude,codex \
    --verify "go test ./..."
```

What happens:

- Two worktrees are created in parallel, one per competitor.
- Both agents run independently on the same task. They cannot see
  each other's worktrees.
- AWO runs `go test ./...` against each candidate and scores them
  with a deterministic function (verification status, file count,
  diff size, tests added, protected-paths hits, parsed agent
  confidence). The weights are listed in `internal/orchestrator/scoring.go`.
- `comparison.md` records the score breakdown for both candidates so
  you can disagree with the ranking on the spot.

Inspect:

```sh
LATEST=$(ls -t .awo/runs | head -n 1)
cat .awo/runs/$LATEST/comparison.md
cat .awo/runs/$LATEST/proof-pack.md
git -C .awo/worktrees/$LATEST-claude diff   # candidate A
git -C .awo/worktrees/$LATEST-codex diff    # candidate B
```

When the scores tie within `TieEpsilon`, AWO records the
recommendation as `needs_human_attention` and does not pick a
winner. That is on purpose: a thin scoring margin is the most likely
place for an explainable function to be wrong, and that's exactly
where a human should look.

## 5. Preparing a PR

After a successful run you like:

```sh
awo pr prepare --run-id <id>
```

This writes `.awo/runs/<id>/pr-description.md` you can paste into
your platform's "open PR" UI. It does not call `gh` and does not
push. Pushing and PR creation are deliberately yours.

## A note on verify commands

Every example above uses `go test ./...`. The string is **a shell
command** that AWO runs inside the candidate's worktree. AWO does
not parse it, sandbox it, or limit what it can do. Pin verification
to commands you would run yourself in CI; avoid commands with side
effects on shared resources. See [`safety.md`](safety.md) for the
full discussion.

## Cleaning up worktrees

```sh
awo worktrees list
awo worktrees cleanup --run-id <id>
```

Worktrees stay around after a run on purpose: you may want to
`git -C <worktree> diff`, `git -C <worktree> log`, or run additional
checks before you decide what to do. When you're done, `awo
worktrees cleanup` removes the worktree and its branch — bounded
strictly to paths under `cfg.WorktreeBaseDir`.
