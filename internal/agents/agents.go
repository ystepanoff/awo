// Package agents defines the agent backend abstraction used by AWO's
// orchestrator. Agent invocations are isolated here so command construction
// stays configurable and testable.
//
// Agents are launched directly via execx.Run — never through a shell.
package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
)

// Request describes a single agent invocation.
type Request struct {
	WorktreeDir string
	Prompt      string
	StdoutPath  string
	StderrPath  string
	LiveOutput  bool
	RedactLogs  bool
}

// Agent runs a single coding agent against a worktree.
type Agent interface {
	Kind() domain.AgentKind
	Invoke(ctx context.Context, req Request) (*execx.CommandResult, error)
}

// New constructs an Agent from the relevant slice of AwoConfig.
func New(kind domain.AgentKind, cfg config.AwoConfig) (Agent, error) {
	switch kind {
	case domain.AgentClaude:
		if !cfg.Agents.Claude.Enabled {
			return nil, fmt.Errorf("agents: claude is disabled in config")
		}
		return &claudeAgent{cfg: cfg.Agents.Claude}, nil
	case domain.AgentCodex:
		if !cfg.Agents.Codex.Enabled {
			return nil, fmt.Errorf("agents: codex is disabled in config")
		}
		return &codexAgent{cfg: cfg.Agents.Codex}, nil
	default:
		return nil, fmt.Errorf("agents: unknown kind %q", kind)
	}
}

type claudeAgent struct{ cfg config.ClaudeConfig }

func (a *claudeAgent) Kind() domain.AgentKind { return domain.AgentClaude }
func (a *claudeAgent) Invoke(ctx context.Context, req Request) (*execx.CommandResult, error) {
	bin := a.cfg.Command
	if bin == "" {
		bin = "claude"
	}
	args := append([]string(nil), a.cfg.Args...)
	return execx.Run(ctx, execx.CommandSpec{
		Command:    bin,
		Args:       args,
		Cwd:        req.WorktreeDir,
		Timeout:    secondsToDuration(a.cfg.TimeoutSeconds),
		StdoutPath: req.StdoutPath,
		StderrPath: req.StderrPath,
		LiveOutput: req.LiveOutput,
		RedactLogs: req.RedactLogs,
	})
}

type codexAgent struct{ cfg config.CodexConfig }

func (a *codexAgent) Kind() domain.AgentKind { return domain.AgentCodex }
func (a *codexAgent) Invoke(ctx context.Context, req Request) (*execx.CommandResult, error) {
	bin := a.cfg.Command
	if bin == "" {
		bin = "codex"
	}
	args := append([]string(nil), a.cfg.Args...)
	if len(args) == 0 {
		args = []string{"exec"}
	}
	return execx.Run(ctx, execx.CommandSpec{
		Command:    bin,
		Args:       args,
		Cwd:        req.WorktreeDir,
		Timeout:    secondsToDuration(a.cfg.TimeoutSeconds),
		StdoutPath: req.StdoutPath,
		StderrPath: req.StderrPath,
		LiveOutput: req.LiveOutput,
		RedactLogs: req.RedactLogs,
	})
}

func secondsToDuration(s int) time.Duration {
	if s <= 0 {
		return 0
	}
	return time.Duration(s) * time.Second
}
