package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/awo-dev/awo/internal/agents"
	"github.com/awo-dev/awo/internal/artifacts"
	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/awo-dev/awo/internal/reports"
	"github.com/awo-dev/awo/internal/runid"
)

// SingleRunOptions captures everything needed to run a single-agent
// orchestration end-to-end.
type SingleRunOptions struct {
	RepoRoot         string
	Task             string
	Agent            domain.AgentKind
	VerifyCommands   []string
	BaseBranch       string
	KeepWorktrees    bool
	DryRun           bool
	LiveOutput       bool
	MaxChangedFiles  int // 0 → cfg.Safety.MaxChangedFiles
	Config           config.AwoConfig

	// AgentFactory is optional. When nil, agents.New is used. Tests
	// inject a fake factory to avoid spawning real CLIs.
	AgentFactory func(domain.AgentKind, config.AwoConfig) (agents.Agent, error)

	// GitFacade is optional. When nil, defaultGit is used. Tests inject
	// a stub so they don't need a real git binary or worktree.
	GitFacade GitFacade

	// VerifyOptions exposes verification knobs (mainly for tests).
	VerifyOptions VerificationOptions

	// Stdout receives the human-readable run summary. Defaults to
	// os.Stdout when nil.
	Stdout io.Writer
}

// GitFacade is the slice of gitx that orchestration modes need.
// Production code uses defaultGit; tests inject a fake.
type GitFacade interface {
	CreateWorktree(ctx context.Context, opts gitx.CreateWorktreeOptions) (*gitx.WorktreeInfo, error)
	GetChangedFiles(ctx context.Context, worktreePath string) ([]string, error)
	GetDiffPatch(ctx context.Context, worktreePath string) (string, error)
	GetDiffStat(ctx context.Context, worktreePath string) (string, error)
	ApplyPatch(ctx context.Context, worktreePath, patchPath string) error
	RemoveWorktree(ctx context.Context, opts gitx.RemoveWorktreeOptions) error
}

type defaultGit struct{}

func (defaultGit) CreateWorktree(ctx context.Context, o gitx.CreateWorktreeOptions) (*gitx.WorktreeInfo, error) {
	return gitx.CreateWorktree(ctx, o)
}
func (defaultGit) GetChangedFiles(ctx context.Context, p string) ([]string, error) {
	return gitx.GetChangedFiles(ctx, p)
}
func (defaultGit) GetDiffPatch(ctx context.Context, p string) (string, error) {
	return gitx.GetDiffPatch(ctx, p)
}
func (defaultGit) GetDiffStat(ctx context.Context, p string) (string, error) {
	return gitx.GetDiffStat(ctx, p)
}
func (defaultGit) ApplyPatch(ctx context.Context, worktreePath, patchPath string) error {
	return gitx.ApplyPatch(ctx, worktreePath, patchPath)
}
func (defaultGit) RemoveWorktree(ctx context.Context, o gitx.RemoveWorktreeOptions) error {
	return gitx.RemoveWorktree(ctx, o)
}

