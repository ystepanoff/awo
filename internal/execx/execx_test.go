package execx

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func skipOnWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("posix shell unavailable on windows")
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func TestRunSuccessWritesStdoutFile(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "logs", "stdout.log")
	res, err := Run(context.Background(), CommandSpec{
		Command:    "sh",
		Args:       []string{"-c", "printf hello"},
		StdoutPath: out,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	if got := read(t, out); got != "hello" {
		t.Fatalf("stdout=%q", got)
	}
	if res.TimedOut {
		t.Fatal("unexpected timeout")
	}
}

func TestRunNonZeroExitWritesStderr(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "stdout.log")
	stderrPath := filepath.Join(dir, "stderr.log")
	res, err := Run(context.Background(), CommandSpec{
		Command:    "sh",
		Args:       []string{"-c", "echo boom >&2; exit 7"},
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit=%d want 7", res.ExitCode)
	}
	if got := read(t, stderrPath); !strings.Contains(got, "boom") {
		t.Fatalf("stderr=%q", got)
	}
	if got := read(t, stdoutPath); got != "" {
		t.Fatalf("stdout should be empty, got %q", got)
	}
}

func TestRunTimeout(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	res, err := Run(context.Background(), CommandSpec{
		Command:    "sh",
		Args:       []string{"-c", "sleep 5"},
		Timeout:    50 * time.Millisecond,
		StdoutPath: filepath.Join(dir, "out.log"),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("expected TimedOut=true, got %+v", res)
	}
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit on timeout, got %d", res.ExitCode)
	}
	if res.Duration > time.Second {
		t.Fatalf("duration too long for a 50ms timeout: %s", res.Duration)
	}
}

func TestRunRedactsLogs(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "stdout.log")
	res, err := Run(context.Background(), CommandSpec{
		Command:    "sh",
		Args:       []string{"-c", "printf 'OPENAI_API_KEY=sk-abcdefghij\\n'"},
		StdoutPath: out,
		RedactLogs: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	body := read(t, out)
	if strings.Contains(body, "sk-abcdefghij") {
		t.Fatalf("log leaked secret: %q", body)
	}
	if !strings.Contains(body, "[REDACTED]") {
		t.Fatalf("log missing redaction marker: %q", body)
	}
	if !strings.Contains(body, "OPENAI_API_KEY=") {
		t.Fatalf("log dropped variable name: %q", body)
	}
}

func TestRunRedactsTrailingLineWithoutNewline(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "stdout.log")
	if _, err := Run(context.Background(), CommandSpec{
		Command:    "sh",
		Args:       []string{"-c", "printf 'OPENAI_API_KEY=sk-trailing12345'"},
		StdoutPath: out,
		RedactLogs: true,
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	body := read(t, out)
	if strings.Contains(body, "sk-trailing12345") {
		t.Fatalf("trailing line not redacted: %q", body)
	}
}

func TestRunNoLogPathsStillSucceeds(t *testing.T) {
	skipOnWindows(t)
	res, err := Run(context.Background(), CommandSpec{
		Command: "sh",
		Args:    []string{"-c", "true"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
}

func TestRunEmptyCommand(t *testing.T) {
	if _, err := Run(context.Background(), CommandSpec{}); err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestCommandExists(t *testing.T) {
	skipOnWindows(t)
	if !CommandExists("sh") {
		t.Fatal("expected sh to exist")
	}
	if CommandExists("definitely-not-a-real-binary-awo") {
		t.Fatal("expected missing binary to be reported absent")
	}
	if CommandExists("") {
		t.Fatal("empty string should not match any command")
	}
}

func TestRunShellVerification(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	stdoutPath := filepath.Join(dir, "out.log")
	stderrPath := filepath.Join(dir, "err.log")
	res, err := RunShellVerification(
		context.Background(),
		"echo first && echo second",
		"",
		stdoutPath,
		stderrPath,
	)
	if err != nil {
		t.Fatalf("run shell: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d", res.ExitCode)
	}
	body := read(t, stdoutPath)
	if !strings.Contains(body, "first") || !strings.Contains(body, "second") {
		t.Fatalf("stdout missing chained output: %q", body)
	}
}

func TestRunShellVerificationNonZero(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	res, err := RunShellVerification(
		context.Background(),
		"echo ok && false",
		"",
		filepath.Join(dir, "out.log"),
		filepath.Join(dir, "err.log"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("expected non-zero exit, got 0")
	}
}

func TestRunShellVerificationEmpty(t *testing.T) {
	if _, err := RunShellVerification(context.Background(), "  ", "", "", ""); err == nil {
		t.Fatal("expected error for empty shell command")
	}
}

func TestRunHonorsCwd(t *testing.T) {
	skipOnWindows(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "out.log")
	if _, err := Run(context.Background(), CommandSpec{
		Command:    "sh",
		Args:       []string{"-c", "pwd"},
		Cwd:        dir,
		StdoutPath: out,
	}); err != nil {
		t.Fatalf("run: %v", err)
	}
	body := strings.TrimSpace(read(t, out))
	// macOS may resolve the temp dir through /private; allow either.
	if !(body == dir || strings.HasSuffix(body, dir)) {
		t.Fatalf("pwd=%q want suffix %q", body, dir)
	}
}
