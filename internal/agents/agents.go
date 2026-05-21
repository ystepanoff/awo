// Package agents defines the agent backend abstraction used by AWO's
// orchestrator. Agent invocations are isolated here so command construction
// stays configurable and testable.
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
}

// Agent runs a single coding agent against a worktree.
type Agent interface {
	Kind() domain.AgentKind
	Invoke(ctx context.Context, req Request) (execx.Result, error)
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
func (a *claudeAgent) Invoke(ctx context.Context, req Request) (execx.Result, error) {
	bin := a.cfg.Command
	if bin == "" {
		bin = "claude"
	}
	args := append([]string(nil), a.cfg.Args...)
	cctx, cancel := withTimeout(ctx, a.cfg.TimeoutSeconds)
	defer cancel()
	return execx.Run(cctx, bin, args, execx.RunOptions{
		Dir:   req.WorktreeDir,
		Stdin: stringReader(req.Prompt),
	})
}

type codexAgent struct{ cfg config.CodexConfig }

func (a *codexAgent) Kind() domain.AgentKind { return domain.AgentCodex }
func (a *codexAgent) Invoke(ctx context.Context, req Request) (execx.Result, error) {
	bin := a.cfg.Command
	if bin == "" {
		bin = "codex"
	}
	args := append([]string(nil), a.cfg.Args...)
	if len(args) == 0 {
		args = []string{"exec"}
	}
	cctx, cancel := withTimeout(ctx, a.cfg.TimeoutSeconds)
	defer cancel()
	return execx.Run(cctx, bin, args, execx.RunOptions{
		Dir:   req.WorktreeDir,
		Stdin: stringReader(req.Prompt),
	})
}

func withTimeout(parent context.Context, secs int) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if secs <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(secs)*time.Second)
}