// RunSingle executes the single-mode orchestration and returns the
// resulting RunReport. The report is also persisted under the run's
// artifact directory; callers may inspect either.
//
// Hard rules enforced here:
//   - One worktree, one agent.
//   - Changed files come from `git status`, never from the agent.
//   - Verification exit codes are the only trusted success signal.
//   - No commits, no merges, no pushes. Worktree is removed only when
//     KeepWorktrees is false AND the path is under <repo>/.awo/worktrees.
//   - Cleanup failures never destroy artifacts.
func RunSingle(ctx context.Context, opts SingleRunOptions) (*domain.RunReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateSingleOptions(&opts); err != nil {
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
			Mode:            domain.ModeSingle,
			Agent:           agentPtr(opts.Agent),
			VerifyCommands:  append([]string(nil), opts.VerifyCommands...),
			BaseBranch:      opts.BaseBranch,
			KeepWorktrees:   opts.KeepWorktrees,
			DryRun:          opts.DryRun,
			MaxChangedFiles: opts.MaxChangedFiles,
		},
		Status:    domain.StatusPreparing,
		StartedAt: time.Now().UTC(),
	}

	// Persist a preliminary run.json so a crash leaves something behind.
	_ = layout.WriteJSONAtomic(layout.RunJSONPath(), report)

	wt, err := gitFacade.CreateWorktree(ctx, gitx.CreateWorktreeOptions{
		RepoRoot:     opts.RepoRoot,
		RunID:        rid,
		Agent:        string(opts.Agent),
		Role:         string(domain.RoleWriter),
		BaseBranch:   opts.BaseBranch,
		BranchPrefix: opts.Config.BranchPrefix,
	})
	if err != nil {
		report.Status = domain.StatusFailed
		report.FinishedAt = time.Now().UTC()
		report.Warnings = append(report.Warnings, "create worktree: "+err.Error())
		_ = persistReport(layout, report)
		return report, fmt.Errorf("orchestrator: create worktree: %w", err)
	}

	defer func() {
		if opts.KeepWorktrees {
			return
		}
		rerr := gitFacade.RemoveWorktree(ctx, gitx.RemoveWorktreeOptions{
			RepoRoot:     opts.RepoRoot,
			WorktreePath: wt.Path,
			Force:        false,
		})
		if rerr != nil {
			report.Warnings = append(report.Warnings, "cleanup worktree: "+rerr.Error())
			_ = persistReport(layout, report)
		}
	}()

	// Build the writer prompt. Existing files/diff are usually empty for
	// a fresh worktree; we still pass them so the template stays honest.
	preChanged, _ := gitFacade.GetChangedFiles(ctx, wt.Path)
	preDiff, _ := gitFacade.GetDiffPatch(ctx, wt.Path)
	prompt, err := agents.BuildWriterPrompt(agents.PromptInput{
		Task:           opts.Task,
		Mode:           string(domain.ModeSingle),
		WorktreePath:   wt.Path,
		ChangedFiles:   preChanged,
		Diff:           preDiff,
		ProtectedPaths: opts.Config.Safety.ProtectedPaths,
	})
	if err != nil {
		report.Status = domain.StatusFailed
		report.FinishedAt = time.Now().UTC()
		report.Warnings = append(report.Warnings, "build prompt: "+err.Error())
		_ = persistReport(layout, report)
		return report, fmt.Errorf("orchestrator: build prompt: %w", err)
	}

	report.Status = domain.StatusRunning
	_ = persistReport(layout, report)

	agent, err := agentFactory(opts.Agent, opts.Config)
	if err != nil {
		report.Status = domain.StatusFailed
		report.FinishedAt = time.Now().UTC()
		report.Warnings = append(report.Warnings, "construct agent: "+err.Error())
		_ = persistReport(layout, report)
		return report, fmt.Errorf("orchestrator: construct agent: %w", err)
	}
	agentArtifactDir := layout.AgentDir(string(opts.Agent), string(domain.RoleWriter))

	agentRes, agentErr := agent.Run(ctx, agents.AgentRunInput{
		RunID:        rid,
		Task:         opts.Task,
		Role:         domain.RoleWriter,
		Mode:         domain.ModeSingle,
		WorktreePath: wt.Path,
		BranchName:   wt.Branch,
		ArtifactDir:  agentArtifactDir,
		Config:       opts.Config,
		Prompt:       prompt,
		DryRun:       opts.DryRun,
		LiveOutput:   opts.LiveOutput,
	})
	if agentErr != nil {
		report.Warnings = append(report.Warnings, "agent invocation: "+agentErr.Error())
	}

	// Always derive changed-file truth from git, never from the agent.
	changedFiles, _ := gitFacade.GetChangedFiles(ctx, wt.Path)
	diffPatch, _ := gitFacade.GetDiffPatch(ctx, wt.Path)

	if err := layout.WriteFileAtomic(layout.DiffPatchPath(), []byte(diffPatch), 0o644); err != nil {
		report.Warnings = append(report.Warnings, "write diff.patch: "+err.Error())
	}

	ar := buildAgentResult(agentRes, agentErr, wt, changedFiles)
	report.AgentResults = append(report.AgentResults, ar)
	report.Status = domain.StatusVerifying
	_ = persistReport(layout, report)

	// Verification.
	verifyResults, verifyErr := runVerification(ctx, wt.Path, opts.VerifyCommands, layout, opts.Config, opts.VerifyOptions)
	report.VerificationResults = verifyResults
	if verifyErr != nil {
		report.Warnings = append(report.Warnings, "verification: "+verifyErr.Error())
	}

	// Safety analysis + recommendation.
	maxFiles := opts.MaxChangedFiles
	if maxFiles <= 0 {
		maxFiles = opts.Config.Safety.MaxChangedFiles
	}
	report.Safety = AnalyzeSafety(changedFiles, opts.Config.Safety.ProtectedPaths, maxFiles)
	report.Recommendation = recommendSingle(report)
	report.FinishedAt = time.Now().UTC()
	if report.Recommendation == domain.RecFailedVerification {
		report.Status = domain.StatusFailed
	} else {
		report.Status = domain.StatusCompleted
	}

	if err := reports.WriteRunReportFiles(reports.Inputs{
		Report:         *report,
		ProtectedPaths: opts.Config.Safety.ProtectedPaths,
		DiffPatchPath:  layout.DiffPatchPath(),
	}, layout); err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.ProofPackPath = layout.ProofPackPath()

	if err := persistReport(layout, report); err != nil {
		return report, fmt.Errorf("orchestrator: persist run.json: %w", err)
	}

	printRunSummary(opts.Stdout, report, layout, wt)
	return report, nil
}

