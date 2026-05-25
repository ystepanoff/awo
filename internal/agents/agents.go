// Package agents owns the contract between AWO and the agent CLIs.
//
// Each adapter (Claude, Codex) takes a fully resolved AgentRunInput and
// returns a typed AgentRunResult. Adapters never invent file paths, never
// commit, never push, never trust agent claims about changed files —
// orchestration treats their output as advisory only.
//
// Command construction is split out into BuildClaudeCommand /
// BuildCodexCommand so the exact CLI flags can be tested in isolation
// and adjusted as the upstream tools evolve.
package agents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/awo-dev/awo/internal/config"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/execx"
)

// AgentRunInput is the resolved input passed to Agent.Run.
type AgentRunInput struct {
	RunID        string
	Task         string
	Role         domain.AgentRole
	Mode         domain.RunMode
	WorktreePath string
	BranchName   string
	ArtifactDir  string // per-agent artifact dir (e.g. <run>/agents/claude-writer)
	Config       config.AwoConfig
	Prompt       string
	ReadOnly     bool
	DryRun       bool
	LiveOutput   bool
}

// AgentRunResult is the structured outcome of one adapter invocation.
//
// ChangedFiles is intentionally left empty by adapters: the orchestrator
// derives it from `git status` afterwards rather than trusting agent
// self-reports. Adapters surface advisory data (parsed result block,
// review block, warnings) and the raw exec result.
type AgentRunResult struct {
	Agent          domain.AgentKind
	Role           domain.AgentRole
	Command        execx.CommandSpec
	StartedAt      time.Time
	FinishedAt     time.Time
	ExitCode       int
	TimedOut       bool
	StdoutPath     string
	StderrPath     string
	PromptPath     string
	DryRun         bool
	ParsedResult   *domain.ParsedAgentResult
	ParsedReview   *ParsedReviewResult
	Warnings       []string
}

// Agent is the contract every adapter implements.
type Agent interface {
	Kind() domain.AgentKind
	Run(ctx context.Context, input AgentRunInput) (*AgentRunResult, error)
}

// runner is the function that actually launches an external process.
// It exists so tests can inject a fake without spawning real binaries.
type runner func(ctx context.Context, spec execx.CommandSpec) (*execx.CommandResult, error)

// New constructs an adapter for kind from the global config.
func New(kind domain.AgentKind, _ config.AwoConfig) (Agent, error) {
	switch kind {
	case domain.AgentClaude:
		return &ClaudeCLIAdapter{run: execx.Run}, nil
	case domain.AgentCodex:
		return &CodexCLIAdapter{run: execx.Run}, nil
	default:
		return nil, fmt.Errorf("agents: unknown kind %q", kind)
	}
}

// ----- ClaudeCLIAdapter ---------------------------------------------------

// ClaudeCLIAdapter invokes the `claude` CLI with the rendered prompt
// piped on stdin.
type ClaudeCLIAdapter struct {
	run runner
}

// NewClaudeCLIAdapter returns a Claude adapter using execx.Run.
func NewClaudeCLIAdapter() *ClaudeCLIAdapter { return &ClaudeCLIAdapter{run: execx.Run} }

// Kind returns AgentClaude.
func (a *ClaudeCLIAdapter) Kind() domain.AgentKind { return domain.AgentClaude }

// Run renders the command, writes the prompt to disk, and invokes the
// CLI (or skips invocation in dry-run mode).
func (a *ClaudeCLIAdapter) Run(ctx context.Context, in AgentRunInput) (*AgentRunResult, error) {
	if err := validateInput(in); err != nil {
		return nil, err
	}
	spec := BuildClaudeCommand(in.Config.Agents.Claude, in.Prompt)
	return runAdapter(ctx, in, spec, a.kindRoleSlug(in.Role), a.run, a.Kind(), in.Role)
}

func (a *ClaudeCLIAdapter) kindRoleSlug(r domain.AgentRole) string { return "claude-" + string(r) }

