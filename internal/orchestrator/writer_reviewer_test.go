package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/agents"
	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
	"github.com/awo-dev/awo/internal/gitx"
)

// ----- per-path fake git --------------------------------------------------

// pathFakeGit returns different changed-file lists for different
// worktree paths, lets the test assert ApplyPatch was invoked, and
// optionally fails patch application.
type pathFakeGit struct {
	WriterChanged []string
	WriterDiff    string
	WriterPath    string
	WriterBranch  string

	ReviewerPath   string
	ReviewerBranch string
	// Pre/post lists for the reviewer worktree. PreReviewer is the state
	// captured immediately after writer-patch apply (or before, on apply
	// failure). PostReviewer is the state after the reviewer ran.
	PreReviewer  []string
	PostReviewer []string

	ApplyErr error
	// CreateErrAfter triggers an error on the Nth (1-indexed) call to
	// CreateWorktree. 0 disables.
	CreateErrAfter int

	createCalls int
	applyCalls  int
	removeCalls int
	preReturned bool
}

func (g *pathFakeGit) CreateWorktree(_ context.Context, opts gitx.CreateWorktreeOptions) (*gitx.WorktreeInfo, error) {
	g.createCalls++
	if g.CreateErrAfter > 0 && g.createCalls == g.CreateErrAfter {
		return nil, errors.New("synthetic create-worktree error")
	}
	var path, branch string
	switch opts.Role {
	case string(domain.RoleWriter):
		path = g.WriterPath
		branch = g.WriterBranch
	case string(domain.RoleReviewer):
		path = g.ReviewerPath
		branch = g.ReviewerBranch
	}
	if path == "" {
		path = filepath.Join(opts.RepoRoot, ".awo", "worktrees", opts.RunID, opts.Agent+"-"+opts.Role)
	}
	if branch == "" {
		branch = "awo/" + opts.RunID + "/" + opts.Agent + "-" + opts.Role
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	return &gitx.WorktreeInfo{Path: path, Branch: branch, RunID: opts.RunID, Agent: opts.Agent, Role: opts.Role}, nil
}

func (g *pathFakeGit) GetChangedFiles(_ context.Context, p string) ([]string, error) {
	switch p {
	case g.WriterPath:
		return append([]string(nil), g.WriterChanged...), nil
	case g.ReviewerPath:
		// First call inside the orchestrator after apply is the "pre"
		// snapshot; the second is "post". Return them in that order.
		if !g.preReturned {
			g.preReturned = true
			return append([]string(nil), g.PreReviewer...), nil
		}
		return append([]string(nil), g.PostReviewer...), nil
	}
	return nil, nil
}

func (g *pathFakeGit) GetDiffPatch(_ context.Context, p string) (string, error) {
	if p == g.WriterPath {
		return g.WriterDiff, nil
	}
	return "", nil
}

func (g *pathFakeGit) GetDiffStat(_ context.Context, _ string) (string, error) { return "", nil }

func (g *pathFakeGit) ApplyPatch(_ context.Context, _, _ string) error {
	g.applyCalls++
	return g.ApplyErr
}

func (g *pathFakeGit) RemoveWorktree(_ context.Context, _ gitx.RemoveWorktreeOptions) error {
	g.removeCalls++
	return nil
}

// ----- role-aware fake agent ---------------------------------------------

type roleFakeAgent struct {
	kind         domain.AgentKind
	parsedWriter *domain.ParsedAgentResult
	parsedReview *agents.ParsedReviewResult
	exitCode     int
	onReviewer   func(in agents.AgentRunInput)
	gotInputs    []agents.AgentRunInput
}

func (f *roleFakeAgent) Kind() domain.AgentKind { return f.kind }

func (f *roleFakeAgent) Run(_ context.Context, in agents.AgentRunInput) (*agents.AgentRunResult, error) {
	f.gotInputs = append(f.gotInputs, in)
	if err := os.MkdirAll(in.ArtifactDir, 0o755); err != nil {
		return nil, err
	}
	stdout := filepath.Join(in.ArtifactDir, "stdout.log")
	stderr := filepath.Join(in.ArtifactDir, "stderr.log")
	prompt := filepath.Join(in.ArtifactDir, "prompt.md")
	_ = os.WriteFile(stdout, []byte("output\n"), 0o644)
	_ = os.WriteFile(stderr, nil, 0o644)
	_ = os.WriteFile(prompt, []byte(in.Prompt), 0o644)

	res := &agents.AgentRunResult{
		Agent:      f.kind,
		Role:       in.Role,
		StartedAt:  time.Now().UTC(),
		FinishedAt: time.Now().UTC().Add(time.Millisecond),
		ExitCode:   f.exitCode,
		StdoutPath: stdout,
		StderrPath: stderr,
		PromptPath: prompt,
		DryRun:     in.DryRun,
	}
	switch in.Role {
	case domain.RoleWriter:
		res.ParsedResult = f.parsedWriter
	case domain.RoleReviewer:
		res.ParsedReview = f.parsedReview
		if f.onReviewer != nil {
			f.onReviewer(in)
		}
	}
	return res, nil
}

// pairFactory returns a factory that hands back the right fake based on
// kind. Different kinds may share the same fake instance — that lets a
// test set parsedWriter and parsedReview on a single agent.
func pairFactory(primary, reviewer *roleFakeAgent) func(domain.AgentKind, config.AwoConfig) (agents.Agent, error) {
	return func(k domain.AgentKind, _ config.AwoConfig) (agents.Agent, error) {
		switch k {
		case primary.kind:
			return primary, nil
		case reviewer.kind:
			return reviewer, nil
		}
		return nil, errors.New("unknown agent kind")
	}
}

// ----- shell stub ---------------------------------------------------------

type wrShellRunner struct {
	exits map[string]int
	calls int
}

func (s *wrShellRunner) run(_ context.Context, command, _, stdoutPath, stderrPath string) (*execx.CommandResult, error) {
	s.calls++
	if stdoutPath != "" {
		_ = os.WriteFile(stdoutPath, []byte("ok\n"), 0o644)
	}
	if stderrPath != "" {
		_ = os.WriteFile(stderrPath, nil, 0o644)
	}
	return &execx.CommandResult{ExitCode: s.exits[command]}, nil
}

// ----- common setup -------------------------------------------------------

func baseWriterReviewerOpts(t *testing.T) (
	WriterReviewerOptions,
	*pathFakeGit,
	*roleFakeAgent,
	*roleFakeAgent,
	*wrShellRunner,
) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".awo", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writerPath := filepath.Join(repo, ".awo", "worktrees", "run", "claude-writer")
	reviewerPath := filepath.Join(repo, ".awo", "worktrees", "run", "codex-reviewer")

	fg := &pathFakeGit{
		WriterChanged:  []string{"server/checkout.go", "server/checkout_test.go"},
		WriterDiff:     "diff --git a/server/checkout.go b/server/checkout.go\n+ok\n",
		WriterPath:     writerPath,
		WriterBranch:   "awo/run/claude-writer",
		ReviewerPath:   reviewerPath,
		ReviewerBranch: "awo/run/codex-reviewer",
		// After successful patch apply, both files appear in the reviewer
		// worktree as well.
		PreReviewer:  []string{"server/checkout.go", "server/checkout_test.go"},
		PostReviewer: []string{"server/checkout.go", "server/checkout_test.go"},
	}
	primary := &roleFakeAgent{
		kind:         domain.AgentClaude,
		parsedWriter: &domain.ParsedAgentResult{Summary: "fixed checkout validation"},
	}
	reviewer := &roleFakeAgent{
		kind: domain.AgentCodex,
		parsedReview: &agents.ParsedReviewResult{
			NonBlocking:    []string{"consider extracting the helper"},
			Recommendation: "approve",
			RiskSummary:    "low risk",
		},
	}
	shell := &wrShellRunner{exits: map[string]int{"go test ./...": 0}}

	cfg := config.Default()
	opts := WriterReviewerOptions{
		RepoRoot:       repo,
		Task:           "fix checkout validation",
		Primary:        domain.AgentClaude,
		Reviewer:       domain.AgentCodex,
		VerifyCommands: []string{"go test ./..."},
		Config:         cfg,
		AgentFactory:   pairFactory(primary, reviewer),
		GitFacade:      fg,
		VerifyOptions:  VerificationOptions{runner: shell.run},
		Stdout:         &strings.Builder{},
	}
	return opts, fg, primary, reviewer, shell
}