func validateSingleOptions(opts *SingleRunOptions) error {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return errors.New("orchestrator: SingleRunOptions.RepoRoot required")
	}
	if strings.TrimSpace(opts.Task) == "" {
		return errors.New("orchestrator: SingleRunOptions.Task required")
	}
	if err := opts.Agent.Validate(); err != nil {
		return fmt.Errorf("orchestrator: %w", err)
	}
	if err := opts.Config.Validate(); err != nil {
		return fmt.Errorf("orchestrator: invalid config: %w", err)
	}
	return nil
}

func agentPtr(k domain.AgentKind) *domain.AgentKind { return &k }

func buildAgentResult(
	res *agents.AgentRunResult,
	runErr error,
	wt *gitx.WorktreeInfo,
	changedFiles []string,
) domain.AgentRunResult {
	out := domain.AgentRunResult{
		WorktreePath: wt.Path,
		BranchName:   wt.Branch,
		ChangedFiles: append([]string(nil), changedFiles...),
	}
	if res != nil {
		out.Agent = res.Agent
		out.Role = res.Role
		out.StartedAt = res.StartedAt
		out.FinishedAt = res.FinishedAt
		out.ExitCode = res.ExitCode
		out.StdoutPath = res.StdoutPath
		out.StderrPath = res.StderrPath
		out.ParsedResult = res.ParsedResult
		if res.ParsedReview != nil {
			out.Review = &domain.ReviewFindings{
				Blocking:       append([]string(nil), res.ParsedReview.Blocking...),
				NonBlocking:    append([]string(nil), res.ParsedReview.NonBlocking...),
				SuggestedTests: append([]string(nil), res.ParsedReview.SuggestedTests...),
				RiskSummary:    res.ParsedReview.RiskSummary,
				Recommendation: res.ParsedReview.Recommendation,
			}
		}
		if len(res.Warnings) > 0 {
			out.Warnings = append(out.Warnings, res.Warnings...)
		}
		switch {
		case res.DryRun:
			out.Status = "dry-run"
		case res.TimedOut:
			out.Status = "timed-out"
		case res.ExitCode == 0:
			out.Status = "ok"
		default:
			out.Status = "failed"
		}
	}
	if runErr != nil {
		out.Status = "failed"
		out.Warnings = append(out.Warnings, runErr.Error())
	}
	return out
}

