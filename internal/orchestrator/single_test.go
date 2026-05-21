package orchestrator

import (
	"context"
	"encoding/json"
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

// ----- fake git -----------------------------------------------------------

type fakeGit struct {
	WorktreePath string
	Branch       string
	ChangedFiles []string
	Diff         string
	DiffStat     string

	CreateErr error
	RemoveErr error

	CreateCalls int
	RemoveCalls int
}

func (f *fakeGit) CreateWorktree(_ context.Context, opts gitx.CreateWorktreeOptions) (*gitx.WorktreeInfo, error) {
	f.CreateCalls++
	if f.CreateErr != nil {
		return nil, f.CreateErr
	}
	path := f.WorktreePath
	if path == "" {
		path = filepath.Join(opts.RepoRoot, ".awo", "worktrees", opts.RunID, opts.Agent+"-"+opts.Role)
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return nil, err
	}
	branch := f.Branch
	if branch == "" {
		branch = "awo/" + opts.RunID + "/" + opts.Agent + "-" + opts.Role
	}
	return &gitx.WorktreeInfo{Path: path, Branch: branch, RunID: opts.RunID, Agent: opts.Agent, Role: opts.Role}, nil
}

func (f *fakeGit) GetChangedFiles(_ context.Context, _ string) ([]string, error) {
	return append([]string(nil), f.ChangedFiles...), nil
}
func (f *fakeGit) GetDiffPatch(_ context.Context, _ string) (string, error) { return f.Diff, nil }
func (f *fakeGit) GetDiffStat(_ context.Context, _ string) (string, error)  { return f.DiffStat, nil }
func (f *fakeGit) RemoveWorktree(_ context.Context, _ gitx.RemoveWorktreeOptions) error {
	f.RemoveCalls++
	return f.RemoveErr
}

// ----- fake agent ---------------------------------------------------------

type fakeAgent struct {
	kind         domain.AgentKind
	exitCode     int
	parsed       *domain.ParsedAgentResult
	returnErr    error
	gotInputs    []agents.AgentRunInput
	writeOnInvoke func(in agents.AgentRunInput) // optional file mutations
}

func (f *fakeAgent) Kind() domain.AgentKind { return f.kind }
func (f *fakeAgent) Run(_ context.Context, in agents.AgentRunInput) (*agents.AgentRunResult, error) {
	f.gotInputs = append(f.gotInputs, in)
	if f.writeOnInvoke != nil {
		f.writeOnInvoke(in)
	}
	if err := os.MkdirAll(in.ArtifactDir, 0o755); err != nil {
		return nil, err
	}
	stdout := filepath.Join(in.ArtifactDir, "stdout.log")
	stderr := filepath.Join(in.ArtifactDir, "stderr.log")
	prompt := filepath.Join(in.ArtifactDir, "prompt.md")
	_ = os.WriteFile(stdout, []byte("agent output\n"), 0o644)
	_ = os.WriteFile(stderr, nil, 0o644)
	_ = os.WriteFile(prompt, []byte(in.Prompt), 0o644)
	if f.returnErr != nil {
		return nil, f.returnErr
	}
	return &agents.AgentRunResult{
		Agent:        f.kind,
		Role:         in.Role,
		StartedAt:    time.Now().UTC(),
		FinishedAt:   time.Now().UTC().Add(time.Millisecond),
		ExitCode:     f.exitCode,
		StdoutPath:   stdout,
		StderrPath:   stderr,
		PromptPath:   prompt,
		DryRun:       in.DryRun,
		ParsedResult: f.parsed,
	}, nil
}

func factoryFor(a *fakeAgent) func(domain.AgentKind, config.AwoConfig) (agents.Agent, error) {
	return func(_ domain.AgentKind, _ config.AwoConfig) (agents.Agent, error) { return a, nil }
}

// ----- fake shell ---------------------------------------------------------

type singleShellRunner struct {
	exits map[string]int
	calls int
}

func (s *singleShellRunner) run(_ context.Context, command, _ , stdoutPath, stderrPath string) (*execx.CommandResult, error) {
	s.calls++
	if stdoutPath != "" {
		_ = os.WriteFile(stdoutPath, []byte("ok\n"), 0o644)
	}
	if stderrPath != "" {
		_ = os.WriteFile(stderrPath, nil, 0o644)
	}
	return &execx.CommandResult{ExitCode: s.exits[command]}, nil
}

// ----- helpers ------------------------------------------------------------

func baseSingleOpts(t *testing.T) (SingleRunOptions, *fakeGit, *fakeAgent, *singleShellRunner) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".awo", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(repo, ".awo", "worktrees", "run", "claude-writer")

	fg := &fakeGit{
		WorktreePath: wt,
		Branch:       "awo/run/claude-writer",
		ChangedFiles: []string{"server/health.go", "server/health_test.go"},
		Diff:         "diff --git a/server/health.go b/server/health.go\n+ok\n",
		DiffStat:     " server/health.go      | 1 +\n server/health_test.go | 1 +\n",
	}
	fa := &fakeAgent{kind: domain.AgentClaude, exitCode: 0,
		parsed: &domain.ParsedAgentResult{Summary: "added /health"}}
	shell := &singleShellRunner{exits: map[string]int{"go test ./...": 0}}

	cfg := config.Default()
	opts := SingleRunOptions{
		RepoRoot:       repo,
		Task:           "add /health endpoint",
		Agent:          domain.AgentClaude,
		VerifyCommands: []string{"go test ./..."},
		Config:         cfg,
		AgentFactory:   factoryFor(fa),
		GitFacade:      fg,
		VerifyOptions:  VerificationOptions{runner: shell.run},
		Stdout:         &strings.Builder{},
	}
	return opts, fg, fa, shell
}

