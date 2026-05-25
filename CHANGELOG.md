# Changelog

All notable changes to AWO will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- **Agents now actually receive the prompt.** Previously the rendered
  prompt was written to `prompt.md` in the agent's artifact directory
  but never piped to the CLI's stdin, so `claude --print` exited
  immediately with `Input must be provided either through stdin or as
  a prompt argument`. `execx.CommandSpec` now has a `Stdin` field that
  both adapters populate; the prompt is delivered on stdin in
  non-interactive mode.
- **Claude can actually edit files in its worktree.** The default
  Claude args are now `["-p", "--permission-mode", "acceptEdits"]`.
  Without `acceptEdits`, the CLI prompts for permission on every
  Edit and there is no terminal to answer, so the writer would exit
  cleanly with an empty diff. `acceptEdits` accepts file writes
  inside the cwd only; AWO's cwd is always an isolated worktree
  under `.awo/worktrees/`, so this matches the existing trust
  boundary. It does not bypass permissions for bash or for paths
  outside cwd.

### Changed

- **Recommendation when a run produces no diff.** Previously a run
  whose worktree ended up unchanged was labeled
  `ready_for_human_review` because verification (run against unchanged
  code) "passed". The verdict now distinguishes:
  - agent failed (non-zero exit / timeout) AND no changes →
    `needs_human_attention` so a human looks at the agent log;
  - agent succeeded but produced no changes → `no_recommendation`
    because there is no candidate for a human to review.

## [0.1.0] - 2026-05-25

Initial public release.

### Added

- Three orchestration modes for coding-agent runs inside isolated git
  worktrees: `single`, `writer-reviewer`, and `competitive`.
- `awo init`, `awo run`, `awo doctor`, `awo examples create-fixture`,
  and `awo pr prepare` commands.
- Safety primitives: path containment (`safety.SafeJoin`,
  `safety.MustBeUnder`), protected-path globs, configurable changed-file
  cap, log redaction.
- Worktree isolation under `.awo/worktrees/<run-id>/`. AWO never
  commits, merges, pushes, fetches, or deletes outside `.awo/worktrees`.
- Deterministic, explainable competitive scoring (no LLM judge).
- Recommendation ladder:
  `failed_verification > needs_revision > needs_human_attention >
  too_large_for_auto_review > ready_for_human_review`.
- Verification commands run in the writer worktree only; exit code is
  the only trusted success signal.
- Atomic artifact writes (`run.json`, `proof-pack.md`, `summary.md`,
  `comparison.md`, `diff.patch`, per-agent stdout/stderr).
- Marker-managed config sections so `awo init --force` is idempotent.
- Embedded prompt and report templates (`text/template` + `embed.FS`).
- Adapters for `claude` and `codex` CLIs that build commands without
  going through a shell. Verification commands are the only place AWO
  uses `sh -c` / `cmd /C`.
- Documentation: `README.md`, `docs/architecture.md`, `docs/safety.md`,
  `docs/prompts.md`, `docs/examples.md`, `docs/configuration.md`,
  `docs/development.md`, `docs/run-json-schema.md`.
- `Makefile` with `build`, `test`, `vet`, `fmt`, `lint`, `tidy`,
  `install`, `clean` targets.

### Known limitations

- AWO shells out to local CLIs.
- CLI flags may need adjustment depending on installed Claude/Codex
  versions.
- AWO does not guarantee correctness.
- AWO does not replace code review.
- AWO does not prevent all possible agent mistakes.

### Future work

- `goreleaser` configuration for prebuilt cross-platform binaries.
- Signing / attestation pass over `run.json`.
- Adapter support for additional coding-agent CLIs.

[Unreleased]: https://github.com/ystepanoff/awo/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ystepanoff/awo/releases/tag/v0.1.0
