package orchestrator

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/agents"
	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
	"github.com/awo-dev/awo/internal/gitx"
)

// ----- per-agent fake git -------------------------------------------------

type compFakeGit struct {
	// per-agent state, keyed by AgentKind
	state map[domain.AgentKind]*compCandidateState

	// records (under mu)
	mu          sync.Mutex
	createCalls int
	removeCalls int

	// pathOf maps a worktree path back to its agent so the per-path
	// methods (GetChangedFiles / GetDiffPatch) can find the right entry.
	pathOf map[string]domain.AgentKind
}

type compCandidateState struct {
	WorktreePath string
	Branch       string
	ChangedFiles []string
	Diff         string
}

func newCompFakeGit() *compFakeGit {
	return &compFakeGit{
		state:  map[domain.AgentKind]*compCandidateState{},
		pathOf: map[string]domain.AgentKind{},
	}
}

func (g *compFakeGit) CreateWorktree(_ context.Context, opts gitx.CreateWorktreeOptions) (*gitx.WorktreeInfo, error) {
	g.mu.Lock()
	g.createCalls++
	g.mu.Unlock()
	kind := domain.AgentKind(opts.Agent)
	st, ok := g.state[kind]
	if !ok {
		return nil, errors.New("compFakeGit: no state seeded for agent " + opts.Agent)
	}
	if st.WorktreePath == "" {
		st.WorktreePath = filepath.Join(opts.RepoRoot, ".awo", "worktrees", opts.RunID, opts.Agent+"-"+opts.Role)
	}
	if st.Branch == "" {
		st.Branch = "awo/" + opts.RunID + "/" + opts.Agent + "-" + opts.Role
	}
	if err := os.MkdirAll(st.WorktreePath, 0o755); err != nil {
		return nil, err
	}
	g.mu.Lock()
	g.pathOf[st.WorktreePath] = kind
	g.mu.Unlock()
	return &gitx.WorktreeInfo{
		Path:   st.WorktreePath,
		Branch: st.Branch,
		RunID:  opts.RunID,
		Agent:  opts.Agent,
		Role:   opts.Role,
	}, nil
}

func (g *compFakeGit) GetChangedFiles(_ context.Context, p string) ([]string, error) {
	g.mu.Lock()
	kind, ok := g.pathOf[p]
	g.mu.Unlock()
	if !ok {
		return nil, nil
	}
	return append([]string(nil), g.state[kind].ChangedFiles...), nil
}

func (g *compFakeGit) GetDiffPatch(_ context.Context, p string) (string, error) {
	g.mu.Lock()
	kind, ok := g.pathOf[p]
	g.mu.Unlock()
	if !ok {
		return "", nil
	}
	return g.state[kind].Diff, nil
}

func (g *compFakeGit) GetDiffStat(_ context.Context, _ string) (string, error) { return "", nil }

func (g *compFakeGit) ApplyPatch(_ context.Context, _, _ string) error { return nil }

func (g *compFakeGit) RemoveWorktree(_ context.Context, _ gitx.RemoveWorktreeOptions) error {
	g.mu.Lock()
	g.removeCalls++
	g.mu.Unlock()
	return nil
}

// ----- fake agent matched on kind ----------------------------------------

type compFakeAgent struct {
	kind   domain.AgentKind
	parsed *domain.ParsedAgentResult
	exit   int

	mu        sync.Mutex
	gotInputs []agents.AgentRunInput
}

func (a *compFakeAgent) Kind() domain.AgentKind { return a.kind }

