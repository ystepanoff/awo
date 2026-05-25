package prhelper

import (
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/domain"
)

// ----- helpers ------------------------------------------------------------

func baseReport(mode domain.RunMode) domain.RunReport {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	return domain.RunReport{
		RunID: "20260525-120000-abc123",
		Spec: domain.RunSpec{
			Task: "add /health endpoint",
			Mode: mode,
		},
		Status:     domain.StatusCompleted,
		StartedAt:  now,
		FinishedAt: now.Add(2 * time.Second),
	}
}

func writerResult(agent domain.AgentKind, branch string, files []string) domain.AgentRunResult {
	return domain.AgentRunResult{
		Agent:        agent,
		Role:         domain.RoleWriter,
		WorktreePath: ".awo/worktrees/run/" + string(agent) + "-writer",
		BranchName:   branch,
		Status:       "completed",
		ExitCode:     0,
		ChangedFiles: files,
		ParsedResult: &domain.ParsedAgentResult{
			Summary: "Added /health endpoint and a unit test.",
			Notes:   []string{"depends on net/http", "no auth required"},
		},
	}
}

func reviewerResult(agent domain.AgentKind, branch string, findings *domain.ReviewFindings) domain.AgentRunResult {
	return domain.AgentRunResult{
		Agent:        agent,
		Role:         domain.RoleReviewer,
		WorktreePath: ".awo/worktrees/run/" + string(agent) + "-reviewer",
		BranchName:   branch,
		Status:       "completed",
		ExitCode:     0,
		Review:       findings,
	}
}

func competitorResult(agent domain.AgentKind, branch string, files []string) domain.AgentRunResult {
	r := writerResult(agent, branch, files)
	r.Role = domain.RoleCompetitor
	return r
}

// ----- SelectCandidate ----------------------------------------------------

func TestSelectCandidateSinglePicksWriter(t *testing.T) {
	r := baseReport(domain.ModeSingle)
	r.AgentResults = []domain.AgentRunResult{
		writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"server/health.go"}),
	}
	got, err := SelectCandidate(r, "")
	if err != nil {
		t.Fatalf("SelectCandidate: %v", err)
	}
	if got.Agent != domain.AgentClaude || got.Role != domain.RoleWriter {
		t.Errorf("got %+v", got)
	}
}

func TestSelectCandidateWriterReviewerPicksWriter(t *testing.T) {
	r := baseReport(domain.ModeWriterReviewer)
	r.AgentResults = []domain.AgentRunResult{
		writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"server/health.go"}),
		reviewerResult(domain.AgentCodex, "awo/run/codex-reviewer", &domain.ReviewFindings{
			Recommendation: "approve",
		}),
	}
	got, err := SelectCandidate(r, "")
	if err != nil {
		t.Fatalf("SelectCandidate: %v", err)
	}
	if got.Role != domain.RoleWriter || got.Agent != domain.AgentClaude {
		t.Errorf("expected writer claude, got %+v", got)
	}
}

func TestSelectCandidateCompetitiveRequiresSelector(t *testing.T) {
	r := baseReport(domain.ModeCompetitive)
	r.AgentResults = []domain.AgentRunResult{
		competitorResult(domain.AgentClaude, "awo/run/claude-competitor", []string{"a.go"}),
		competitorResult(domain.AgentCodex, "awo/run/codex-competitor", []string{"b.go"}),
	}
	if _, err := SelectCandidate(r, ""); err == nil {
		t.Error("expected error for competitive without selector")
	}
}

func TestSelectCandidateCompetitiveByAgentName(t *testing.T) {
	r := baseReport(domain.ModeCompetitive)
	r.AgentResults = []domain.AgentRunResult{
		competitorResult(domain.AgentClaude, "awo/run/claude-competitor", []string{"a.go"}),
		competitorResult(domain.AgentCodex, "awo/run/codex-competitor", []string{"b.go"}),
	}
	got, err := SelectCandidate(r, "codex")
	if err != nil {
		t.Fatalf("SelectCandidate: %v", err)
	}
	if got.Agent != domain.AgentCodex {
		t.Errorf("got agent %q", got.Agent)
	}
}

