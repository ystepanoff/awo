package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Mode is one of the supported orchestration modes.
type Mode string

const (
	ModeSingle         Mode = "single"
	ModeWriterReviewer Mode = "writer-reviewer"
	ModeCompetitive    Mode = "competitive"
)

// AgentKind names a supported agent backend.
type AgentKind string

const (
	AgentClaude AgentKind = "claude"
	AgentCodex  AgentKind = "codex"
)

// Config is the AWO config file schema.
type Config struct {
	Version       int               `json:"version"`
	DefaultMode   Mode              `json:"defaultMode"`
	Agents        map[string]Agent  `json:"agents"`
	Verification  VerificationCfg   `json:"verification"`
	Safety        SafetyCfg         `json:"safety"`
	Worktrees     WorktreesCfg      `json:"worktrees"`
}

type Agent struct {
	Kind    AgentKind `json:"kind"`
	Bin     string    `json:"bin,omitempty"`
	Args    []string  `json:"args,omitempty"`
	Timeout string    `json:"timeout,omitempty"`
}

type VerificationCfg struct {
	Commands []string `json:"commands"`
	Timeout  string   `json:"timeout,omitempty"`
}

type SafetyCfg struct {
	ProtectedPaths []string `json:"protectedPaths"`
	RedactPatterns []string `json:"redactPatterns"`
}

type WorktreesCfg struct {
	BranchPrefix string `json:"branchPrefix"`
	Root         string `json:"root"`
}

// Default returns the built-in default configuration.
func Default() Config {
	return Config{
		Version:     1,
		DefaultMode: ModeSingle,
		Agents: map[string]Agent{
			"claude": {Kind: AgentClaude, Bin: "claude", Timeout: "10m"},
			"codex":  {Kind: AgentCodex, Bin: "codex", Args: []string{"exec"}, Timeout: "10m"},
		},
		Verification: VerificationCfg{
			Commands: []string{"go test ./..."},
			Timeout:  "15m",
		},
		Safety: SafetyCfg{
			ProtectedPaths: []string{".github/", "Makefile", "go.mod", "go.sum"},
			RedactPatterns: []string{`(?i)api[_-]?key`, `(?i)secret`, `(?i)token`, `(?i)password`},
		},
		Worktrees: WorktreesCfg{
			BranchPrefix: "awo/",
			Root:         ".awo/worktrees",
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

// Load reads and validates a config from disk.
func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	return Parse(b)
}

// LoadOrDefault tries to read the config file. If it does not exist, the
// built-in default is returned and the source is reported as "default".
func LoadOrDefault(path string) (Config, string, error) {
	cfg, err := Load(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), "default", nil
		}
		return Config{}, "", err
	}
	return cfg, path, nil
}

// Parse validates raw config bytes.
func Parse(b []byte) (Config, error) {
	var cfg Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate enforces the config invariants.
func (c Config) Validate() error {
	if c.Version <= 0 {
		return errors.New("config: version must be >= 1")
	}
	switch c.DefaultMode {
	case ModeSingle, ModeWriterReviewer, ModeCompetitive:
	case "":
		return errors.New("config: defaultMode is required")
	default:
		return fmt.Errorf("config: unknown defaultMode %q", c.DefaultMode)
	}
	if len(c.Agents) == 0 {
		return errors.New("config: at least one agent must be defined")
	}
	for name, a := range c.Agents {
		if name == "" {
			return errors.New("config: agent name must not be empty")
		}
		switch a.Kind {
		case AgentClaude, AgentCodex:
		default:
			return fmt.Errorf("config: agent %q has unknown kind %q", name, a.Kind)
		}
	}
	if c.Worktrees.BranchPrefix == "" {
		return errors.New("config: worktrees.branchPrefix must not be empty")
	}
	if !strings.HasPrefix(c.Worktrees.BranchPrefix, "awo/") {
		return fmt.Errorf("config: worktrees.branchPrefix must start with %q", "awo/")
	}
	if c.Worktrees.Root == "" {
		return errors.New("config: worktrees.root must not be empty")
	}
	return nil
}