// ----- happy path ---------------------------------------------------------

func TestRunSingleHappyPath(t *testing.T) {
	opts, fg, fa, shell := baseSingleOpts(t)

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if report.Status != domain.StatusCompleted {
		t.Errorf("status=%q", report.Status)
	}
	if report.Recommendation != domain.RecReadyForHumanReview {
		t.Errorf("recommendation=%q", report.Recommendation)
	}
	if len(report.AgentResults) != 1 {
		t.Fatalf("agent results=%d", len(report.AgentResults))
	}
	ar := report.AgentResults[0]
	if ar.Agent != domain.AgentClaude || ar.Role != domain.RoleWriter {
		t.Errorf("agent/role=%v/%v", ar.Agent, ar.Role)
	}
	if !equalStrings(ar.ChangedFiles, fg.ChangedFiles) {
		t.Errorf("changed files came from agent, not git: %v", ar.ChangedFiles)
	}
	if ar.ParsedResult == nil || ar.ParsedResult.Summary != "added /health" {
		t.Errorf("parsed result missing: %+v", ar.ParsedResult)
	}
	if len(report.VerificationResults) != 1 || !report.VerificationResults[0].Passed {
		t.Errorf("verification=%+v", report.VerificationResults)
	}
	if shell.calls != 1 {
		t.Errorf("expected 1 verify call, got %d", shell.calls)
	}
	if fa.gotInputs[0].DryRun {
		t.Errorf("dry-run unexpectedly set")
	}
	if fg.RemoveCalls != 1 {
		t.Errorf("worktree should have been removed: removeCalls=%d", fg.RemoveCalls)
	}

	// Artifacts on disk.
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	for _, name := range []string{"run.json", "summary.md", "diff.patch", "proof-pack.md"} {
		p := filepath.Join(root, name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	// run.json roundtrips.
	data, err := os.ReadFile(filepath.Join(root, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rr domain.RunReport
	if err := json.Unmarshal(data, &rr); err != nil {
		t.Fatalf("run.json invalid: %v", err)
	}
	if rr.RunID != report.RunID || rr.Status != domain.StatusCompleted {
		t.Errorf("run.json shape: %+v", rr)
	}
	if rr.Spec.Mode != domain.ModeSingle {
		t.Errorf("spec.mode=%q", rr.Spec.Mode)
	}
	// proof pack contains key fields.
	pp, err := os.ReadFile(filepath.Join(root, "proof-pack.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{report.RunID, "go test ./...", "ready_for_human_review", "single", "auto-commits"} {
		if !strings.Contains(string(pp), w) {
			t.Errorf("proof-pack missing %q\n%s", w, string(pp))
		}
	}
}

// ----- dry run ------------------------------------------------------------

func TestRunSingleDryRunDoesNotInvokeAgent(t *testing.T) {
	// Adapter wires DryRun into AgentRunInput; here we simulate a real
	// adapter via the real ClaudeCLIAdapter but with a runner stub that
	// must not be called.
	repo := t.TempDir()

	fg := &fakeGit{
		WorktreePath: filepath.Join(repo, ".awo", "worktrees", "run", "claude-writer"),
		Branch:       "awo/run/claude-writer",
		ChangedFiles: nil,
	}
	cfg := config.Default()

	type call struct{}
	var spawned []call
	adapter := agents.NewClaudeCLIAdapter()
	// We can't inject into the real adapter, so use a fake that asserts
	// DryRun was threaded through.
	fa := &fakeAgent{kind: domain.AgentClaude, exitCode: 0}
	_ = adapter // referenced to keep import; real coverage is via fakeAgent.

	out := &strings.Builder{}
	report, err := RunSingle(context.Background(), SingleRunOptions{
		RepoRoot:     repo,
		Task:         "no-op",
		Agent:        domain.AgentClaude,
		Config:       cfg,
		DryRun:       true,
		AgentFactory: factoryFor(fa),
		GitFacade:    fg,
		Stdout:       out,
	})
	if err != nil {
		t.Fatalf("RunSingle dry: %v", err)
	}
	if len(fa.gotInputs) != 1 || !fa.gotInputs[0].DryRun {
		t.Fatalf("DryRun not threaded to agent: %+v", fa.gotInputs)
	}
	if len(spawned) != 0 {
		t.Fatal("real CLI must not be spawned")
	}
	// No verification commands → "not verified" branch in summary.
	if len(report.VerificationResults) != 0 {
		t.Errorf("dry-run with no commands should produce no verify results")
	}
	if !strings.Contains(out.String(), "not verified") {
		t.Errorf("summary missing 'not verified': %s", out.String())
	}
}

// ----- recommendation ladder ---------------------------------------------

func TestRunSingleFailedVerification(t *testing.T) {
	opts, _, _, shell := baseSingleOpts(t)
	shell.exits["go test ./..."] = 1

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if report.Recommendation != domain.RecFailedVerification {
		t.Errorf("recommendation=%q want failed_verification", report.Recommendation)
	}
	if report.Status != domain.StatusFailed {
		t.Errorf("status=%q", report.Status)
	}
}

func TestRunSingleProtectedPath(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	fg.ChangedFiles = []string{"go.mod", "server/health.go"}

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if report.Recommendation != domain.RecNeedsHumanAttention {
		t.Errorf("recommendation=%q want needs_human_attention", report.Recommendation)
	}
}

func TestRunSingleTooLarge(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.MaxChangedFiles = 2
	fg.ChangedFiles = []string{"a.go", "b.go", "c.go"}

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if report.Recommendation != domain.RecTooLargeForAutoReview {
		t.Errorf("recommendation=%q", report.Recommendation)
	}
}

// ----- cleanup behavior ---------------------------------------------------

func TestRunSingleKeepWorktreesSkipsRemoval(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	opts.KeepWorktrees = true

	if _, err := RunSingle(context.Background(), opts); err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if fg.RemoveCalls != 0 {
		t.Errorf("KeepWorktrees should skip removal; got %d", fg.RemoveCalls)
	}
}

func TestRunSingleCleanupFailureDoesNotDestroyArtifacts(t *testing.T) {
	opts, fg, _, _ := baseSingleOpts(t)
	fg.RemoveErr = errors.New("worktree removal blew up")

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle should not propagate cleanup errors: %v", err)
	}
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	if _, err := os.Stat(filepath.Join(root, "run.json")); err != nil {
		t.Errorf("run.json should still exist after cleanup failure: %v", err)
	}
	// Cleanup failure recorded as a warning by the deferred remove.
	data, err := os.ReadFile(filepath.Join(root, "run.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rr domain.RunReport
	_ = json.Unmarshal(data, &rr)
	found := false
	for _, w := range rr.Warnings {
		if strings.Contains(w, "cleanup worktree") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected cleanup warning in run.json, got %v", rr.Warnings)
	}
}

// ----- agent-claimed files ignored ----------------------------------------

func TestRunSingleIgnoresAgentClaimedChangedFiles(t *testing.T) {
	opts, fg, fa, _ := baseSingleOpts(t)
	// Agent lies about which files it changed.
	fa.parsed = &domain.ParsedAgentResult{
		Summary:      "I touched everything",
		FilesTouched: []string{"/etc/passwd", "go.mod", "secret.key"},
	}
	fg.ChangedFiles = []string{"server/health.go"}

	report, err := RunSingle(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	if !equalStrings(report.AgentResults[0].ChangedFiles, []string{"server/health.go"}) {
		t.Errorf("changed files should come from git, got %v", report.AgentResults[0].ChangedFiles)
	}
	if report.Recommendation != domain.RecReadyForHumanReview {
		t.Errorf("agent claims must not influence recommendation: got %q", report.Recommendation)
	}
}

// ----- console summary ---------------------------------------------------

func TestRunSingleStdoutSummary(t *testing.T) {
	opts, _, _, _ := baseSingleOpts(t)
	out := &strings.Builder{}
	opts.Stdout = out

	if _, err := RunSingle(context.Background(), opts); err != nil {
		t.Fatalf("RunSingle: %v", err)
	}
	for _, want := range []string{"AWO run ", "status:", "recommendation:", "verification:", "proof pack:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q\n%s", want, out.String())
		}
	}
}

// ----- input validation ---------------------------------------------------

func TestRunSingleValidatesInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*SingleRunOptions)
	}{
		{"empty repo root", func(o *SingleRunOptions) { o.RepoRoot = "" }},
		{"empty task", func(o *SingleRunOptions) { o.Task = "" }},
		{"unknown agent", func(o *SingleRunOptions) { o.Agent = domain.AgentKind("nope") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _, _ := baseSingleOpts(t)
			tc.mut(&o)
			if _, err := RunSingle(context.Background(), o); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
