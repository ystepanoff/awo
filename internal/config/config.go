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
type ClaudeConfig struct {
	Enabled        bool     `json:"enabled"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

// CodexConfig configures the Codex backend.
type CodexConfig struct {
	Enabled        bool     `json:"enabled"`
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	Sandbox        string   `json:"sandbox"`
	ApprovalMode   string   `json:"approvalMode"`
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
		DefaultVerifyCommands: []string{"go test ./..."},
		Agents: AgentsConfig{
			Claude: ClaudeConfig{
				Enabled:        true,
				Command:        "claude",
				TimeoutSeconds: 600,
			},
			Codex: CodexConfig{
				Enabled:        true,
				Command:        "codex",
				Args:           []string{"exec"},
				TimeoutSeconds: 600,
				Sandbox:        "workspace-write",
				ApprovalMode:   "on-request",
			},
		},
		Safety: SafetyConfig{
			MaxChangedFiles:                      50,
			MaxIterations:                        1,
			ProtectedPaths:                       []string{".github/", "Makefile", "go.mod", "go.sum"},
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
	return nil
}
