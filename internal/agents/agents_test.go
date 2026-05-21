package agents

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
)

// fakeRunner records every CommandSpec it sees and returns a fixed
// CommandResult plus an optional stdout body that the adapter test can
// observe.
type fakeRunner struct {
	calls       []execx.CommandSpec
	stdoutBody  string
	stderrBody  string
	exitCode    int
	timedOut    bool
	returnError error
}

func (f *fakeRunner) run(_ context.Context, spec execx.CommandSpec) (*execx.CommandResult, error) {
	f.calls = append(f.calls, spec)
	if spec.StdoutPath != "" && f.stdoutBody != "" {
		_ = os.WriteFile(spec.StdoutPath, []byte(f.stdoutBody), 0o644)
	}
	if spec.StderrPath != "" && f.stderrBody != "" {
		_ = os.WriteFile(spec.StderrPath, []byte(f.stderrBody), 0o644)
	}
	if f.returnError != nil {
		return nil, f.returnError
	}
	return &execx.CommandResult{ExitCode: f.exitCode, TimedOut: f.timedOut}, nil
}

func defaultInput(t *testing.T, kind domain.AgentKind, role domain.AgentRole) AgentRunInput {
	t.Helper()
	worktree := t.TempDir()
	artifactDir := filepath.Join(t.TempDir(), "agents", string(kind)+"-"+string(role))
	cfg := config.Default()
	prompt := "do the thing\n"
	return AgentRunInput{
		RunID:        "20260521-143022-a1b2c3",
		Task:         "do the thing",
		Role:         role,
		Mode:         domain.ModeSingle,
		WorktreePath: worktree,
		BranchName:   "awo/run/" + string(kind) + "-" + string(role),
		ArtifactDir:  artifactDir,
		Config:       cfg,
		Prompt:       prompt,
	}
}

// ----- BuildClaudeCommand -------------------------------------------------

func TestBuildClaudeCommandDefaults(t *testing.T) {
	got := BuildClaudeCommand(config.ClaudeConfig{}, "p")
	if got.Command != "claude" {
		t.Errorf("Command=%q want %q", got.Command, "claude")
	}
	if len(got.Args) != 0 {
		t.Errorf("Args=%v, want none for defaults", got.Args)
	}
	if got.Timeout != 0 {
		t.Errorf("Timeout=%v want 0 for unset", got.Timeout)
	}
}

func TestBuildClaudeCommandHonorsConfig(t *testing.T) {
	cfg := config.ClaudeConfig{
		Command:        "/opt/claude-bin",
		Args:           []string{"--profile", "ci"},
		TimeoutSeconds: 30,
	}
	got := BuildClaudeCommand(cfg, "p")
	if got.Command != "/opt/claude-bin" {
		t.Errorf("Command=%q", got.Command)
	}
	if !equal(got.Args, []string{"--profile", "ci"}) {
		t.Errorf("Args=%v", got.Args)
	}
	if got.Timeout != 30*time.Second {
		t.Errorf("Timeout=%v", got.Timeout)
	}
}

// ----- BuildCodexCommand --------------------------------------------------

func TestBuildCodexCommandDefaults(t *testing.T) {
	cfg := config.Default().Agents.Codex // exec + sandbox + approvalMode
	got := BuildCodexCommand(cfg, "p")
	if got.Command != "codex" {
		t.Errorf("Command=%q", got.Command)
	}
	want := []string{"exec", "--sandbox", "workspace-write", "--approval-mode", "on-request"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v", got.Args, want)
	}
	if got.Timeout != 600*time.Second {
		t.Errorf("Timeout=%v", got.Timeout)
	}
}

func TestBuildCodexCommandUsesExecWhenArgsEmpty(t *testing.T) {
	cfg := config.CodexConfig{} // no Args, no sandbox, no approval
	got := BuildCodexCommand(cfg, "p")
	if !equal(got.Args, []string{"exec"}) {
		t.Errorf("Args=%v want [exec]", got.Args)
	}
}

