package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awo-dev/awo/internal/artifacts"
	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
)

const verifyRunID = "20260521-143022-a1b2c3"

// shellCall records one fake shell invocation.
type shellCall struct {
	Command string
	Cwd     string
}

// fakeShell maps commands to (exit, stdout, stderr, err) and records
// every invocation in Calls.
type fakeShell struct {
	Calls   []shellCall
	Outcome map[string]struct {
		Exit   int
		Stdout string
		Stderr string
		Err    error
	}
}

func (f *fakeShell) run(_ context.Context, command, cwd, stdoutPath, stderrPath string) (*execx.CommandResult, error) {
	f.Calls = append(f.Calls, shellCall{Command: command, Cwd: cwd})
	o := f.Outcome[command]
	// Mirror the real shell runner: always create the log files when
	// paths are provided, even if their bodies are empty.
	if stdoutPath != "" {
		_ = os.WriteFile(stdoutPath, []byte(o.Stdout), 0o644)
	}
	if stderrPath != "" {
		_ = os.WriteFile(stderrPath, []byte(o.Stderr), 0o644)
	}
	if o.Err != nil {
		return nil, o.Err
	}
	return &execx.CommandResult{ExitCode: o.Exit, StdoutPath: stdoutPath, StderrPath: stderrPath}, nil
}

func newLayoutForVerify(t *testing.T) *artifacts.Layout {
	t.Helper()
	repo := t.TempDir()
	l, err := artifacts.NewLayout(repo, ".awo/runs", verifyRunID)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	return l
}

// ----- happy path ---------------------------------------------------------

func TestRunVerificationAllPass(t *testing.T) {
	layout := newLayoutForVerify(t)
	worktree := t.TempDir()
	fs := &fakeShell{Outcome: map[string]struct {
		Exit   int
		Stdout string
		Stderr string
		Err    error
	}{
		"go test ./...": {Exit: 0, Stdout: "ok\n"},
		"go vet ./...":  {Exit: 0, Stdout: "ok\n"},
	}}

	got, err := RunVerificationWithOptions(
		context.Background(),
		worktree,
		[]string{"go test ./...", "go vet ./..."},
		layout,
		config.Default(),
		VerificationOptions{runner: fs.run},
	)
	if err != nil {
		t.Fatalf("RunVerification: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(got), got)
	}
	for i, r := range got {
		if !r.Passed || r.ExitCode != 0 {
			t.Errorf("result %d: %+v", i, r)
		}
		if r.StdoutPath == "" || r.StderrPath == "" {
			t.Errorf("result %d missing log paths: %+v", i, r)
		}
	}
	if !AllPassed(got) {
		t.Errorf("AllPassed should be true: %+v", got)
	}
	if len(fs.Calls) != 2 {
		t.Fatalf("expected 2 shell calls, got %d", len(fs.Calls))
	}
	for _, c := range fs.Calls {
		if c.Cwd != worktree {
			t.Errorf("cwd=%q want %q", c.Cwd, worktree)
		}
	}
}

// ----- artifact paths -----------------------------------------------------

func TestRunVerificationWritesArtifacts(t *testing.T) {
	layout := newLayoutForVerify(t)
	fs := &fakeShell{Outcome: map[string]struct {
		Exit   int
		Stdout string
		Stderr string
		Err    error
	}{
		"go test ./...":     {Exit: 0, Stdout: "PASS\n"},
		"pnpm test":         {Exit: 0, Stdout: "ok\n"},
		"pytest -q":         {Exit: 0, Stdout: "5 passed\n"},
	}}

	cmds := []string{"go test ./...", "pnpm test", "pytest -q"}
	got, err := RunVerificationWithOptions(
		context.Background(),
		t.TempDir(),
		cmds,
		layout,
		config.Default(),
		VerificationOptions{runner: fs.run},
	)
	if err != nil {
		t.Fatalf("RunVerification: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d results, want 3", len(got))
	}

	for i, cmd := range cmds {
		dir := layout.VerificationDir(i + 1)
		// Standard layout subdir naming: 001, 002, ...
		if !strings.HasSuffix(dir, filepath.Join("verify", []string{"001", "002", "003"}[i])) {
			t.Errorf("dir %d=%q has wrong suffix", i+1, dir)
		}
		for _, name := range []string{"stdout.log", "stderr.log", "command.txt", "result.json"} {
			p := filepath.Join(dir, name)
			if _, err := os.Stat(p); err != nil {
				t.Errorf("missing %s for command %q: %v", name, cmd, err)
			}
		}
		// command.txt contains the original snippet.
		if data, err := os.ReadFile(filepath.Join(dir, "command.txt")); err != nil || !strings.Contains(string(data), cmd) {
			t.Errorf("command.txt %d wrong: data=%q err=%v", i+1, string(data), err)
		}
		// result.json roundtrips to a VerificationResult.
		var rj domain.VerificationResult
		data, err := os.ReadFile(filepath.Join(dir, "result.json"))
		if err != nil {
			t.Fatalf("read result.json %d: %v", i+1, err)
		}
		if err := json.Unmarshal(data, &rj); err != nil {
			t.Fatalf("decode result.json %d: %v", i+1, err)
		}
		if rj.Command != cmd {
			t.Errorf("result.json %d command=%q want %q", i+1, rj.Command, cmd)
		}
		if !rj.Passed || rj.ExitCode != 0 {
			t.Errorf("result.json %d not passed: %+v", i+1, rj)
		}
		if rj.DurationMillis < 0 {
			t.Errorf("negative duration: %+v", rj)
		}
	}
}

// ----- failure stops by default -------------------------------------------

func TestRunVerificationStopsOnFailure(t *testing.T) {
	layout := newLayoutForVerify(t)
	fs := &fakeShell{Outcome: map[string]struct {
		Exit   int
		Stdout string
		Stderr string
		Err    error
	}{
		"go vet ./...":  {Exit: 0},
		"go test ./...": {Exit: 1, Stdout: "FAIL\n"},
		"pnpm test":     {Exit: 0},
	}}

	got, err := RunVerificationWithOptions(
		context.Background(),
		t.TempDir(),
		[]string{"go vet ./...", "go test ./...", "pnpm test"},
		layout,
		config.Default(),
		VerificationOptions{runner: fs.run},
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results (stopped after failure), got %d: %+v", len(got), got)
	}
	if got[0].Passed != true {
		t.Errorf("first should pass: %+v", got[0])
	}
	if got[1].Passed != false || got[1].ExitCode != 1 {
		t.Errorf("second should fail: %+v", got[1])
	}
	if AllPassed(got) {
		t.Error("AllPassed should be false")
	}
	if len(fs.Calls) != 2 {
		t.Errorf("third command should not run; got %d calls", len(fs.Calls))
	}
	if _, err := os.Stat(layout.VerificationDir(3)); err == nil {
		t.Error("verify/003 should not exist when we stopped after step 2")
	}
}

func TestRunVerificationContinueOnFailure(t *testing.T) {
	layout := newLayoutForVerify(t)
	fs := &fakeShell{Outcome: map[string]struct {
		Exit   int
		Stdout string
		Stderr string
		Err    error
	}{
		"a": {Exit: 1},
		"b": {Exit: 0},
		"c": {Exit: 2},
	}}

	got, err := RunVerificationWithOptions(
		context.Background(),
		t.TempDir(),
		[]string{"a", "b", "c"},
		layout,
		config.Default(),
		VerificationOptions{runner: fs.run, ContinueOnFailure: true},
	)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results when continuing, got %d", len(got))
	}
	if got[0].Passed || !got[1].Passed || got[2].Passed {
		t.Errorf("results=%+v", got)
	}
}

