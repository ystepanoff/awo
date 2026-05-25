package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/awo-dev/awo/internal/prhelper"
	"github.com/spf13/cobra"
)

func newPRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pr",
		Short: "Local PR helper commands",
		Long: `PR helpers prepare a human-facing PR description from an AWO run.

AWO never commits, pushes, merges, or opens pull requests on your behalf.
The "prepare" subcommand only renders pr-description.md from a finished
run's artifacts; the human is responsible for any git or PR action.`,
	}
	cmd.AddCommand(newPRPrepareCmd())
	return cmd
}

func newPRPrepareCmd() *cobra.Command {
	var (
		runID     string
		candidate string
	)
	cmd := &cobra.Command{
		Use:   "prepare",
		Short: "Render pr-description.md for a finished AWO run",
		Long: `Render pr-description.md for a finished AWO run.

This command does not commit, push, merge, or open PRs. It reads
.awo/runs/<run-id>/run.json, picks the relevant candidate, and writes
pr-description.md alongside the proof pack.

Examples:
  awo pr prepare --run-id 20260525-094200-abc123
  awo pr prepare --run-id 20260525-094200-abc123 --candidate claude
  awo pr prepare --run-id 20260525-094200-abc123 --candidate awo/run/claude-competitor`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(runID) == "" {
				return errors.New("pr prepare: --run-id is required")
			}
			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			repoRoot, err := gitx.GetRepoRoot(ctx, cwd)
			if err != nil {
				return fmt.Errorf("pr prepare: %w", err)
			}
			cfg, _, err := config.LoadOrDefault(filepath.Join(repoRoot, config.Filename))
			if err != nil {
				return fmt.Errorf("pr prepare: load config: %w", err)
			}
			return runPRPrepare(ctx, cmd.OutOrStdout(), prepareOpts{
				RepoRoot:    repoRoot,
				ArtifactDir: cfg.ArtifactDir,
				RunID:       runID,
				Candidate:   candidate,
			})
		},
	}
	cmd.Flags().StringVar(&runID, "run-id", "", "AWO run id (directory under .awo/runs)")
	cmd.Flags().StringVar(&candidate, "candidate", "", "candidate selector (agent name or branch); required for competitive mode")
	_ = cmd.MarkFlagRequired("run-id")
	return cmd
}

type prepareOpts struct {
	RepoRoot    string
	ArtifactDir string
	RunID       string
	Candidate   string
}

// runPRPrepare reads the run report, renders the PR description, writes
// it to disk, and prints handoff instructions. It never executes git or
// opens a PR — by design.
func runPRPrepare(_ context.Context, out io.Writer, opts prepareOpts) error {
	runDir, err := resolveRunDir(opts.RepoRoot, opts.ArtifactDir, opts.RunID)
	if err != nil {
		return err
	}
	runJSONPath := filepath.Join(runDir, "run.json")

	report, err := loadRunReport(runJSONPath)
	if err != nil {
		return err
	}

	proofPackPath := filepath.Join(runDir, "proof-pack.md")

	body, err := prhelper.Render(prhelper.Inputs{
		Report:            report,
		CandidateSelector: opts.Candidate,
		ProofPackPath:     proofPackPath,
	})
	if err != nil {
		return err
	}

	prPath := filepath.Join(runDir, "pr-description.md")
	if err := os.WriteFile(prPath, []byte(body), 0o644); err != nil {
		return fmt.Errorf("pr prepare: write %s: %w", prPath, err)
	}

	candidate, _ := prhelper.SelectCandidate(report, opts.Candidate)
	printPRHandoff(out, prPath, proofPackPath, candidate)
	return nil
}

// resolveRunDir resolves the run directory regardless of whether
// ArtifactDir is configured as an absolute path or as a repo-relative
// path like ".awo/runs".
func resolveRunDir(repoRoot, artifactDir, runID string) (string, error) {
	if strings.TrimSpace(runID) == "" {
		return "", errors.New("pr prepare: empty run id")
	}
	if strings.ContainsAny(runID, `/\`) {
		return "", fmt.Errorf("pr prepare: run id %q must not contain path separators", runID)
	}
	dir := artifactDir
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot, dir)
	}
	runDir := filepath.Join(dir, runID)
	info, err := os.Stat(runDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("pr prepare: run %q not found at %s", runID, runDir)
		}
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("pr prepare: %s is not a directory", runDir)
	}
	return runDir, nil
}

func loadRunReport(path string) (domain.RunReport, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.RunReport{}, fmt.Errorf("pr prepare: run.json not found at %s", path)
		}
		return domain.RunReport{}, fmt.Errorf("pr prepare: read %s: %w", path, err)
	}
	var r domain.RunReport
	if err := json.Unmarshal(b, &r); err != nil {
		return domain.RunReport{}, fmt.Errorf("pr prepare: parse %s: %w", path, err)
	}
	return r, nil
}

func printPRHandoff(out io.Writer, prPath, proofPackPath string, candidate domain.AgentRunResult) {
	fmt.Fprintln(out, "PR description written.")
	fmt.Fprintln(out, "  pr-description.md:", prPath)
	fmt.Fprintln(out, "  proof-pack.md:    ", proofPackPath)
	if candidate.WorktreePath != "" {
		fmt.Fprintln(out, "  worktree:         ", candidate.WorktreePath)
	}
	if candidate.BranchName != "" {
		fmt.Fprintln(out, "  branch:           ", candidate.BranchName)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps (you, not AWO):")
	fmt.Fprintln(out, "  1. Inspect the worktree diff:")
	if candidate.WorktreePath != "" {
		fmt.Fprintf(out, "       cd %s && git diff\n", candidate.WorktreePath)
	} else {
		fmt.Fprintln(out, "       (worktree path unavailable; check the run.json for details)")
	}
	fmt.Fprintln(out, "  2. Optionally re-run verification commands manually.")
	fmt.Fprintln(out, "  3. Commit the change yourself (AWO did not commit).")
	fmt.Fprintln(out, "  4. Push the branch yourself (AWO did not push).")
	fmt.Fprintln(out, "  5. Open the PR yourself, e.g.:")
	fmt.Fprintf(out, "       gh pr create --body-file %s\n", prPath)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "AWO did not commit, push, merge, or auto-approve this change.")
}