func TestSelectCandidateCompetitiveByBranchName(t *testing.T) {
	r := baseReport(domain.ModeCompetitive)
	r.AgentResults = []domain.AgentRunResult{
		competitorResult(domain.AgentClaude, "awo/run/claude-competitor", []string{"a.go"}),
		competitorResult(domain.AgentCodex, "awo/run/codex-competitor", []string{"b.go"}),
	}
	got, err := SelectCandidate(r, "awo/run/codex-competitor")
	if err != nil {
		t.Fatalf("SelectCandidate: %v", err)
	}
	if got.Agent != domain.AgentCodex {
		t.Errorf("branch lookup wrong: %+v", got)
	}
}

func TestSelectCandidateCompetitiveUnknownSelector(t *testing.T) {
	r := baseReport(domain.ModeCompetitive)
	r.AgentResults = []domain.AgentRunResult{
		competitorResult(domain.AgentClaude, "awo/run/claude-competitor", []string{"a.go"}),
	}
	_, err := SelectCandidate(r, "gemini")
	if err == nil || !strings.Contains(err.Error(), "gemini") {
		t.Errorf("expected error mentioning %q, got %v", "gemini", err)
	}
}

func TestSelectCandidateNoResults(t *testing.T) {
	r := baseReport(domain.ModeSingle)
	if _, err := SelectCandidate(r, ""); err != ErrNoCandidate {
		t.Errorf("got %v, want ErrNoCandidate", err)
	}
}

// ----- Render: single mode ------------------------------------------------

func TestRenderSinglePopulatesAllSections(t *testing.T) {
	r := baseReport(domain.ModeSingle)
	r.AgentResults = []domain.AgentRunResult{
		writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"go.mod", "server/health.go"}),
	}
	r.VerificationResults = []domain.VerificationResult{
		{Command: "go test ./...", ExitCode: 0, Passed: true, DurationMillis: 1234},
	}
	r.Recommendation = domain.RecReadyForHumanReview

	body, err := Render(Inputs{Report: r, ProofPackPath: ".awo/runs/20260525-120000-abc123/proof-pack.md"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"add /health endpoint",
		"## Summary",
		"Added /health endpoint",
		"## AWO run",
		"`single`",
		"`claude`",
		"awo/run/claude-writer",
		"## Task",
		"## Verification results",
		"`go test ./...`",
		"passed",
		"## Changed files",
		"`go.mod`",
		"`server/health.go`",
		"## Human checklist",
		"AWO did not commit",
		"## Proof pack",
		".awo/runs/20260525-120000-abc123/proof-pack.md",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	for _, mustNot := range []string{
		"## Reviewer findings",
		"## Competitive comparison",
	} {
		if strings.Contains(body, mustNot) {
			t.Errorf("single-mode body should not contain %q", mustNot)
		}
	}
}

// ----- Render: writer-reviewer --------------------------------------------

func TestRenderWriterReviewerIncludesFindings(t *testing.T) {
	r := baseReport(domain.ModeWriterReviewer)
	r.AgentResults = []domain.AgentRunResult{
		writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"server/health.go"}),
		reviewerResult(domain.AgentCodex, "awo/run/codex-reviewer", &domain.ReviewFindings{
			Recommendation: "approve",
			Blocking:       []string{"missing context.Context plumb-through"},
			NonBlocking:    []string{"consider rate limiting"},
			SuggestedTests: []string{"unhealthy backend returns 503"},
			RiskSummary:    "low",
		}),
	}
	r.VerificationResults = []domain.VerificationResult{
		{Command: "go test ./...", ExitCode: 0, Passed: true, DurationMillis: 1234},
	}

	body, err := Render(Inputs{Report: r})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"## Reviewer findings",
		"`codex`",
		"### Blocking",
		"missing context.Context plumb-through",
		"### Non-blocking",
		"consider rate limiting",
		"### Suggested tests",
		"unhealthy backend returns 503",
		"### Risk summary",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
}

