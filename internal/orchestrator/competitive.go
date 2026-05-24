package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/awo-dev/awo/internal/agents"
	"github.com/awo-dev/awo/internal/artifacts"
	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/awo-dev/awo/internal/reports"
	"github.com/awo-dev/awo/internal/runid"
)

// CompetitiveRunOptions captures everything needed to run two agents
// against the same task in parallel.
type CompetitiveRunOptions struct {
	RepoRoot        string
	Task            string
	Competitors     []domain.AgentKind
	VerifyCommands  []string
	BaseBranch      string
	KeepWorktrees   bool
	DryRun          bool
	LiveOutput      bool
	MaxChangedFiles int
	Config          config.AwoConfig

	AgentFactory  func(domain.AgentKind, config.AwoConfig) (agents.Agent, error)
	GitFacade     GitFacade
	VerifyOptions VerificationOptions
	Stdout        io.Writer
}

// RunCompetitive runs each competitor in its own isolated worktree
// concurrently, scores the results with the deterministic heuristic in
// scoring.go, and writes a comparison.md alongside the standard proof
// pack. It never lets a competitor see the other competitor's worktree
// and never auto-applies any candidate.
//
// Hard rules (mirrored from spec):
//   - Exactly two unique competitors from {claude, codex}.
//   - One worktree per competitor.
//   - Competitors run in parallel via goroutines.
//   - Verification runs in each competitor's worktree independently.
//   - No commits, merges, pushes, or fetches.
//   - Cleanup is best-effort and never destroys artifacts.
func RunCompetitive(ctx context.Context, opts CompetitiveRunOptions) (*domain.RunReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateCompetitiveOptions(&opts); err != nil {
		return nil, err
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	gitFacade := opts.GitFacade
	if gitFacade == nil {
		gitFacade = defaultGit{}
	}
	agentFactory := opts.AgentFactory
	if agentFactory == nil {
		agentFactory = agents.New
	}

	rid := runid.New()
	layout, err := artifacts.NewLayout(opts.RepoRoot, opts.Config.ArtifactDir, rid)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: layout: %w", err)
	}
	if err := layout.Ensure(); err != nil {
		return nil, fmt.Errorf("orchestrator: ensure layout: %w", err)
	}

	report := &domain.RunReport{
		RunID: rid,
		Spec: domain.RunSpec{
			Task:            opts.Task,
			Mode:            domain.ModeCompetitive,
			Competitors:     append([]domain.AgentKind(nil), opts.Competitors...),
			VerifyCommands:  append([]string(nil), opts.VerifyCommands...),
			BaseBranch:      opts.BaseBranch,
			KeepWorktrees:   opts.KeepWorktrees,
			DryRun:          opts.DryRun,
			MaxChangedFiles: opts.MaxChangedFiles,
		},
		Status:    domain.StatusPreparing,
		StartedAt: time.Now().UTC(),
	}
	_ = layout.WriteJSONAtomic(layout.RunJSONPath(), report)

	// Run each competitor in its own goroutine. We collect the
	// per-candidate state into ordered slots so report ordering matches
	// opts.Competitors regardless of finish order.
	candidates := make([]competitorState, len(opts.Competitors))

	report.Status = domain.StatusRunning
	_ = persistReport(layout, report)

	var wg sync.WaitGroup
	for i, kind := range opts.Competitors {
		wg.Add(1)
		go func(i int, kind domain.AgentKind) {
			defer wg.Done()
			candidates[i] = runOneCompetitor(ctx, runOneCompetitorInput{
				index:         i,
				kind:          kind,
				rid:           rid,
				opts:          opts,
				layout:        layout,
				gitFacade:     gitFacade,
				agentFactory:  agentFactory,
			})
		}(i, kind)
	}
	wg.Wait()

	// Schedule cleanup *after* all goroutines have finished so removal
	// never races with the agent process.
	for _, c := range candidates {
		if c.wt == nil {
			continue
		}
		defer scheduleCleanup(ctx, gitFacade, opts.RepoRoot, c.wt.Path, opts.KeepWorktrees, layout, report, "competitor "+string(c.agent))
	}

	report.Status = domain.StatusVerifying
	_ = persistReport(layout, report)

	// Verification (in each worktree). Done sequentially so log paths
	// don't clash and so resource use is bounded.
	for i := range candidates {
		c := &candidates[i]
		if c.wt == nil {
			continue
		}
		results, verifyErr := runVerificationForCompetitor(ctx, c.wt.Path, opts.VerifyCommands, layout, opts.Config, opts.VerifyOptions, c.index)
		c.snapshot.Verifications = results
		c.ar.Status = competitorStatus(results, c.ar.ExitCode)
		if verifyErr != nil {
			c.warnings = append(c.warnings, "verification: "+verifyErr.Error())
		}
	}

	// Aggregate per-candidate results into the report and score them.
	snaps := make([]CandidateSnapshot, len(candidates))
	scores := make([]ScoreBreakdown, len(candidates))
	for i, c := range candidates {
		report.AgentResults = append(report.AgentResults, c.ar)
		report.VerificationResults = append(report.VerificationResults, c.snapshot.Verifications...)
		report.Warnings = appendIfNonEmpty(report.Warnings, c.warnings)
		snaps[i] = c.snapshot
		scores[i] = ScoreCandidate(c.snapshot)
	}

	// Compare and recommend.
	verdict := CompareCandidates(snaps, scores)
	report.Recommendation = verdict.Recommendation
	report.FinishedAt = time.Now().UTC()
	report.Status = pickCompetitiveStatus(verdict.Recommendation)

	// Render artifacts. Comparison + proof pack + summary all derive from
	// the same final report.
	if err := writeCompetitiveArtifacts(opts, layout, report, snaps, scores, verdict); err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.ProofPackPath = layout.ProofPackPath()
	_ = persistReport(layout, report)

	printCompetitiveSummary(opts.Stdout, report, layout, candidates, scores, verdict)
	return report, nil
}

