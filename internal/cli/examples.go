package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/awo-dev/awo/internal/examples"
	"github.com/awo-dev/awo/internal/gitx"
	"github.com/spf13/cobra"
)

func newExamplesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "examples",
		Short: "Generate dogfooding fixtures for AWO",
		Long: `Generate small, self-contained fixtures you can use to dogfood AWO.

Fixtures are created under .awo/fixtures/ inside the host repo, but
each fixture is its own git repository so any commits, branches, or
worktrees AWO produces while operating against a fixture stay inside
that fixture and never touch the host repo's git history.`,
	}
	cmd.AddCommand(newExamplesCreateFixtureCmd())
	return cmd
}

func newExamplesCreateFixtureCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "create-fixture",
		Short: "Create the sample-go-app fixture under .awo/fixtures/",
		Long: `Create a tiny Go-module fixture under .awo/fixtures/sample-go-app/.

The fixture is a self-contained git repository with go.mod,
calculator.go, and calculator_test.go. The existing tests already
pass; the calculator's Divide() panics on a zero divisor, which is
the intentional edge case the demo task asks agents to expose.

Safety:
  - The fixture is generated deterministically (file content is fixed).
  - Existing fixtures are not overwritten without --force.
  - When --force is given, AWO will only overwrite directories that
    contain the .awo-fixture marker file. Unrelated user data living
    at the same path is left alone.

Try it:

  cd .awo/fixtures/sample-go-app
  awo init
  awo run "add tests for the calculator edge cases" \
    --mode competitive \
    --competitors claude,codex \
    --verify "go test ./..."`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			cwd, err := os.Getwd()
			if err != nil {
				return err
			}
			repoRoot, err := gitx.GetRepoRoot(ctx, cwd)
			if err != nil {
				return fmt.Errorf("examples create-fixture: %w", err)
			}
			fixturesDir := filepath.Join(repoRoot, ".awo", "fixtures")
			return runCreateFixture(cmd.OutOrStdout(), fixturesDir, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing AWO fixture in place")
	return cmd
}

func runCreateFixture(out io.Writer, fixturesDir string, force bool) error {
	res, err := examples.CreateSampleGoApp(fixturesDir, force, out)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "Fixture ready.")
	if res.Replaced {
		fmt.Fprintln(out, "  status: replaced existing fixture (--force)")
	} else {
		fmt.Fprintln(out, "  status: created")
	}
	fmt.Fprintln(out, "  path: ", res.Path)
	fmt.Fprintln(out, "  files:")
	for _, f := range res.Files {
		fmt.Fprintln(out, "    -", f)
	}
	if res.GitCommit != "" {
		fmt.Fprintln(out, "  git: ", res.GitCommit, "on", res.BranchName)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "This fixture is a SAFE demo environment:")
	fmt.Fprintln(out, "  - It is its own git repo, separate from your host repo.")
	fmt.Fprintln(out, "  - AWO worktrees, branches, and commits made against the")
	fmt.Fprintln(out, "    fixture stay inside the fixture and do not touch your")
	fmt.Fprintln(out, "    real codebase.")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Next steps:")
	fmt.Fprintln(out, "  cd", res.Path)
	fmt.Fprintln(out, "  awo init")
	fmt.Fprintln(out, "  go test ./...   # confirm the baseline passes")
	fmt.Fprintln(out, `  awo run "add tests for the calculator edge cases" \`)
	fmt.Fprintln(out, "    --mode competitive \\")
	fmt.Fprintln(out, "    --competitors claude,codex \\")
	fmt.Fprintln(out, `    --verify "go test ./..."`)
	return nil
}
