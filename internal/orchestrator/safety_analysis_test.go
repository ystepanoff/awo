package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awo-dev/awo/internal/domain"
)

// ----- AnalyzeSafety ------------------------------------------------------

func TestAnalyzeSafetyClean(t *testing.T) {
	r := AnalyzeSafety(
		[]string{"server/health.go", "server/health_test.go"},
		[]string{"auth/**", "**/*secret*"},
		10,
	)
	if r == nil {
		t.Fatal("nil safety report")
	}
	if len(r.ProtectedHits) != 0 {
		t.Errorf("expected no hits, got %v", r.ProtectedHits)
	}
	if r.ExceedsMaxChanged {
		t.Errorf("should not exceed max")
	}
	if r.ChangedFileCount != 2 || r.MaxChangedFiles != 10 {
		t.Errorf("count/limit wrong: %+v", r)
	}
}

func TestAnalyzeSafetyProtectedHitsCarryPatterns(t *testing.T) {
	r := AnalyzeSafety(
		[]string{"auth/login.go", "config/.env.production", "server/x.go"},
		[]string{"auth/**", "**/.env*", "infra/**"},
		10,
	)
	if len(r.ProtectedHits) != 2 {
		t.Fatalf("hits=%d want 2: %v", len(r.ProtectedHits), r.ProtectedHits)
	}
	// Sorted by path
	want := map[string]string{
		"auth/login.go":           "auth/**",
		"config/.env.production": "**/.env*",
	}
	for _, h := range r.ProtectedHits {
		pat, ok := want[h.Path]
		if !ok {
			t.Errorf("unexpected hit %q", h.Path)
			continue
		}
		if len(h.Patterns) == 0 || h.Patterns[0] != pat {
			t.Errorf("hit %q matched=%v want first=%q", h.Path, h.Patterns, pat)
		}
	}
}

func TestAnalyzeSafetyExceedsMax(t *testing.T) {
	r := AnalyzeSafety(
		[]string{"a.go", "b.go", "c.go", "d.go"},
		nil,
		2,
	)
	if !r.ExceedsMaxChanged {
		t.Errorf("expected ExceedsMaxChanged")
	}
	if r.ChangedFileCount != 4 {
		t.Errorf("count=%d want 4", r.ChangedFileCount)
	}
}

func TestAnalyzeSafetyMaxZeroIsUnlimited(t *testing.T) {
	r := AnalyzeSafety(
		[]string{"a.go", "b.go", "c.go", "d.go"},
		nil,
		0,
	)
	if r.ExceedsMaxChanged {
		t.Errorf("max=0 should be unlimited")
	}
}

// ----- escalateForSafety --------------------------------------------------

func TestEscalateForSafetyKeepsStrongerVerdicts(t *testing.T) {
	r := &domain.SafetyReport{
		ProtectedHits:     []domain.ProtectedPathHit{{Path: "auth/x.go"}},
		ExceedsMaxChanged: true,
	}
	for _, in := range []domain.Recommendation{
		domain.RecFailedVerification,
		domain.RecNeedsRevision,
		domain.RecNeedsHumanAttention,
		domain.RecNoRecommendation,
	} {
		if got := escalateForSafety(in, r); got != in {
			t.Errorf("escalateForSafety(%q, %+v) = %q; should keep stronger verdict", in, r, got)
		}
	}
}

func TestEscalateForSafetyTooLargeBeatsReady(t *testing.T) {
	r := &domain.SafetyReport{ExceedsMaxChanged: true}
	if got := escalateForSafety(domain.RecReadyForHumanReview, r); got != domain.RecTooLargeForAutoReview {
		t.Errorf("ready + size breach should escalate to too_large_for_auto_review, got %q", got)
	}
}

func TestEscalateForSafetyProtectedBeatsReady(t *testing.T) {
	r := &domain.SafetyReport{
		ProtectedHits: []domain.ProtectedPathHit{{Path: "auth/x.go"}},
	}
	if got := escalateForSafety(domain.RecReadyForHumanReview, r); got != domain.RecNeedsHumanAttention {
		t.Errorf("ready + protected hit should escalate to needs_human_attention, got %q", got)
	}
}