// competitorState bundles the per-candidate state the orchestrator
// builds up as it runs. We pre-allocate one slot per competitor so
// goroutines can write to disjoint indices without coordinating.
type competitorState struct {
	index    int
	agent    domain.AgentKind
	wt       *gitx.WorktreeInfo
	ar       domain.AgentRunResult
	snapshot CandidateSnapshot
	warnings []string
}

// ----- per-candidate execution -------------------------------------------

type runOneCompetitorInput struct {
	index        int
	kind         domain.AgentKind
	rid          string
	opts         CompetitiveRunOptions
	layout       *artifacts.Layout
	gitFacade    GitFacade
	agentFactory func(domain.AgentKind, config.AwoConfig) (agents.Agent, error)
}

func runOneCompetitor(ctx context.Context, in runOneCompetitorInput) competitorState {
	out := competitorState{index: in.index, agent: in.kind}

	wt, err := in.gitFacade.CreateWorktree(ctx, gitx.CreateWorktreeOptions{
		RepoRoot:     in.opts.RepoRoot,
		RunID:        in.rid,
		Agent:        string(in.kind),
		Role:         string(domain.RoleCompetitor),
		BaseBranch:   in.opts.BaseBranch,
		BranchPrefix: in.opts.Config.BranchPrefix,
	})
	if err != nil {
		out.warnings = append(out.warnings, fmt.Sprintf("create worktree (%s): %s", in.kind, err.Error()))
		out.ar = domain.AgentRunResult{Agent: in.kind, Role: domain.RoleCompetitor, Status: "failed"}
		out.snapshot = CandidateSnapshot{Agent: in.kind}
		return out
	}
	out.wt = wt

	prompt, err := agents.BuildCompetitorPrompt(agents.PromptInput{
		Task:           in.opts.Task,
		Mode:           string(domain.ModeCompetitive),
		WorktreePath:   wt.Path,
		ProtectedPaths: in.opts.Config.Safety.ProtectedPaths,
	})
	if err != nil {
		out.warnings = append(out.warnings, fmt.Sprintf("build prompt (%s): %s", in.kind, err.Error()))
		out.ar = domain.AgentRunResult{Agent: in.kind, Role: domain.RoleCompetitor, WorktreePath: wt.Path, BranchName: wt.Branch, Status: "failed"}
		out.snapshot = CandidateSnapshot{Agent: in.kind}
		return out
	}

	agent, err := in.agentFactory(in.kind, in.opts.Config)
	if err != nil {
		out.warnings = append(out.warnings, fmt.Sprintf("construct agent (%s): %s", in.kind, err.Error()))
		out.ar = domain.AgentRunResult{Agent: in.kind, Role: domain.RoleCompetitor, WorktreePath: wt.Path, BranchName: wt.Branch, Status: "failed"}
		out.snapshot = CandidateSnapshot{Agent: in.kind}
		return out
	}
	artifactDir := in.layout.AgentDir(string(in.kind), string(domain.RoleCompetitor))
	res, runErr := agent.Run(ctx, agents.AgentRunInput{
		RunID:        in.rid,
		Task:         in.opts.Task,
		Role:         domain.RoleCompetitor,
		Mode:         domain.ModeCompetitive,
		WorktreePath: wt.Path,
		BranchName:   wt.Branch,
		ArtifactDir:  artifactDir,
		Config:       in.opts.Config,
		Prompt:       prompt,
		DryRun:       in.opts.DryRun,
		LiveOutput:   in.opts.LiveOutput,
	})
	if runErr != nil {
		out.warnings = append(out.warnings, fmt.Sprintf("agent invocation (%s): %s", in.kind, runErr.Error()))
	}

	// Truth from git, never from the agent.
	changed, _ := in.gitFacade.GetChangedFiles(ctx, wt.Path)
	diff, _ := in.gitFacade.GetDiffPatch(ctx, wt.Path)

	// Persist this competitor's diff.patch under the agent dir so users
	// can inspect each candidate independently.
	if err := os.MkdirAll(artifactDir, 0o755); err == nil {
		_ = os.WriteFile(filepath.Join(artifactDir, "diff.patch"), []byte(diff), 0o644)
	}

	ar := buildAgentResult(res, runErr, wt, changed)
	out.ar = ar
	out.snapshot = CandidateSnapshot{
		Agent:           in.kind,
		ChangedFiles:    append([]string(nil), changed...),
		DiffLines:       CountDiffLines(diff),
		TestFiles:       CollectTestFiles(changed),
		ProtectedHits:   CollectProtectedHits(changed, in.opts.Config.Safety.ProtectedPaths),
		AgentSummary:    agentSummaryFrom(res),
		AgentRisks:      agentRisksFrom(res),
		AgentConfidence: confidenceFrom(res),
	}
	return out
}