func TestBuildCodexCommandUserArgsReplaceDefault(t *testing.T) {
	cfg := config.CodexConfig{
		Args:         []string{"--profile", "ci", "exec"},
		Sandbox:      "read-only",
		ApprovalMode: "never",
	}
	got := BuildCodexCommand(cfg, "p")
	want := []string{"--profile", "ci", "exec", "--sandbox", "read-only", "--approval-mode", "never"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v", got.Args, want)
	}
}

func TestBuildCodexCommandNoDangerousDefaults(t *testing.T) {
	cfg := config.Default().Agents.Codex
	got := BuildCodexCommand(cfg, "p")
	for _, a := range got.Args {
		if strings.Contains(a, "dangerously") || strings.Contains(a, "skip-permission") {
			t.Errorf("must not auto-bypass permissions; got arg %q in %v", a, got.Args)
		}
	}
}

// ----- ClaudeCLIAdapter.Run -----------------------------------------------

func TestClaudeRunInvokesCLIAndParsesResult(t *testing.T) {
	in := defaultInput(t, domain.AgentClaude, domain.RoleWriter)
	fr := &fakeRunner{
		stdoutBody: "narrative\nAWO_RESULT_JSON\n{\"summary\": \"did it\", \"confidence\": \"high\"}\n",
	}
	a := &ClaudeCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Agent != domain.AgentClaude || res.Role != domain.RoleWriter {
		t.Errorf("agent/role mismatch: %+v", res)
	}
	if res.DryRun {
		t.Error("DryRun should be false")
	}
	if len(fr.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fr.calls))
	}
	got := fr.calls[0]
	if got.Command != "claude" {
		t.Errorf("Command=%q", got.Command)
	}
	if got.Cwd != in.WorktreePath {
		t.Errorf("Cwd=%q want %q", got.Cwd, in.WorktreePath)
	}
	if got.StdoutPath != filepath.Join(in.ArtifactDir, "stdout.log") {
		t.Errorf("StdoutPath=%q", got.StdoutPath)
	}
	// Prompt and command record on disk.
	if data, err := os.ReadFile(filepath.Join(in.ArtifactDir, "prompt.md")); err != nil || string(data) != in.Prompt {
		t.Errorf("prompt.md not written correctly: data=%q err=%v", string(data), err)
	}
	if data, err := os.ReadFile(filepath.Join(in.ArtifactDir, "command.txt")); err != nil || !strings.HasPrefix(string(data), "claude") {
		t.Errorf("command.txt not written correctly: data=%q err=%v", string(data), err)
	}
	if res.ParsedResult == nil || res.ParsedResult.Summary != "did it" {
		t.Errorf("parsed result missing or wrong: %+v", res.ParsedResult)
	}
	if res.ParsedReview != nil {
		t.Errorf("writer should not produce review block, got %+v", res.ParsedReview)
	}
}

