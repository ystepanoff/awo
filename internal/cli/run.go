package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/awo-dev/awo/internal/orchestrator"
	"github.com/spf13/cobra"
)

func newRunCmd() *cobra.Command {
	var (
		mode            string
		agent           string
		primary         string
		reviewer        string
		competitors     []string
		verify          []string
		baseBranch      string
		keepWorktrees   bool
		dryRun          bool
		liveOutput      bool
		maxChangedFiles int
	)

	cmd := &cobra.Command{
		Use:   "run [task]",
		Short: "Run a task with one or more agents inside isolated worktrees",
		Long: `Run a task with one or more agents inside isolated worktrees.

The "single" mode runs one agent end-to-end. The "writer-reviewer" mode
runs a primary agent in a writer worktree, runs verification there,
then asks a reviewer agent to review the writer's diff in a separate
worktree carved from the same base. The reviewer is read-only — any
files it modifies are detected and surfaced as a warning, never applied
to the writer worktree.

AWO never commits, merges, or pushes on your behalf. Worktrees are
removed only when --keep-worktrees is unset and the path is strictly
under .awo/worktrees.

Examples:
  awo run "add tests for calculator" --mode single --agent claude --verify "go test ./..."
  awo run "fix the null pointer bug" --mode single --agent codex --verify "go test ./..."
  awo run "add tests for calculator" --mode single --agent claude --dry-run
  awo run "fix checkout validation" --mode writer-reviewer --primary claude --reviewer codex --verify "go test ./..."
  awo run "fix checkout validation" --mode writer-reviewer --primary codex --reviewer claude --verify "go test ./..."
  awo run "migrate date utility usage" --mode competitive --competitors claude,codex --verify "go test ./..."`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			task := args[0]
			for _, extra := range args[1:] {
				task += " " + extra
			}

			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			repoRoot, err := gitx.GetRepoRoot(ctx, cwd)
			if err != nil {
				return fmt.Errorf("run: %w", err)
			}
			cfg, _, err := config.LoadOrDefault(filepath.Join(repoRoot, config.Filename))
			if err != nil {
				return fmt.Errorf("run: load config: %w", err)
			}

			runMode, err := orchestrator.ResolveMode(mode, domain.ModeSingle)
			if err != nil {
				return err
			}

			cmds := orchestrator.ResolveVerifyCommands(verify, cfg)

			switch runMode {
			case domain.ModeSingle:
				agentKind := domain.AgentKind(agent)
				if agent == "" {
					return errors.New("run: --agent is required for single mode")
				}
				if err := agentKind.Validate(); err != nil {
					return err
				}
				report, err := orchestrator.RunSingle(ctx, orchestrator.SingleRunOptions{
					RepoRoot:        repoRoot,
					Task:            task,
					Agent:           agentKind,
					VerifyCommands:  cmds,
					BaseBranch:      baseBranch,
					KeepWorktrees:   keepWorktrees,
					DryRun:          dryRun,
					LiveOutput:      liveOutput,
					MaxChangedFiles: maxChangedFiles,
					Config:          cfg,
					Stdout:          cmd.OutOrStdout(),
				})
				if err != nil {
					return err
				}
				if report.Recommendation == domain.RecFailedVerification {
					return errors.New("verification failed")
				}
				return nil

			case domain.ModeWriterReviewer:
				if primary == "" {
					return errors.New("run: --primary is required for writer-reviewer mode")
				}
				if reviewer == "" {
					return errors.New("run: --reviewer is required for writer-reviewer mode")
				}
				primaryKind := domain.AgentKind(primary)
				reviewerKind := domain.AgentKind(reviewer)
				if err := primaryKind.Validate(); err != nil {
					return fmt.Errorf("run: --primary: %w", err)
				}
				if err := reviewerKind.Validate(); err != nil {
					return fmt.Errorf("run: --reviewer: %w", err)
				}
				report, err := orchestrator.RunWriterReviewer(ctx, orchestrator.WriterReviewerOptions{
					RepoRoot:        repoRoot,
					Task:            task,
					Primary:         primaryKind,
					Reviewer:        reviewerKind,
					VerifyCommands:  cmds,
					BaseBranch:      baseBranch,
					KeepWorktrees:   keepWorktrees,
					DryRun:          dryRun,
					LiveOutput:      liveOutput,
					MaxChangedFiles: maxChangedFiles,
					Config:          cfg,
					Stdout:          cmd.OutOrStdout(),
				})
				if err != nil {
					return err
				}
				if report.Recommendation == domain.RecFailedVerification {
					return errors.New("verification failed")
				}
				return nil

			case domain.ModeCompetitive:
				kinds, err := orchestrator.ParseCompetitorList(competitors)
				if err != nil {
					return fmt.Errorf("run: --competitors: %w", err)
				}
				report, err := orchestrator.RunCompetitive(ctx, orchestrator.CompetitiveRunOptions{
					RepoRoot:        repoRoot,
					Task:            task,
					Competitors:     kinds,
					VerifyCommands:  cmds,
					BaseBranch:      baseBranch,
					KeepWorktrees:   keepWorktrees,
					DryRun:          dryRun,
					LiveOutput:      liveOutput,
					MaxChangedFiles: maxChangedFiles,
					Config:          cfg,
					Stdout:          cmd.OutOrStdout(),
				})
				if err != nil {
					return err
				}
				if report.Recommendation == domain.RecFailedVerification {
					return errors.New("verification failed")
				}
				return nil

			default:
				return fmt.Errorf("run: mode %q is not implemented yet", runMode)
			}
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "single", "orchestration mode: single | writer-reviewer | competitive")
	cmd.Flags().StringVar(&agent, "agent", "", "agent kind for single mode: claude or codex")
	cmd.Flags().StringVar(&primary, "primary", "", "primary (writer) agent for writer-reviewer mode: claude or codex")
	cmd.Flags().StringVar(&reviewer, "reviewer", "", "reviewer agent for writer-reviewer mode: claude or codex")
	cmd.Flags().StringSliceVar(&competitors, "competitors", nil, "competing agents for competitive mode (e.g. claude,codex)")
	cmd.Flags().StringArrayVar(&verify, "verify", nil, "verification command (repeatable; falls back to config defaults when omitted)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "base branch for the worktree (defaults to HEAD)")
	cmd.Flags().BoolVar(&keepWorktrees, "keep-worktrees", false, "do not remove worktrees after the run")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not invoke agents; record prompts and commands only")
	cmd.Flags().BoolVar(&liveOutput, "live-output", false, "mirror agent stdout/stderr to the terminal")
	cmd.Flags().IntVar(&maxChangedFiles, "max-changed-files", 0, "override safety.maxChangedFiles for this run (0 = use config)")
	return cmd
}