// ----- happy path ---------------------------------------------------------

func TestRunWriterReviewerHappyPath(t *testing.T) {
	opts, fg, primary, reviewer, shell := baseWriterReviewerOpts(t)

	report, err := RunWriterReviewer(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunWriterReviewer: %v", err)
	}
	if report.Status != domain.StatusCompleted {
		t.Errorf("status=%q", report.Status)
	}
	if report.Recommendation != domain.RecReadyForHumanReview {
		t.Errorf("recommendation=%q", report.Recommendation)
	}
	if len(report.AgentResults) != 2 {
		t.Fatalf("agent results=%d", len(report.AgentResults))
	}
	if report.AgentResults[0].Role != domain.RoleWriter ||
		report.AgentResults[0].Agent != domain.AgentClaude {
		t.Errorf("writer slot mismatch: %+v", report.AgentResults[0])
	}
	if report.AgentResults[1].Role != domain.RoleReviewer ||
		report.AgentResults[1].Agent != domain.AgentCodex {
		t.Errorf("reviewer slot mismatch: %+v", report.AgentResults[1])
	}
	if !equalStrings(report.AgentResults[0].ChangedFiles, fg.WriterChanged) {
		t.Errorf("writer changed files came from agent, not git: %v",
			report.AgentResults[0].ChangedFiles)
	}
	if report.AgentResults[1].Review == nil ||
		report.AgentResults[1].Review.Recommendation != "approve" {
		t.Errorf("reviewer review missing or wrong: %+v", report.AgentResults[1].Review)
	}
	if shell.calls != 1 {
		t.Errorf("expected 1 verification call, got %d", shell.calls)
	}
	if fg.applyCalls != 1 {
		t.Errorf("expected git apply to be invoked once, got %d", fg.applyCalls)
	}
	if fg.removeCalls != 2 {
		t.Errorf("expected 2 worktree removals, got %d", fg.removeCalls)
	}
	// Reviewer received ReadOnly=true.
	if len(reviewer.gotInputs) != 1 || !reviewer.gotInputs[0].ReadOnly {
		t.Errorf("reviewer should be invoked read-only: %+v", reviewer.gotInputs)
	}
	// Writer was not given ReadOnly.
	if len(primary.gotInputs) != 1 || primary.gotInputs[0].ReadOnly {
		t.Errorf("writer must not be read-only: %+v", primary.gotInputs)
	}

	// Proof pack on disk includes reviewer findings and recommendation.
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	pp, err := os.ReadFile(filepath.Join(root, "proof-pack.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		report.RunID,
		"writer-reviewer",
		"## Reviewer",
		"approve",
		"consider extracting the helper",
		"low risk",
		"AWO did not commit, push, merge, or auto-approve this change.",
	} {
		if !strings.Contains(string(pp), want) {
			t.Errorf("proof pack missing %q\n%s", want, string(pp))
		}
	}
}

// ----- recommendation ladder ---------------------------------------------

func TestRunWriterReviewerVerificationFailure(t *testing.T) {
	opts, _, _, _, shell := baseWriterReviewerOpts(t)
	shell.exits["go test ./..."] = 1

	report, err := RunWriterReviewer(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunWriterReviewer: %v", err)
	}
	if report.Recommendation != domain.RecFailedVerification {
		t.Errorf("recommendation=%q want failed_verification", report.Recommendation)
	}
	if report.Status != domain.StatusFailed {
		t.Errorf("status=%q want failed", report.Status)
	}
}

func TestRunWriterReviewerBlockingFinding(t *testing.T) {
	opts, _, _, reviewer, _ := baseWriterReviewerOpts(t)
	reviewer.parsedReview = &agents.ParsedReviewResult{
		Blocking:       []string{"missing nil check on inputs.Items"},
		Recommendation: "approve", // even with approve, blocking trumps
	}

	report, err := RunWriterReviewer(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunWriterReviewer: %v", err)
	}
	if report.Recommendation != domain.RecNeedsRevision {
		t.Errorf("recommendation=%q want needs_revision", report.Recommendation)
	}
}

func TestRunWriterReviewerRecommendationReject(t *testing.T) {
	opts, _, _, reviewer, _ := baseWriterReviewerOpts(t)
	reviewer.parsedReview = &agents.ParsedReviewResult{
		Recommendation: "reject",
	}

	report, err := RunWriterReviewer(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunWriterReviewer: %v", err)
	}
	if report.Recommendation != domain.RecNeedsRevision {
		t.Errorf("recommendation=%q want needs_revision", report.Recommendation)
	}
}

// ----- patch apply fallback ----------------------------------------------

func TestRunWriterReviewerPatchApplyFallback(t *testing.T) {
	opts, fg, _, reviewer, _ := baseWriterReviewerOpts(t)
	fg.ApplyErr = errors.New("patch does not apply: conflict in checkout.go")
	// When apply fails the reviewer worktree starts empty, and stays empty
	// (the reviewer is read-only).
	fg.PreReviewer = nil
	fg.PostReviewer = nil

	report, err := RunWriterReviewer(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunWriterReviewer: %v", err)
	}
	// Run still completes — the writer's diff is included inline in the
	// reviewer prompt as a fallback.
	if report.Status != domain.StatusCompleted {
		t.Errorf("status=%q want completed", report.Status)
	}
	if len(reviewer.gotInputs) != 1 {
		t.Fatalf("reviewer should still run on apply failure: %+v", reviewer.gotInputs)
	}
	if !strings.Contains(reviewer.gotInputs[0].Prompt, "diff --git") {
		t.Errorf("reviewer prompt should include writer diff inline:\n%s",
			reviewer.gotInputs[0].Prompt)
	}
	foundWarn := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "git apply (reviewer)") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected git-apply warning, got %v", report.Warnings)
	}
}

