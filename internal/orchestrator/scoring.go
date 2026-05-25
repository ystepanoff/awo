package orchestrator

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/safety"
)

// CandidateSnapshot is the orchestrator's view of one competitor before
// scoring. It is intentionally derivable from things AWO already trusts
// (git status, verification exit codes, parsed agent JSON) so the
// scoring step is a pure function of inputs the user can audit.
type CandidateSnapshot struct {
	Agent           domain.AgentKind
	ChangedFiles    []string
	DiffLines       int // total +/- lines, derived from the patch
	TestFiles       []string
	ProtectedHits   []string
	Verifications   []domain.VerificationResult
	AgentSummary    string
	AgentRisks      []string
	AgentConfidence string
}

// ScoreBreakdown captures the explainable contributions to a candidate's
// final score so comparison.md can show *why* one candidate beat the
// other. Each field is a signed contribution; Total is their sum.
type ScoreBreakdown struct {
	Verification float64 // +pass / -fail
	NoChange     float64 // -- when zero changes
	FileCount    float64 // - for many files
	DiffSize     float64 // - for large diffs
	Tests        float64 // + when tests added/updated
	Protected    float64 // - per protected hit
	Confidence   float64 // small +/- from parsed JSON
	Total        float64
	Notes        []string // human-readable explanations
}

// Add appends a note describing one of the contributions.
func (b *ScoreBreakdown) note(format string, args ...any) {
	b.Notes = append(b.Notes, fmt.Sprintf(format, args...))
}

// CompareVerdict captures the deterministic outcome of comparing two
// candidates: who wins, whether it's a tie, and the human-readable
// recommendation that should land in the run report.
type CompareVerdict struct {
	WinnerIndex    int // -1 when tie or no clear winner
	Tie            bool
	Recommendation domain.Recommendation
	Reason         string
}

// scoreWeights gathers the magic numbers in one place so they're easy
// to tune and easy to read in tests. Comments describe what each lever
// is meant to express; values are chosen so verification dwarfs cosmetic
// signals (size, file count) but a clear pass with a smaller diff still
// beats a clear pass with a 10x larger diff.
var scoreWeights = struct {
	VerificationPass float64
	VerificationFail float64
	NoChangePenalty  float64
	PerFile          float64
	PerDiffLine      float64
	TestsBonus       float64
	PerProtectedHit  float64
	ConfidenceHigh   float64
	ConfidenceMed    float64
	ConfidenceLow    float64
	TieEpsilon       float64
}{
	VerificationPass: 50,
	VerificationFail: -60,
	NoChangePenalty:  -100,
	PerFile:          -1.0,
	PerDiffLine:      -0.05,
	TestsBonus:       8,
	PerProtectedHit:  -15,
	ConfidenceHigh:   2,
	ConfidenceMed:    1,
	ConfidenceLow:    0,
	TieEpsilon:       3.0,
}

// ScoreCandidate produces a deterministic, explainable score for a
// single candidate. It is a pure function of CandidateSnapshot — no
// I/O, no clock — which is what makes it auditable and testable.
func ScoreCandidate(c CandidateSnapshot) ScoreBreakdown {
	b := ScoreBreakdown{}

	verifPassed := allVerificationsPassed(c.Verifications)
	verifFailed := anyVerificationFailed(c.Verifications)
	switch {
	case verifPassed:
		b.Verification = scoreWeights.VerificationPass
		b.note("verification passed (+%.0f)", scoreWeights.VerificationPass)
	case verifFailed:
		b.Verification = scoreWeights.VerificationFail
		b.note("verification failed (%.0f)", scoreWeights.VerificationFail)
	default:
		b.note("no verification commands ran (0)")
	}

	if len(c.ChangedFiles) == 0 {
		b.NoChange = scoreWeights.NoChangePenalty
		b.note("no files changed (%.0f)", scoreWeights.NoChangePenalty)
	} else {
		b.FileCount = float64(len(c.ChangedFiles)) * scoreWeights.PerFile
		b.note("%d files changed (%.1f)", len(c.ChangedFiles), b.FileCount)

		b.DiffSize = float64(c.DiffLines) * scoreWeights.PerDiffLine
		if c.DiffLines > 0 {
			b.note("%d diff lines (%.1f)", c.DiffLines, b.DiffSize)
		}
	}

	if len(c.TestFiles) > 0 {
		b.Tests = scoreWeights.TestsBonus
		b.note("tests added/updated: %d file(s) (+%.0f)", len(c.TestFiles), scoreWeights.TestsBonus)
	}

	if len(c.ProtectedHits) > 0 {
		b.Protected = float64(len(c.ProtectedHits)) * scoreWeights.PerProtectedHit
		b.note("protected paths touched: %d (%.1f)", len(c.ProtectedHits), b.Protected)
	}

	switch strings.ToLower(strings.TrimSpace(c.AgentConfidence)) {
	case "high":
		b.Confidence = scoreWeights.ConfidenceHigh
	case "medium", "med":
		b.Confidence = scoreWeights.ConfidenceMed
	case "low":
		b.Confidence = scoreWeights.ConfidenceLow
	}
	if b.Confidence != 0 {
		b.note("agent confidence %s (+%.0f)", c.AgentConfidence, b.Confidence)
	}

	b.Total = b.Verification + b.NoChange + b.FileCount + b.DiffSize + b.Tests + b.Protected + b.Confidence
	return b
}