// recommendSingle applies the single-mode verdict ladder. Failed
// verification always wins; a run with no changes (whether the agent
// failed or succeeded but produced nothing) cannot be "ready for
// human review" because there is nothing to review and verification
// ran against an unchanged worktree. Otherwise the safety analysis
// (protected paths, patch size) decides whether to escalate from
// "ready for human review".
func recommendSingle(r *domain.RunReport) domain.Recommendation {
	if len(r.VerificationResults) > 0 && !AllPassed(r.VerificationResults) {
		return domain.RecFailedVerification
	}
	if rec, ok := emptyRunVerdict(r); ok {
		return rec
	}
	return escalateForSafety(domain.RecReadyForHumanReview, r.Safety)
}

// emptyRunVerdict produces a recommendation for a run that produced
// no candidate diff. There are two flavors:
//
//   - The agent itself reported failure / timeout. The verification
//     verdict (run against an unchanged worktree) is meaningless;
//     surface this as needs_human_attention so a human looks at the
//     stderr/log to see what happened.
//   - The agent succeeded but produced nothing. No human action is
//     possible — there is no diff to merge — so report no_recommendation
//     instead of misleadingly claiming "ready for human review".
//
// Returns (rec, true) when one of those applies; (_, false) means the
// run did produce changed files and the normal ladder should run.
func emptyRunVerdict(r *domain.RunReport) (domain.Recommendation, bool) {
	if r == nil {
		return "", false
	}
	totalChanged := 0
	writerFailed := false
	sawWriter := false
	for _, ar := range r.AgentResults {
		totalChanged += len(ar.ChangedFiles)
		if ar.Role != domain.RoleWriter && ar.Role != domain.RoleCompetitor {
			continue
		}
		sawWriter = true
		if ar.Status == "failed" || ar.Status == "timed-out" {
			writerFailed = true
		}
	}
	if totalChanged > 0 || !sawWriter {
		return "", false
	}
	if writerFailed {
		return domain.RecNeedsHumanAttention, true
	}
	return domain.RecNoRecommendation, true
}

func persistReport(layout *artifacts.Layout, r *domain.RunReport) error {
	return layout.WriteJSONAtomic(layout.RunJSONPath(), r)
}

func printRunSummary(out io.Writer, r *domain.RunReport, layout *artifacts.Layout, wt *gitx.WorktreeInfo) {
	fmt.Fprintf(out, "AWO run %s\n", r.RunID)
	fmt.Fprintf(out, "  status:         %s\n", r.Status)
	if r.Recommendation != "" {
		fmt.Fprintf(out, "  recommendation: %s\n", r.Recommendation)
	}
	fmt.Fprintf(out, "  worktree:       %s\n", wt.Path)
	fmt.Fprintf(out, "  branch:         %s\n", wt.Branch)

	if len(r.AgentResults) > 0 {
		ar := r.AgentResults[0]
		fmt.Fprintf(out, "  agent:          %s/%s — %s (exit %d)\n",
			ar.Agent, ar.Role, ar.Status, ar.ExitCode)
		fmt.Fprintf(out, "  changed files:  %d\n", len(ar.ChangedFiles))
	}
	if len(r.VerificationResults) == 0 {
		fmt.Fprintln(out, "  verification:   not verified")
	} else {
		passed, total := 0, len(r.VerificationResults)
		for _, v := range r.VerificationResults {
			if v.Passed {
				passed++
			}
		}
		fmt.Fprintf(out, "  verification:   %d/%d passed\n", passed, total)
	}
	printSafetyHighlights(out, r.Safety)
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "  warning:        %s\n", w)
	}
	fmt.Fprintf(out, "  artifacts:      %s\n", layout.Root)
	fmt.Fprintf(out, "  proof pack:     %s\n", filepath.Join(layout.Root, "proof-pack.md"))
}
