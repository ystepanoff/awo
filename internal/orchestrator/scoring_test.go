package orchestrator

import (
	"strings"
	"testing"

	"github.com/awo-dev/awo/internal/domain"
)

// helper: build a verification result of N commands all passing/failing.
func verif(passed bool, n int) []domain.VerificationResult {
	out := make([]domain.VerificationResult, 0, n)
	for i := 0; i < n; i++ {
		exit := 0
		if !passed {
			exit = 1
		}
		out = append(out, domain.VerificationResult{
			Command:  "go test ./...",
			ExitCode: exit,
			Passed:   passed,
		})
	}
	return out
}

func mixedVerif() []domain.VerificationResult {
	return []domain.VerificationResult{
		{Command: "go test ./...", ExitCode: 0, Passed: true},
		{Command: "go vet ./...", ExitCode: 1, Passed: false},
	}
}

// ----- ScoreCandidate ------------------------------------------------------

func TestScorePassingBeatsFailing(t *testing.T) {
	pass := ScoreCandidate(CandidateSnapshot{
		Agent:         domain.AgentClaude,
		ChangedFiles:  []string{"a.go", "a_test.go"},
		DiffLines:     20,
		TestFiles:     []string{"a_test.go"},
		Verifications: verif(true, 1),
	})
	fail := ScoreCandidate(CandidateSnapshot{
		Agent:         domain.AgentCodex,
		ChangedFiles:  []string{"a.go", "a_test.go"},
		DiffLines:     20,
		TestFiles:     []string{"a_test.go"},
		Verifications: verif(false, 1),
	})
	if pass.Total <= fail.Total {
		t.Errorf("passing total %.2f should beat failing %.2f", pass.Total, fail.Total)
	}
	// Specifically, the gap should reflect the verification weights.
	if (pass.Total - fail.Total) < scoreWeights.VerificationPass-scoreWeights.VerificationFail-1 {
		t.Errorf("passing should outscore failing by at least the verification spread; got %.2f", pass.Total-fail.Total)
	}
}

func TestScoreNoChangePenalty(t *testing.T) {
	b := ScoreCandidate(CandidateSnapshot{
		Agent:         domain.AgentClaude,
		Verifications: verif(true, 1),
	})
	if b.NoChange != scoreWeights.NoChangePenalty {
		t.Errorf("expected no-change penalty %.0f, got %.0f", scoreWeights.NoChangePenalty, b.NoChange)
	}
	// The penalty must dominate the verification bonus so a "do nothing"
	// candidate cannot win.
	if b.Total >= 0 {
		t.Errorf("no-change candidate should have negative total, got %.2f", b.Total)
	}
}

func TestScoreFewerFilesIsBetter(t *testing.T) {
	small := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:  []string{"a.go"},
		DiffLines:     10,
		Verifications: verif(true, 1),
	})
	big := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:  []string{"a.go", "b.go", "c.go", "d.go"},
		DiffLines:     200,
		Verifications: verif(true, 1),
	})
	if small.Total <= big.Total {
		t.Errorf("small change %.2f should beat big change %.2f", small.Total, big.Total)
	}
}

func TestScoreTestsBonus(t *testing.T) {
	withTests := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:  []string{"a.go", "a_test.go"},
		DiffLines:     20,
		TestFiles:     []string{"a_test.go"},
		Verifications: verif(true, 1),
	})
	without := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:  []string{"a.go", "a_test.go"},
		DiffLines:     20,
		Verifications: verif(true, 1),
	})
	if withTests.Total <= without.Total {
		t.Errorf("tests-present should outscore no-tests: %.2f vs %.2f",
			withTests.Total, without.Total)
	}
}

func TestScoreProtectedPathPenalty(t *testing.T) {
	clean := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:  []string{"server/health.go"},
		DiffLines:     10,
		Verifications: verif(true, 1),
	})
	risky := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:  []string{"server/health.go", "go.mod"},
		DiffLines:     12,
		ProtectedHits: []string{"go.mod"},
		Verifications: verif(true, 1),
	})
	if risky.Total >= clean.Total {
		t.Errorf("protected-path candidate should score lower; got %.2f vs %.2f",
			risky.Total, clean.Total)
	}
	// And the breakdown should explicitly carry a Protected contribution.
	if risky.Protected >= 0 {
		t.Errorf("expected negative Protected contribution, got %.2f", risky.Protected)
	}
}