func TestEscalateForSafetySizeBeatsProtected(t *testing.T) {
	// Both flags set → too_large is the stronger size-based escalation
	// and wins per the implementation order.
	r := &domain.SafetyReport{
		ProtectedHits:     []domain.ProtectedPathHit{{Path: "auth/x.go"}},
		ExceedsMaxChanged: true,
	}
	if got := escalateForSafety(domain.RecReadyForHumanReview, r); got != domain.RecTooLargeForAutoReview {
		t.Errorf("size breach should win when both fire, got %q", got)
	}
}

func TestEscalateForSafetyNoSafetyReportIsPassthrough(t *testing.T) {
	if got := escalateForSafety(domain.RecReadyForHumanReview, nil); got != domain.RecReadyForHumanReview {
		t.Errorf("nil safety should not change verdict, got %q", got)
	}
}

// ----- end-to-end through RunSingle --------------------------------------

func TestRunSingleSetsSafetyReportOnReport(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.Config.Safety.ProtectedPaths = []string{"auth/**", "**/.env*"}
	fg.ChangedFiles = []string{"auth/login.go", "server/health.go", "config/.env.production"}

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if report.Safety == nil {
		t.Fatal("expected report.Safety populated")
	}
	if len(report.Safety.ProtectedHits) != 2 {
		t.Errorf("expected 2 protected hits, got %d (%v)", len(report.Safety.ProtectedHits), report.Safety.ProtectedHits)
	}
	if report.Recommendation != domain.RecNeedsHumanAttention {
		t.Errorf("recommendation=%q want needs_human_attention", report.Recommendation)
	}
}

func TestRunSingleTooLargeSetsSafetyReport(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.MaxChangedFiles = 2
	fg.ChangedFiles = []string{"a.go", "b.go", "c.go"}

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if report.Safety == nil || !report.Safety.ExceedsMaxChanged {
		t.Errorf("expected ExceedsMaxChanged on safety report, got %+v", report.Safety)
	}
	if report.Safety.MaxChangedFiles != 2 {
		t.Errorf("MaxChangedFiles=%d want 2", report.Safety.MaxChangedFiles)
	}
	if report.Recommendation != domain.RecTooLargeForAutoReview {
		t.Errorf("recommendation=%q", report.Recommendation)
	}
}

// ----- proof pack rendering reflects safety ------------------------------

func TestRunSingleProofPackHighlightsProtectedAndSize(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.Config.Safety.ProtectedPaths = []string{"auth/**"}
	opts.MaxChangedFiles = 1
	fg.ChangedFiles = []string{"auth/login.go", "server/health.go"}

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	body, err := os.ReadFile(filepath.Join(root, "proof-pack.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Protected path warnings",
		"auth/login.go",
		"`auth/**`",
		"Size warning",
		"exceeds the configured `safety.maxChangedFiles` limit",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("proof-pack.md missing %q:\n%s", want, string(body))
		}
	}
}

// ----- console output highlights protected paths -------------------------

func TestRunSingleStdoutHighlightsProtectedPaths(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.Config.Safety.ProtectedPaths = []string{"auth/**"}
	fg.ChangedFiles = []string{"auth/login.go"}
	out := &strings.Builder{}
	opts.Stdout = out

	if _, err := RunSingle(context.Background(), opts); err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	got := out.String()
	for _, want := range []string{"protected:", "auth/login.go", "human review"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}
}

func TestRunSingleStdoutHighlightsSizeBreach(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.MaxChangedFiles = 1
	fg.ChangedFiles = []string{"a.go", "b.go"}
	out := &strings.Builder{}
	opts.Stdout = out

	if _, err := RunSingle(context.Background(), opts); err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	got := out.String()
	for _, want := range []string{"size warning", "exceed configured max"} {
		if !strings.Contains(got, want) {
			t.Errorf("stdout missing %q:\n%s", want, got)
		}
	}
}
