package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCreateFixtureWritesAndPrintsHandoff(t *testing.T) {
	dir := t.TempDir()
	fixturesDir := filepath.Join(dir, ".awo", "fixtures")

	var buf bytes.Buffer
	if err := runCreateFixture(&buf, fixturesDir, false); err != nil {
		t.Fatalf("runCreateFixture: %v", err)
	}

	fixture := filepath.Join(fixturesDir, "sample-go-app")
	for _, f := range []string{"go.mod", "calculator.go", "calculator_test.go", ".git", ".awo-fixture"} {
		if _, err := os.Stat(filepath.Join(fixture, f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}

	out := buf.String()
	for _, want := range []string{
		"Fixture ready.",
		"sample-go-app",
		"SAFE demo environment",
		"its own git repo",
		"awo init",
		"--mode competitive",
		"--competitors claude,codex",
		`--verify "go test ./..."`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("handoff missing %q:\n%s", want, out)
		}
	}
}

func TestRunCreateFixtureRefusesWithoutForce(t *testing.T) {
	dir := t.TempDir()
	fixturesDir := filepath.Join(dir, ".awo", "fixtures")
	if err := runCreateFixture(&bytes.Buffer{}, fixturesDir, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	err := runCreateFixture(&bytes.Buffer{}, fixturesDir, false)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("expected refusal without --force, got %v", err)
	}
}

func TestRunCreateFixtureForceReplaces(t *testing.T) {
	dir := t.TempDir()
	fixturesDir := filepath.Join(dir, ".awo", "fixtures")
	if err := runCreateFixture(&bytes.Buffer{}, fixturesDir, false); err != nil {
		t.Fatalf("first: %v", err)
	}
	calc := filepath.Join(fixturesDir, "sample-go-app", "calculator.go")
	if err := os.WriteFile(calc, []byte("// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runCreateFixture(&bytes.Buffer{}, fixturesDir, true); err != nil {
		t.Fatalf("force: %v", err)
	}
	body, _ := os.ReadFile(calc)
	if strings.Contains(string(body), "tampered") {
		t.Errorf("--force did not restore calculator.go:\n%s", body)
	}
}

func TestRootHelpListsExamplesCommand(t *testing.T) {
	cmd := NewRootCmd()
	cmd.SetArgs([]string{"--help"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(buf.String(), "examples") {
		t.Errorf("root help missing examples subcommand:\n%s", buf.String())
	}
}
