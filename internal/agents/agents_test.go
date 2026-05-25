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

func TestBuildClaudeCommandWriterDefaults(t *testing.T) {
	got := BuildClaudeCommand(config.ClaudeConfig{}, domain.RoleWriter, "the-prompt", "/wt", "/wt/out.log", "/wt/err.log")
	if got.Command != "claude" {
		t.Errorf("Command=%q want %q", got.Command, "claude")
	}
	want := []string{"-p", "--permission-mode", "acceptEdits"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v (writer = non-interactive print + auto-accept inside cwd)", got.Args, want)
	}
	if string(got.Stdin) != "the-prompt" {
		t.Errorf("Stdin=%q want prompt to be piped on stdin", string(got.Stdin))
	}
	if got.Timeout != defaultAgentTimeout {
		t.Errorf("Timeout=%v want default %v", got.Timeout, defaultAgentTimeout)
	}
}

func TestBuildClaudeCommandReviewerDefaults(t *testing.T) {
	got := BuildClaudeCommand(config.ClaudeConfig{}, domain.RoleReviewer, "p", "/wt", "/wt/out", "/wt/err")
	want := []string{"-p", "--permission-mode", "plan"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v (reviewer = plan-only)", got.Args, want)
	}
}

func TestBuildClaudeCommandNoDangerousDefaults(t *testing.T) {
	for _, role := range []domain.AgentRole{domain.RoleWriter, domain.RoleReviewer, domain.RoleCompetitor} {
		got := BuildClaudeCommand(config.ClaudeConfig{}, role, "p", "/wt", "/o", "/e")
		for _, a := range got.Args {
			low := strings.ToLower(a)
			if strings.Contains(low, "dangerously") || strings.Contains(low, "skip-permission") || low == "bypasspermissions" {
				t.Errorf("role=%s must not auto-bypass permissions; got arg %q in %v", role, a, got.Args)
			}
		}
	}
}

func TestBuildClaudeCommandHonorsRoleArgs(t *testing.T) {
	cfg := config.ClaudeConfig{
		Command:        "/opt/claude-bin",
		WriterArgs:     []string{"--writer-flag"},
		ReviewerArgs:   []string{"--reviewer-flag"},
		TimeoutSeconds: 30,
	}
	w := BuildClaudeCommand(cfg, domain.RoleWriter, "p", "/wt", "/o", "/e")
	if w.Command != "/opt/claude-bin" {
		t.Errorf("writer Command=%q", w.Command)
	}
	if !equal(w.Args, []string{"--writer-flag"}) {
		t.Errorf("writer Args=%v", w.Args)
	}
	if w.Timeout != 30*time.Second {
		t.Errorf("writer Timeout=%v", w.Timeout)
	}

	r := BuildClaudeCommand(cfg, domain.RoleReviewer, "p", "/wt", "/o", "/e")
	if !equal(r.Args, []string{"--reviewer-flag"}) {
		t.Errorf("reviewer Args=%v", r.Args)
	}
}

func TestBuildClaudeCommandHonorsLegacyArgs(t *testing.T) {
	// Legacy single Args list applies to every role when no per-role
	// list is set.
	cfg := config.ClaudeConfig{
		Args:           []string{"--profile", "ci"},
		TimeoutSeconds: 30,
	}
	for _, role := range []domain.AgentRole{domain.RoleWriter, domain.RoleReviewer, domain.RoleCompetitor} {
		got := BuildClaudeCommand(cfg, role, "p", "/wt", "/o", "/e")
		if !equal(got.Args, []string{"--profile", "ci"}) {
			t.Errorf("role=%s Args=%v want legacy fallback", role, got.Args)
		}
	}
}

