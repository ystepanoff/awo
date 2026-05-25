// Package prhelper renders a human-facing PR description from an AWO
// RunReport. The output is written to disk by the CLI but never used to
// open a PR automatically — AWO doesn't commit, push, merge, or
// auto-approve. The human is the only thing that turns a generated
// pr-description.md into an actual pull request.
package prhelper

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"sort"
	"strings"
	"text/template"

	"github.com/awo-dev/awo/internal/domain"
)

//go:embed templates/pr_description.md.tmpl
var prTmpl string

// Inputs is everything the renderer needs. Report is the source of truth
// for the run; CandidateSelector identifies which agent in the report
// represents the change humans should turn into a PR (only meaningful
// for competitive mode); ProofPackPath is the relative path to
// proof-pack.md that goes into the description so reviewers can find it.
type Inputs struct {
	Report            domain.RunReport
	CandidateSelector string
	ProofPackPath     string
}

// Render returns the rendered pr-description.md body. It does not
// touch the filesystem; the CLI is responsible for writing the result.
func Render(in Inputs) (string, error) {
	if err := in.Report.Status.Validate(); err != nil {
		return "", fmt.Errorf("prhelper: invalid status %q: %w", in.Report.Status, err)
	}
	primary, err := SelectCandidate(in.Report, in.CandidateSelector)
	if err != nil {
		return "", err
	}
	data, err := buildData(in, primary)
	if err != nil {
		return "", err
	}
	t, err := template.New("pr").Parse(prTmpl)
	if err != nil {
		return "", fmt.Errorf("prhelper: parse template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("prhelper: render: %w", err)
	}
	return buf.String(), nil
}

// SelectCandidate picks the agent result that represents the change a
// human will turn into a PR. The selection rule depends on the mode:
//
//   - single:           the (only) writer.
//   - writer-reviewer:  the writer (always the first AgentResult).
//   - competitive:      the candidate that matches `selector`. The
//     selector is matched against agent name (e.g. "claude") or branch
//     name. If selector is empty in competitive mode an error is
//     returned, since there is no orchestrator-level "winner" stored on
//     the report.
//
// SelectCandidate returns ErrNoCandidate when the report has no agent
// results to choose from, and a wrapped error if the selector fails to
// match any candidate in competitive mode.
func SelectCandidate(r domain.RunReport, selector string) (domain.AgentRunResult, error) {
	if len(r.AgentResults) == 0 {
		return domain.AgentRunResult{}, ErrNoCandidate
	}
	switch r.Spec.Mode {
	case domain.ModeSingle:
		// Tolerate trailing reviewer-style entries by preferring the
		// first writer; fall back to AgentResults[0].
		if w := firstWithRole(r.AgentResults, domain.RoleWriter); w != nil {
			return *w, nil
		}
		return r.AgentResults[0], nil

	case domain.ModeWriterReviewer:
		if w := firstWithRole(r.AgentResults, domain.RoleWriter); w != nil {
			return *w, nil
		}
		return r.AgentResults[0], nil

	case domain.ModeCompetitive:
		if strings.TrimSpace(selector) == "" {
			return domain.AgentRunResult{}, fmt.Errorf("prhelper: --candidate is required for competitive mode (choose one of %s)",
				summarizeCandidates(r.AgentResults))
		}
		s := strings.TrimSpace(selector)
		for _, a := range r.AgentResults {
			if string(a.Agent) == s || a.BranchName == s {
				return a, nil
			}
		}
		return domain.AgentRunResult{}, fmt.Errorf("prhelper: candidate %q not found (available: %s)", selector, summarizeCandidates(r.AgentResults))

	default:
		return domain.AgentRunResult{}, fmt.Errorf("prhelper: unsupported mode %q", r.Spec.Mode)
	}
}

// ErrNoCandidate is returned when the run report has no agent results.
var ErrNoCandidate = errors.New("prhelper: run has no agent results to select from")

func firstWithRole(rs []domain.AgentRunResult, role domain.AgentRole) *domain.AgentRunResult {
	for i := range rs {
		if rs[i].Role == role {
			return &rs[i]
		}
	}
	return nil
}

func summarizeCandidates(rs []domain.AgentRunResult) string {
	xs := make([]string, 0, len(rs))
	for _, a := range rs {
		if a.Role == domain.RoleReviewer {
			continue
		}
		xs = append(xs, fmt.Sprintf("%s (%s)", a.Agent, a.BranchName))
	}
	if len(xs) == 0 {
		return "<none>"
	}
	return strings.Join(xs, ", ")
}

// ----- view model ---------------------------------------------------------

type prData struct {
	RunID             string
	Mode              string
	Task              string
	Recommendation    string
	HasRecommendation bool

	Candidate       candidateView
	IsCompetitive   bool
	CompetingAgents []string

	Verifications    []verificationView
	HasVerifications bool

	HasReviewer    bool
	ReviewerAgent  string
	ReviewFindings *domain.ReviewFindings

	HasProtectedHits  bool
	ProtectedHits     []domain.ProtectedPathHit
	ExceedsMaxChanged bool
	ChangedFileCount  int
	MaxChangedFiles   int

	ChangedFiles    []string
	HasChangedFiles bool

	AgentSummary string
	AgentRisks   []string

	ProofPackPath    string
	HasProofPackPath bool
}

type candidateView struct {
	Agent      string
	Role       string
	BranchName string
	Worktree   string
	Status     string
	ExitCode   int
}

type verificationView struct {
	Command  string
	ExitCode int
	Passed   bool
	Duration int64
}

func buildData(in Inputs, candidate domain.AgentRunResult) (prData, error) {
	r := in.Report

	d := prData{
		RunID:             r.RunID,
		Mode:              string(r.Spec.Mode),
		Task:              strings.TrimSpace(r.Spec.Task),
		Recommendation:    string(r.Recommendation),
		HasRecommendation: r.Recommendation != "" && r.Recommendation != domain.RecNoRecommendation,
		Candidate: candidateView{
			Agent:      string(candidate.Agent),
			Role:       string(candidate.Role),
			BranchName: candidate.BranchName,
			Worktree:   candidate.WorktreePath,
			Status:     candidate.Status,
			ExitCode:   candidate.ExitCode,
		},
		ProofPackPath:    strings.TrimSpace(in.ProofPackPath),
		HasProofPackPath: strings.TrimSpace(in.ProofPackPath) != "",
	}
	if d.Task == "" {
		d.Task = "_no task recorded_"
	}

	// Verification — only the run-level verifications matter for a PR
	// description; per-candidate verifications in competitive mode are
	// rolled up into report.VerificationResults by the orchestrator.
	for _, v := range r.VerificationResults {
		d.Verifications = append(d.Verifications, verificationView{
			Command:  v.Command,
			ExitCode: v.ExitCode,
			Passed:   v.Passed,
			Duration: v.DurationMillis,
		})
	}
	d.HasVerifications = len(d.Verifications) > 0

	// Reviewer findings (writer-reviewer only).
	if r.Spec.Mode == domain.ModeWriterReviewer {
		if rev := firstWithRole(r.AgentResults, domain.RoleReviewer); rev != nil {
			d.HasReviewer = true
			d.ReviewerAgent = string(rev.Agent)
			d.ReviewFindings = rev.Review
		}
	}

	// Competitive comparison surface.
	if r.Spec.Mode == domain.ModeCompetitive {
		d.IsCompetitive = true
		seen := map[domain.AgentKind]struct{}{}
		for _, a := range r.AgentResults {
			if a.Role == domain.RoleReviewer {
				continue
			}
			if _, ok := seen[a.Agent]; ok {
				continue
			}
			seen[a.Agent] = struct{}{}
			d.CompetingAgents = append(d.CompetingAgents, fmt.Sprintf("`%s` on `%s`", a.Agent, a.BranchName))
		}
	}

	// Safety: prefer the run-level safety report; fall back to the
	// candidate's changed files for the file list when no report was
	// produced (older runs predating safety analysis).
	if r.Safety != nil {
		d.HasProtectedHits = len(r.Safety.ProtectedHits) > 0
		d.ProtectedHits = append(d.ProtectedHits, r.Safety.ProtectedHits...)
		d.ExceedsMaxChanged = r.Safety.ExceedsMaxChanged
		d.ChangedFileCount = r.Safety.ChangedFileCount
		d.MaxChangedFiles = r.Safety.MaxChangedFiles
	}

	d.ChangedFiles = append([]string(nil), candidate.ChangedFiles...)
	sort.Strings(d.ChangedFiles)
	d.HasChangedFiles = len(d.ChangedFiles) > 0

	// Agent-reported summary / risks (advisory).
	if pr := candidate.ParsedResult; pr != nil {
		d.AgentSummary = strings.TrimSpace(pr.Summary)
		d.AgentRisks = append([]string(nil), pr.Notes...)
	}
	return d, nil
}