// BuildClaudeCommand turns ClaudeConfig + prompt into a CommandSpec.
//
// The defaults are intentionally minimal: just the binary name, with
// any user-configured Args appended verbatim. The prompt is delivered
// on stdin via PromptPath in runAdapter — this adapter does not embed
// it as a CLI flag because the supported flags differ across versions.
func BuildClaudeCommand(cfg config.ClaudeConfig, prompt string) execx.CommandSpec {
	bin := strings.TrimSpace(cfg.Command)
	if bin == "" {
		bin = "claude"
	}
	args := append([]string(nil), cfg.Args...)
	return execx.CommandSpec{
		Command: bin,
		Args:    args,
		Timeout: secondsToDuration(cfg.TimeoutSeconds),
	}
}

// ----- CodexCLIAdapter ----------------------------------------------------

// CodexCLIAdapter invokes the `codex exec` CLI with the prompt piped on
// stdin and configurable sandbox / approval flags.
type CodexCLIAdapter struct {
	run runner
}

// NewCodexCLIAdapter returns a Codex adapter using execx.Run.
func NewCodexCLIAdapter() *CodexCLIAdapter { return &CodexCLIAdapter{run: execx.Run} }

// Kind returns AgentCodex.
func (a *CodexCLIAdapter) Kind() domain.AgentKind { return domain.AgentCodex }

// Run renders the command, writes the prompt to disk, and invokes the
// CLI (or skips invocation in dry-run mode).
func (a *CodexCLIAdapter) Run(ctx context.Context, in AgentRunInput) (*AgentRunResult, error) {
	if err := validateInput(in); err != nil {
		return nil, err
	}
	spec := BuildCodexCommand(in.Config.Agents.Codex, in.Prompt)
	return runAdapter(ctx, in, spec, a.kindRoleSlug(in.Role), a.run, a.Kind(), in.Role)
}

func (a *CodexCLIAdapter) kindRoleSlug(r domain.AgentRole) string { return "codex-" + string(r) }

// BuildCodexCommand turns CodexConfig + prompt into a CommandSpec.
//
// The default base command is "codex exec" — non-interactive execution.
// User-supplied Args replace the default base when present, so a config
// like {"args": ["--profile", "ci", "exec"]} passes through verbatim.
//
// Sandbox and ApprovalMode are appended as --sandbox / --approval-mode
// flags. AWO never sets dangerous bypasses on the user's behalf; if the
// user wants a permissive mode they must set it explicitly in config.
func BuildCodexCommand(cfg config.CodexConfig, prompt string) execx.CommandSpec {
	bin := strings.TrimSpace(cfg.Command)
	if bin == "" {
		bin = "codex"
	}

	var args []string
	if len(cfg.Args) > 0 {
		args = append(args, cfg.Args...)
	} else {
		args = append(args, "exec")
	}
	if s := strings.TrimSpace(cfg.Sandbox); s != "" {
		args = append(args, "--sandbox", s)
	}
	if m := strings.TrimSpace(cfg.ApprovalMode); m != "" {
		args = append(args, "--approval-mode", m)
	}
	return execx.CommandSpec{
		Command: bin,
		Args:    args,
		Timeout: secondsToDuration(cfg.TimeoutSeconds),
	}
}

// ----- shared adapter machinery -------------------------------------------

func validateInput(in AgentRunInput) error {
	switch {
	case strings.TrimSpace(in.RunID) == "":
		return errors.New("agents: AgentRunInput.RunID required")
	case strings.TrimSpace(in.Task) == "":
		return errors.New("agents: AgentRunInput.Task required")
	case strings.TrimSpace(in.WorktreePath) == "":
		return errors.New("agents: AgentRunInput.WorktreePath required")
	case strings.TrimSpace(in.ArtifactDir) == "":
		return errors.New("agents: AgentRunInput.ArtifactDir required")
	case strings.TrimSpace(in.Prompt) == "":
		return errors.New("agents: AgentRunInput.Prompt required")
	}
	if err := in.Role.Validate(); err != nil {
		return fmt.Errorf("agents: %w", err)
	}
	return nil
}

