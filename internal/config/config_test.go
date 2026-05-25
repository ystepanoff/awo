package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awo-dev/awo/internal/domain"
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
	// New default Codex configs do not ship Sandbox/ApprovalMode at the
	// top level — those are baked into the per-role default args
	// (see TestCodexRoleArgsDefaults). Old configs with the legacy
	// fields are still honored via the RoleArgs fallback path.
	if c.Agents.Codex.Sandbox != "" {
		t.Errorf("codex sandbox should be empty in new default; got %q", c.Agents.Codex.Sandbox)
	}
	if c.Agents.Codex.ApprovalMode != "" {
		t.Errorf("codex approvalMode should be empty in new default; got %q", c.Agents.Codex.ApprovalMode)
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

// ----- per-role args resolution ------------------------------------------

func TestClaudeRoleArgsDefaults(t *testing.T) {
	c := ClaudeConfig{}
	for _, tc := range []struct {
		role domain.AgentRole
		want []string
	}{
		{domain.RoleWriter, []string{"-p", "--permission-mode", "acceptEdits"}},
		{domain.RoleCompetitor, []string{"-p", "--permission-mode", "acceptEdits"}},
		{domain.RoleReviewer, []string{"-p", "--permission-mode", "plan"}},
	} {
		got := c.RoleArgs(tc.role)
		if !sliceEq(got, tc.want) {
			t.Errorf("role=%s got=%v want=%v", tc.role, got, tc.want)
		}
	}
}

func TestCodexRoleArgsDefaults(t *testing.T) {
	c := CodexConfig{}
	for _, tc := range []struct {
		role domain.AgentRole
		want []string
	}{
		{domain.RoleWriter, []string{"exec", "--sandbox", "workspace-write", "--ask-for-approval", "never"}},
		{domain.RoleCompetitor, []string{"exec", "--sandbox", "workspace-write", "--ask-for-approval", "never"}},
		{domain.RoleReviewer, []string{"exec", "--sandbox", "read-only", "--ask-for-approval", "never"}},
	} {
		got := c.RoleArgs(tc.role)
		if !sliceEq(got, tc.want) {
			t.Errorf("role=%s got=%v want=%v", tc.role, got, tc.want)
		}
	}
}

func TestRoleArgsRoleSpecificWins(t *testing.T) {
	cl := ClaudeConfig{
		Args:         []string{"--from-legacy"},
		WriterArgs:   []string{"--writer-only"},
		ReviewerArgs: []string{"--reviewer-only"},
	}
	if got := cl.RoleArgs(domain.RoleWriter); !sliceEq(got, []string{"--writer-only"}) {
		t.Errorf("writer args=%v want [--writer-only]", got)
	}
	if got := cl.RoleArgs(domain.RoleReviewer); !sliceEq(got, []string{"--reviewer-only"}) {
		t.Errorf("reviewer args=%v want [--reviewer-only]", got)
	}
	// Competitor has no specific list -> falls back to legacy Args.
	if got := cl.RoleArgs(domain.RoleCompetitor); !sliceEq(got, []string{"--from-legacy"}) {
		t.Errorf("competitor args=%v want legacy fallback [--from-legacy]", got)
	}
}

func TestCodexRoleArgsLegacyFallbackAppendsSandboxApproval(t *testing.T) {
	// A legacy config (no per-role args, but Args + Sandbox + ApprovalMode
	// set) must keep working: RoleArgs returns Args + the legacy
	// --sandbox / --approval-mode flags so behavior matches the old code.
	c := CodexConfig{
		Args:         []string{"exec", "--profile", "ci"},
		Sandbox:      "read-only",
		ApprovalMode: "on-request",
	}
	want := []string{"exec", "--profile", "ci", "--sandbox", "read-only", "--approval-mode", "on-request"}
	if got := c.RoleArgs(domain.RoleWriter); !sliceEq(got, want) {
		t.Errorf("legacy writer args=%v want=%v", got, want)
	}
}

func TestRoleArgsReturnsCopy(t *testing.T) {
	c := ClaudeConfig{WriterArgs: []string{"-p"}}
	a := c.RoleArgs(domain.RoleWriter)
	a[0] = "MUTATED"
	b := c.RoleArgs(domain.RoleWriter)
	if b[0] != "-p" {
		t.Errorf("RoleArgs returned an aliased slice: caller mutation leaked back to config (%q)", b[0])
	}
}

func TestValidateRejectsDangerousArgsClaude(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*AwoConfig)
	}{
		{"legacy Args bypassPermissions", func(c *AwoConfig) {
			c.Agents.Claude.Args = []string{"-p", "--permission-mode", "bypassPermissions"}
		}},
		{"legacy Args dangerously-skip-permissions", func(c *AwoConfig) {
			c.Agents.Claude.Args = []string{"-p", "--dangerously-skip-permissions"}
		}},
		{"writer-specific bypass", func(c *AwoConfig) {
			c.Agents.Claude.WriterArgs = []string{"-p", "--permission-mode", "BypassPermissions"}
		}},
		{"reviewer-specific bypass", func(c *AwoConfig) {
			c.Agents.Claude.ReviewerArgs = []string{"-p", "--dangerously-skip-permissions"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected error for dangerous args")
			}
		})
	}
}

func TestValidateRejectsDangerousArgsCodex(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*AwoConfig)
	}{
		{"legacy Args danger-full-access", func(c *AwoConfig) {
			c.Agents.Codex.Args = []string{"exec", "--sandbox", "danger-full-access"}
		}},
		{"writer-specific dangerously-bypass", func(c *AwoConfig) {
			c.Agents.Codex.WriterArgs = []string{"exec", "--dangerously-bypass-approvals-and-sandbox"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mut(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected error for dangerous codex args")
			}
		})
	}
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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
