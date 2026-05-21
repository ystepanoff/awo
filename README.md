# AWO — Agent Worktree Orchestrator

AWO is a local Go CLI that coordinates Claude Code and Codex across isolated
git worktrees with deterministic verification.

## Status

v0.1 scaffold. Implemented:

- `awo --help`
- `awo doctor`
- `awo init`
- `awo config print`

The orchestration modes (`single`, `writer-reviewer`, `competitive`),
worktree management, and proof-pack rendering are scaffolded but not yet
wired into a `run` command.

## Safety

- Never auto-merges, auto-commits, or auto-pushes.
- Never deletes paths outside `.awo/worktrees/`.
- Trusts only verification command exit codes — not agent self-reports.

## Build

```
go build ./cmd/awo
go test ./...
```