func TestClaudeRunDryRunDoesNotInvokeCLI(t *testing.T) {
	in := defaultInput(t, domain.AgentClaude, domain.RoleWriter)
	in.DryRun = true
	fr := &fakeRunner{}
	a := &ClaudeCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.DryRun {
		t.Error("DryRun=false on dry-run result")
	}
	if len(fr.calls) != 0 {
		t.Fatalf("dry run must not invoke runner; got %d calls", len(fr.calls))
	}
	if data, err := os.ReadFile(filepath.Join(in.ArtifactDir, "prompt.md")); err != nil || string(data) != in.Prompt {
		t.Errorf("prompt should still be written in dry-run: data=%q err=%v", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(in.ArtifactDir, "command.txt")); err != nil {
		t.Errorf("command.txt should still be written in dry-run: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(in.ArtifactDir, "stdout.log")); !strings.Contains(string(data), "dry-run") {
		t.Errorf("stdout.log should mark dry-run, got %q", string(data))
	}
}

func TestClaudeRunRequiresFields(t *testing.T) {
	a := &ClaudeCLIAdapter{run: (&fakeRunner{}).run}
	for name, mut := range map[string]func(*AgentRunInput){
		"empty RunID":        func(i *AgentRunInput) { i.RunID = "" },
		"empty Task":         func(i *AgentRunInput) { i.Task = "" },
		"empty WorktreePath": func(i *AgentRunInput) { i.WorktreePath = "" },
		"empty ArtifactDir":  func(i *AgentRunInput) { i.ArtifactDir = "" },
		"empty Prompt":       func(i *AgentRunInput) { i.Prompt = "" },
		"invalid Role":       func(i *AgentRunInput) { i.Role = domain.AgentRole("bogus") },
	} {
		in := defaultInput(t, domain.AgentClaude, domain.RoleWriter)
		mut(&in)
		if _, err := a.Run(context.Background(), in); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

// ----- CodexCLIAdapter.Run ------------------------------------------------

func TestCodexRunInvokesCLIWithSandboxFlags(t *testing.T) {
	in := defaultInput(t, domain.AgentCodex, domain.RoleCompetitor)
	fr := &fakeRunner{exitCode: 0}
	a := &CodexCLIAdapter{run: fr.run}

	if _, err := a.Run(context.Background(), in); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fr.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fr.calls))
	}
	got := fr.calls[0]
	want := []string{"exec", "--sandbox", "workspace-write", "--approval-mode", "on-request"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v", got.Args, want)
	}
}

func TestCodexRunReviewerParsesReviewBlock(t *testing.T) {
	in := defaultInput(t, domain.AgentCodex, domain.RoleReviewer)
	fr := &fakeRunner{
		stdoutBody: "review notes\nAWO_REVIEW_JSON\n{\"recommendation\": \"approve_for_human_review\"}\n",
	}
	a := &CodexCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ParsedReview == nil || res.ParsedReview.Recommendation != "approve_for_human_review" {
		t.Errorf("review missing or wrong: %+v", res.ParsedReview)
	}
	if res.ParsedResult != nil {
		t.Errorf("reviewer must not produce result block, got %+v", res.ParsedResult)
	}
}

func TestCodexRunMalformedResultProducesWarning(t *testing.T) {
	in := defaultInput(t, domain.AgentCodex, domain.RoleWriter)
	fr := &fakeRunner{stdoutBody: "AWO_RESULT_JSON\n{not json}\n"}
	a := &CodexCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ParsedResult != nil {
		t.Errorf("expected nil parsed result for malformed JSON, got %+v", res.ParsedResult)
	}
	if len(res.Warnings) == 0 {
		t.Error("expected warning for malformed JSON")
	}
}

func TestCodexRunReturnsErrorFromRunner(t *testing.T) {
	in := defaultInput(t, domain.AgentCodex, domain.RoleWriter)
	fr := &fakeRunner{returnError: errors.New("boom")}
	a := &CodexCLIAdapter{run: fr.run}

	if _, err := a.Run(context.Background(), in); err == nil {
		t.Fatal("expected error from runner to propagate")
	}
}

// ----- New() factory ------------------------------------------------------

func TestNewReturnsAdapters(t *testing.T) {
	cfg := config.Default()
	cl, err := New(domain.AgentClaude, cfg)
	if err != nil {
		t.Fatalf("New claude: %v", err)
	}
	if cl.Kind() != domain.AgentClaude {
		t.Errorf("kind=%q", cl.Kind())
	}
	co, err := New(domain.AgentCodex, cfg)
	if err != nil {
		t.Fatalf("New codex: %v", err)
	}
	if co.Kind() != domain.AgentCodex {
		t.Errorf("kind=%q", co.Kind())
	}
	if _, err := New(domain.AgentKind("nope"), cfg); err == nil {
		t.Error("expected error for unknown kind")
	}
}

// ----- helpers ------------------------------------------------------------

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