// runVerificationForCompetitor runs verification commands for one
// competitor. Each candidate's commands run sequentially after the
// parallel agent phase, so indices in verify/ stay stable across
// candidates and don't need a per-competitor offset.
func runVerificationForCompetitor(
	ctx context.Context,
	worktreePath string,
	commands []string,
	layout *artifacts.Layout,
	cfg config.AwoConfig,
	opts VerificationOptions,
	_ int,
) ([]domain.VerificationResult, error) {
	return runVerification(ctx, worktreePath, commands, layout, cfg, opts)
}

// ----- artifact rendering -------------------------------------------------

func writeCompetitiveArtifacts(
	opts CompetitiveRunOptions,
	layout *artifacts.Layout,
	report *domain.RunReport,
	snaps []CandidateSnapshot,
	scores []ScoreBreakdown,
	verdict CompareVerdict,
) error {
	cmpInputs := buildComparisonInputs(report, snaps, scores, verdict)
	if err := reports.WriteComparison(cmpInputs, layout); err != nil {
		return err
	}
	// Proof pack + summary still get rendered so callers always have
	// the same canonical files.
	if err := reports.WriteRunReportFiles(reports.Inputs{
		Report:         *report,
		ProtectedPaths: opts.Config.Safety.ProtectedPaths,
		DiffPatchPath:  layout.DiffPatchPath(),
	}, layout); err != nil {
		return err
	}
	return nil
}

func buildComparisonInputs(
	report *domain.RunReport,
	snaps []CandidateSnapshot,
	scores []ScoreBreakdown,
	verdict CompareVerdict,
) reports.ComparisonInputs {
	cands := make([]reports.ComparisonCandidate, len(snaps))
	for i, s := range snaps {
		cands[i] = reports.ComparisonCandidate{
			Agent:         s.Agent,
			ChangedFiles:  append([]string(nil), s.ChangedFiles...),
			DiffLines:     s.DiffLines,
			TestFiles:     append([]string(nil), s.TestFiles...),
			ProtectedHits: append([]string(nil), s.ProtectedHits...),
			Verifications: append([]domain.VerificationResult(nil), s.Verifications...),
			AgentRisks:    append([]string(nil), s.AgentRisks...),
			Score:         scores[i].Total,
			ScoreNotes:    append([]string(nil), scores[i].Notes...),
		}
	}
	in := reports.ComparisonInputs{
		RunID:          report.RunID,
		Task:           report.Spec.Task,
		Mode:           string(report.Spec.Mode),
		Recommendation: string(verdict.Recommendation),
		Reason:         verdict.Reason,
		Candidates:     cands,
		Tie:            verdict.Tie,
	}
	if verdict.WinnerIndex >= 0 && verdict.WinnerIndex < len(snaps) {
		in.WinnerIndex = verdict.WinnerIndex + 1
		in.WinnerAgent = snaps[verdict.WinnerIndex].Agent
	}
	return in
}

// ----- console output -----------------------------------------------------

