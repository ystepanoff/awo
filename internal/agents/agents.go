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

// defaultAgentTimeout is applied to any agent invocation whose
// configured TimeoutSeconds is <= 0. Agents that hang forever in
// non-interactive mode are AWO's most painful failure mode, so we
// always pin a ceiling — see the spec.
const defaultAgentTimeout = 1800 * time.Second

// PromptPlaceholder is the substring AWO replaces with the rendered
// prompt when it appears in a configured args list. When no
// placeholder appears, the prompt is appended as the final argument
// instead.
const PromptPlaceholder = "{{prompt}}"

// FailureKind classifies why a run did not produce a clean result.
// "" means no failure was recorded.
const (
	FailureProcessFailed      = "process_failed"
	FailureTimeout            = "timeout"
	FailurePermissionRequired = "permission_required"
	FailureParseWarning       = "parse_warning"
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
	Agent        domain.AgentKind
	Role         domain.AgentRole
	Command      execx.CommandSpec
	StartedAt    time.Time
	FinishedAt   time.Time
	ExitCode     int
	TimedOut     bool
	StdoutPath   string
	StderrPath   string
	PromptPath   string
	DryRun       bool
	ParsedResult *domain.ParsedAgentResult
	ParsedReview *ParsedReviewResult
	Warnings     []string
	// FailureKind classifies the failure: "" (none), "process_failed",
	// "timeout", "permission_required", "parse_warning".
	FailureKind string
	// FailureReason is a short human-readable explanation associated
	// with FailureKind.
	FailureReason string
	// PermissionFailure is set when FailureKind == "permission_required".
	PermissionFailure *PermissionFailure
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
// piped on stdin (or substituted into the args list if the user wrote
// {{prompt}} explicitly).
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
	stdoutPath := filepath.Join(in.ArtifactDir, "stdout.log")
	stderrPath := filepath.Join(in.ArtifactDir, "stderr.log")
	spec := BuildClaudeCommand(in.Config.Agents.Claude, in.Role, in.Prompt, in.WorktreePath, stdoutPath, stderrPath)
	return runAdapter(ctx, in, spec, a.run, a.Kind(), in.Role)
}

// BuildClaudeCommand turns ClaudeConfig + role + prompt into a CommandSpec.
//
// The args list comes from cfg.RoleArgs(role) — see config.ClaudeConfig
// docs for the resolution rules. Inside that list:
//
//   - any element exactly equal to "{{prompt}}" is replaced by the
//     rendered prompt;
//   - if no element contains "{{prompt}}", the prompt is appended as
//     the final argument when args end with "-p" (CLI expected to
//     accept it as a positional argument), otherwise the prompt is
//     piped on stdin.
//
// The worktreePath / stdoutPath / stderrPath parameters are accepted
// for parity with BuildCodexCommand even though Claude does not need
// them in its argv today; future role-specific args may want to
// embed them via {{worktree}} etc., and routing them through the
// signature now keeps adapter call sites stable.
func BuildClaudeCommand(
	cfg config.ClaudeConfig,
	role domain.AgentRole,
	prompt string,
	worktreePath string,
	stdoutPath string,
	stderrPath string,
) execx.CommandSpec {
	_ = worktreePath
	_ = stdoutPath
	_ = stderrPath

	bin := strings.TrimSpace(cfg.Command)
	if bin == "" {
		bin = "claude"
	}
	args := cfg.RoleArgs(role)
	args, stdin := embedPrompt(args, prompt)

	return execx.CommandSpec{
		Command: bin,
		Args:    args,
		Stdin:   stdin,
		Timeout: resolveTimeout(cfg.TimeoutSeconds),
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
	stdoutPath := filepath.Join(in.ArtifactDir, "stdout.log")
	stderrPath := filepath.Join(in.ArtifactDir, "stderr.log")
	spec := BuildCodexCommand(in.Config.Agents.Codex, in.Role, in.Prompt, in.WorktreePath, stdoutPath, stderrPath)
	return runAdapter(ctx, in, spec, a.run, a.Kind(), in.Role)
}

// BuildCodexCommand turns CodexConfig + role + prompt into a CommandSpec.
//
// The args list comes from cfg.RoleArgs(role) — see
// config.CodexConfig docs. {{prompt}} substitution and prompt-on-stdin
// fallback are handled exactly as for Claude.
//
// AWO never adds dangerous bypasses on the user's behalf. If the user
// wants a permissive sandbox they must say so explicitly in their
// per-role args.
func BuildCodexCommand(
	cfg config.CodexConfig,
	role domain.AgentRole,
	prompt string,
	worktreePath string,
	stdoutPath string,
	stderrPath string,
) execx.CommandSpec {
	_ = worktreePath
	_ = stdoutPath
	_ = stderrPath

	bin := strings.TrimSpace(cfg.Command)
	if bin == "" {
		bin = "codex"
	}
	args := cfg.RoleArgs(role)
	args, stdin := embedPrompt(args, prompt)

	return execx.CommandSpec{
		Command: bin,
		Args:    args,
		Stdin:   stdin,
		Timeout: resolveTimeout(cfg.TimeoutSeconds),
	}
}

// embedPrompt resolves the {{prompt}} placeholder in args. If any
// element contains the placeholder, every occurrence is replaced
// with prompt and stdin is left empty. Otherwise the original args
// are returned unchanged and the prompt is delivered on stdin so the
// CLI's non-interactive mode (-p, --print, exec, ...) reads it.
func embedPrompt(args []string, prompt string) ([]string, []byte) {
	hasPlaceholder := false
	for _, a := range args {
		if strings.Contains(a, PromptPlaceholder) {
			hasPlaceholder = true
			break
		}
	}
	if !hasPlaceholder {
		return args, []byte(prompt)
	}
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(a, PromptPlaceholder, prompt)
	}
	return out, nil
}