func TestScoreConfidenceLowWeight(t *testing.T) {
	high := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:    []string{"a.go"},
		DiffLines:       10,
		Verifications:   verif(true, 1),
		AgentConfidence: "high",
	})
	low := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:    []string{"a.go"},
		DiffLines:       10,
		Verifications:   verif(true, 1),
		AgentConfidence: "low",
	})
	gap := high.Total - low.Total
	if gap <= 0 {
		t.Errorf("high confidence should outscore low; gap=%.2f", gap)
	}
	// Confidence should never come close to flipping a verification
	// outcome — so it must be tiny relative to the verification weight.
	if gap > scoreWeights.VerificationPass/4 {
		t.Errorf("confidence weight too large; gap=%.2f, verif weight=%.0f",
			gap, scoreWeights.VerificationPass)
	}
}

func TestScoreNotesAreExplainable(t *testing.T) {
	b := ScoreCandidate(CandidateSnapshot{
		ChangedFiles:    []string{"a.go", "a_test.go"},
		DiffLines:       20,
		TestFiles:       []string{"a_test.go"},
		ProtectedHits:   []string{"go.mod"},
		Verifications:   verif(true, 1),
		AgentConfidence: "high",
	})
	joined := strings.Join(b.Notes, "\n")
	for _, want := range []string{
		"verification passed",
		"files changed",
		"diff lines",
		"tests added/updated",
		"protected paths touched",
		"agent confidence",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("score notes missing %q in:\n%s", want, joined)
		}
	}
}

// ----- CompareCandidates --------------------------------------------------

func TestCompareBothFailingRecommendsFailedVerification(t *testing.T) {
	snaps := []CandidateSnapshot{
		{Agent: domain.AgentClaude, ChangedFiles: []string{"a.go"}, Verifications: verif(false, 1)},
		{Agent: domain.AgentCodex, ChangedFiles: []string{"a.go"}, Verifications: verif(false, 1)},
	}
	scores := []ScoreBreakdown{
		ScoreCandidate(snaps[0]),
		ScoreCandidate(snaps[1]),
	}
	v := CompareCandidates(snaps, scores)
	if v.Recommendation != domain.RecFailedVerification {
		t.Errorf("recommendation=%q want failed_verification", v.Recommendation)
	}
	if v.WinnerIndex != -1 {
		t.Errorf("expected no winner, got index %d", v.WinnerIndex)
	}
}

func TestCompareOnePassingOneFailing(t *testing.T) {
	snaps := []CandidateSnapshot{
		{Agent: domain.AgentClaude, ChangedFiles: []string{"a.go"}, Verifications: verif(true, 1)},
		{Agent: domain.AgentCodex, ChangedFiles: []string{"a.go"}, Verifications: verif(false, 1)},
	}
	scores := []ScoreBreakdown{
		ScoreCandidate(snaps[0]),
		ScoreCandidate(snaps[1]),
	}
	v := CompareCandidates(snaps, scores)
	if v.WinnerIndex != 0 {
		t.Errorf("winner=%d want 0 (claude)", v.WinnerIndex)
	}
	if v.Recommendation != domain.RecReadyForHumanReview {
		t.Errorf("recommendation=%q want ready_for_human_review", v.Recommendation)
	}
}

func TestCompareTie(t *testing.T) {
	snap := CandidateSnapshot{
		Agent:         domain.AgentClaude,
		ChangedFiles:  []string{"a.go"},
		DiffLines:     10,
		Verifications: verif(true, 1),
	}
	other := snap
	other.Agent = domain.AgentCodex
	scores := []ScoreBreakdown{
		ScoreCandidate(snap),
		ScoreCandidate(other),
	}
	v := CompareCandidates([]CandidateSnapshot{snap, other}, scores)
	if !v.Tie {
		t.Errorf("expected tie, got winner=%d (%.2f vs %.2f)",
			v.WinnerIndex, scores[0].Total, scores[1].Total)
	}
	if v.Recommendation != domain.RecNeedsHumanAttention {
		t.Errorf("recommendation=%q want needs_human_attention", v.Recommendation)
	}
}

func TestCompareClearWinnerByScore(t *testing.T) {
	snaps := []CandidateSnapshot{
		// candidate 0: small clean change with tests
		{
			Agent:         domain.AgentClaude,
			ChangedFiles:  []string{"a.go", "a_test.go"},
			DiffLines:     20,
			TestFiles:     []string{"a_test.go"},
			Verifications: verif(true, 1),
		},
		// candidate 1: big sprawling change without tests
		{
			Agent:         domain.AgentCodex,
			ChangedFiles:  []string{"a.go", "b.go", "c.go", "d.go", "e.go"},
			DiffLines:     500,
			Verifications: verif(true, 1),
		},
	}
	scores := []ScoreBreakdown{
		ScoreCandidate(snaps[0]),
		ScoreCandidate(snaps[1]),
	}
	v := CompareCandidates(snaps, scores)
	if v.WinnerIndex != 0 {
		t.Errorf("winner=%d want 0 (smaller, with tests); scores %.2f vs %.2f",
			v.WinnerIndex, scores[0].Total, scores[1].Total)
	}
	if v.Recommendation != domain.RecReadyForHumanReview {
		t.Errorf("recommendation=%q", v.Recommendation)
	}
}

