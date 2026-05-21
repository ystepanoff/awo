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

The "single" mode runs one agent end-to-end. The agent works inside a
fresh git worktree under .awo/worktrees, AWO collects the diff from
git, runs the configured verification commands, and writes a proof
pack under .awo/runs/<run-id>/.

AWO never commits, merges, or pushes on your behalf. Worktrees are
removed only when --keep-worktrees is unset and the path is strictly
under .awo/worktrees.

Examples:
  awo run "add tests for calculator" --mode single --agent claude --verify "go test ./..."
  awo run "fix the null pointer bug" --mode single --agent codex --verify "go test ./..."
  awo run "add tests for calculator" --mode single --agent claude --dry-run`,
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
			if runMode != domain.ModeSingle {
				return fmt.Errorf("run: only --mode single is implemented (got %q)", runMode)
			}

			agentKind := domain.AgentKind(agent)
			if agent == "" {
				return errors.New("run: --agent is required for single mode")
			}
			if err := agentKind.Validate(); err != nil {
				return err
			}

			cmds := orchestrator.ResolveVerifyCommands(verify, cfg)

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
		},
	}

	cmd.Flags().StringVar(&mode, "mode", "single", "orchestration mode (single)")
	cmd.Flags().StringVar(&agent, "agent", "", "agent kind: claude or codex")
	cmd.Flags().StringArrayVar(&verify, "verify", nil, "verification command (repeatable; falls back to config defaults when omitted)")
	cmd.Flags().StringVar(&baseBranch, "base-branch", "", "base branch for the worktree (defaults to HEAD)")
	cmd.Flags().BoolVar(&keepWorktrees, "keep-worktrees", false, "do not remove the worktree after the run")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "do not invoke the agent; record the prompt and command only")
	cmd.Flags().BoolVar(&liveOutput, "live-output", false, "mirror agent stdout/stderr to the terminal")
	cmd.Flags().IntVar(&maxChangedFiles, "max-changed-files", 0, "override safety.maxChangedFiles for this run (0 = use config)")
	return cmd
}
