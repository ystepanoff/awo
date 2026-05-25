# AWO proof pack — 20260521-120000-abc123

- mode: `single`
- agent: `claude` (role=writer)
- branch: `awo/run/claude-writer`
- worktree: `.awo/worktrees/run/claude-writer`
- status: **failed**
- started: 2026-05-21T12:00:00Z
- finished: 2026-05-21T12:00:02Z
- recommendation: **needs_human_attention**

> AWO ran agents in **non-interactive** mode. CLIs were not allowed to
> prompt for approval; they either had pre-granted permission for the
> operation or the run failed closed.

## Task

add /health endpoint

## ⚠ Agent failure (permission_required)

agent appears to have hit an interactive permission/approval prompt (stderr: "Error: permission required to edit /etc/passwd")

The agent CLI tried to prompt for an interactive approval AWO cannot
grant. The most common fixes:

- Use the per-role args in `awo.config.json` to pre-grant the
  permission the writer needs (Claude: `agents.claude.writerArgs`;
  Codex: `agents.codex.writerArgs`).
- For Claude, ensure `--permission-mode` is one of `acceptEdits` or
  `default` with explicit `--allowedTools`. AWO refuses
  `bypassPermissions` and `--dangerously-skip-permissions`.
- For Codex, ensure `--ask-for-approval never` and a writable sandbox
  (`--sandbox workspace-write`). AWO refuses `danger-full-access`.
- Confirm flag names against your installed `claude --help` /
  `codex --help`; CLI flags evolve.


## Changed files

_no files changed_

## Verification

_not verified_

## Diff

Patch: `.awo/runs/20260521-120000-abc123/diff.patch`

---

AWO did not commit, push, merge, or auto-approve this change.
AWO never auto-merges, auto-commits, or auto-pushes. Review the worktree before any human commit.

Recommended next step: this run needs a human look — see the failure section and protected-path warnings (if any) before proceeding.