func (a *compFakeAgent) Run(_ context.Context, in agents.AgentRunInput) (*agents.AgentRunResult, error) {
	a.mu.Lock()
	a.gotInputs = append(a.gotInputs, in)
	a.mu.Unlock()
	if err := os.MkdirAll(in.ArtifactDir, 0o755); err != nil {
		return nil, err
	}
	stdout := filepath.Join(in.ArtifactDir, "stdout.log")
	stderr := filepath.Join(in.ArtifactDir, "stderr.log")
	prompt := filepath.Join(in.ArtifactDir, "prompt.md")
	_ = os.WriteFile(stdout, []byte("competitor "+string(a.kind)+"\n"), 0o644)
	_ = os.WriteFile(stderr, nil, 0o644)
	_ = os.WriteFile(prompt, []byte(in.Prompt), 0o644)

	return &agents.AgentRunResult{
		Agent:        a.kind,
		Role:         in.Role,
		StartedAt:    time.Now().UTC(),
		FinishedAt:   time.Now().UTC().Add(time.Millisecond),
		ExitCode:     a.exit,
		StdoutPath:   stdout,
		StderrPath:   stderr,
		PromptPath:   prompt,
		DryRun:       in.DryRun,
		ParsedResult: a.parsed,
	}, nil
}

func compFactory(claude, codex *compFakeAgent) func(domain.AgentKind, config.AwoConfig) (agents.Agent, error) {
	return func(k domain.AgentKind, _ config.AwoConfig) (agents.Agent, error) {
		switch k {
		case domain.AgentClaude:
			return claude, nil
		case domain.AgentCodex:
			return codex, nil
		}
		return nil, errors.New("unknown agent kind")
	}
}

// ----- shell stub ---------------------------------------------------------

type compShellRunner struct {
	exits map[string]int
	// failFor allows a test to make a specific cwd fail verification.
	failFor map[string]bool
	mu      sync.Mutex
	calls   int
}

func (s *compShellRunner) run(_ context.Context, command, cwd, stdoutPath, stderrPath string) (*execx.CommandResult, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	if stdoutPath != "" {
		_ = os.WriteFile(stdoutPath, []byte("ok\n"), 0o644)
	}
	if stderrPath != "" {
		_ = os.WriteFile(stderrPath, nil, 0o644)
	}
	exit := s.exits[command]
	if s.failFor != nil && s.failFor[cwd] {
		exit = 1
	}
	return &execx.CommandResult{ExitCode: exit}, nil
}

// ----- common setup -------------------------------------------------------

func baseCompetitiveOpts(t *testing.T) (
	CompetitiveRunOptions,
	*compFakeGit,
	*compFakeAgent,
	*compFakeAgent,
	*compShellRunner,
) {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".awo", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}

	fg := newCompFakeGit()
	fg.state[domain.AgentClaude] = &compCandidateState{
		WorktreePath: filepath.Join(repo, ".awo", "worktrees", "run", "claude-competitor"),
		ChangedFiles: []string{"server/migrate.go", "server/migrate_test.go"},
		Diff: strings.Join([]string{
			"diff --git a/server/migrate.go b/server/migrate.go",
			"--- a/server/migrate.go",
			"+++ b/server/migrate.go",
			"+ ok",
			"+ another",
		}, "\n"),
	}
	codexDiff := []string{
		"diff --git a/server/migrate.go b/server/migrate.go",
		"--- a/server/migrate.go",
		"+++ b/server/migrate.go",
	}
	for i := 0; i < 80; i++ {
		codexDiff = append(codexDiff, "+ extra line")
	}
	fg.state[domain.AgentCodex] = &compCandidateState{
		WorktreePath: filepath.Join(repo, ".awo", "worktrees", "run", "codex-competitor"),
		ChangedFiles: []string{"server/migrate.go", "server/migrate_test.go", "server/extra.go", "server/more.go", "server/lots.go"},
		Diff:         strings.Join(codexDiff, "\n"),
	}

	claude := &compFakeAgent{
		kind: domain.AgentClaude,
		parsed: &domain.ParsedAgentResult{
			Summary: "claude approach: minimal patch",
			Notes:   []string{"confidence: medium"},
		},
	}
	codex := &compFakeAgent{
		kind: domain.AgentCodex,
		parsed: &domain.ParsedAgentResult{
			Summary: "codex approach: bigger refactor",
			Notes:   []string{"confidence: low", "follow_up: more cleanup possible"},
		},
	}

	shell := &compShellRunner{exits: map[string]int{"go test ./...": 0}}

	cfg := config.Default()
	opts := CompetitiveRunOptions{
		RepoRoot:       repo,
		Task:           "migrate date utility usage",
		Competitors:    []domain.AgentKind{domain.AgentClaude, domain.AgentCodex},
		VerifyCommands: []string{"go test ./..."},
		Config:         cfg,
		AgentFactory:   compFactory(claude, codex),
		GitFacade:      fg,
		VerifyOptions:  VerificationOptions{runner: shell.run},
		Stdout:         &strings.Builder{},
	}
	return opts, fg, claude, codex, shell
}