func printCompetitiveSummary(
	out io.Writer,
	r *domain.RunReport,
	layout *artifacts.Layout,
	cands []competitorState,
	scores []ScoreBreakdown,
	verdict CompareVerdict,
) {
	fmt.Fprintf(out, "AWO run %s\n", r.RunID)
	fmt.Fprintf(out, "  status:         %s\n", r.Status)
	fmt.Fprintf(out, "  recommendation: %s\n", r.Recommendation)
	fmt.Fprintf(out, "  reason:         %s\n", verdict.Reason)
	for i, c := range cands {
		passed, total := 0, len(c.snapshot.Verifications)
		for _, v := range c.snapshot.Verifications {
			if v.Passed {
				passed++
			}
		}
		var verifStr string
		if total == 0 {
			verifStr = "not verified"
		} else {
			verifStr = fmt.Sprintf("%d/%d passed", passed, total)
		}
		marker := "  "
		if verdict.WinnerIndex == i {
			marker = "* "
		}
		fmt.Fprintf(out, "%scandidate %d:  %s — files=%d, diff=%d, tests=%d, protected=%d, score=%.2f, verify=%s\n",
			marker,
			i+1,
			c.agent,
			len(c.snapshot.ChangedFiles),
			c.snapshot.DiffLines,
			len(c.snapshot.TestFiles),
			len(c.snapshot.ProtectedHits),
			scores[i].Total,
			verifStr,
		)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "  warning:        %s\n", w)
	}
	fmt.Fprintf(out, "  artifacts:      %s\n", layout.Root)
	fmt.Fprintf(out, "  comparison:     %s\n", filepath.Join(layout.Root, "comparison.md"))
	fmt.Fprintf(out, "  proof pack:     %s\n", filepath.Join(layout.Root, "proof-pack.md"))
}

// ----- helpers ------------------------------------------------------------

func validateCompetitiveOptions(opts *CompetitiveRunOptions) error {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return errors.New("orchestrator: CompetitiveRunOptions.RepoRoot required")
	}
	if strings.TrimSpace(opts.Task) == "" {
		return errors.New("orchestrator: CompetitiveRunOptions.Task required")
	}
	if len(opts.Competitors) != 2 {
		return errors.New("orchestrator: competitive mode requires exactly two competitors")
	}
	if opts.Competitors[0] == opts.Competitors[1] {
		return errors.New("orchestrator: competitive mode requires two distinct agents")
	}
	for i, k := range opts.Competitors {
		if err := k.Validate(); err != nil {
			return fmt.Errorf("orchestrator: competitor %d: %w", i+1, err)
		}
	}
	if err := opts.Config.Validate(); err != nil {
		return fmt.Errorf("orchestrator: invalid config: %w", err)
	}
	return nil
}

func pickCompetitiveStatus(rec domain.Recommendation) domain.RunStatus {
	if rec == domain.RecFailedVerification {
		return domain.StatusFailed
	}
	return domain.StatusCompleted
}

func competitorStatus(res []domain.VerificationResult, exit int) string {
	switch {
	case len(res) > 0 && AllPassed(res):
		return "ok"
	case len(res) > 0:
		return "failed"
	case exit == 0:
		return "ok"
	default:
		return "failed"
	}
}

func appendIfNonEmpty(dst, src []string) []string {
	for _, s := range src {
		if strings.TrimSpace(s) == "" {
			continue
		}
		dst = append(dst, s)
	}
	return dst
}

func agentSummaryFrom(res *agents.AgentRunResult) string {
	if res == nil || res.ParsedResult == nil {
		return ""
	}
	return strings.TrimSpace(res.ParsedResult.Summary)
}

func agentRisksFrom(res *agents.AgentRunResult) []string {
	if res == nil || res.ParsedResult == nil {
		return nil
	}
	return append([]string(nil), res.ParsedResult.Notes...)
}

func confidenceFrom(res *agents.AgentRunResult) string {
	if res == nil || res.ParsedResult == nil {
		return ""
	}
	for _, n := range res.ParsedResult.Notes {
		if strings.HasPrefix(n, "confidence: ") {
			return strings.TrimSpace(strings.TrimPrefix(n, "confidence: "))
		}
	}
	return ""
}

// ParseCompetitorList accepts a CSV string (e.g. "claude,codex") or a
// pre-split slice and returns a deduplicated, validated list of two
// agent kinds. Used by the CLI.
func ParseCompetitorList(input []string) ([]domain.AgentKind, error) {
	var raw []string
	for _, s := range input {
		for _, part := range strings.Split(s, ",") {
			if v := strings.TrimSpace(part); v != "" {
				raw = append(raw, v)
			}
		}
	}
	if len(raw) != 2 {
		return nil, fmt.Errorf("competitive mode requires exactly two competitors, got %d", len(raw))
	}
	out := make([]domain.AgentKind, 0, 2)
	for _, s := range raw {
		k := domain.AgentKind(s)
		if err := k.Validate(); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	if out[0] == out[1] {
		return nil, errors.New("competitive mode requires two distinct agents")
	}
	return out, nil
}
