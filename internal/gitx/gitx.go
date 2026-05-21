// Package gitx wraps a small subset of the git CLI used by AWO.
//
// All operations shell out to `git`. AWO never performs destructive git
// operations on its own; helpers here are read-mostly.
package gitx

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/awo-dev/awo/internal/execx"
)

// Version returns the output of `git --version`.
func Version(ctx context.Context) (string, error) {
	stdout, _, err := captureGit(ctx, "", "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// TopLevel returns the absolute path of the repository top-level for dir.
func TopLevel(ctx context.Context, dir string) (string, error) {
	stdout, stderr, err := captureGit(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not a git repository (rev-parse: %s)", strings.TrimSpace(stderr))
	}
	return strings.TrimSpace(stdout), nil
}

// captureGit runs git with args under ctx, returning stdout/stderr as
// strings. Logs are written to a per-call temp dir which is cleaned up
// before return — gitx is read-only and does not need persistent logs.
func captureGit(ctx context.Context, cwd string, args ...string) (string, string, error) {
	tmp, err := os.MkdirTemp("", "awo-gitx-*")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)

	stdoutPath := filepath.Join(tmp, "stdout")
	stderrPath := filepath.Join(tmp, "stderr")

	res, err := execx.Run(ctx, execx.CommandSpec{
		Command:    "git",
		Args:       args,
		Cwd:        cwd,
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
	})
	if err != nil {
		return "", "", err
	}
	stdout, _ := os.ReadFile(stdoutPath)
	stderr, _ := os.ReadFile(stderrPath)
	if res.ExitCode != 0 {
		return string(stdout), string(stderr), fmt.Errorf("git %s exited %d", strings.Join(args, " "), res.ExitCode)
	}
	return string(stdout), string(stderr), nil
}