// ----- runner errors ------------------------------------------------------

func TestRunVerificationRunnerError(t *testing.T) {
	layout := newLayoutForVerify(t)
	fs := &fakeShell{Outcome: map[string]struct {
		Exit   int
		Stdout string
		Stderr string
		Err    error
	}{
		"boom": {Err: errors.New("setup failed")},
	}}

	got, err := RunVerificationWithOptions(
		context.Background(),
		t.TempDir(),
		[]string{"boom"},
		layout,
		config.Default(),
		VerificationOptions{runner: fs.run},
	)
	if err == nil {
		t.Fatal("expected error to surface from runner")
	}
	if len(got) != 1 || got[0].Passed || got[0].ExitCode != -1 {
		t.Errorf("expected one failed result with exit -1, got %+v", got)
	}
}

// ----- empty / blank ------------------------------------------------------

func TestRunVerificationNoCommandsReturnsNil(t *testing.T) {
	layout := newLayoutForVerify(t)
	got, err := RunVerification(context.Background(), t.TempDir(), nil, layout, config.Default())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != nil {
		t.Fatalf("want nil result for no commands, got %+v", got)
	}
}

func TestRunVerificationFiltersBlankCommands(t *testing.T) {
	layout := newLayoutForVerify(t)
	got, err := RunVerification(context.Background(), t.TempDir(), []string{"  ", ""}, layout, config.Default())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got != nil {
		t.Errorf("want nil for all-blank commands, got %+v", got)
	}
}

func TestRunVerificationRejectsBadInputs(t *testing.T) {
	if _, err := RunVerification(context.Background(), "", nil, nil, config.Default()); err == nil {
		t.Error("expected error for nil layout")
	}
	layout := newLayoutForVerify(t)
	if _, err := RunVerification(context.Background(), "", []string{"go test"}, layout, config.Default()); err == nil {
		t.Error("expected error for empty worktreePath")
	}
}

// ----- ResolveVerifyCommands ----------------------------------------------

func TestResolveVerifyCommandsFlagWins(t *testing.T) {
	cfg := config.Default()
	got := ResolveVerifyCommands([]string{"flag1", "flag2"}, cfg)
	if !equalStrings(got, []string{"flag1", "flag2"}) {
		t.Errorf("got=%v", got)
	}
}

func TestResolveVerifyCommandsFallsBackToConfig(t *testing.T) {
	cfg := config.Default()
	got := ResolveVerifyCommands(nil, cfg)
	if !equalStrings(got, cfg.DefaultVerifyCommands) {
		t.Errorf("got=%v want=%v", got, cfg.DefaultVerifyCommands)
	}
}

func TestResolveVerifyCommandsEmpty(t *testing.T) {
	cfg := config.Default()
	cfg.DefaultVerifyCommands = nil
	got := ResolveVerifyCommands(nil, cfg)
	if got != nil {
		t.Errorf("want nil, got %v", got)
	}
}

func TestResolveVerifyCommandsTrimsBlanks(t *testing.T) {
	cfg := config.Default()
	got := ResolveVerifyCommands([]string{"", "  ", "go test"}, cfg)
	if !equalStrings(got, []string{"go test"}) {
		t.Errorf("got=%v", got)
	}
}

// ----- helpers ------------------------------------------------------------

func equalStrings(a, b []string) bool {
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
