// Package config defines AwoConfig — AWO's user-facing configuration —
// along with its defaults, JSON loading, and validation.
//
// Loading layers JSON onto Default(): any field a user omits keeps its
// default value, so partial configs work as natural overrides.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/awo-dev/awo/internal/domain"
)

// Filename is the conventional config filename in a project root.
const Filename = "awo.config.json"

// AwoConfig is the AWO configuration schema written to disk as JSON.
type AwoConfig struct {
	WorktreeBaseDir       string       `json:"worktreeBaseDir"`
	BranchPrefix          string       `json:"branchPrefix"`
	ArtifactDir           string       `json:"artifactDir"`
	DefaultVerifyCommands []string     `json:"defaultVerifyCommands"`
	Agents                AgentsConfig `json:"agents"`
	Safety                SafetyConfig `json:"safety"`
}

// AgentsConfig groups per-agent backend configuration.
type AgentsConfig struct {
	Claude ClaudeConfig `json:"claude"`
	Codex  CodexConfig  `json:"codex"`
}

// ClaudeConfig configures the Claude Code backend.
//
// AWO supports per-role argument lists so the writer can have edit
// permissions while the reviewer is locked into a read-only mode. The
// legacy `Args` field is kept for backward compatibility: when a role's
// specific args list is empty, RoleArgs falls back to `Args`, then to
// the built-in safe defaults for that role.
type ClaudeConfig struct {
	Enabled bool   `json:"enabled"`
	Command string `json:"command"`
	// Args is the legacy single args list. New configs should prefer
	// the per-role lists below.
	Args           []string `json:"args,omitempty"`
	WriterArgs     []string `json:"writerArgs,omitempty"`
	ReviewerArgs   []string `json:"reviewerArgs,omitempty"`
	CompetitorArgs []string `json:"competitorArgs,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

// CodexConfig configures the Codex backend.
//
// Per-role args follow the same pattern as ClaudeConfig. The legacy
// Sandbox / ApprovalMode fields are still honored for backward
// compatibility but are only consulted when the resolver falls back to
// the legacy Args path; new safe defaults bake their sandbox and
// approval flags directly into the per-role args.
type CodexConfig struct {
	Enabled        bool     `json:"enabled"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	WriterArgs     []string `json:"writerArgs,omitempty"`
	ReviewerArgs   []string `json:"reviewerArgs,omitempty"`
	CompetitorArgs []string `json:"competitorArgs,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	Sandbox        string   `json:"sandbox,omitempty"`
	ApprovalMode   string   `json:"approvalMode,omitempty"`
}

// SafetyConfig holds AWO's safety knobs.
type SafetyConfig struct {
	MaxChangedFiles                      int      `json:"maxChangedFiles"`
	MaxIterations                        int      `json:"maxIterations"`
	ProtectedPaths                       []string `json:"protectedPaths"`
	RequireConfirmationForProtectedPaths bool     `json:"requireConfirmationForProtectedPaths"`
	RedactLogs                           bool     `json:"redactLogs"`
}

// Default returns the built-in default configuration.
func Default() AwoConfig {
	return AwoConfig{
		WorktreeBaseDir:       ".awo/worktrees",
		BranchPrefix:          "awo",
		ArtifactDir:           ".awo/runs",
		DefaultVerifyCommands: []string{},
		Agents: AgentsConfig{
			Claude: ClaudeConfig{
				Enabled:        true,
				Command:        "claude",
				TimeoutSeconds: 1800,
			},
			Codex: CodexConfig{
				Enabled:        true,
				Command:        "codex",
				TimeoutSeconds: 1800,
			},
		},
		Safety: SafetyConfig{
			MaxChangedFiles: 50,
			MaxIterations:   1,
			ProtectedPaths: []string{
				"auth/**",
				"payments/**",
				"migrations/**",
				"infra/**",
				".github/workflows/**",
				"**/.env*",
				"**/*secret*",
				"**/*credential*",
				"**/*permission*",
			},
			RequireConfirmationForProtectedPaths: true,
			RedactLogs:                           true,
		},
	}
}

// DefaultJSON returns the default config rendered as pretty JSON.
func DefaultJSON() string {
	b, err := json.MarshalIndent(Default(), "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

// Parse parses raw JSON layered on top of Default(). Missing fields keep
// their default values; provided fields override them.
func Parse(b []byte) (AwoConfig, error) {
	if len(strings.TrimSpace(string(b))) == 0 {
		return AwoConfig{}, errors.New("parse config: empty input")
	}
	cfg := Default()
	if err := json.Unmarshal(b, &cfg); err != nil {
		return AwoConfig{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return AwoConfig{}, err
	}
	return cfg, nil
}

// Load reads and validates a config from disk.
func Load(path string) (AwoConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return AwoConfig{}, err
	}
	return Parse(b)
}

// LoadOrDefault returns a parsed config, or Default() when the file is
// absent. The second return value is the source — either the path or the
// literal "default".
func LoadOrDefault(path string) (AwoConfig, string, error) {
	cfg, err := Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), "default", nil
		}
		return AwoConfig{}, "", err
	}
	return cfg, path, nil
}

// Validate enforces config invariants.
func (c AwoConfig) Validate() error {
	if strings.TrimSpace(c.WorktreeBaseDir) == "" {
		return errors.New("config: worktreeBaseDir must not be empty")
	}
	if strings.TrimSpace(c.ArtifactDir) == "" {
		return errors.New("config: artifactDir must not be empty")
	}
	if c.BranchPrefix == "" {
		return errors.New("config: branchPrefix must not be empty")
	}
	if !strings.HasPrefix(c.BranchPrefix, "awo") {
		return fmt.Errorf("config: branchPrefix must start with %q (got %q)", "awo", c.BranchPrefix)
	}
	if strings.ContainsAny(c.BranchPrefix, " \t\n\r") {
		return fmt.Errorf("config: branchPrefix must not contain whitespace (got %q)", c.BranchPrefix)
	}
	for i, cmd := range c.DefaultVerifyCommands {
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("config: defaultVerifyCommands[%d] must not be empty", i)
		}
	}
	if c.Safety.MaxChangedFiles < 0 {
		return fmt.Errorf("config: safety.maxChangedFiles must be >= 0 (got %d)", c.Safety.MaxChangedFiles)
	}
	if c.Safety.MaxIterations < 0 {
		return fmt.Errorf("config: safety.maxIterations must be >= 0 (got %d)", c.Safety.MaxIterations)
	}
	if c.Agents.Claude.TimeoutSeconds < 0 {
		return fmt.Errorf("config: agents.claude.timeoutSeconds must be >= 0 (got %d)", c.Agents.Claude.TimeoutSeconds)
	}
	if c.Agents.Codex.TimeoutSeconds < 0 {
		return fmt.Errorf("config: agents.codex.timeoutSeconds must be >= 0 (got %d)", c.Agents.Codex.TimeoutSeconds)
	}
	for i, p := range c.Safety.ProtectedPaths {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("config: safety.protectedPaths[%d] must not be empty", i)
		}
	}
	if err := validateAgentArgs("agents.claude", c.Agents.Claude.Args, c.Agents.Claude.WriterArgs, c.Agents.Claude.ReviewerArgs, c.Agents.Claude.CompetitorArgs); err != nil {
		return err
	}
	if err := validateAgentArgs("agents.codex", c.Agents.Codex.Args, c.Agents.Codex.WriterArgs, c.Agents.Codex.ReviewerArgs, c.Agents.Codex.CompetitorArgs); err != nil {
		return err
	}
	return nil
}

// dangerousArgTokens lists case-insensitive substrings that AWO refuses
// to accept anywhere in an agent args list, regardless of which role the
// args target. The list is intentionally narrow — these are the explicit
// "bypass everything" knobs both CLIs ship today.
var dangerousArgTokens = []string{
	"bypasspermissions",
	"--dangerously-skip-permissions",
	"danger-full-access",
	"dangerously-bypass",
}

func validateAgentArgs(prefix string, lists ...[]string) error {
	for _, list := range lists {
		for i, a := range list {
			low := strings.ToLower(a)
			for _, bad := range dangerousArgTokens {
				if strings.Contains(low, bad) {
					return fmt.Errorf("config: %s contains dangerous arg %q at position %d; AWO refuses to use unrestricted permission bypasses", prefix, a, i)
				}
			}
		}
	}
	return nil
}

// ----- per-role args resolution ------------------------------------------

// RoleArgs returns the resolved args list for the given role.
//
// Resolution order:
//  1. The role-specific args list when non-empty.
//  2. The legacy Args list when non-empty (back-compat for configs
//     written before the per-role split).
//  3. The safe built-in default for that role.
//
// The returned slice is freshly allocated so callers may mutate it
// without affecting the config.
func (c ClaudeConfig) RoleArgs(role domain.AgentRole) []string {
	switch role {
	case domain.RoleWriter:
		if len(c.WriterArgs) > 0 {
			return append([]string(nil), c.WriterArgs...)
		}
	case domain.RoleReviewer:
		if len(c.ReviewerArgs) > 0 {
			return append([]string(nil), c.ReviewerArgs...)
		}
	case domain.RoleCompetitor:
		if len(c.CompetitorArgs) > 0 {
			return append([]string(nil), c.CompetitorArgs...)
		}
	}
	if len(c.Args) > 0 {
		return append([]string(nil), c.Args...)
	}
	return defaultClaudeRoleArgs(role)
}

// defaultClaudeRoleArgs returns the built-in safe default Claude args
// for a given role. The defaults are tuned to produce useful work
// non-interactively while keeping AWO's worktree as the only writable
// surface:
//
//   - writer/competitor: -p --permission-mode acceptEdits
//     non-interactive print mode + auto-accept edits inside cwd.
//   - reviewer:          -p --permission-mode plan
//     non-interactive print mode + plan-only (no Edit/Write/Bash).
//
// None of these flags grant access outside cwd or bypass bash; that
// boundary is enforced by AWO's worktree isolation.
func defaultClaudeRoleArgs(role domain.AgentRole) []string {
	switch role {
	case domain.RoleReviewer:
		return []string{"-p", "--permission-mode", "plan"}
	default: // writer, competitor
		return []string{"-p", "--permission-mode", "acceptEdits"}
	}
}

// RoleArgs returns the resolved args list for the given role.
//
// The resolution mirrors ClaudeConfig.RoleArgs. When falling back to
// the legacy Args path, the legacy Sandbox / ApprovalMode fields are
// appended as --sandbox / --approval-mode flags so old configs keep
// working. The new per-role defaults bake their sandbox and approval
// flags directly into the args.
func (c CodexConfig) RoleArgs(role domain.AgentRole) []string {
	switch role {
	case domain.RoleWriter:
		if len(c.WriterArgs) > 0 {
			return append([]string(nil), c.WriterArgs...)
		}
	case domain.RoleReviewer:
		if len(c.ReviewerArgs) > 0 {
			return append([]string(nil), c.ReviewerArgs...)
		}
	case domain.RoleCompetitor:
		if len(c.CompetitorArgs) > 0 {
			return append([]string(nil), c.CompetitorArgs...)
		}
	}
	if len(c.Args) > 0 {
		out := append([]string(nil), c.Args...)
		if s := strings.TrimSpace(c.Sandbox); s != "" {
			out = append(out, "--sandbox", s)
		}
		if m := strings.TrimSpace(c.ApprovalMode); m != "" {
			out = append(out, "--approval-mode", m)
		}
		return out
	}
	return defaultCodexRoleArgs(role)
}

// defaultCodexRoleArgs returns the built-in safe default Codex args
// for a given role:
//
//   - writer/competitor: exec --sandbox workspace-write --ask-for-approval never
//   - reviewer:          exec --sandbox read-only      --ask-for-approval never
//
// "ask-for-approval never" makes the CLI fail closed instead of
// hanging on an interactive prompt. The reviewer uses read-only so an
// agent that ignores its prompt and tries to write still cannot.
func defaultCodexRoleArgs(role domain.AgentRole) []string {
	switch role {
	case domain.RoleReviewer:
		return []string{"exec", "--sandbox", "read-only", "--ask-for-approval", "never"}
	default: // writer, competitor
		return []string{"exec", "--sandbox", "workspace-write", "--ask-for-approval", "never"}
	}
}
