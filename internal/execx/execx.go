// Package execx wraps os/exec with context-aware helpers, captured output,
// and structured results suitable for run artifacts.
package execx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// Result captures the outcome of running a command.
type Result struct {
	Cmd      string        `json:"cmd"`
	Args     []string      `json:"args"`
	Dir      string        `json:"dir,omitempty"`
	ExitCode int           `json:"exitCode"`
	Stdout   string        `json:"stdout"`
	Stderr   string        `json:"stderr"`
	Duration time.Duration `json:"durationNs"`
	TimedOut bool          `json:"timedOut,omitempty"`
}

// RunOptions controls how a command is executed.
type RunOptions struct {
	Dir    string
	Env    []string
	Stdin  io.Reader
	Stdout io.Writer // optional, in addition to capture
	Stderr io.Writer // optional, in addition to capture
}

// Run executes name with args under ctx. Both stdout and stderr are
// captured into the Result; if opts.Stdout / opts.Stderr are non-nil they
// are also written to.
func Run(ctx context.Context, name string, args []string, opts RunOptions) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = opts.Dir
	cmd.Env = opts.Env
	cmd.Stdin = opts.Stdin

	var stdout, stderr bytes.Buffer
	if opts.Stdout != nil {
		cmd.Stdout = io.MultiWriter(&stdout, opts.Stdout)
	} else {
		cmd.Stdout = &stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = io.MultiWriter(&stderr, opts.Stderr)
	} else {
		cmd.Stderr = &stderr
	}

	runErr := cmd.Run()
	res := Result{
		Cmd:      name,
		Args:     append([]string(nil), args...),
		Dir:      opts.Dir,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
	}

	if ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}

	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
			return res, nil
		}
		return res, runErr
	}
	res.ExitCode = 0
	return res, nil
}