// resolveTimeout normalizes user-supplied timeout seconds. Zero,
// negative, or unset values fall back to the AWO default rather than
// "no timeout"; the latter would let a hung CLI block forever in
// non-interactive mode.
func resolveTimeout(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultAgentTimeout
	}
	return time.Duration(seconds) * time.Second
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
	stderrBytes, _ := os.ReadFile(stderrPath)
	stdout := string(stdoutBytes)
	stderr := string(stderrBytes)

	switch role {
	case domain.RoleReviewer:
		if review, perr := ParseReviewResult(stdout); review != nil {
			res.ParsedReview = review
		} else if perr != nil {
			res.Warnings = append(res.Warnings, perr.Error())
			if res.FailureKind == "" {
				res.FailureKind = FailureParseWarning
				res.FailureReason = perr.Error()
			}
		}
	default:
		if parsed, perr := ParseAgentResult(stdout); parsed != nil {
			res.ParsedResult = parsed
		} else if perr != nil {
			res.Warnings = append(res.Warnings, perr.Error())
			if res.FailureKind == "" {
				res.FailureKind = FailureParseWarning
				res.FailureReason = perr.Error()
			}
		}
	}

	classifyFailure(res, stdout, stderr)
	return res, nil
}

// classifyFailure populates FailureKind / FailureReason /
// PermissionFailure based on the run's exit code, timeout flag, and
// log content. The classification ladder, strongest first:
//
//  1. timeout — the runner reported a context-deadline kill.
//  2. permission_required — the logs match a known interactive-approval
//     pattern. This applies even on exit code 0 because some CLIs
//     print "permission required" and then exit cleanly without
//     making the requested change.
//  3. process_failed — non-zero exit, no other classification.
//
// A previously-set FailureKind from the parser path (parse_warning)
// is overwritten by anything stronger above, since a permission
// failure is the more actionable problem.
func classifyFailure(res *AgentRunResult, stdout, stderr string) {
	if res.TimedOut {
		res.FailureKind = FailureTimeout
		res.FailureReason = fmt.Sprintf(
			"agent timed out after %s; AWO defaults to %s when no timeout is configured",
			res.Command.Timeout, defaultAgentTimeout,
		)
		return
	}
	if pf := DetectPermissionFailure(stdout, stderr); pf != nil {
		res.FailureKind = FailurePermissionRequired
		res.FailureReason = fmt.Sprintf(
			"agent appears to have hit an interactive permission/approval prompt (%s: %q)",
			pf.Source, pf.Sample,
		)
		res.PermissionFailure = pf
		return
	}
	if res.ExitCode != 0 && res.FailureKind != FailureParseWarning {
		res.FailureKind = FailureProcessFailed
		res.FailureReason = fmt.Sprintf("agent exited with code %d", res.ExitCode)
		return
	}
	// Even with exit 0, if the parser flagged a warning we keep that
	// classification (set above). Otherwise leave FailureKind == "".
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
	if len(spec.Stdin) > 0 {
		b.WriteString("\n# stdin: prompt piped from prompt.md (non-interactive)")
	}
	b.WriteString("\n")
	return b.String()
}
