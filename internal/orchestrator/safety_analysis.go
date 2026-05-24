package orchestrator

import (
	"fmt"
	"io"
	"strings"

	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/safety"
)

// AnalyzeSafety inspects the changed files against the configured
// protected-path patterns and the resolved max-changed-files limit and
// returns the matching domain.SafetyReport. It is a pure function — no
// I/O, no orchestrator state — so callers can use it both for the
// recommendation step and for snapshot tests.
func AnalyzeSafety(changedFiles, protectedPaths []string, maxChangedFiles int) *domain.SafetyReport {
	check := safety.CheckMaxChangedFiles(changedFiles, maxChangedFiles)
	matches := safety.MatchProtectedPaths(changedFiles, protectedPaths)

	hits := make([]domain.ProtectedPathHit, 0, len(matches))
	for _, m := range matches {
		hits = append(hits, domain.ProtectedPathHit{
			Path:     m.Path,
			Patterns: append([]string(nil), m.Patterns...),
		})
	}
	return &domain.SafetyReport{
		ProtectedHits:     hits,
		ChangedFileCount:  check.Count,
		MaxChangedFiles:   check.Limit,
		ExceedsMaxChanged: check.ExceedsLimit,
	}
}

// escalateForSafety raises the recommendation when safety findings
// require human attention. The ordering follows the verdict ladder
// already used in single/writer-reviewer:
//
//   - failed_verification stays as-is — it is the strongest signal.
//   - too_large_for_auto_review wins over a "ready" verdict only.
//   - protected hits push a "ready" verdict to needs_human_attention.
//
// Verdicts that already require human attention (needs_human_attention,
// needs_revision, no_recommendation) are returned unchanged.
func escalateForSafety(rec domain.Recommendation, r *domain.SafetyReport) domain.Recommendation {
	if r == nil {
		return rec
	}
	switch rec {
	case domain.RecFailedVerification, domain.RecNeedsRevision,
		domain.RecNeedsHumanAttention, domain.RecNoRecommendation:
		return rec
	}
	if r.ExceedsMaxChanged {
		return domain.RecTooLargeForAutoReview
	}
	if len(r.ProtectedHits) > 0 {
		return domain.RecNeedsHumanAttention
	}
	return rec
}

// winnerOrUnionChangedFiles returns the changed-file list to feed into
// AnalyzeSafety for a multi-candidate run. When a winner exists, its
// files are the right thing to summarize. When none does, we union all
// candidates' files so the run-level safety report still reflects the
// risk surface a human is about to evaluate.
func winnerOrUnionChangedFiles(snaps []CandidateSnapshot, winner int) []string {
	if winner >= 0 && winner < len(snaps) {
		return append([]string(nil), snaps[winner].ChangedFiles...)
	}
	seen := map[string]struct{}{}
	var out []string
	for _, s := range snaps {
		for _, f := range s.ChangedFiles {
			if _, ok := seen[f]; ok {
				continue
			}
			seen[f] = struct{}{}
			out = append(out, f)
		}
	}
	return out
}

// printSafetyHighlights writes a short, eye-catching block to out
// when the safety report carries either protected-path hits or a
// size limit breach. It deliberately stays quiet when there is
// nothing to highlight so existing summaries stay tight.
func printSafetyHighlights(out io.Writer, r *domain.SafetyReport) {
	if r == nil {
		return
	}
	if len(r.ProtectedHits) > 0 {
		paths := make([]string, 0, len(r.ProtectedHits))
		for _, h := range r.ProtectedHits {
			paths = append(paths, h.Path)
		}
		fmt.Fprintf(out, "  protected:      %d path(s) need human review: %s\n",
			len(paths), strings.Join(paths, ", "))
	}
	if r.ExceedsMaxChanged {
		fmt.Fprintf(out, "  size warning:   %d changed files exceed configured max %d\n",
			r.ChangedFileCount, r.MaxChangedFiles)
	}
}
