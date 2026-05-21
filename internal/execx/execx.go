// Package execx is AWO's safe process-execution layer.
//
// Design rules (enforced by API shape, not just docs):
//
//  1. Normal command execution — agents, git, anything else — never goes
//     through a shell. Run takes Command and Args separately and uses
//     exec.CommandContext.
//
//  2. Shell execution exists for one purpose only: deterministic
//     verification commands such as "pnpm test && pnpm typecheck". It is
//     isolated to a single entry point, RunShellVerification.
//
//  3. Logs (stdout/stderr) are streamed to files with optional terminal
//     mirroring. When RedactLogs is true, lines are passed through a
//     first-pass secret redactor before being written.
//
//  4. Timeouts and cancellation are honored via context. Non-zero exit
//     codes are returned in CommandResult, never hidden behind a generic
//     error.
package execx

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CommandSpec describes a single process execution.
type CommandSpec struct {
	Command    string
	Args       []string
	Cwd        string
	Env        []string
	Timeout    time.Duration
	StdoutPath string
	StderrPath string
	LiveOutput bool
	RedactLogs bool
}

// CommandResult is the structured outcome of a CommandSpec execution.
type CommandResult struct {
	ExitCode   int           `json:"exitCode"`
	Signal     string        `json:"signal,omitempty"`
	Duration   time.Duration `json:"durationNs"`
	StdoutPath string        `json:"stdoutPath,omitempty"`
	StderrPath string        `json:"stderrPath,omitempty"`
	TimedOut   bool          `json:"timedOut,omitempty"`
}

// Run executes spec.Command directly (no shell). Stdout and stderr are
// streamed to log files; if LiveOutput is true they are also mirrored to
// the parent process's stdout/stderr. Returns a CommandResult populated
// even on non-zero exit; errors are reserved for setup/IO failures.
func Run(ctx context.Context, spec CommandSpec) (*CommandResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(spec.Command) == "" {
		return nil, errors.New("execx: empty command")
	}

	runCtx, cancel := withTimeout(ctx, spec.Timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, spec.Command, spec.Args...)
	cmd.Dir = spec.Cwd
	if spec.Env != nil {
		cmd.Env = spec.Env
	}

	stdoutW, closeStdout, err := openStreamWriter(spec.StdoutPath, os.Stdout, spec.LiveOutput, spec.RedactLogs)
	if err != nil {
		return nil, fmt.Errorf("execx: open stdout log: %w", err)
	}
	defer closeStdout()
	stderrW, closeStderr, err := openStreamWriter(spec.StderrPath, os.Stderr, spec.LiveOutput, spec.RedactLogs)
	if err != nil {
		return nil, fmt.Errorf("execx: open stderr log: %w", err)
	}
	defer closeStderr()

	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	start := time.Now()
	runErr := cmd.Run()
	dur := time.Since(start)

	// Flush any trailing partial line from each redacting writer.
	stdoutW.flush()
	stderrW.flush()

	res := &CommandResult{
		Duration:   dur,
		StdoutPath: spec.StdoutPath,
		StderrPath: spec.StderrPath,
	}

	if runCtx.Err() != nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}

	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
			res.Signal = extractSignal(ee.ProcessState)
			return res, nil
		}
		// On context cancel/timeout exec.Cmd returns a non-ExitError
		// error before the process state is populated. Mark exit -1.
		if res.TimedOut || errors.Is(ctx.Err(), context.Canceled) {
			res.ExitCode = -1
			return res, nil
		}
		return res, runErr
	}
	res.ExitCode = 0
	if cmd.ProcessState != nil {
		res.Signal = extractSignal(cmd.ProcessState)
	}
	return res, nil
}

// CommandExists reports whether command is resolvable on PATH.
func CommandExists(command string) bool {
	if strings.TrimSpace(command) == "" {
		return false
	}
	_, err := exec.LookPath(command)
	return err == nil
}

// RunShellVerification runs a verification command through the platform
// shell. This is the ONE place AWO invokes a shell — it exists because
// verification snippets are user-authored and may chain commands such as
// "pnpm test && pnpm typecheck". Do not call this for agent/git work.
func RunShellVerification(ctx context.Context, command, cwd, stdoutPath, stderrPath string) (*CommandResult, error) {
	if strings.TrimSpace(command) == "" {
		return nil, errors.New("execx: empty shell verification command")
	}
	bin, args := shellInvocation(command)
	return Run(ctx, CommandSpec{
		Command:    bin,
		Args:       args,
		Cwd:        cwd,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		RedactLogs: true,
	})
}

func shellInvocation(command string) (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/C", command}
	}
	return "sh", []string{"-c", command}
}

func withTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, d)
}

// streamWriter is the writer attached to cmd.Stdout/Stderr. It line-buffers
// so the redactor (when enabled) sees complete lines, then writes to a log
// file and (optionally) to a terminal stream.
type streamWriter struct {
	dest    io.Writer
	buf     bytes.Buffer
	redact  bool
}

func (w *streamWriter) Write(p []byte) (int, error) {
	if w == nil || w.dest == nil {
		return len(p), nil
	}
	w.buf.Write(p)
	for {
		data := w.buf.Bytes()
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		line := append([]byte(nil), data[:i+1]...)
		w.buf.Next(i + 1)
		out := line
		if w.redact {
			out = []byte(Redact(string(line)))
		}
		if _, err := w.dest.Write(out); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *streamWriter) flush() {
	if w == nil || w.dest == nil || w.buf.Len() == 0 {
		return
	}
	out := w.buf.Bytes()
	if w.redact {
		out = []byte(Redact(string(out)))
	}
	_, _ = w.dest.Write(out)
	w.buf.Reset()
}

// openStreamWriter prepares a streamWriter wired to the log file (if any)
// and, when live is true, mirrored to terminalDest. The returned closer
// closes any owned files; it never closes terminalDest.
func openStreamWriter(path string, terminalDest io.Writer, live, redact bool) (*streamWriter, func(), error) {
	closer := func() {}

	var dests []io.Writer
	if path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, closer, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, closer, err
		}
		dests = append(dests, bufio.NewWriter(f))
		// Capture the bufio.Writer so we can flush it before closing.
		bw := dests[len(dests)-1].(*bufio.Writer)
		closer = func() {
			_ = bw.Flush()
			_ = f.Close()
		}
	}
	if live && terminalDest != nil {
		dests = append(dests, terminalDest)
	}

	var dest io.Writer
	switch len(dests) {
	case 0:
		dest = io.Discard
	case 1:
		dest = dests[0]
	default:
		dest = io.MultiWriter(dests...)
	}
	return &streamWriter{dest: dest, redact: redact}, closer, nil
}
