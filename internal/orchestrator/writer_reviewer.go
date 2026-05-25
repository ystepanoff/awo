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

// WriterReviewerOptions captures everything needed to run a
// writer-reviewer orchestration end-to-end.
type WriterReviewerOptions struct {
	RepoRoot        string
	Task            string
	Primary         domain.AgentKind
	Reviewer        domain.AgentKind
	VerifyCommands  []string
	BaseBranch      string
	KeepWorktrees   bool
	DryRun          bool
	LiveOutput      bool
	MaxChangedFiles int
	Config          config.AwoConfig

	// AgentFactory is optional. When nil, agents.New is used. Tests
	// inject a fake factory to avoid spawning real CLIs.
	AgentFactory func(domain.AgentKind, config.AwoConfig) (agents.Agent, error)

	// GitFacade is optional. When nil, defaultGit is used.
	GitFacade GitFacade

	// VerifyOptions exposes verification knobs.
	VerifyOptions VerificationOptions

	// Stdout receives the human-readable run summary. Defaults to
	// os.Stdout when nil.
	Stdout io.Writer
}

// RunWriterReviewer runs the primary agent inside a writer worktree,
// runs verification there, then runs the reviewer agent inside a fresh
// worktree carved from the same base. The reviewer is read-only: it is
// instructed not to modify files, and any modifications it makes anyway
// are detected and recorded as warnings.
//
// Hard rules enforced here:
//   - Two worktrees: one writer, one reviewer.
//   - Reviewer prompt explicitly says "do not modify".
//   - Reviewer-side modifications are detected via git status and surfaced
//     as a warning. They are NEVER applied to the writer worktree.
//   - Verification runs only in the writer worktree. The reviewer never
//     runs verification commands.
//   - Changed files come from `git status` in the writer worktree, never
//     from either agent's self-report.
//   - No commits, merges, pushes, or fetches.
//   - Cleanup is best-effort and never destroys artifacts.
func RunWriterReviewer(ctx context.Context, opts WriterReviewerOptions) (*domain.RunReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateWriterReviewerOptions(&opts); err != nil {
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

	primary := opts.Primary
	reviewer := opts.Reviewer
	report := &domain.RunReport{
		RunID: rid,
		Spec: domain.RunSpec{
			Task:            opts.Task,
			Mode:            domain.ModeWriterReviewer,
			Primary:         agentPtr(primary),
			Reviewer:        agentPtr(reviewer),
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

	// ----- writer worktree --------------------------------------------------
	writerWT, err := gitFacade.CreateWorktree(ctx, gitx.CreateWorktreeOptions{
		RepoRoot:     opts.RepoRoot,
		RunID:        rid,
		Agent:        string(primary),
		Role:         string(domain.RoleWriter),
		BaseBranch:   opts.BaseBranch,
		BranchPrefix: opts.Config.BranchPrefix,
	})
	if err != nil {
		report.Status = domain.StatusFailed
		report.FinishedAt = time.Now().UTC()
		report.Warnings = append(report.Warnings, "create writer worktree: "+err.Error())
		_ = persistReport(layout, report)
		return report, fmt.Errorf("orchestrator: create writer worktree: %w", err)
	}
	defer scheduleCleanup(ctx, gitFacade, opts.RepoRoot, writerWT.Path, opts.KeepWorktrees, layout, report, "writer")

	writerArtifactDir := layout.AgentDir(string(primary), string(domain.RoleWriter))
	writerPrompt, err := agents.BuildWriterPrompt(agents.PromptInput{
		Task:           opts.Task,
		Mode:           string(domain.ModeWriterReviewer),
		WorktreePath:   writerWT.Path,
		ProtectedPaths: opts.Config.Safety.ProtectedPaths,
	})
	if err != nil {
		report.Status = domain.StatusFailed
		report.FinishedAt = time.Now().UTC()
		report.Warnings = append(report.Warnings, "build writer prompt: "+err.Error())
		_ = persistReport(layout, report)
		return report, fmt.Errorf("orchestrator: build writer prompt: %w", err)
	}

	report.Status = domain.StatusRunning
	_ = persistReport(layout, report)

	writerAgent, err := agentFactory(primary, opts.Config)
	if err != nil {
		report.Status = domain.StatusFailed
		report.FinishedAt = time.Now().UTC()
		report.Warnings = append(report.Warnings, "construct writer agent: "+err.Error())
		_ = persistReport(layout, report)
		return report, fmt.Errorf("orchestrator: construct writer agent: %w", err)
	}
	writerRes, writerErr := writerAgent.Run(ctx, agents.AgentRunInput{
		RunID:        rid,
		Task:         opts.Task,
		Role:         domain.RoleWriter,
		Mode:         domain.ModeWriterReviewer,
		WorktreePath: writerWT.Path,
		BranchName:   writerWT.Branch,
		ArtifactDir:  writerArtifactDir,
		Config:       opts.Config,
		Prompt:       writerPrompt,
		DryRun:       opts.DryRun,
		LiveOutput:   opts.LiveOutput,
	})
	if writerErr != nil {
		report.Warnings = append(report.Warnings, "writer invocation: "+writerErr.Error())
	}

	// Truth from git, never from the agent.
	writerChangedFiles, _ := gitFacade.GetChangedFiles(ctx, writerWT.Path)
	writerDiff, _ := gitFacade.GetDiffPatch(ctx, writerWT.Path)
	if err := layout.WriteFileAtomic(layout.DiffPatchPath(), []byte(writerDiff), 0o644); err != nil {
		report.Warnings = append(report.Warnings, "write diff.patch: "+err.Error())
	}

	writerAR := buildAgentResult(writerRes, writerErr, writerWT, writerChangedFiles)
	report.AgentResults = append(report.AgentResults, writerAR)
	report.Status = domain.StatusVerifying
	_ = persistReport(layout, report)

	// ----- verification (writer worktree only) ------------------------------
	verifyResults, verifyErr := runVerification(ctx, writerWT.Path, opts.VerifyCommands, layout, opts.Config, opts.VerifyOptions)
	report.VerificationResults = verifyResults
	if verifyErr != nil {
		report.Warnings = append(report.Warnings, "verification: "+verifyErr.Error())
	}

	// ----- reviewer worktree ------------------------------------------------
	reviewerWT, err := gitFacade.CreateWorktree(ctx, gitx.CreateWorktreeOptions{
		RepoRoot:     opts.RepoRoot,
		RunID:        rid,
		Agent:        string(reviewer),
		Role:         string(domain.RoleReviewer),
		BaseBranch:   opts.BaseBranch,
		BranchPrefix: opts.Config.BranchPrefix,
	})
	if err != nil {
		report.Warnings = append(report.Warnings, "create reviewer worktree: "+err.Error())
		// Treat as a soft failure: we still produce a proof pack with
		// the writer's work and verification results.
		report.Safety = AnalyzeSafety(writerChangedFiles, opts.Config.Safety.ProtectedPaths, resolvedMaxFiles(opts))
		report.Recommendation = recommendWriterReviewer(report, nil)
		report.FinishedAt = time.Now().UTC()
		report.Status = pickStatus(report.Recommendation)
		writeReportArtifacts(opts, layout, report)
		printWriterReviewerSummary(opts.Stdout, report, layout, writerWT, nil)
		return report, nil
	}
	defer scheduleCleanup(ctx, gitFacade, opts.RepoRoot, reviewerWT.Path, opts.KeepWorktrees, layout, report, "reviewer")

	// Try to apply the writer patch to the reviewer worktree so the
	// reviewer can navigate the post-change tree directly. If apply
	// fails (or the patch is empty), fall back to passing the patch
	// inline in the prompt.
	patchAppliedCleanly := false
	patchApplyWarning := ""
	if strings.TrimSpace(writerDiff) != "" {
		patchPath := filepath.Join(reviewerWT.Path, ".awo-review.patch")
		if err := os.WriteFile(patchPath, []byte(writerDiff), 0o644); err != nil {
			patchApplyWarning = "stage reviewer patch: " + err.Error()
		} else {
			applyErr := gitFacade.ApplyPatch(ctx, reviewerWT.Path, patchPath)
			_ = os.Remove(patchPath)
			if applyErr != nil {
				patchApplyWarning = "git apply (reviewer): " + applyErr.Error()
			} else {
				patchAppliedCleanly = true
			}
		}
	}
	if patchApplyWarning != "" {
		report.Warnings = append(report.Warnings, patchApplyWarning)
	}

	preReviewerChanged, _ := gitFacade.GetChangedFiles(ctx, reviewerWT.Path)

	reviewerInput := agents.PromptInput{
		Task:           opts.Task,
		Mode:           string(domain.ModeWriterReviewer),
		WorktreePath:   reviewerWT.Path,
		ChangedFiles:   writerChangedFiles,
		Diff:           writerDiff,
		ProtectedPaths: opts.Config.Safety.ProtectedPaths,
	}
	if patchAppliedCleanly {
		reviewerInput.ExtraContext = map[string]string{
			"patch_status": "writer patch was applied cleanly to this worktree; you may navigate the post-change tree directly. Do NOT modify files.",
		}
	} else if strings.TrimSpace(writerDiff) != "" {
		reviewerInput.ExtraContext = map[string]string{
			"patch_status": "writer patch could NOT be applied to this worktree; review the diff text shown above instead.",
		}
	}
	reviewerPrompt, err := agents.BuildReviewerPrompt(reviewerInput)
	if err != nil {
		report.Warnings = append(report.Warnings, "build reviewer prompt: "+err.Error())
		report.Safety = AnalyzeSafety(writerChangedFiles, opts.Config.Safety.ProtectedPaths, resolvedMaxFiles(opts))
		report.Recommendation = recommendWriterReviewer(report, nil)
		report.FinishedAt = time.Now().UTC()
		report.Status = pickStatus(report.Recommendation)
		writeReportArtifacts(opts, layout, report)
		printWriterReviewerSummary(opts.Stdout, report, layout, writerWT, reviewerWT)
		return report, nil
	}

	reviewerArtifactDir := layout.AgentDir(string(reviewer), string(domain.RoleReviewer))
	reviewerAgent, err := agentFactory(reviewer, opts.Config)
	if err != nil {
		report.Warnings = append(report.Warnings, "construct reviewer agent: "+err.Error())
		report.Safety = AnalyzeSafety(writerChangedFiles, opts.Config.Safety.ProtectedPaths, resolvedMaxFiles(opts))
		report.Recommendation = recommendWriterReviewer(report, nil)
		report.FinishedAt = time.Now().UTC()
		report.Status = pickStatus(report.Recommendation)
		writeReportArtifacts(opts, layout, report)
		printWriterReviewerSummary(opts.Stdout, report, layout, writerWT, reviewerWT)
		return report, nil
	}
	reviewerRes, reviewerErr := reviewerAgent.Run(ctx, agents.AgentRunInput{
		RunID:        rid,
		Task:         opts.Task,
		Role:         domain.RoleReviewer,
		Mode:         domain.ModeWriterReviewer,
		WorktreePath: reviewerWT.Path,
		BranchName:   reviewerWT.Branch,
		ArtifactDir:  reviewerArtifactDir,
		Config:       opts.Config,
		Prompt:       reviewerPrompt,
		ReadOnly:     true,
		DryRun:       opts.DryRun,
		LiveOutput:   opts.LiveOutput,
	})
	if reviewerErr != nil {
		report.Warnings = append(report.Warnings, "reviewer invocation: "+reviewerErr.Error())
	}

	// Detect reviewer-side modifications. We compare against the post-apply
	// baseline so legitimate writer changes don't show up as reviewer edits.
	postReviewerChanged, _ := gitFacade.GetChangedFiles(ctx, reviewerWT.Path)
	reviewerExtraEdits := diffStrings(postReviewerChanged, preReviewerChanged)
	if len(reviewerExtraEdits) > 0 {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("reviewer modified %d file(s) in violation of read-only contract: %v",
				len(reviewerExtraEdits), reviewerExtraEdits))
	}

	reviewerAR := buildAgentResult(reviewerRes, reviewerErr, reviewerWT, reviewerExtraEdits)
	if reviewerAR.Agent == "" {
		// When agent.Run returns nil res (e.g., construction error inside
		// adapter), fill in what we know so the proof pack still names
		// the reviewer agent.
		reviewerAR.Agent = reviewer
		reviewerAR.Role = domain.RoleReviewer
	}
	report.AgentResults = append(report.AgentResults, reviewerAR)

	// ----- recommendation ---------------------------------------------------
	report.Safety = AnalyzeSafety(writerChangedFiles, opts.Config.Safety.ProtectedPaths, resolvedMaxFiles(opts))
	report.Recommendation = recommendWriterReviewer(report, reviewerAR.Review)
	report.FinishedAt = time.Now().UTC()
	report.Status = pickStatus(report.Recommendation)

	writeReportArtifacts(opts, layout, report)
	printWriterReviewerSummary(opts.Stdout, report, layout, writerWT, reviewerWT)
	return report, nil
}

// scheduleCleanup encapsulates the deferred worktree-removal pattern
// shared between writer and reviewer worktrees.
func scheduleCleanup(
	ctx context.Context,
	gitFacade GitFacade,
	repoRoot, worktreePath string,
	keep bool,
	layout *artifacts.Layout,
	report *domain.RunReport,
	label string,
) {
	if keep {
		return
	}
	rerr := gitFacade.RemoveWorktree(ctx, gitx.RemoveWorktreeOptions{
		RepoRoot:     repoRoot,
		WorktreePath: worktreePath,
		Force:        false,
	})
	if rerr != nil {
		report.Warnings = append(report.Warnings, fmt.Sprintf("cleanup %s worktree: %s", label, rerr.Error()))
		_ = persistReport(layout, report)
	}
}

func validateWriterReviewerOptions(opts *WriterReviewerOptions) error {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return errors.New("orchestrator: WriterReviewerOptions.RepoRoot required")
	}
	if strings.TrimSpace(opts.Task) == "" {
		return errors.New("orchestrator: WriterReviewerOptions.Task required")
	}
	if err := opts.Primary.Validate(); err != nil {
		return fmt.Errorf("orchestrator: primary: %w", err)
	}
	if err := opts.Reviewer.Validate(); err != nil {
		return fmt.Errorf("orchestrator: reviewer: %w", err)
	}
	if err := opts.Config.Validate(); err != nil {
		return fmt.Errorf("orchestrator: invalid config: %w", err)
	}
	return nil
}

func resolvedMaxFiles(opts WriterReviewerOptions) int {
	if opts.MaxChangedFiles > 0 {
		return opts.MaxChangedFiles
	}
	return opts.Config.Safety.MaxChangedFiles
}

// recommendWriterReviewer applies the writer-reviewer verdict ladder:
// failed verification beats reviewer recommendation beats blocking
// findings; remaining safety escalations (protected paths, size) are
// delegated to escalateForSafety so the rules stay consistent across
// modes.
func recommendWriterReviewer(r *domain.RunReport, review *domain.ReviewFindings) domain.Recommendation {
	if len(r.VerificationResults) > 0 && !AllPassed(r.VerificationResults) {
		return domain.RecFailedVerification
	}
	if review != nil {
		switch strings.ToLower(strings.TrimSpace(review.Recommendation)) {
		case "reject", "needs_revision":
			return domain.RecNeedsRevision
		}
		if len(review.Blocking) > 0 {
			return domain.RecNeedsRevision
		}
	}
	if rec, ok := emptyRunVerdict(r); ok {
		return rec
	}
	return escalateForSafety(domain.RecReadyForHumanReview, r.Safety)
}

func pickStatus(rec domain.Recommendation) domain.RunStatus {
	if rec == domain.RecFailedVerification {
		return domain.StatusFailed
	}
	return domain.StatusCompleted
}

func writeReportArtifacts(opts WriterReviewerOptions, layout *artifacts.Layout, report *domain.RunReport) {
	if err := reports.WriteRunReportFiles(reports.Inputs{
		Report:         *report,
		ProtectedPaths: opts.Config.Safety.ProtectedPaths,
		DiffPatchPath:  layout.DiffPatchPath(),
	}, layout); err != nil {
		report.Warnings = append(report.Warnings, err.Error())
	}
	report.ProofPackPath = layout.ProofPackPath()
	_ = persistReport(layout, report)
}

func printWriterReviewerSummary(out io.Writer, r *domain.RunReport, layout *artifacts.Layout, writerWT, reviewerWT *gitx.WorktreeInfo) {
	fmt.Fprintf(out, "AWO run %s\n", r.RunID)
	fmt.Fprintf(out, "  status:         %s\n", r.Status)
	if r.Recommendation != "" {
		fmt.Fprintf(out, "  recommendation: %s\n", r.Recommendation)
	}
	if writerWT != nil {
		fmt.Fprintf(out, "  writer:         %s @ %s\n", writerWT.Branch, writerWT.Path)
	}
	if reviewerWT != nil {
		fmt.Fprintf(out, "  reviewer:       %s @ %s\n", reviewerWT.Branch, reviewerWT.Path)
	}
	for _, ar := range r.AgentResults {
		fmt.Fprintf(out, "  agent:          %s/%s — %s (exit %d)\n",
			ar.Agent, ar.Role, ar.Status, ar.ExitCode)
		if ar.Role == domain.RoleWriter {
			fmt.Fprintf(out, "  changed files:  %d\n", len(ar.ChangedFiles))
		}
		if ar.Review != nil {
			fmt.Fprintf(out, "  review:         %d blocking, %d non-blocking (rec=%s)\n",
				len(ar.Review.Blocking), len(ar.Review.NonBlocking), ar.Review.Recommendation)
		}
	}
	if len(r.VerificationResults) == 0 {
		fmt.Fprintln(out, "  verification:   not verified")
	} else {
		passed := 0
		for _, v := range r.VerificationResults {
			if v.Passed {
				passed++
			}
		}
		fmt.Fprintf(out, "  verification:   %d/%d passed\n", passed, len(r.VerificationResults))
	}
	printSafetyHighlights(out, r.Safety)
	for _, w := range r.Warnings {
		fmt.Fprintf(out, "  warning:        %s\n", w)
	}
	fmt.Fprintf(out, "  artifacts:      %s\n", layout.Root)
	fmt.Fprintf(out, "  proof pack:     %s\n", filepath.Join(layout.Root, "proof-pack.md"))
}

// diffStrings returns elements of a that are not in b, preserving the
// order of a. Used to compute reviewer-side edits relative to the
// post-apply baseline.
func diffStrings(a, b []string) []string {
	if len(a) == 0 {
		return nil
	}
	have := make(map[string]struct{}, len(b))
	for _, s := range b {
		have[s] = struct{}{}
	}
	var out []string
	for _, s := range a {
		if _, ok := have[s]; ok {
			continue
		}
		out = append(out, s)
	}
	return out
}