func TestComparePassingCandidateWinsEvenWithProtectedHit(t *testing.T) {
	// Spec: "If one passes and one fails, recommend the passing candidate
	// unless protected-path risk is severe." With the current weights a
	// single protected hit (-15) is dwarfed by failing verification
	// (-60), so the passing candidate wins. This documents that contract.
	snaps := []CandidateSnapshot{
		{
			Agent:         domain.AgentClaude,
			ChangedFiles:  []string{"server/health.go", "go.mod"},
			ProtectedHits: []string{"go.mod"},
			DiffLines:     30,
			Verifications: verif(true, 1),
		},
		{
			Agent:         domain.AgentCodex,
			ChangedFiles:  []string{"server/health.go"},
			DiffLines:     30,
			Verifications: verif(false, 1),
		},
	}
	scores := []ScoreBreakdown{
		ScoreCandidate(snaps[0]),
		ScoreCandidate(snaps[1]),
	}
	v := CompareCandidates(snaps, scores)
	if v.WinnerIndex != 0 {
		t.Errorf("winner=%d want 0 (passing despite protected hit); scores %.2f vs %.2f",
			v.WinnerIndex, scores[0].Total, scores[1].Total)
	}
}

func TestCompareWrongCandidateCount(t *testing.T) {
	v := CompareCandidates(nil, nil)
	if v.Recommendation != domain.RecNoRecommendation {
		t.Errorf("recommendation=%q want no_recommendation", v.Recommendation)
	}
}

// ----- helpers ------------------------------------------------------------

func TestCountDiffLines(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/x.go b/x.go",
		"index 1234..5678 100644",
		"--- a/x.go",
		"+++ b/x.go",
		"@@ -1,3 +1,4 @@",
		" unchanged",
		"-old line",
		"+new line",
		"+another new line",
	}, "\n")
	got := CountDiffLines(patch)
	if got != 3 {
		t.Errorf("CountDiffLines=%d want 3 (--- and +++ should be excluded)", got)
	}
	if CountDiffLines("") != 0 {
		t.Error("empty patch should have 0 diff lines")
	}
}

func TestCollectTestFiles(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"a.go", "a_test.go"}, []string{"a_test.go"}},
		{[]string{"src/foo.ts", "src/foo.test.ts", "src/bar.spec.tsx"}, []string{"src/foo.test.ts", "src/bar.spec.tsx"}},
		{[]string{"tests/integration.py", "lib.py"}, []string{"tests/integration.py"}},
		{[]string{"a.go", "b.go"}, nil},
	}
	for i, c := range cases {
		got := CollectTestFiles(c.in)
		if !equalStrings(got, c.want) {
			t.Errorf("case %d: got %v want %v", i, got, c.want)
		}
	}
}

func TestCollectProtectedHitsDeduplicates(t *testing.T) {
	hits := CollectProtectedHits(
		[]string{"go.mod", "server/health.go", "go.mod"},
		[]string{"go.mod", "go.sum"},
	)
	if !equalStrings(hits, []string{"go.mod"}) {
		t.Errorf("hits=%v want [go.mod]", hits)
	}
}

// ----- ParseCompetitorList ------------------------------------------------

func TestParseCompetitorList(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []domain.AgentKind
		err  bool
	}{
		{"csv", []string{"claude,codex"}, []domain.AgentKind{domain.AgentClaude, domain.AgentCodex}, false},
		{"split", []string{"claude", "codex"}, []domain.AgentKind{domain.AgentClaude, domain.AgentCodex}, false},
		{"too few", []string{"claude"}, nil, true},
		{"too many", []string{"claude,codex,claude"}, nil, true},
		{"duplicate", []string{"claude,claude"}, nil, true},
		{"unknown", []string{"claude,bogus"}, nil, true},
		{"empty", []string{""}, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParseCompetitorList(c.in)
			if c.err {
				if err == nil {
					t.Fatalf("expected error for %v", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(c.want) {
				t.Fatalf("len=%d want %d", len(got), len(c.want))
			}
			for i, g := range got {
				if g != c.want[i] {
					t.Errorf("[%d]=%q want %q", i, g, c.want[i])
				}
			}
		})
	}
}
