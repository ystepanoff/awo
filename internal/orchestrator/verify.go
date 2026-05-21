package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/awo-dev/awo/internal/artifacts"
	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
)

// shellRunner is the function used to invoke a verification command.
// It exists so tests can inject a fake without spawning real shells.
type shellRunner func(ctx context.Context, command, cwd, stdoutPath, stderrPath string) (*execx.CommandResult, error)

// defaultShellRunner is the production runner. It always uses
// execx.RunShellVerification because verification commands are
// user-authored shell snippets like "pnpm test && pnpm typecheck".
func defaultShellRunner(ctx context.Context, command, cwd, stdoutPath, stderrPath string) (*execx.CommandResult, error) {
	return execx.RunShellVerification(ctx, command, cwd, stdoutPath, stderrPath)
}

// VerificationOptions tunes RunVerification behavior. Zero values are
// safe defaults: stop after the first failure.
type VerificationOptions struct {
	// ContinueOnFailure runs every command even when one fails. The
	// default (false) is to stop after the first non-zero exit code.
	ContinueOnFailure bool

	// runner overrides the shell runner. Tests use this; production
	// code should leave it nil so defaultShellRunner is used.
	runner shellRunner
}

// RunVerification executes commands in order against worktreePath,
// capturing each invocation's stdout/stderr and result.json under
// layout.VerificationDir(i+1).
//
// Verification commands are user-authored shell snippets — they may
// chain operations with && or pipes. They run through
// execx.RunShellVerification, the only place AWO invokes a shell.
//
// The exit code is the only trusted signal. Agent self-reports about
// whether tests passed are deliberately ignored here.
//
// Returns the result for every command that ran. When commands is
// empty, returns (nil, nil) so callers can mark the proof pack as
// "not verified" without treating the absence as an error.
func RunVerification(
	ctx context.Context,
	worktreePath string,
	commands []string,
	layout *artifacts.Layout,
	cfg config.AwoConfig,
) ([]domain.VerificationResult, error) {
	return runVerification(ctx, worktreePath, commands, layout, cfg, VerificationOptions{})
}

// RunVerificationWithOptions is RunVerification with explicit options.
func RunVerificationWithOptions(
	ctx context.Context,
	worktreePath string,
	commands []string,
	layout *artifacts.Layout,
	cfg config.AwoConfig,
	opts VerificationOptions,
) ([]domain.VerificationResult, error) {
	return runVerification(ctx, worktreePath, commands, layout, cfg, opts)
}

func runVerification(
	ctx context.Context,
	worktreePath string,
	commands []string,
	layout *artifacts.Layout,
	cfg config.AwoConfig,
	opts VerificationOptions,
) ([]domain.VerificationResult, error) {
	if layout == nil {
		return nil, errors.New("orchestrator: RunVerification: nil layout")
	}
	if strings.TrimSpace(worktreePath) == "" {
		return nil, errors.New("orchestrator: RunVerification: empty worktreePath")
	}

	// Filter out blank commands but preserve the user's order so the
	// indexed verify directories line up with what they passed.
	var clean []string
	for _, c := range commands {
		if strings.TrimSpace(c) != "" {
			clean = append(clean, c)
		}
	}
	if len(clean) == 0 {
		return nil, nil
	}

	runShell := opts.runner
	if runShell == nil {
		runShell = defaultShellRunner
	}

	_ = cfg.Safety.RedactLogs // RunShellVerification redacts unconditionally

	results := make([]domain.VerificationResult, 0, len(clean))
	for i, cmd := range clean {
		idx := i + 1
		dir, err := layout.EnsureVerificationDir(idx)
		if err != nil {
			return results, fmt.Errorf("orchestrator: ensure verify dir %d: %w", idx, err)
		}
		stdoutPath := filepath.Join(dir, "stdout.log")
		stderrPath := filepath.Join(dir, "stderr.log")
		commandPath := filepath.Join(dir, "command.txt")
		resultPath := filepath.Join(dir, "result.json")

		if err := os.WriteFile(commandPath, []byte(cmd+"\n"), 0o644); err != nil {
			return results, fmt.Errorf("orchestrator: write command record: %w", err)
		}

		started := time.Now().UTC()
		exec, runErr := runShell(ctx, cmd, worktreePath, stdoutPath, stderrPath)
		finished := time.Now().UTC()

		vr := domain.VerificationResult{
			Command:        cmd,
			StartedAt:      started,
			FinishedAt:     finished,
			DurationMillis: finished.Sub(started).Milliseconds(),
			StdoutPath:     stdoutPath,
			StderrPath:     stderrPath,
		}
		if runErr != nil {
			// Setup/IO failure: record exit -1 and surface the error if
			// no result was returned at all. Otherwise treat as a fail.
			vr.ExitCode = -1
			vr.Passed = false
			results = append(results, vr)
			if writeErr := layout.WriteJSONAtomic(resultPath, vr); writeErr != nil {
				return results, fmt.Errorf("orchestrator: write result.json: %w", writeErr)
			}
			if !opts.ContinueOnFailure {
				return results, fmt.Errorf("orchestrator: verification command %d failed: %w", idx, runErr)
			}
			continue
		}

		vr.ExitCode = exec.ExitCode
		vr.Passed = exec.ExitCode == 0
		results = append(results, vr)

		if err := layout.WriteJSONAtomic(resultPath, vr); err != nil {
			return results, fmt.Errorf("orchestrator: write result.json: %w", err)
		}

		if !vr.Passed && !opts.ContinueOnFailure {
			break
		}
	}
	return results, nil
}

// ResolveVerifyCommands returns the verification commands to run for
// this invocation. Explicit --verify flags win; otherwise the config
// defaults are used. An empty result is allowed — callers should mark
// the proof pack as "not verified" in that case.
func ResolveVerifyCommands(flagCommands []string, cfg config.AwoConfig) []string {
	clean := func(in []string) []string {
		var out []string
		for _, c := range in {
			if strings.TrimSpace(c) != "" {
				out = append(out, c)
			}
		}
		return out
	}
	if explicit := clean(flagCommands); len(explicit) > 0 {
		return explicit
	}
	return clean(cfg.DefaultVerifyCommands)
}

// AllPassed reports whether every result in res passed. An empty slice
// returns false: nothing ran, so nothing is verified.
func AllPassed(res []domain.VerificationResult) bool {
	if len(res) == 0 {
		return false
	}
	for _, r := range res {
		if !r.Passed {
			return false
		}
	}
	return true
}
