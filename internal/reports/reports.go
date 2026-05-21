// Package reports renders human-readable proof packs and summaries from
// RunReports.
//
// Templates are embedded so binaries do not depend on filesystem layout.
// All renderers consume domain.RunReport (plus a few orchestrator inputs
// like the resolved diff path and protected-path matches) and produce
// markdown intended to be persisted under the run's artifact directory.
package reports

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/awo-dev/awo/internal/artifacts"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/safety"
)

//go:embed templates/proof_pack.md.tmpl
var proofPackTmpl string

//go:embed templates/summary.md.tmpl
var summaryTmpl string

// Inputs carries the orchestrator-derived context needed to render a
// run's reports. The RunReport itself is the source of truth; Inputs
// supplies extras (protected path config, the on-disk diff path) that
// don't naturally live on RunReport.
type Inputs struct {
	Report         domain.RunReport
	ProtectedPaths []string
	DiffPatchPath  string
}

// RenderProofPack renders the long-form markdown proof pack from the
// given run inputs. It accepts either an Inputs value or a bare
// RunReport for backward compatibility.
func RenderProofPack(v any) (string, error) {
	in, err := coerceInputs(v)
	if err != nil {
		return "", err
	}
	data := buildProofPackData(in)
	return execute("proof", proofPackTmpl, data)
}

// RenderSummary renders the short-form markdown summary.
func RenderSummary(v any) (string, error) {
	in, err := coerceInputs(v)
	if err != nil {
		return "", err
	}
	data := buildSummaryData(in)
	return execute("summary", summaryTmpl, data)
}

// WriteRunReportFiles renders both proof-pack.md and summary.md for the
// given inputs and writes them to their canonical paths under the run
// layout. Writes are atomic.
func WriteRunReportFiles(in Inputs, layout *artifacts.Layout) error {
	if layout == nil {
		return errors.New("reports: nil layout")
	}
	proof, err := RenderProofPack(in)
	if err != nil {
		return fmt.Errorf("reports: render proof pack: %w", err)
	}
	if err := layout.WriteFileAtomic(layout.ProofPackPath(), []byte(proof), 0o644); err != nil {
		return fmt.Errorf("reports: write proof pack: %w", err)
	}
	summary, err := RenderSummary(in)
	if err != nil {
		return fmt.Errorf("reports: render summary: %w", err)
	}
	if err := layout.WriteFileAtomic(layout.SummaryPath(), []byte(summary), 0o644); err != nil {
		return fmt.Errorf("reports: write summary: %w", err)
	}
	return nil
}

// ----- view models --------------------------------------------------------

type proofPackData struct {
	RunID           string
	Task            string
	Mode            string
	Status          string
	Recommendation  string
	StartedAt       string
	FinishedAt      string
	HasAgent        bool
	PrimaryAgent    domain.AgentRunResult
	ChangedFiles    []string
	ProtectedHits   []string
	Verifications   []domain.VerificationResult
	AgentSummary    string
	AgentRisks      []string
	DiffPatchPath   string
	Warnings        []string
	NextHumanAction string
}

type summaryData struct {
	RunID              string
	Task               string
	Mode               string
	Recommendation     string
	VerificationStatus string
	ChangedFileCount   int
	NextHumanAction    string
}

// ----- builders -----------------------------------------------------------