// ----- happy path ---------------------------------------------------------

func TestRunCompetitiveHappyPath(t *testing.T) {
	opts, fg, claude, codex, shell := baseCompetitiveOpts(t)

	report, err := RunCompetitive(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunCompetitive: %v", err)
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
	// Ordering must follow opts.Competitors regardless of finish order.
	if report.AgentResults[0].Agent != domain.AgentClaude ||
		report.AgentResults[1].Agent != domain.AgentCodex {
		t.Errorf("agent ordering wrong: %s, %s",
			report.AgentResults[0].Agent, report.AgentResults[1].Agent)
	}
	// Both competitors invoked once each.
	if len(claude.gotInputs) != 1 || len(codex.gotInputs) != 1 {
		t.Errorf("expected 1 invocation per competitor, got %d/%d",
			len(claude.gotInputs), len(codex.gotInputs))
	}
	// Each candidate got its own worktree path.
	if claude.gotInputs[0].WorktreePath == codex.gotInputs[0].WorktreePath {
		t.Errorf("competitors should not share a worktree")
	}
	// Two creates, two removes.
	if fg.createCalls != 2 || fg.removeCalls != 2 {
		t.Errorf("expected 2 create+2 remove, got create=%d remove=%d",
			fg.createCalls, fg.removeCalls)
	}
	// Verification ran twice (once per candidate).
	if shell.calls != 2 {
		t.Errorf("expected 2 verify calls, got %d", shell.calls)
	}

	// comparison.md exists and contains both agents and the recommendation.
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	cmpBody, err := os.ReadFile(filepath.Join(root, "comparison.md"))
	if err != nil {
		t.Fatalf("comparison.md missing: %v", err)
	}
	for _, want := range []string{
		report.RunID,
		"competitive",
		"claude",
		"codex",
		"ready_for_human_review",
		"Human review is required before any commit, push, or merge.",
		"Score breakdown",
		"Recommended candidate",
	} {
		if !strings.Contains(string(cmpBody), want) {
			t.Errorf("comparison.md missing %q\n%s", want, string(cmpBody))
		}
	}
	// Per-candidate diff persisted under agents/<agent>-competitor/diff.patch.
	for _, kind := range []domain.AgentKind{domain.AgentClaude, domain.AgentCodex} {
		p := filepath.Join(root, "agents", string(kind)+"-competitor", "diff.patch")
		if fi, err := os.Stat(p); err != nil || fi.Size() == 0 {
			t.Errorf("missing per-candidate diff at %s: %v", p, err)
		}
	}
}

// ----- one passes, one fails ---------------------------------------------

func TestRunCompetitiveOnePassingOneFailing(t *testing.T) {
	opts, fg, _, _, shell := baseCompetitiveOpts(t)
	// Make codex's worktree fail verification.
	shell.failFor = map[string]bool{
		fg.state[domain.AgentCodex].WorktreePath: true,
	}

	report, err := RunCompetitive(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunCompetitive: %v", err)
	}
	if report.Recommendation != domain.RecReadyForHumanReview {
		t.Errorf("recommendation=%q want ready_for_human_review", report.Recommendation)
	}
	if report.Status != domain.StatusCompleted {
		t.Errorf("status=%q want completed", report.Status)
	}
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	cmpBody, err := os.ReadFile(filepath.Join(root, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	// Claude wins.
	if !strings.Contains(string(cmpBody), "Recommended candidate:** `claude`") {
		t.Errorf("expected claude to be recommended:\n%s", string(cmpBody))
	}
}

// ----- both fail ----------------------------------------------------------

func TestRunCompetitiveBothFail(t *testing.T) {
	opts, _, _, _, shell := baseCompetitiveOpts(t)
	shell.exits["go test ./..."] = 1

	report, err := RunCompetitive(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunCompetitive: %v", err)
	}
	if report.Recommendation != domain.RecFailedVerification {
		t.Errorf("recommendation=%q want failed_verification", report.Recommendation)
	}
	if report.Status != domain.StatusFailed {
		t.Errorf("status=%q want failed", report.Status)
	}
}

// ----- tie ----------------------------------------------------------------

func TestRunCompetitiveTie(t *testing.T) {
	opts, fg, _, _, _ := baseCompetitiveOpts(t)
	// Make both candidates produce identical-shaped output.
	matched := &compCandidateState{
		WorktreePath: fg.state[domain.AgentClaude].WorktreePath,
		ChangedFiles: []string{"a.go", "a_test.go"},
		Diff:         "diff --git a/a.go b/a.go\n+ ok\n",
	}
	fg.state[domain.AgentClaude] = matched
	fg.state[domain.AgentCodex] = &compCandidateState{
		WorktreePath: fg.state[domain.AgentCodex].WorktreePath,
		ChangedFiles: []string{"a.go", "a_test.go"},
		Diff:         "diff --git a/a.go b/a.go\n+ ok\n",
	}

	report, err := RunCompetitive(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunCompetitive: %v", err)
	}
	if report.Recommendation != domain.RecNeedsHumanAttention {
		t.Errorf("recommendation=%q want needs_human_attention (tie)", report.Recommendation)
	}
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	cmpBody, err := os.ReadFile(filepath.Join(root, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cmpBody), "Tie") {
		t.Errorf("comparison.md missing tie note:\n%s", string(cmpBody))
	}
}

// ----- protected path penalty applied to a real run ----------------------

func TestRunCompetitiveProtectedPathPenalty(t *testing.T) {
	opts, fg, _, _, _ := baseCompetitiveOpts(t)
	// Make claude touch a protected file but otherwise be the smaller
	// change. With both passing, the protected hit should drop claude
	// below codex.
	fg.state[domain.AgentClaude].ChangedFiles = []string{"go.mod", "server/migrate.go"}
	fg.state[domain.AgentCodex].ChangedFiles = []string{"server/migrate.go", "server/migrate_test.go"}
	fg.state[domain.AgentCodex].Diff = "diff --git a/server/migrate.go b/server/migrate.go\n+ small\n"

	report, err := RunCompetitive(context.Background(), opts)
	if err != nil {
		t.Fatalf("RunCompetitive: %v", err)
	}
	root := filepath.Join(opts.RepoRoot, ".awo", "runs", report.RunID)
	cmpBody, err := os.ReadFile(filepath.Join(root, "comparison.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Protected path warnings",
		"go.mod",
		"Recommended candidate:** `codex`",
	} {
		if !strings.Contains(string(cmpBody), want) {
			t.Errorf("comparison.md missing %q:\n%s", want, string(cmpBody))
		}
	}
}

// ----- input validation ---------------------------------------------------

func TestRunCompetitiveValidatesInput(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*CompetitiveRunOptions)
	}{
		{"empty repo root", func(o *CompetitiveRunOptions) { o.RepoRoot = "" }},
		{"empty task", func(o *CompetitiveRunOptions) { o.Task = "" }},
		{"too few competitors", func(o *CompetitiveRunOptions) { o.Competitors = []domain.AgentKind{domain.AgentClaude} }},
		{"duplicate competitors", func(o *CompetitiveRunOptions) {
			o.Competitors = []domain.AgentKind{domain.AgentClaude, domain.AgentClaude}
		}},
		{"unknown competitor", func(o *CompetitiveRunOptions) {
			o.Competitors = []domain.AgentKind{domain.AgentClaude, domain.AgentKind("nope")}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o, _, _, _, _ := baseCompetitiveOpts(t)
			tc.mut(&o)
			if _, err := RunCompetitive(context.Background(), o); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