func TestRenderWriterReviewerWithoutParsedFindings(t *testing.T) {
	r := baseReport(domain.ModeWriterReviewer)
	r.AgentResults = []domain.AgentRunResult{
		writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"server/health.go"}),
		reviewerResult(domain.AgentCodex, "awo/run/codex-reviewer", nil),
	}
	body, err := Render(Inputs{Report: r})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(body, "did not produce parsed findings") {
		t.Errorf("expected reviewer-no-findings note, got:\n%s", body)
	}
}

// ----- Render: competitive ------------------------------------------------

func TestRenderCompetitiveIncludesComparisonAndPicksSelected(t *testing.T) {
	r := baseReport(domain.ModeCompetitive)
	r.AgentResults = []domain.AgentRunResult{
		competitorResult(domain.AgentClaude, "awo/run/claude-competitor", []string{"server/health.go"}),
		competitorResult(domain.AgentCodex, "awo/run/codex-competitor", []string{"server/health2.go"}),
	}
	r.VerificationResults = []domain.VerificationResult{
		{Command: "go test ./...", ExitCode: 0, Passed: true, DurationMillis: 1500},
	}

	body, err := Render(Inputs{Report: r, CandidateSelector: "codex"})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"## Competitive comparison",
		"selected for this\nPR description is `codex`",
		"`claude` on `awo/run/claude-competitor`",
		"`codex` on `awo/run/codex-competitor`",
		"comparison.md",
		"server/health2.go",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(body, "## Reviewer findings") {
		t.Errorf("competitive body should not include reviewer section:\n%s", body)
	}
}

// ----- Render: safety surfaces -------------------------------------------

func TestRenderProtectedHitsAndSizeWarning(t *testing.T) {
	r := baseReport(domain.ModeSingle)
	r.AgentResults = []domain.AgentRunResult{
		writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"auth/login.go", "server/health.go"}),
	}
	r.Safety = &domain.SafetyReport{
		ProtectedHits: []domain.ProtectedPathHit{
			{Path: "auth/login.go", Patterns: []string{"auth/**"}},
		},
		ChangedFileCount:  2,
		MaxChangedFiles:   1,
		ExceedsMaxChanged: true,
	}
	r.Recommendation = domain.RecNeedsHumanAttention

	body, err := Render(Inputs{Report: r})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{
		"## ⚠ Safety warnings",
		"### Protected path warnings",
		"`auth/login.go`",
		"`auth/**`",
		"### Size warning",
		"`safety.maxChangedFiles`",
		"Consider splitting this change",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q:\n%s", want, body)
		}
	}
}

// ----- Render: hard rule footer is always present -------------------------

func TestRenderAlwaysIncludesAwoDidNotFooter(t *testing.T) {
	for _, mode := range []domain.RunMode{
		domain.ModeSingle,
		domain.ModeWriterReviewer,
	} {
		r := baseReport(mode)
		r.AgentResults = []domain.AgentRunResult{
			writerResult(domain.AgentClaude, "awo/run/claude-writer", []string{"a.go"}),
		}
		if mode == domain.ModeWriterReviewer {
			r.AgentResults = append(r.AgentResults,
				reviewerResult(domain.AgentCodex, "awo/run/codex-reviewer", nil))
		}
		body, err := Render(Inputs{Report: r})
		if err != nil {
			t.Fatalf("Render(%s): %v", mode, err)
		}
		for _, want := range []string{
			"AWO did not commit, push, merge, or auto-approve",
			"AWO never auto-merges, auto-commits, or auto-pushes",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("mode=%s missing footer %q", mode, want)
			}
		}
	}
}