func TestBuildClaudeCommandPromptPlaceholder(t *testing.T) {
	cfg := config.ClaudeConfig{
		WriterArgs: []string{"-p", "--prompt", "{{prompt}}"},
	}
	got := BuildClaudeCommand(cfg, domain.RoleWriter, "DO IT", "/wt", "/o", "/e")
	want := []string{"-p", "--prompt", "DO IT"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v ({{prompt}} should be substituted)", got.Args, want)
	}
	if len(got.Stdin) != 0 {
		t.Errorf("Stdin should be empty when placeholder is used; got %q", string(got.Stdin))
	}
}

// ----- BuildCodexCommand --------------------------------------------------

func TestBuildCodexCommandWriterDefaults(t *testing.T) {
	cfg := config.Default().Agents.Codex
	got := BuildCodexCommand(cfg, domain.RoleWriter, "the-prompt", "/wt", "/o", "/e")
	if got.Command != "codex" {
		t.Errorf("Command=%q", got.Command)
	}
	want := []string{"exec", "--sandbox", "workspace-write"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v", got.Args, want)
	}
	if string(got.Stdin) != "the-prompt" {
		t.Errorf("Stdin=%q want prompt to be piped on stdin", string(got.Stdin))
	}
	if got.Timeout != 1800*time.Second {
		t.Errorf("Timeout=%v", got.Timeout)
	}
}

func TestBuildCodexCommandReviewerDefaults(t *testing.T) {
	cfg := config.Default().Agents.Codex
	got := BuildCodexCommand(cfg, domain.RoleReviewer, "p", "/wt", "/o", "/e")
	want := []string{"exec", "--sandbox", "read-only"}
	if !equal(got.Args, want) {
		t.Errorf("reviewer Args=%v want %v", got.Args, want)
	}
}

func TestBuildCodexCommandUsesEmptyConfigDefaults(t *testing.T) {
	cfg := config.CodexConfig{}
	got := BuildCodexCommand(cfg, domain.RoleWriter, "p", "/wt", "/o", "/e")
	want := []string{"exec", "--sandbox", "workspace-write"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want safe writer default %v", got.Args, want)
	}
}

func TestBuildCodexCommandLegacyArgsFallback(t *testing.T) {
	// A pre-per-role config uses Args + Sandbox + ApprovalMode and
	// must keep working unchanged.
	cfg := config.CodexConfig{
		Args:         []string{"exec", "--profile", "ci"},
		Sandbox:      "read-only",
		ApprovalMode: "on-request",
	}
	got := BuildCodexCommand(cfg, domain.RoleWriter, "p", "/wt", "/o", "/e")
	want := []string{"exec", "--profile", "ci", "--sandbox", "read-only", "--approval-mode", "on-request"}
	if !equal(got.Args, want) {
		t.Errorf("legacy fallback Args=%v want %v", got.Args, want)
	}
}

func TestBuildCodexCommandPromptPlaceholder(t *testing.T) {
	cfg := config.CodexConfig{
		WriterArgs: []string{"exec", "{{prompt}}"},
	}
	got := BuildCodexCommand(cfg, domain.RoleWriter, "DO IT", "/wt", "/o", "/e")
	want := []string{"exec", "DO IT"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v", got.Args, want)
	}
	if len(got.Stdin) != 0 {
		t.Errorf("Stdin should be empty with placeholder; got %q", string(got.Stdin))
	}
}

func TestBuildCodexCommandNoDangerousDefaults(t *testing.T) {
	for _, role := range []domain.AgentRole{domain.RoleWriter, domain.RoleReviewer, domain.RoleCompetitor} {
		got := BuildCodexCommand(config.CodexConfig{}, role, "p", "/wt", "/o", "/e")
		for _, a := range got.Args {
			low := strings.ToLower(a)
			if strings.Contains(low, "dangerously") || strings.Contains(low, "skip-permission") || low == "danger-full-access" {
				t.Errorf("role=%s must not auto-bypass permissions; got arg %q in %v", role, a, got.Args)
			}
		}
	}
}

// ----- timeout normalization ---------------------------------------------