func buildProofPackData(in Inputs) proofPackData {
	r := in.Report
	d := proofPackData{
		RunID:           r.RunID,
		Task:            strings.TrimSpace(r.Spec.Task),
		Mode:            string(r.Spec.Mode),
		Status:          string(r.Status),
		Recommendation:  string(r.Recommendation),
		StartedAt:       formatTime(r.StartedAt),
		FinishedAt:      formatTime(r.FinishedAt),
		Verifications:   append([]domain.VerificationResult(nil), r.VerificationResults...),
		DiffPatchPath:   diffPath(in),
		Warnings:        append([]string(nil), r.Warnings...),
		NextHumanAction: nextHumanAction(r.Recommendation),
	}
	if d.Task == "" {
		d.Task = "_no task recorded_"
	}
	if len(r.AgentResults) > 0 {
		d.HasAgent = true
		d.PrimaryAgent = r.AgentResults[0]
		d.ChangedFiles = append([]string(nil), r.AgentResults[0].ChangedFiles...)
		if pr := r.AgentResults[0].ParsedResult; pr != nil {
			d.AgentSummary = strings.TrimSpace(pr.Summary)
			d.AgentRisks = append([]string(nil), pr.Notes...)
		}
	}
	d.ProtectedHits = collectProtectedHits(d.ChangedFiles, in.ProtectedPaths)
	return d
}

func buildSummaryData(in Inputs) summaryData {
	r := in.Report
	task := strings.TrimSpace(r.Spec.Task)
	if task == "" {
		task = "_no task recorded_"
	}
	rec := string(r.Recommendation)
	if rec == "" {
		rec = string(domain.RecNoRecommendation)
	}
	count := 0
	if len(r.AgentResults) > 0 {
		count = len(r.AgentResults[0].ChangedFiles)
	}
	return summaryData{
		RunID:              r.RunID,
		Task:               task,
		Mode:               string(r.Spec.Mode),
		Recommendation:     rec,
		VerificationStatus: verificationStatus(r.VerificationResults),
		ChangedFileCount:   count,
		NextHumanAction:    nextHumanAction(r.Recommendation),
	}
}

// ----- helpers ------------------------------------------------------------

func coerceInputs(v any) (Inputs, error) {
	switch x := v.(type) {
	case Inputs:
		return x, nil
	case *Inputs:
		if x == nil {
			return Inputs{}, errors.New("reports: nil Inputs")
		}
		return *x, nil
	case domain.RunReport:
		return Inputs{Report: x}, nil
	case *domain.RunReport:
		if x == nil {
			return Inputs{}, errors.New("reports: nil RunReport")
		}
		return Inputs{Report: *x}, nil
	default:
		return Inputs{}, fmt.Errorf("reports: unsupported input type %T", v)
	}
}

func execute(name, tmpl string, data any) (string, error) {
	t, err := template.New(name).Parse(tmpl)
	if err != nil {
		return "", fmt.Errorf("reports: parse %s: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("reports: execute %s: %w", name, err)
	}
	return buf.String(), nil
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func diffPath(in Inputs) string {
	if strings.TrimSpace(in.DiffPatchPath) != "" {
		return in.DiffPatchPath
	}
	return "diff.patch"
}

func collectProtectedHits(changed, patterns []string) []string {
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

func verificationStatus(res []domain.VerificationResult) string {
	if len(res) == 0 {
		return "not verified"
	}
	passed := 0
	for _, v := range res {
		if v.Passed {
			passed++
		}
	}
	if passed == len(res) {
		return fmt.Sprintf("%d/%d passed", passed, len(res))
	}
	return fmt.Sprintf("%d/%d passed (failures present)", passed, len(res))
}

func nextHumanAction(rec domain.Recommendation) string {
	switch rec {
	case domain.RecReadyForHumanReview:
		return "review the worktree diff and, if it looks right, commit and push it yourself."
	case domain.RecFailedVerification:
		return "verification failed — inspect the verify/ artifacts, fix the failure, and rerun before considering the change."
	case domain.RecNeedsHumanAttention:
		return "changed files include protected paths — review carefully before merging."
	case domain.RecTooLargeForAutoReview:
		return "the diff is larger than the configured review threshold — consider splitting it before merging."
	case domain.RecNeedsRevision:
		return "a reviewer flagged this run as needing revision — read the reviewer notes before continuing."
	case domain.RecNoRecommendation, "":
		return "no automatic recommendation — review the worktree manually."
	}
	return "review the worktree manually."
}
