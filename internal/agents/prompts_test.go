package agents

import (
	"strings"
	"testing"
)

func baseInput() PromptInput {
	return PromptInput{
		Task:           "Add a /health endpoint that returns 200 OK.",
		Mode:           "single",
		WorktreePath:   "/repo/.awo/worktrees/run1/claude-writer",
		ChangedFiles:   []string{"server/health.go", "server/health_test.go"},
		Diff:           "diff --git a/server/health.go b/server/health.go\n",
		ProtectedPaths: []string{"go.mod", "internal/safety/"},
		ExtraContext:   map[string]string{"commit_message_style": "imperative, <72 chars"},
	}
}

func mustContain(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(haystack, n) {
			t.Errorf("missing %q in:\n%s", n, haystack)
		}
	}
}

func mustNotContain(t *testing.T, haystack string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			t.Errorf("unexpected %q in:\n%s", n, haystack)
		}
	}
}

// ----- writer -------------------------------------------------------------

func TestBuildWriterPrompt(t *testing.T) {
	got, err := BuildWriterPrompt(baseInput())
	if err != nil {
		t.Fatalf("BuildWriterPrompt: %v", err)
	}
	mustContain(t, got,
		"# AWO Writer Task",
		"Add a /health endpoint that returns 200 OK.",
		"`single`",
		"/repo/.awo/worktrees/run1/claude-writer",
		"server/health.go",
		"server/health_test.go",
		"go.mod",
		"internal/safety/",
		"commit_message_style",
		"Do not commit.",
		"Do not push.",
		"Do not merge.",
		"AWO_RESULT_JSON",
		"```diff",
	)
	mustNotContain(t, got, "AWO_REVIEW_JSON", "{{")
}

func TestBuildWriterPromptOmitsEmptySections(t *testing.T) {
	in := PromptInput{
		Task:         "Refactor the cache",
		Mode:         "single",
		WorktreePath: "/wt",
	}
	got, err := BuildWriterPrompt(in)
	if err != nil {
		t.Fatalf("BuildWriterPrompt: %v", err)
	}
	mustNotContain(t, got,
		"## Existing diff",
		"## Additional context",
		"Files already changed",
		"Protected paths",
		"<no value>",
	)
}

func TestBuildWriterPromptRejectsEmptyTask(t *testing.T) {
	if _, err := BuildWriterPrompt(PromptInput{Mode: "single"}); err == nil {
		t.Fatal("expected error for empty task")
	}
}

// ----- reviewer -----------------------------------------------------------

func TestBuildReviewerPrompt(t *testing.T) {
	in := baseInput()
	in.Diff = "diff --git a/x b/x\n+hello\n"
	got, err := BuildReviewerPrompt(in)
	if err != nil {
		t.Fatalf("BuildReviewerPrompt: %v", err)
	}
	mustContain(t, got,
		"# AWO Reviewer Task",
		"Review the diff only. Do not modify any files.",
		"diff --git a/x b/x",
		"+hello",
		"AWO_REVIEW_JSON",
		"approve_for_human_review|needs_revision|reject",
		"Maintainability",
		"Security risks",
	)
	mustNotContain(t, got, "AWO_RESULT_JSON")
}

// ----- competitor ---------------------------------------------------------

func TestBuildCompetitorPrompt(t *testing.T) {
	got, err := BuildCompetitorPrompt(baseInput())
	if err != nil {
		t.Fatalf("BuildCompetitorPrompt: %v", err)
	}
	mustContain(t, got,
		"# AWO Competitor Task",
		"Independently implement the task.",
		"Do not inspect other agents' worktrees.",
		"smallest correct patch",
		"Do not commit.",
		"Do not push.",
		"AWO_RESULT_JSON",
	)
	mustNotContain(t, got, "AWO_REVIEW_JSON")
}