// ----- reviewer modification warning -------------------------------------

func TestRunWriterReviewerReviewerModificationWarning(t *testing.T) {
	opts, fg, _, _, _ := baseWriterReviewerOpts(t)
	// Reviewer added a file beyond the writer's two.
	fg.PostReviewer = []string{
		"server/checkout.go",
		"server/checkout_test.go",
		"server/secret_notes.md",
	}

	report, err := RunWriterReviewer(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunWriterReviewer: %v", err)
	}
	foundWarn := false
	for _, w := range report.Warnings {
		if strings.Contains(w, "reviewer modified") &&
			strings.Contains(w, "secret_notes.md") {
			foundWarn = true
			break
		}
	}
	if !foundWarn {
		t.Errorf("expected reviewer-modified warning, got %v", report.Warnings)
	}
}

// ----- input validation ---------------------------------------------------

func TestRunWriterReviewerValidatesInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*WriterReviewerOptions)
	}{
		{"empty repo root", func(o *WriterReviewerOptions) { o.RepoRoot = "" }},
		{"empty task", func(o *WriterReviewerOptions) { o.Task = "" }},
		{"unknown primary", func(o *WriterReviewerOptions) { o.Primary = domain.AgentKind("nope") }},
		{"unknown reviewer", func(o *WriterReviewerOptions) { o.Reviewer = domain.AgentKind("nope") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _, _, _ := baseWriterReviewerOpts(t)
			tc.mut(&o)
			if _, err := RunWriterReviewer(context.Background(), o); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
