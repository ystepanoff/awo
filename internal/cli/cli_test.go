package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRootHelpRuns(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--help"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "AWO coordinates Claude Code and Codex") {
		t.Fatalf("help missing description: %s", out)
	}
	for _, want := range []string{"doctor", "init", "config"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing subcommand %q", want)
		}
	}
}

func TestInitCreatesFiles(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runInit(&buf, dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, p := range []string{
		"awo.config.json",
		".awo/README.md",
		".gitignore",
		"CLAUDE.md",
		"AGENTS.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, want := range []string{".awo/runs/", ".awo/worktrees/"} {
		if !strings.Contains(string(gi), want) {
			t.Errorf(".gitignore missing %q", want)
		}
	}
}

func TestInitDoesNotOverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("KEEP ME"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := runInit(&buf, dir); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		b, _ := os.ReadFile(filepath.Join(dir, name))
		if string(b) != "KEEP ME" {
			t.Errorf("%s was overwritten", name)
		}
	}
}

func TestInitGitignoreIdempotent(t *testing.T) {
	dir := t.TempDir()
	var buf bytes.Buffer
	if err := runInit(&buf, dir); err != nil {
		t.Fatalf("init1: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err := runInit(&buf, dir); err != nil {
		t.Fatalf("init2: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(first) != string(second) {
		t.Fatalf("gitignore changed on second init:\nfirst=%s\nsecond=%s", first, second)
	}
}