// runAdapter is the shared body of every adapter Run: it ensures the
// artifact dir, writes prompt + command record, runs the CLI (or skips
// in dry-run), and parses any AWO_RESULT_JSON / AWO_REVIEW_JSON block.
func runAdapter(
	ctx context.Context,
	in AgentRunInput,
	spec execx.CommandSpec,
	_ string,
	run runner,
	kind domain.AgentKind,
	role domain.AgentRole,
) (*AgentRunResult, error) {
	if err := os.MkdirAll(in.ArtifactDir, 0o755); err != nil {
		return nil, fmt.Errorf("agents: mkdir artifact dir: %w", err)
	}

	stdoutPath := filepath.Join(in.ArtifactDir, "stdout.log")
	stderrPath := filepath.Join(in.ArtifactDir, "stderr.log")
	promptPath := filepath.Join(in.ArtifactDir, "prompt.md")
	commandPath := filepath.Join(in.ArtifactDir, "command.txt")

	if err := os.WriteFile(promptPath, []byte(in.Prompt), 0o644); err != nil {
		return nil, fmt.Errorf("agents: write prompt: %w", err)
	}

	spec.Cwd = in.WorktreePath
	spec.StdoutPath = stdoutPath
	spec.StderrPath = stderrPath
	spec.LiveOutput = in.LiveOutput
	spec.RedactLogs = in.Config.Safety.RedactLogs

	if err := os.WriteFile(commandPath, []byte(formatCommand(spec)), 0o644); err != nil {
		return nil, fmt.Errorf("agents: write command record: %w", err)
	}

	res := &AgentRunResult{
		Agent:      kind,
		Role:       role,
		Command:    spec,
		StartedAt:  time.Now().UTC(),
		StdoutPath: stdoutPath,
		StderrPath: stderrPath,
		PromptPath: promptPath,
		DryRun:     in.DryRun,
	}

	if in.DryRun {
		// Touch the log files so downstream readers always find them.
		_ = os.WriteFile(stdoutPath, []byte("[dry-run] no execution\n"), 0o644)
		_ = os.WriteFile(stderrPath, nil, 0o644)
		res.FinishedAt = time.Now().UTC()
		return res, nil
	}

	if run == nil {
		return nil, errors.New("agents: no runner configured")
	}
	cr, err := run(ctx, spec)
	res.FinishedAt = time.Now().UTC()
	if err != nil {
		return res, err
	}
	res.ExitCode = cr.ExitCode
	res.TimedOut = cr.TimedOut

	stdoutBytes, _ := os.ReadFile(stdoutPath)
	stdout := string(stdoutBytes)
	switch role {
	case domain.RoleReviewer:
		if review, perr := ParseReviewResult(stdout); review != nil {
			res.ParsedReview = review
		} else if perr != nil {
			res.Warnings = append(res.Warnings, perr.Error())
		}
	default:
		if parsed, perr := ParseAgentResult(stdout); parsed != nil {
			res.ParsedResult = parsed
		} else if perr != nil {
			res.Warnings = append(res.Warnings, perr.Error())
		}
	}
	return res, nil
}

func formatCommand(spec execx.CommandSpec) string {
	var b strings.Builder
	b.WriteString(spec.Command)
	for _, a := range spec.Args {
		b.WriteString(" ")
		b.WriteString(a)
	}
	if spec.Cwd != "" {
		b.WriteString("\n# cwd: ")
		b.WriteString(spec.Cwd)
	}
	if spec.Timeout > 0 {
		fmt.Fprintf(&b, "\n# timeout: %s", spec.Timeout)
	}
	b.WriteString("\n")
	return b.String()
}

func secondsToDuration(s int) time.Duration {
	if s <= 0 {
		return 0
	}
	return time.Duration(s) * time.Second
}
