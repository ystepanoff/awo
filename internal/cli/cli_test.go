package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awo-dev/awo/internal/config"
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
	if err := runInit(&buf, dir, false); err != nil {
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
	if err := runInit(&buf, dir, false); err != nil {
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
	if err := runInit(&buf, dir, false); err != nil {
		t.Fatalf("init1: %v", err)
	}
	first, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err := runInit(&buf, dir, false); err != nil {
		t.Fatalf("init2: %v", err)
	}
	second, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if string(first) != string(second) {
		t.Fatalf("gitignore changed on second init:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestInitGeneratedConfigValidates(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(&bytes.Buffer{}, dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, err := config.Load(filepath.Join(dir, "awo.config.json"))
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if cfg.BranchPrefix != "awo" {
		t.Errorf("BranchPrefix=%q", cfg.BranchPrefix)
	}
	if cfg.Agents.Claude.TimeoutSeconds != 1800 {
		t.Errorf("claude timeout=%d want 1800", cfg.Agents.Claude.TimeoutSeconds)
	}
	if cfg.Agents.Codex.TimeoutSeconds != 1800 {
		t.Errorf("codex timeout=%d want 1800", cfg.Agents.Codex.TimeoutSeconds)
	}
	wantedProtected := []string{"auth/**", "payments/**", "**/*secret*"}
	for _, w := range wantedProtected {
		found := false
		for _, p := range cfg.Safety.ProtectedPaths {
			if p == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("protectedPaths missing %q (got %v)", w, cfg.Safety.ProtectedPaths)
		}
	}
}

func TestInitInstructionFilesContainAwoRules(t *testing.T) {
	dir := t.TempDir()
	if err := runInit(&bytes.Buffer{}, dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		s := string(body)
		for _, want := range []string{
			"AWO",
			"Do not commit",
			"Do not push",
			"Do not merge",
			"protected paths",
			"verification",
		} {
			if !strings.Contains(s, want) {
				t.Errorf("%s missing %q:\n%s", name, want, s)
			}
		}
		// Concise: the instruction body should fit on a screen — keep
		// the cap loose but prevent unbounded growth.
		if len(s) > 1500 {
			t.Errorf("%s is too long (%d bytes); keep it concise", name, len(s))
		}
	}
}

func TestInitForceOverwritesAwoOwnedFiles(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "awo.config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"branchPrefix": "awo-old"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInit(&bytes.Buffer{}, dir, true); err != nil {
		t.Fatalf("init --force: %v", err)
	}
	body, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(body), `"branchPrefix": "awo"`) {
		t.Errorf("--force should overwrite awo.config.json, got:\n%s", body)
	}
}

func TestInitForcePreservesUserContentAroundMarkers(t *testing.T) {
	dir := t.TempDir()
	userTop := "# My project\n\nThis is my own README content above.\n"
	userBottom := "\n## My own section below AWO\n\nKeep me.\n"

	if err := runInit(&bytes.Buffer{}, dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	claudePath := filepath.Join(dir, "CLAUDE.md")
	body, _ := os.ReadFile(claudePath)
	combined := userTop + string(body) + userBottom
	if err := os.WriteFile(claudePath, []byte(combined), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := runInit(&bytes.Buffer{}, dir, true); err != nil {
		t.Fatalf("init --force: %v", err)
	}
	out, _ := os.ReadFile(claudePath)
	s := string(out)
	if !strings.Contains(s, userTop) {
		t.Errorf("user content above markers lost:\n%s", s)
	}
	if !strings.Contains(s, "Keep me.") {
		t.Errorf("user content below markers lost:\n%s", s)
	}
	if !strings.Contains(s, mdMarkerBegin) || !strings.Contains(s, mdMarkerEnd) {
		t.Errorf("markers missing after --force:\n%s", s)
	}
	// AWO content should still be present and only once.
	if strings.Count(s, "AWO will run deterministic verification") != 1 {
		t.Errorf("AWO body should appear exactly once, got %d:\n%s",
			strings.Count(s, "AWO will run deterministic verification"), s)
	}
}

func TestInitWithoutForceLeavesExistingMarkerSectionAlone(t *testing.T) {
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "CLAUDE.md")
	tampered := "# Mine\n\n" + mdMarkerBegin + "\nstale-tampered-body\n" + mdMarkerEnd + "\n"
	if err := os.WriteFile(claudePath, []byte(tampered), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInit(&bytes.Buffer{}, dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	out, _ := os.ReadFile(claudePath)
	if !strings.Contains(string(out), "stale-tampered-body") {
		t.Errorf("init without --force should not touch existing AWO markers:\n%s", out)
	}
}

func TestInitGitignorePreservesUserEntries(t *testing.T) {
	dir := t.TempDir()
	gi := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gi, []byte("node_modules/\n*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runInit(&bytes.Buffer{}, dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	body, _ := os.ReadFile(gi)
	s := string(body)
	for _, want := range []string{"node_modules/", "*.tmp", ".awo/runs/", ".awo/worktrees/"} {
		if !strings.Contains(s, want) {
			t.Errorf(".gitignore missing %q:\n%s", want, s)
		}
	}
}

func TestInitOutputListsCreatedAndSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("KEEP"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := runInit(&buf, dir, false); err != nil {
		t.Fatalf("init: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "created:") || !strings.Contains(out, "skipped:") {
		t.Errorf("output should contain both created and skipped sections:\n%s", out)
	}
	if !strings.Contains(out, "awo.config.json") {
		t.Errorf("output should list awo.config.json as created:\n%s", out)
	}
	// CLAUDE.md existed; non-force run should report it as skipped.
	skipped := strings.SplitN(out, "skipped:", 2)
	if len(skipped) != 2 || !strings.Contains(skipped[1], "CLAUDE.md") {
		t.Errorf("CLAUDE.md should appear under skipped:\n%s", out)
	}
}