func TestResolveTimeoutDefaultsTo1800OnZero(t *testing.T) {
	if got := resolveTimeout(0); got != defaultAgentTimeout {
		t.Errorf("resolveTimeout(0)=%v want %v", got, defaultAgentTimeout)
	}
	if got := resolveTimeout(-5); got != defaultAgentTimeout {
		t.Errorf("resolveTimeout(-5)=%v want %v", got, defaultAgentTimeout)
	}
	if got := resolveTimeout(60); got != 60*time.Second {
		t.Errorf("resolveTimeout(60)=%v want 60s", got)
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
	if res.FailureKind != "" {
		t.Errorf("FailureKind=%q want empty for clean run", res.FailureKind)
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
	// command.txt records resolved args + timeout + non-interactive marker.
	cmdRec, _ := os.ReadFile(filepath.Join(in.ArtifactDir, "command.txt"))
	for _, want := range []string{"-p", "--permission-mode", "acceptEdits", "# timeout:", "# stdin:"} {
		if !strings.Contains(string(cmdRec), want) {
			t.Errorf("command.txt missing %q:\n%s", want, string(cmdRec))
		}
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

func TestClaudeRunPermissionFailureClassified(t *testing.T) {
	in := defaultInput(t, domain.AgentClaude, domain.RoleWriter)
	fr := &fakeRunner{
		exitCode:   0, // many CLIs print the error and still exit 0
		stderrBody: "Error: permission required to edit /etc/passwd\n",
	}
	a := &ClaudeCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FailureKind != FailurePermissionRequired {
		t.Errorf("FailureKind=%q want %q", res.FailureKind, FailurePermissionRequired)
	}
	if res.PermissionFailure == nil {
		t.Fatal("PermissionFailure not populated")
	}
	if res.PermissionFailure.Source != "stderr" {
		t.Errorf("source=%q want stderr", res.PermissionFailure.Source)
	}
}

func TestClaudeRunTimeoutClassified(t *testing.T) {
	in := defaultInput(t, domain.AgentClaude, domain.RoleWriter)
	fr := &fakeRunner{exitCode: -1, timedOut: true}
	a := &ClaudeCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FailureKind != FailureTimeout {
		t.Errorf("FailureKind=%q want %q", res.FailureKind, FailureTimeout)
	}
	if !strings.Contains(res.FailureReason, "timed out") {
		t.Errorf("FailureReason=%q should mention timeout", res.FailureReason)
	}
}

func TestClaudeRunProcessFailedClassified(t *testing.T) {
	in := defaultInput(t, domain.AgentClaude, domain.RoleWriter)
	fr := &fakeRunner{exitCode: 1, stderrBody: "ENOENT or whatever\n"}
	a := &ClaudeCLIAdapter{run: fr.run}

	res, err := a.Run(context.Background(), in)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.FailureKind != FailureProcessFailed {
		t.Errorf("FailureKind=%q want %q", res.FailureKind, FailureProcessFailed)
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
	want := []string{"exec", "--sandbox", "workspace-write"}
	if !equal(got.Args, want) {
		t.Errorf("Args=%v want %v", got.Args, want)
	}
}

func TestCodexRunReviewerUsesReadOnlySandbox(t *testing.T) {
	in := defaultInput(t, domain.AgentCodex, domain.RoleReviewer)
	fr := &fakeRunner{exitCode: 0}
	a := &CodexCLIAdapter{run: fr.run}

	if _, err := a.Run(context.Background(), in); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := fr.calls[0]
	want := []string{"exec", "--sandbox", "read-only"}
	if !equal(got.Args, want) {
		t.Errorf("reviewer Args=%v want %v (read-only sandbox)", got.Args, want)
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
	if res.FailureKind != FailureParseWarning {
		t.Errorf("FailureKind=%q want %q for parse warning", res.FailureKind, FailureParseWarning)
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
