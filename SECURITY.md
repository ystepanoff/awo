# Security policy

AWO orchestrates third-party coding-agent CLIs against your local
repository. Several of its safety properties are load-bearing; this
document explains the boundary it enforces, how to report bugs that
breach that boundary, and what is explicitly *out of scope*.

## Reporting a vulnerability

**Please do not open a public GitHub issue for security bugs.** Instead:

- Email the maintainer (see `git log` for the current address) with
  the subject line `awo security: <short summary>`, OR
- Open a private GitHub Security Advisory at
  https://github.com/ystepanoff/awo/security/advisories/new.

Please include:

- AWO version (`awo --version` or commit SHA).
- The output of `awo doctor` (after redacting anything sensitive).
- A minimal reproduction.
- The specific safety property you believe was violated (see below).

We aim to acknowledge reports within 7 days and to publish a fix or
mitigation before disclosing the underlying issue. AWO is a small,
volunteer-maintained project; please be patient with response times.

## What AWO promises (load-bearing safety properties)

These three rules are the boundary. Anything inside the boundary is
fair game; anything outside is a security bug.

### 1. AWO never mutates your git history

AWO does not commit, merge, push, fetch, or reset. It creates and
removes git **worktrees** under `.awo/worktrees/`, and it creates
branches that begin with `cfg.BranchPrefix` (default `awo`). It
never deletes branches outside that prefix, never overwrites your
working tree, and never invokes `git push` of any kind.

If you observe AWO mutating tracked branches, your remote, or files
outside `.awo/worktrees`, that's a security bug.

### 2. Deletion is bounded to `.awo/worktrees`

Every path AWO removes goes through `gitx.RemoveWorktree`, which calls
`safety.MustBeUnder(<repoRoot>/.awo/worktrees, target)`. The
`safety.IsSubpath` primitive resolves symlinks before comparing, so a
worktree path containing a symlink that points outside `.awo/worktrees`
will be rejected.

If you can persuade AWO to delete or overwrite a path outside
`.awo/worktrees/`, that's a security bug.

### 3. Agents cannot reach a shell

AWO invokes coding-agent CLIs (`claude`, `codex`) using
`exec.CommandContext` with explicitly separated `Command` and `Args`.
**The only place AWO uses a shell is verification commands.**
`execx.RunShellVerification` is the sole call site that runs
`sh -c <command>` (or `cmd /C` on Windows), and it accepts only
operator-supplied strings — `--verify` flags or
`cfg.DefaultVerifyCommands`. Agent stdout, agent self-reports, and
agent-suggested commands never reach a shell.

If you can find a code path that interpolates agent output into a
shell command line, that's a security bug.

## Other safety properties

These aren't part of the three load-bearing rules but are still
safety-relevant; bug reports for them are welcome.

- **Path containment.** All artifact writes go through
  `artifacts.Layout.WriteFileAtomic` / `WriteJSONAtomic`, which call
  `safety.MustBeUnder(layout.Root, path)` before writing. A bug that
  lets an artifact be written outside `<artifactDir>/<run-id>/` is a
  safety bug.
- **Atomic writes.** Artifacts are written to a temp file, fsync'd,
  closed, and renamed into place. A bug that leaves a half-written
  `run.json` after a crash is a safety bug.
- **Log redaction.** When `cfg.Safety.RedactLogs` is true,
  `safety.Redact` strips a known set of secret patterns from captured
  stdout/stderr before persisting. The pattern list is best-effort,
  not exhaustive — if you find a common secret format AWO misses,
  please report it.
- **Protected paths.** `cfg.Safety.ProtectedPaths` is a glob list
  matched against changed files. Hits do not block agents; they
  escalate the run's recommendation to `needs_human_attention`.
  Failing to surface a hit is a safety bug; an agent ignoring the
  prompt and editing a protected path is **not** — that's why the
  recommendation exists.
- **Patch size cap.** Runs whose changed-file count exceeds
  `cfg.Safety.MaxChangedFiles` are escalated to
  `too_large_for_auto_review`. Same reasoning: failing to surface the
  cap is a safety bug; agents producing large patches is the
  motivating case, not a bug.

## What is explicitly out of scope

AWO is an orchestrator, not a sandbox. The following are **not**
security guarantees and are not eligible for security advisories:

- **Agent correctness.** AWO does not guarantee agents produce
  correct, safe, or sensible code. AWO does not replace code review.
- **Verification correctness.** Verification commands are
  user-supplied shell strings. They run with the same privileges as
  AWO and can do anything you can do at the shell. AWO does not
  parse, sandbox, filter, or limit them. If you point AWO at a
  malicious `--verify` string, you have invoked it on yourself.
- **Agent CLI behavior.** AWO shells out to local CLIs (`claude`,
  `codex`). Bugs *in those tools* — including their network
  behavior, telemetry, or sandboxing — are not AWO bugs.
- **Telemetry / network egress by agents.** AWO does not block,
  proxy, or audit the network calls made by the agent CLIs it
  invokes. If you need offline operation, configure that at the
  agent or OS level.
- **CLI flag drift.** Agent CLI versions change. If a flag in the
  default config no longer matches your installed `claude` or
  `codex`, that's a configuration mismatch, not a security bug.
  Adjust `agents.<name>.command` / `args` in `awo.config.json`.
- **Adversarial agents.** AWO assumes agents are buggy, sloppy, and
  occasionally wrong. It does not assume they are actively hostile.
  An agent CLI that ignores its prompt, attempts to escape its
  worktree, or scans your filesystem is doing something AWO does not
  defend against — please report that to the upstream agent project.

## Threat model summary

- **Trusted:** AWO operator; the local OS; the `git` binary; the
  user's `awo.config.json`; the agent CLIs themselves.
- **Untrusted:** agent stdout, stderr, and parsed JSON blobs
  (`AWO_RESULT_JSON`, `AWO_REVIEW_JSON`); the contents of files an
  agent writes inside its worktree.
- **Boundary:** the three load-bearing rules above.

In short: AWO is designed so that even a confused or wrong agent
cannot escape its worktree, modify your git history, or run shell
commands of its choosing. It is **not** designed to defend against a
malicious agent that exploits a flaw in `claude` or `codex` itself,
and it is not a substitute for human review.
