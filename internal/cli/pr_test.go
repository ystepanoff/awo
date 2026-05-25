package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/domain"
)

// initRepo turns a temp dir into a git repo so gitx.GetRepoRoot works
// for tests that exercise the cobra entrypoint. The pr prepare logic
// itself does not require a git repo, but the cobra command does.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
}

func writeRunReport(t *testing.T, dir string, r domain.RunReport) string {
	t.Helper()
	runDir := filepath.Join(dir, ".awo", "runs", r.RunID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "proof-pack.md"),
		[]byte("# proof pack\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "run.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return runDir
}

func sampleSingleReport() domain.RunReport {
	now := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	return domain.RunReport{
		RunID: "20260525-120000-aaa111",
		Spec: domain.RunSpec{
			Task: "add /health endpoint",
			Mode: domain.ModeSingle,
		},
		Status:     domain.StatusCompleted,
		StartedAt:  now,
		FinishedAt: now.Add(time.Second),
		AgentResults: []domain.AgentRunResult{
			{
				Agent:        domain.AgentClaude,
				Role:         domain.RoleWriter,
				BranchName:   "awo/run/claude-writer",
				WorktreePath: ".awo/worktrees/run/claude-writer",
				Status:       "completed",
				ExitCode:     0,
				ChangedFiles: []string{"server/health.go"},
				ParsedResult: &domain.ParsedAgentResult{
					Summary: "Added /health endpoint.",
				},
			},
		},
		VerificationResults: []domain.VerificationResult{
			{Command: "go test ./...", ExitCode: 0, Passed: true, DurationMillis: 100},
		},
		Recommendation: domain.RecReadyForHumanReview,
	}
}

func sampleCompetitiveReport() domain.RunReport {
	r := sampleSingleReport()
	r.RunID = "20260525-120000-ccc333"
	r.Spec.Mode = domain.ModeCompetitive
	r.AgentResults = []domain.AgentRunResult{
		{
			Agent:        domain.AgentClaude,
			Role:         domain.RoleCompetitor,
			BranchName:   "awo/run/claude-competitor",
			WorktreePath: ".awo/worktrees/run/claude-competitor",
			Status:       "completed",
			ExitCode:     0,
			ChangedFiles: []string{"server/health.go"},
		},
		{
			Agent:        domain.AgentCodex,
			Role:         domain.RoleCompetitor,
			BranchName:   "awo/run/codex-competitor",
			WorktreePath: ".awo/worktrees/run/codex-competitor",
			Status:       "completed",
			ExitCode:     0,
			ChangedFiles: []string{"server/health.go"},
		},
	}
	return r
}

// ----- happy paths --------------------------------------------------------

func TestPRPrepareSingleWritesFileAndPrintsHandoff(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := sampleSingleReport()
	runDir := writeRunReport(t, dir, r)

	var buf bytes.Buffer
	err := runPRPrepare(context.Background(), &buf, prepareOpts{
		RepoRoot:    dir,
		ArtifactDir: ".awo/runs",
		RunID:       r.RunID,
	})
	if err != nil {
		t.Fatalf("runPRPrepare: %v", err)
	}

	prPath := filepath.Join(runDir, "pr-description.md")
	if _, err := os.Stat(prPath); err != nil {
		t.Fatalf("pr-description.md missing: %v", err)
	}
	body, _ := os.ReadFile(prPath)
	for _, want := range []string{
		"add /health endpoint",
		"`single`",
		"awo/run/claude-writer",
		"AWO did not commit",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("pr-description.md missing %q", want)
		}
	}
	out := buf.String()
	for _, want := range []string{
		"PR description written.",
		prPath,
		"Inspect the worktree diff",
		"Commit the change yourself",
		"Push the branch yourself",
		"Open the PR yourself",
		"gh pr create --body-file",
		"AWO did not commit, push, merge, or auto-approve",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff output missing %q:\n%s", want, out)
		}
	}
}

func TestPRPrepareCompetitiveRequiresCandidate(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := sampleCompetitiveReport()
	writeRunReport(t, dir, r)

	err := runPRPrepare(context.Background(), &bytes.Buffer{}, prepareOpts{
		RepoRoot:    dir,
		ArtifactDir: ".awo/runs",
		RunID:       r.RunID,
	})
	if err == nil || !strings.Contains(err.Error(), "candidate") {
		t.Fatalf("expected candidate-required error, got %v", err)
	}
}

func TestPRPrepareCompetitiveSelectsByAgent(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	r := sampleCompetitiveReport()
	runDir := writeRunReport(t, dir, r)

	var buf bytes.Buffer
	err := runPRPrepare(context.Background(), &buf, prepareOpts{
		RepoRoot:    dir,
		ArtifactDir: ".awo/runs",
		RunID:       r.RunID,
		Candidate:   "codex",
	})
	if err != nil {
		t.Fatalf("runPRPrepare: %v", err)
	}
	body, _ := os.ReadFile(filepath.Join(runDir, "pr-description.md"))
	if !strings.Contains(string(body), "awo/run/codex-competitor") {
		t.Errorf("expected codex-competitor branch in body:\n%s", body)
	}
	if !strings.Contains(string(body), "## Competitive comparison") {
		t.Errorf("expected competitive comparison section:\n%s", body)
	}
}

// ----- error paths --------------------------------------------------------

func TestPRPrepareUnknownRunID(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	err := runPRPrepare(context.Background(), &bytes.Buffer{}, prepareOpts{
		RepoRoot:    dir,
		ArtifactDir: ".awo/runs",
		RunID:       "20260525-000000-zzzzzz",
	})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestPRPrepareRejectsRunIDWithSeparators(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	err := runPRPrepare(context.Background(), &bytes.Buffer{}, prepareOpts{
		RepoRoot:    dir,
		ArtifactDir: ".awo/runs",
		RunID:       "../escape",
	})
	if err == nil || !strings.Contains(err.Error(), "path separators") {
		t.Fatalf("expected path-separator rejection, got %v", err)
	}
}

func TestPRPrepareDoesNotInvokeGitOrGh(t *testing.T) {
	// Smoke: confirm the command's output never recommends destructive
	// actions and never claims to have committed/pushed/merged.
	dir := t.TempDir()
	initRepo(t, dir)
	r := sampleSingleReport()
	writeRunReport(t, dir, r)

	var buf bytes.Buffer
	if err := runPRPrepare(context.Background(), &buf, prepareOpts{
		RepoRoot:    dir,
		ArtifactDir: ".awo/runs",
		RunID:       r.RunID,
	}); err != nil {
		t.Fatalf("runPRPrepare: %v", err)
	}
	out := buf.String()
	for _, mustNot := range []string{
		"git push",
		"git commit",
		"merging now",
		"auto-merge",
	} {
		if strings.Contains(out, mustNot) {
			t.Errorf("output should not include %q:\n%s", mustNot, out)
		}
	}
	// Sanity: gh is mentioned only as a suggestion the human can run.
	if !strings.Contains(out, "Open the PR yourself") {
		t.Errorf("expected human-action framing in output:\n%s", out)
	}
}

// ----- root command wiring -----------------------------------------------

func TestRootHelpListsPRCommand(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--help"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(buf.String(), "pr") {
		t.Errorf("root help missing pr subcommand:\n%s", buf.String())
	}
}

func TestPRPrepareHelpListsFlags(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"pr", "prepare", "--help"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	for _, want := range []string{"--run-id", "--candidate"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("pr prepare help missing flag %q:\n%s", want, buf.String())
		}
	}
}
