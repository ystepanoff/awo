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

//go:embed templates/comparison.md.tmpl
var comparisonTmpl string

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

// ----- comparison ---------------------------------------------------------

// ComparisonCandidate is a renderer-agnostic view of one competitor in
// competitive mode. The orchestrator builds these from CandidateSnapshot
// + ScoreBreakdown values; reports does not depend on orchestrator
// types so the import direction stays clean.
type ComparisonCandidate struct {
	Agent              domain.AgentKind
	ChangedFiles       []string
	DiffLines          int
	TestFiles          []string
	ProtectedHits      []string
	Verifications      []domain.VerificationResult
	AgentRisks         []string
	Score              float64
	ScoreNotes         []string
	VerificationStatus string
}

// FileCount, TestFileCount, ProtectedHitCount are template helpers — Go
// templates can't subscript len() across nil slices cleanly, so we
// expose them as methods.
func (c ComparisonCandidate) FileCount() int          { return len(c.ChangedFiles) }
func (c ComparisonCandidate) TestFileCount() int      { return len(c.TestFiles) }
func (c ComparisonCandidate) ProtectedHitCount() int  { return len(c.ProtectedHits) }

// ComparisonInputs is the data passed to RenderComparison.
type ComparisonInputs struct {
	RunID          string
	Task           string
	Mode           string
	Recommendation string
	Reason         string
	Candidates     []ComparisonCandidate
	WinnerIndex    int // 1-based for humans; 0 means none
	WinnerAgent    domain.AgentKind
	Tie            bool
}

type comparisonData struct {
	RunID            string
	Task             string
	Mode             string
	Recommendation   string
	Reason           string
	Candidates       []ComparisonCandidate
	HasWinner        bool
	WinnerIndex      int
	WinnerAgent      domain.AgentKind
	Tie              bool
	AnyProtectedHits bool
}

// RenderComparison renders comparison.md from the supplied inputs.
func RenderComparison(in ComparisonInputs) (string, error) {
	data := buildComparisonData(in)
	return executeWithFuncs("comparison", comparisonTmpl, data, template.FuncMap{
		"add": func(a, b int) int { return a + b },
	})
}

// WriteComparison renders comparison.md and writes it atomically under
// the run's artifact layout.
func WriteComparison(in ComparisonInputs, layout *artifacts.Layout) error {
	if layout == nil {
		return errors.New("reports: nil layout")
	}
	body, err := RenderComparison(in)
	if err != nil {
		return fmt.Errorf("reports: render comparison: %w", err)
	}
	if err := layout.WriteFileAtomic(layout.ComparisonPath(), []byte(body), 0o644); err != nil {
		return fmt.Errorf("reports: write comparison: %w", err)
	}
	return nil
}

func buildComparisonData(in ComparisonInputs) comparisonData {
	d := comparisonData{
		RunID:          in.RunID,
		Task:           strings.TrimSpace(in.Task),
		Mode:           in.Mode,
		Recommendation: in.Recommendation,
		Reason:         in.Reason,
		Candidates:     make([]ComparisonCandidate, len(in.Candidates)),
		Tie:            in.Tie,
	}
	if d.Task == "" {
		d.Task = "_no task recorded_"
	}
	if in.WinnerIndex > 0 {
		d.HasWinner = true
		d.WinnerIndex = in.WinnerIndex
		d.WinnerAgent = in.WinnerAgent
	}
	for i, c := range in.Candidates {
		c.VerificationStatus = verificationStatus(c.Verifications)
		d.Candidates[i] = c
		if len(c.ProtectedHits) > 0 {
			d.AnyProtectedHits = true
		}
	}
	return d
}

// ----- view models --------------------------------------------------------

type proofPackData struct {
	RunID              string
	Task               string
	Mode               string
	Status             string
	Recommendation     string
	StartedAt          string
	FinishedAt         string
	HasAgent           bool
	PrimaryAgent       domain.AgentRunResult
	ChangedFiles       []string
	ProtectedHits      []string
	ProtectedHitDetails []domain.ProtectedPathHit
	HasProtectedHits   bool
	ChangedFileCount   int
	MaxChangedFiles    int
	ExceedsMaxChanged  bool
	Verifications      []domain.VerificationResult
	AgentSummary       string
	AgentRisks         []string
	HasReviewer        bool
	ReviewerAgent      domain.AgentRunResult
	ReviewFindings     *domain.ReviewFindings
	DiffPatchPath      string
	Warnings           []string
	NextHumanAction    string
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
	for i := 1; i < len(r.AgentResults); i++ {
		if r.AgentResults[i].Role == domain.RoleReviewer {
			d.HasReviewer = true
			d.ReviewerAgent = r.AgentResults[i]
			d.ReviewFindings = r.AgentResults[i].Review
			break
		}
	}
	// Prefer the orchestrator-supplied safety report (it carries the
	// patterns each path matched and the size-limit verdict). Fall
	// back to a fresh scan over Inputs.ProtectedPaths so callers that
	// haven't migrated yet still get protected-path output.
	if r.Safety != nil {
		d.ProtectedHitDetails = append([]domain.ProtectedPathHit(nil), r.Safety.ProtectedHits...)
		d.ProtectedHits = make([]string, 0, len(r.Safety.ProtectedHits))
		for _, h := range r.Safety.ProtectedHits {
			d.ProtectedHits = append(d.ProtectedHits, h.Path)
		}
		d.ChangedFileCount = r.Safety.ChangedFileCount
		d.MaxChangedFiles = r.Safety.MaxChangedFiles
		d.ExceedsMaxChanged = r.Safety.ExceedsMaxChanged
	} else {
		d.ProtectedHits = collectProtectedHits(d.ChangedFiles, in.ProtectedPaths)
		for _, p := range d.ProtectedHits {
			d.ProtectedHitDetails = append(d.ProtectedHitDetails, domain.ProtectedPathHit{Path: p})
		}
		d.ChangedFileCount = len(d.ChangedFiles)
	}
	d.HasProtectedHits = len(d.ProtectedHitDetails) > 0
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
	return executeWithFuncs(name, tmpl, data, nil)
}

func executeWithFuncs(name, tmpl string, data any, funcs template.FuncMap) (string, error) {
	t := template.New(name)
	if funcs != nil {
		t = t.Funcs(funcs)
	}
	t, err := t.Parse(tmpl)
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
