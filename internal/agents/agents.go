// Package agents defines the agent backend abstraction used by AWO's
// orchestrator. Agent invocations are isolated here so command construction
// stays configurable and testable.
package agents

import (
	"context"
	"fmt"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/execx"
)

// Request describes a single agent invocation.
type Request struct {
	WorktreeDir string
	Prompt      string
	Timeout     string
}

// Agent runs a single coding agent against a worktree.
type Agent interface {
	Name() string
	Kind() config.AgentKind
	Invoke(ctx context.Context, req Request) (execx.Result, error)
}

// New constructs an Agent from a config entry. It does not invoke the
// agent — it only resolves which binary and arguments would be used.
func New(name string, cfg config.Agent) (Agent, error) {
	switch cfg.Kind {
	case config.AgentClaude:
		return &claudeAgent{name: name, cfg: cfg}, nil
	case config.AgentCodex:
		return &codexAgent{name: name, cfg: cfg}, nil
	default:
		return nil, fmt.Errorf("agents: unknown kind %q for %q", cfg.Kind, name)
	}
}

type claudeAgent struct {
	name string
	cfg  config.Agent
}

func (a *claudeAgent) Name() string            { return a.name }
func (a *claudeAgent) Kind() config.AgentKind  { return config.AgentClaude }
func (a *claudeAgent) Invoke(ctx context.Context, req Request) (execx.Result, error) {
	bin := a.cfg.Bin
	if bin == "" {
		bin = "claude"
	}
	args := append([]string{}, a.cfg.Args...)
	// MVP: pass prompt via stdin; concrete flags will firm up as the
	// orchestrator integration lands.
	return execx.Run(ctx, bin, args, execx.RunOptions{
		Dir:   req.WorktreeDir,
		Stdin: stringReader(req.Prompt),
	})
}

type codexAgent struct {
	name string
	cfg  config.Agent
}

func (a *codexAgent) Name() string           { return a.name }
func (a *codexAgent) Kind() config.AgentKind { return config.AgentCodex }
func (a *codexAgent) Invoke(ctx context.Context, req Request) (execx.Result, error) {
	bin := a.cfg.Bin
	if bin == "" {
		bin = "codex"
	}
	args := append([]string{}, a.cfg.Args...)
	if len(args) == 0 {
		args = []string{"exec"}
	}
	return execx.Run(ctx, bin, args, execx.RunOptions{
		Dir:   req.WorktreeDir,
		Stdin: stringReader(req.Prompt),
	})
}
