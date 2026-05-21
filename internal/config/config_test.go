package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config is invalid: %v", err)
	}
}

func TestDefaultJSONRoundTrip(t *testing.T) {
	cfg, err := Parse([]byte(DefaultJSON()))
	if err != nil {
		t.Fatalf("parse default json: %v", err)
	}
	if cfg.DefaultMode != ModeSingle {
		t.Fatalf("expected default mode single, got %q", cfg.DefaultMode)
	}
	if cfg.Worktrees.BranchPrefix != "awo/" {
		t.Fatalf("branch prefix should be awo/, got %q", cfg.Worktrees.BranchPrefix)
	}
}

func TestValidateRejectsBadMode(t *testing.T) {
	c := Default()
	c.DefaultMode = "yolo"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown mode")
	}
}

func TestValidateRejectsBadBranchPrefix(t *testing.T) {
	c := Default()
	c.Worktrees.BranchPrefix = "feature/"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for non-awo branch prefix")
	}
}

func TestValidateRejectsUnknownAgentKind(t *testing.T) {
	c := Default()
	c.Agents["weird"] = Agent{Kind: "vim"}
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for unknown agent kind")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	bad := `{"version":1,"defaultMode":"single","mystery":true,"agents":{"a":{"kind":"claude"}},"worktrees":{"branchPrefix":"awo/","root":".awo/worktrees"}}`
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("expected error for unknown field")
	} else if !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadOrDefaultMissingFileReturnsDefault(t *testing.T) {
	dir := t.TempDir()
	cfg, source, err := LoadOrDefault(filepath.Join(dir, "missing.json"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if source != "default" {
		t.Fatalf("expected source=default, got %q", source)
	}
	if cfg.DefaultMode != ModeSingle {
		t.Fatalf("default config not returned")
	}
}

func TestLoadReadsFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "awo.config.json")
	if err := os.WriteFile(p, []byte(DefaultJSON()), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, source, err := LoadOrDefault(p)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if source != p {
		t.Fatalf("expected source=%q, got %q", p, source)
	}
	if cfg.Version != 1 {
		t.Fatalf("expected version=1, got %d", cfg.Version)
	}
}
