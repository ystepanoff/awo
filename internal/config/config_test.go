package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if c.WorktreeBaseDir != ".awo/worktrees" {
		t.Errorf("default WorktreeBaseDir=%q", c.WorktreeBaseDir)
	}
	if c.BranchPrefix != "awo" {
		t.Errorf("default BranchPrefix=%q", c.BranchPrefix)
	}
	if c.ArtifactDir != ".awo/runs" {
		t.Errorf("default ArtifactDir=%q", c.ArtifactDir)
	}
	if !c.Agents.Claude.Enabled || c.Agents.Claude.Command != "claude" {
		t.Errorf("claude defaults wrong: %+v", c.Agents.Claude)
	}
	if !c.Agents.Codex.Enabled || c.Agents.Codex.Command != "codex" {
		t.Errorf("codex defaults wrong: %+v", c.Agents.Codex)
	}
	if c.Agents.Codex.Sandbox != "workspace-write" {
		t.Errorf("codex sandbox default wrong: %q", c.Agents.Codex.Sandbox)
	}
	if !c.Safety.RedactLogs {
		t.Error("RedactLogs default should be true")
	}
	if c.Safety.MaxChangedFiles != 50 || c.Safety.MaxIterations != 1 {
		t.Errorf("safety limits wrong: %+v", c.Safety)
	}
}

func TestDefaultJSONRoundTrip(t *testing.T) {
	cfg, err := Parse([]byte(DefaultJSON()))
	if err != nil {
		t.Fatalf("parse default json: %v", err)
	}
	if cfg.BranchPrefix != "awo" {
		t.Errorf("BranchPrefix=%q", cfg.BranchPrefix)
	}
}

func TestLoadOrDefaultMissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, source, err := LoadOrDefault(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if source != "default" {
		t.Fatalf("source=%q want default", source)
	}
	if cfg.BranchPrefix != Default().BranchPrefix {
		t.Fatalf("did not return default config")
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, Filename)
	if err := os.WriteFile(p, []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrDefault(p); err == nil {
		t.Fatal("expected parse error")
	} else if !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseEmptyInputErrors(t *testing.T) {
	if _, err := Parse([]byte("   ")); err == nil {
		t.Fatal("expected error for empty input")
	}
}

func TestParseAppliesPartialOverride(t *testing.T) {
	override := []byte(`{
		"branchPrefix": "awo-mvp",
		"safety": {"maxChangedFiles": 200, "maxIterations": 3, "redactLogs": false},
		"agents": {"codex": {"sandbox": "read-only", "approvalMode": "always"}}
	}`)
	cfg, err := Parse(override)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.BranchPrefix != "awo-mvp" {
		t.Errorf("BranchPrefix=%q", cfg.BranchPrefix)
	}
	if cfg.Safety.MaxChangedFiles != 200 || cfg.Safety.MaxIterations != 3 || cfg.Safety.RedactLogs {
		t.Errorf("safety overrides wrong: %+v", cfg.Safety)
	}
	if cfg.Agents.Codex.Sandbox != "read-only" || cfg.Agents.Codex.ApprovalMode != "always" {
		t.Errorf("codex overrides wrong: %+v", cfg.Agents.Codex)
	}
	// Untouched fields keep defaults.
	if cfg.Agents.Codex.Command != "codex" || cfg.Agents.Claude.Command != "claude" {
		t.Errorf("commands mutated by partial override: %+v %+v",
			cfg.Agents.Codex, cfg.Agents.Claude)
	}
	if cfg.WorktreeBaseDir != ".awo/worktrees" {
		t.Errorf("WorktreeBaseDir mutated: %q", cfg.WorktreeBaseDir)
	}
	// Slice not mentioned should keep default (which is empty by design,
	// since we don't want to assume a build system). Just confirm the
	// field is non-nil so callers can append safely.
	if cfg.DefaultVerifyCommands == nil {
		t.Error("DefaultVerifyCommands should be non-nil even when default is empty")
	}
}

func TestParseAppliesFullOverride(t *testing.T) {
	full := Default()
	full.BranchPrefix = "awo/exp"
	full.DefaultVerifyCommands = []string{"make test"}
	full.Safety.RedactLogs = false
	b, _ := json.Marshal(full)
	cfg, err := Parse(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if cfg.BranchPrefix != "awo/exp" {
		t.Errorf("BranchPrefix=%q", cfg.BranchPrefix)
	}
	if len(cfg.DefaultVerifyCommands) != 1 || cfg.DefaultVerifyCommands[0] != "make test" {
		t.Errorf("verify cmds=%v", cfg.DefaultVerifyCommands)
	}
}

func TestValidateRejectsEmptyBranchPrefix(t *testing.T) {
	c := Default()
	c.BranchPrefix = ""
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty branchPrefix")
	}
}

func TestValidateRejectsBranchPrefixNotStartingWithAwo(t *testing.T) {
	c := Default()
	c.BranchPrefix = "feature"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for non-awo branchPrefix")
	}
}

func TestValidateRejectsBranchPrefixWhitespace(t *testing.T) {
	c := Default()
	c.BranchPrefix = "awo run"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for whitespace in branchPrefix")
	}
}

func TestValidateRejectsEmptyVerifyCommand(t *testing.T) {
	c := Default()
	c.DefaultVerifyCommands = []string{"go test ./...", "  "}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty verify command")
	}
}

func TestValidateRejectsNegativeLimits(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*AwoConfig)
	}{
		{"maxChangedFiles negative", func(c *AwoConfig) { c.Safety.MaxChangedFiles = -1 }},
		{"maxIterations negative", func(c *AwoConfig) { c.Safety.MaxIterations = -1 }},
		{"claude timeout negative", func(c *AwoConfig) { c.Agents.Claude.TimeoutSeconds = -1 }},
		{"codex timeout negative", func(c *AwoConfig) { c.Agents.Codex.TimeoutSeconds = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestValidateRejectsEmptyProtectedPath(t *testing.T) {
	c := Default()
	c.Safety.ProtectedPaths = []string{".github/", ""}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty protected path entry")
	}
}

func TestValidateRejectsEmptyDirs(t *testing.T) {
	c := Default()
	c.WorktreeBaseDir = ""
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for empty worktreeBaseDir")
	}
	c = Default()
	c.ArtifactDir = " "
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for whitespace artifactDir")
	}
}

func TestLoadReadsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, Filename)
	if err := os.WriteFile(p, []byte(DefaultJSON()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, source, err := LoadOrDefault(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if source != p {
		t.Fatalf("source=%q want %q", source, p)
	}
	if cfg.BranchPrefix != "awo" {
		t.Fatalf("BranchPrefix=%q", cfg.BranchPrefix)
	}
}
