# Safety model

AWO sits between two systems that can both go wrong: an LLM-driven
agent that can hallucinate, and a shell that will happily execute
anything. Its job is to make sure neither side can damage the host
repo or quietly produce a bad change that looks fine on the surface.

This document is the full safety contract — what AWO refuses to do,
what it requires before recommending a change, and how those rules
are enforced in code.

## The three load-bearing rules

1. **Verification command exit codes are the only trusted signal of
   success.** Agent self-reports ("I ran the tests, they passed") are
   stored as advisory metadata in the proof pack but never gate the
   recommendation. If the agent says it passed and `go test ./...`
   exits non-zero, AWO records `failed_verification`.
2. **AWO never mutates state outside its sandbox.** Worktree creation
   and deletion are bounded to paths under `cfg.WorktreeBaseDir`
   (default `.awo/worktrees`). Branches are bounded to
   `cfg.BranchPrefix` (default `awo`). The host repo's `HEAD`,
   working tree, hooks, and remotes are never touched.
3. **Human review is always required.** AWO has no merge button,
   never auto-commits, never auto-pushes, and never calls `gh pr
   create`. `awo pr prepare` writes a `pr-description.md` you copy
   into a PR you open yourself.

These rules survive every refactor. Tests in `internal/safety` and
`internal/orchestrator` exist specifically to break the build if a
change quietly violates them.

## A note on verification commands

Verification commands are **shell commands** that AWO executes inside
the candidate's worktree. AWO does not parse them, does not
sandbox them beyond chdir, and does not filter their output. Whatever
you put in `--verify` or `cfg.DefaultVerifyCommands` runs with your
shell's permissions on your machine.

That is intentional — it keeps verification honest, language-agnostic,
and trivially extensible. But it also means:

- **Treat verify commands like CI.** Pin them to commands you would
  run yourself in CI: `go test ./...`, `pnpm test`, `cargo test`,
  `pytest -q`. Avoid commands with side effects on shared resources
  (network calls to staging, writes to a real database).
- **Keep them in `awo.config.json` checked into the repo** when you
  can, so a misconfigured `--verify` flag can't silently turn a
  failing run into a passing one.
- **Don't blindly run a verify command pulled from somewhere else.**
  An attacker who can convince you to set `--verify "rm -rf $HOME"`
  has already won; AWO will not save you from that.

## Path containment

Every filesystem write goes through `internal/safety`:

- `safety.EnsureInside(root, target)` — returns `ErrOutsideRoot` if
  `target` does not resolve inside `root`. Used everywhere AWO opens
  a file.
- `safety.MustBeUnder(parent, child)` — strict; equal paths return
  an error. Used by worktree cleanup so the cleanup of the parent
  cannot accidentally delete the parent.
- `safety.SafeJoin(base, parts...)` — rejects empty components,
  absolute components, and any `..` segment. Used when joining
  caller-supplied identifiers like run ids and agent names.

The net effect: a hostile run id like `../../etc` cannot escape
`.awo/runs/`, and a hostile prompt cannot trick AWO into deleting
`/`.

## Protected paths

`cfg.Safety.ProtectedPaths` is a list of glob patterns. When the
final diff touches any of them, AWO escalates the recommendation to
`needs_human_attention` regardless of whether verification passed.

Default patterns:

```
auth/**
payments/**
migrations/**
infra/**
.github/workflows/**
**/.env*
**/*secret*
**/*credential*
**/*permission*
```

Glob semantics (implemented in `internal/safety/protected.go`):

- `*` matches a single path segment, no slashes.
- `**` matches any number of segments, including zero.
- `?` matches a single character.
- A trailing slash makes the pattern a directory prefix.
- A bare name with no slashes matches the basename or any path
  suffix — `*.env` matches `.env` *and* `app/.env.production`.

You can extend the list in `awo.config.json`:

```json
{
  "safety": {
    "protectedPaths": [
      "auth/**",
      "payments/**",
      "migrations/**",
      "infra/**",
      ".github/workflows/**",
      "**/.env*",
      "**/*secret*",
      "**/*credential*",
      "**/*permission*",
      "src/billing/**",
      "ops/runbooks/**"
    ]
  }
}
```

## Patch size

`cfg.Safety.MaxChangedFiles` (default `50`) caps how big a patch AWO
is willing to recommend without escalation. When the candidate diff
touches more files than that, the recommendation is escalated to
`too_large_for_auto_review`.

This is a heuristic, not a hard refusal. The patch is still produced
and the run completes — it's the *recommendation* that changes, so a
human knows to read the diff in detail rather than skim.

## Recommendation ladder

`internal/orchestrator` and `internal/safety` cooperate to compute
exactly one `domain.Recommendation` per run. From most severe to
least severe:

| Recommendation | When | What it means |
| -------------- | ---- | ------------- |
| `failed_verification` | Any verify command exits non-zero | Don't merge. The patch broke something. |
| `needs_revision` | Reviewer said `needs_revision` or `reject` (writer-reviewer mode) | A different model thinks the patch isn't ready. |
| `needs_human_attention` | Patch touches a protected path; or competitive scores tied within `TieEpsilon` | Don't merge without an extra pair of eyes. |
| `too_large_for_auto_review` | Patch exceeds `safety.maxChangedFiles` | Merge possible, but skim is not enough — read it. |
| `ready_for_human_review` | None of the above | Looks fine; still requires a human. |

If multiple conditions apply, the most severe wins.

## Reviewer-mode constraints

In `writer-reviewer` mode the reviewer agent runs in its own
read-only worktree, separate from the writer's. AWO enforces that:

- The reviewer prompt explicitly forbids edits ("Review the diff
  only. Do not modify any files.").
- After the reviewer exits, AWO inspects the reviewer's worktree.
  Any files the reviewer modified are surfaced as warnings in
  `proof-pack.md` — they are **never** merged into the writer's
  diff.
- Agents cannot inspect each other's worktrees: each agent's `cwd`
  is its own worktree, and prompts forbid cross-worktree reads.

## Competitive-mode constraints

- Both competitors run in fully independent worktrees, branched from
  the same base ref.
- Scoring is **deterministic and explainable**. `ScoreCandidate` is a
  pure function — `comparison.md` lists every weight that contributed
  to the final score.
- There is no LLM judge. Adding one is explicitly out of scope until
  the deterministic version has been used in anger long enough to
  understand its failure modes.

## Log redaction

When `cfg.Safety.RedactLogs` is `true` (the default), AWO scrubs
common secret patterns out of agent stdout/stderr before writing
them to disk. This is a best-effort defense in depth, not a
substitute for not handing secrets to AWO in the first place.

## Things AWO refuses to do

- Auto-commit, auto-push, auto-merge, force-push.
- Open pull requests on your behalf (`awo pr prepare` writes
  text only).
- Delete files outside `cfg.WorktreeBaseDir`.
- Touch branches whose names don't begin with `cfg.BranchPrefix`.
- Modify the host repo's `HEAD` or working tree.
- Run agent CLIs in the host repo's working tree (always inside a
  worktree).
- Use an LLM to score, judge, or rank agent output.
- Trust agent self-reports as a substitute for verification.
- Let one agent see another agent's worktree.

If you find AWO doing any of these, file a bug. They are bugs, not
features.
