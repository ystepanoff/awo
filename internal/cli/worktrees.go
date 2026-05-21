package cli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/spf13/cobra"
)

func newWorktreesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktrees",
		Short: "Inspect and clean AWO-managed git worktrees",
	}
	cmd.AddCommand(newWorktreesListCmd())
	cmd.AddCommand(newWorktreesCleanupCmd())
	return cmd
}

func newWorktreesListCmd() *cobra.Command {
	var runID string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List AWO-managed worktrees in the current repo",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := gitx.GetRepoRoot(ctx, cwd)
			if err != nil {
				return err
			}
			cfg, _, err := config.LoadOrDefault(filepath.Join(root, config.Filename))
			if err != nil {
				return err
			}
			wts, err := gitx.ListAwoWorktrees(ctx, gitx.ListWorktreesOptions{
				RepoRoot:     root,
				RunID:        runID,
				BranchPrefix: cfg.BranchPrefix,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(wts) == 0 {
				fmt.Fprintln(out, "no AWO worktrees found")
				return nil
			}
			for _, w := range wts {
				fmt.Fprintf(out, "%s\trun=%s\tagent=%s\trole=%s\tbranch=%s\n",
					w.Path, w.RunID, w.Agent, w.Role, w.Branch)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "filter by run id")
	return cmd
}

func newWorktreesCleanupCmd() *cobra.Command {
	var runID string
	var force bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove AWO-managed worktrees scoped to a run id",
		Long: `Remove AWO-managed worktrees scoped to a run id.

Only removes worktrees whose path lies under <repo-root>/.awo/worktrees.
Does NOT delete branches.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if runID == "" {
				return errors.New("--run-id is required")
			}
			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			root, err := gitx.GetRepoRoot(ctx, cwd)
			if err != nil {
				return err
			}
			cfg, _, err := config.LoadOrDefault(filepath.Join(root, config.Filename))
			if err != nil {
				return err
			}
			wts, err := gitx.ListAwoWorktrees(ctx, gitx.ListWorktreesOptions{
				RepoRoot:     root,
				RunID:        runID,
				BranchPrefix: cfg.BranchPrefix,
			})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(wts) == 0 {
				fmt.Fprintf(out, "no AWO worktrees found for run-id %q\n", runID)
				return nil
			}
			var failed int
			for _, w := range wts {
				if err := gitx.RemoveWorktree(ctx, gitx.RemoveWorktreeOptions{
					RepoRoot:     root,
					WorktreePath: w.Path,
					Force:        force,
				}); err != nil {
					fmt.Fprintf(out, "FAIL %s: %v\n", w.Path, err)
					failed++
					continue
				}
				fmt.Fprintf(out, "removed %s\n", w.Path)
			}
			if failed > 0 {
				return fmt.Errorf("%d worktree(s) failed to remove", failed)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "run id to clean up (required)")
	cmd.Flags().BoolVar(&force, "force", false, "pass --force to git worktree remove")
	_ = cmd.MarkFlagRequired("run-id")
	return cmd
}
