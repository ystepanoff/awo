// Package gitx wraps a small subset of the git CLI used by AWO.
//
// All operations shell out to `git`. AWO never performs destructive git
// operations on its own; helpers here are read-mostly.
package gitx

import (
	"context"
	"fmt"
	"strings"

	"github.com/awo-dev/awo/internal/execx"
)

// Version returns the output of `git --version`.
func Version(ctx context.Context) (string, error) {
	res, err := execx.Run(ctx, "git", []string{"--version"}, execx.RunOptions{})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git --version exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}

// TopLevel returns the absolute path of the repository top-level for dir.
func TopLevel(ctx context.Context, dir string) (string, error) {
	res, err := execx.Run(ctx, "git", []string{"rev-parse", "--show-toplevel"}, execx.RunOptions{Dir: dir})
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("not a git repository (rev-parse: %s)", strings.TrimSpace(res.Stderr))
	}
	return strings.TrimSpace(res.Stdout), nil
}