// CompareCandidates picks a winner between exactly two scored
// candidates using the rules in the spec:
//   - both fail verification → failed_verification, no winner
//   - exactly one passes → that candidate wins (unless protected risk
//     is severe enough to override — captured in the score itself)
//   - scores within TieEpsilon → tie, recommendation needs_human_attention
//   - otherwise the higher score wins → ready_for_human_review
//
// The function returns a CompareVerdict that carries a human-readable
// reason; the caller persists it into comparison.md.
func CompareCandidates(snaps []CandidateSnapshot, scores []ScoreBreakdown) CompareVerdict {
	if len(snaps) != 2 || len(scores) != 2 {
		return CompareVerdict{
			WinnerIndex:    -1,
			Recommendation: domain.RecNoRecommendation,
			Reason:         "competitive mode requires exactly two candidates",
		}
	}

	aPassed := allVerificationsPassed(snaps[0].Verifications)
	bPassed := allVerificationsPassed(snaps[1].Verifications)
	aFailed := anyVerificationFailed(snaps[0].Verifications)
	bFailed := anyVerificationFailed(snaps[1].Verifications)

	if aFailed && bFailed {
		return CompareVerdict{
			WinnerIndex:    -1,
			Recommendation: domain.RecFailedVerification,
			Reason:         "both candidates failed verification",
		}
	}
	if aPassed && !bPassed {
		return CompareVerdict{
			WinnerIndex:    0,
			Recommendation: domain.RecReadyForHumanReview,
			Reason:         fmt.Sprintf("%s passed verification; %s did not", snaps[0].Agent, snaps[1].Agent),
		}
	}
	if bPassed && !aPassed {
		return CompareVerdict{
			WinnerIndex:    1,
			Recommendation: domain.RecReadyForHumanReview,
			Reason:         fmt.Sprintf("%s passed verification; %s did not", snaps[1].Agent, snaps[0].Agent),
		}
	}

	delta := math.Abs(scores[0].Total - scores[1].Total)
	if delta < scoreWeights.TieEpsilon {
		return CompareVerdict{
			WinnerIndex:    -1,
			Tie:            true,
			Recommendation: domain.RecNeedsHumanAttention,
			Reason:         fmt.Sprintf("scores within tie threshold (%.2f vs %.2f)", scores[0].Total, scores[1].Total),
		}
	}
	if scores[0].Total > scores[1].Total {
		return CompareVerdict{
			WinnerIndex:    0,
			Recommendation: domain.RecReadyForHumanReview,
			Reason:         fmt.Sprintf("%s scored %.2f vs %.2f", snaps[0].Agent, scores[0].Total, scores[1].Total),
		}
	}
	return CompareVerdict{
		WinnerIndex:    1,
		Recommendation: domain.RecReadyForHumanReview,
		Reason:         fmt.Sprintf("%s scored %.2f vs %.2f", snaps[1].Agent, scores[1].Total, scores[0].Total),
	}
}

// ----- helpers ------------------------------------------------------------

func allVerificationsPassed(res []domain.VerificationResult) bool {
	if len(res) == 0 {
		return false
	}
	for _, v := range res {
		if !v.Passed {
			return false
		}
	}
	return true
}

func anyVerificationFailed(res []domain.VerificationResult) bool {
	for _, v := range res {
		if !v.Passed {
			return true
		}
	}
	return false
}

// CountDiffLines returns the total number of added + deleted lines in a
// unified diff, ignoring file headers and hunk markers. It is a
// best-effort approximation — good enough for scoring, not for a stat.
func CountDiffLines(patch string) int {
	if strings.TrimSpace(patch) == "" {
		return 0
	}
	lines := strings.Split(patch, "\n")
	n := 0
	for _, l := range lines {
		switch {
		case strings.HasPrefix(l, "+++ "), strings.HasPrefix(l, "--- "):
			continue
		case strings.HasPrefix(l, "+"), strings.HasPrefix(l, "-"):
			n++
		}
	}
	return n
}

// CollectProtectedHits returns the deduplicated, sorted set of changed
// files that match any of the protected path patterns. It mirrors the
// behaviour reports/reports.go uses so single, writer-reviewer, and
// competitive modes report identically.
func CollectProtectedHits(changed, patterns []string) []string {
	if len(changed) == 0 || len(patterns) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var hits []string
	for _, p := range changed {
		if safety.IsProtectedPath(p, patterns) {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			hits = append(hits, p)
		}
	}
	sort.Strings(hits)
	return hits
}

// CollectTestFiles returns the subset of paths that look like test files
// using a conservative heuristic shared with comparison.md rendering.
// We deliberately do not call out to language toolchains: scoring must
// be deterministic and offline.
func CollectTestFiles(changed []string) []string {
	if len(changed) == 0 {
		return nil
	}
	var out []string
	for _, p := range changed {
		if isTestPath(p) {
			out = append(out, p)
		}
	}
	return out
}

func isTestPath(p string) bool {
	lower := strings.ToLower(p)
	switch {
	case strings.HasSuffix(lower, "_test.go"),
		strings.HasSuffix(lower, ".test.ts"),
		strings.HasSuffix(lower, ".test.tsx"),
		strings.HasSuffix(lower, ".test.js"),
		strings.HasSuffix(lower, ".spec.ts"),
		strings.HasSuffix(lower, ".spec.tsx"),
		strings.HasSuffix(lower, ".spec.js"):
		return true
	}
	if strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/test/") ||
		strings.HasPrefix(lower, "tests/") ||
		strings.HasPrefix(lower, "test/") {
		return true
	}
	return false
}
