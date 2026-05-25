# Prompt contract

AWO talks to Claude and Codex through three prompt templates and one
strict output contract. The prompts live in
`internal/agents/templates/` and are embedded into the binary, so
what ships is exactly what runs.

## Roles

| Role | Template | Used in modes | Worktree | Allowed to edit? |
| ---- | -------- | ------------- | -------- | ---------------- |
| Writer | `writer.md.tmpl` | `single`, `writer-reviewer` | writer worktree | Yes |
| Reviewer | `reviewer.md.tmpl` | `writer-reviewer` | reviewer worktree (read-only) | **No** — edits become warnings |
| Competitor | `competitor.md.tmpl` | `competitive` | one worktree per competitor | Yes |

Each role has its own hard rules baked into the prompt — "do not
commit", "do not push", "do not modify files outside this worktree",
"do not inspect other agents' worktrees". Those are not just polite
requests: AWO independently enforces them at the filesystem layer
(see [`safety.md`](safety.md)). The prompts state them mainly so
the agent's plan stays consistent with what AWO will allow.

## Inputs the templates receive

`agents.PromptInput`:

- `Task` — the natural-language task the user passed to `awo run`.
- `Mode` — `"single"`, `"writer-reviewer"`, or `"competitive"`.
- `WorktreePath` — absolute path of the worktree the agent is
  running in. The agent is told this is its workspace and that it
  may not look outside it.
- `ChangedFiles` — files already modified in the worktree (writer
  starts with `nil`; reviewer sees what the writer produced).
- `Diff` — for the reviewer, the full unified diff under review;
  for writer/competitor, usually empty.
- `ProtectedPaths` — the configured protected globs, surfaced so
  the agent knows where to tread carefully.
- `ExtraContext` — a free-form `map[string]string` the orchestrator
  can pass through (e.g. base ref, run id) without changing the
  template.

The templates use Go's `text/template`, so any change you make has
to go through `agents.BuildWriterPrompt` / `BuildReviewerPrompt` /
`BuildCompetitorPrompt` — no string-substitution shortcuts.

## The output contract

Every prompt asks the agent to end its response with a fenced
JSON block that AWO parses programmatically.

### Writer / Competitor: `AWO_RESULT_JSON`

````
```json
AWO_RESULT_JSON
{
  "summary": "one or two sentences describing what you changed and why",
  "changed_files_intended": ["path/one.go", "path/two.go"],
  "tests_run": ["go test ./..."],
  "risks": ["any caveats a reviewer should know about"],
  "follow_up": ["work that is out of scope but worth doing later"],
  "confidence": "low|medium|high"
}
```
````

### Reviewer: `AWO_REVIEW_JSON`

````
```json
AWO_REVIEW_JSON
{
  "blocking": ["finding 1", "finding 2"],
  "non_blocking": ["suggestion 1"],
  "suggested_tests": ["test name or description"],
  "risk_summary": "one sentence on the residual risk if this lands as-is",
  "recommendation": "approve_for_human_review|needs_revision|reject"
}
```
````

`internal/agents/parser.go` extracts the **last** matching block,
which is forgiving when an agent quotes the schema in its own
narrative before producing the real one.

## What is advisory vs. trusted

The fields above are **advisory metadata**:

- `summary`, `risks`, `follow_up`, `confidence`, `blocking`,
  `non_blocking`, `suggested_tests`, `risk_summary` —
  surfaced verbatim in `proof-pack.md` so a human can read them.
- `tests_run` — recorded but **never** treated as evidence that
  tests passed. AWO runs verification itself.
- `changed_files_intended` — recorded, but the *actual* changed
  files come from `git status` inside the worktree, not from this
  field. If they disagree, the proof pack flags the divergence.
- `recommendation` (reviewer) — feeds the recommendation ladder
  (see [`safety.md`](safety.md)). A reviewer `recommendation` of
  `needs_revision` or `reject` escalates the run.

## Why prompts are templates, not strings

Two reasons:

1. **The prompt is part of AWO's contract with you.** It needs to be
   reviewable, diffable, and unit-testable. `internal/agents/prompts_test.go`
   asserts the rendered output across edge cases (empty diff, many
   protected paths, custom extra context).
2. **Agent CLIs evolve.** When `claude` or `codex` ships a new
   version that needs a slightly different system prompt or extra
   flag, you change the template (or the agent's `args` in
   `awo.config.json`) — not the orchestrator.

## Customizing prompts

You currently customize prompts by forking and editing the files in
`internal/agents/templates/`. There is intentionally no
`promptOverride` config knob: the prompts express AWO's safety
posture (no commits, no pushes, no cross-worktree reads), and
allowing arbitrary user prompts to replace them would be a footgun
for the people *using* AWO without realizing they had loosened a
safety rule. If you have a use case that needs prompt overrides,
open an issue.
