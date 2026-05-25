package examples

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ----- happy path --------------------------------------------------------

func TestCreateSampleGoAppCreatesFixtureAndCommits(t *testing.T) {
	dir := t.TempDir()
	res, err := CreateSampleGoApp(dir, false, io.Discard)
	if err != nil {
		t.Fatalf("CreateSampleGoApp: %v", err)
	}

	if res.Replaced {
		t.Errorf("Replaced=true on first creation")
	}
	if res.GitCommit == "" || res.BranchName == "" {
		t.Errorf("missing git metadata: %+v", res)
	}

	fixture := filepath.Join(dir, SampleGoAppName)
	wantedFiles := []string{
		"go.mod",
		"calculator.go",
		"calculator_test.go",
		"README.md",
		".gitignore",
		".awo-fixture",
	}
	for _, f := range wantedFiles {
		path := filepath.Join(fixture, f)
		st, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing %s: %v", f, err)
			continue
		}
		if st.Size() == 0 {
			t.Errorf("%s is empty", f)
		}
	}

	// Verify it's its own git repo with exactly one commit.
	if _, err := os.Stat(filepath.Join(fixture, ".git")); err != nil {
		t.Fatalf("fixture should be a git repo: %v", err)
	}
	count := strings.TrimSpace(runGit(t, fixture, "rev-list", "--count", "HEAD"))
	if count != "1" {
		t.Errorf("want exactly 1 commit, got %q", count)
	}

	// Confirm the calculator tests pass before any agent run.
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = fixture
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test ./...: %v\n%s", err, out)
	}
}

// ----- determinism -------------------------------------------------------

func TestCreateSampleGoAppIsDeterministic(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	if _, err := CreateSampleGoApp(dirA, false, io.Discard); err != nil {
		t.Fatalf("A: %v", err)
	}
	if _, err := CreateSampleGoApp(dirB, false, io.Discard); err != nil {
		t.Fatalf("B: %v", err)
	}

	// File content must be byte-identical across generations.
	for _, name := range []string{
		"go.mod",
		"calculator.go",
		"calculator_test.go",
		"README.md",
		".gitignore",
		".awo-fixture",
	} {
		a, err := os.ReadFile(filepath.Join(dirA, SampleGoAppName, name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(filepath.Join(dirB, SampleGoAppName, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Errorf("%s differs across generations", name)
		}
	}
}

// ----- safety: refuse to overwrite ---------------------------------------

func TestCreateSampleGoAppRefusesToOverwriteWithoutForce(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateSampleGoApp(dir, false, io.Discard); err != nil {
		t.Fatalf("first: %v", err)
	}
	_, err := CreateSampleGoApp(dir, false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("expected refuse-without-force error, got %v", err)
	}
}

func TestCreateSampleGoAppForceReplacesAwoFixture(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateSampleGoApp(dir, false, io.Discard); err != nil {
		t.Fatalf("first: %v", err)
	}
	// Tamper with a file; --force should restore it.
	calc := filepath.Join(dir, SampleGoAppName, "calculator.go")
	if err := os.WriteFile(calc, []byte("// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := CreateSampleGoApp(dir, true, io.Discard)
	if err != nil {
		t.Fatalf("force: %v", err)
	}
	if !res.Replaced {
		t.Errorf("Replaced=false on --force regen")
	}
	body, _ := os.ReadFile(calc)
	if strings.Contains(string(body), "tampered") {
		t.Errorf("calculator.go not restored:\n%s", body)
	}
	if !strings.Contains(string(body), "func Divide") {
		t.Errorf("calculator.go missing expected content:\n%s", body)
	}
}

// ----- safety: never delete foreign data ---------------------------------

func TestCreateSampleGoAppRefusesToTouchForeignDirectory(t *testing.T) {
	dir := t.TempDir()
	foreign := filepath.Join(dir, SampleGoAppName)
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(foreign, "keep.txt")
	if err := os.WriteFile(keep, []byte("user content"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Without --force: refuse.
	if _, err := CreateSampleGoApp(dir, false, io.Discard); err == nil {
		t.Errorf("expected refuse without --force on foreign dir")
	}

	// With --force: still refuse, because there is no marker file.
	_, err := CreateSampleGoApp(dir, true, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "not an AWO fixture") {
		t.Fatalf("expected refusal on foreign dir even with --force, got %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("user data was removed: %v", err)
	}
}

// ----- safety: never touches outer repo's git ----------------------------

func TestCreateSampleGoAppDoesNotTouchOuterGit(t *testing.T) {
	// The outer dir is a fake user repo. We seed it with a known
	// commit and verify that, after the fixture is created, neither
	// the commit hash nor any worktree files in the outer repo have
	// changed.
	outer := t.TempDir()
	runGit(t, outer, "init", "-q")
	if err := os.WriteFile(filepath.Join(outer, "README"), []byte("outer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-c", "user.email=t@t", "-c", "user.name=t", "add", ".")
	cmd.Dir = outer
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "outer")
	cmd.Dir = outer
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit: %v: %s", err, out)
	}
	beforeHash := runGit(t, outer, "rev-parse", "HEAD")
	beforeStatus := runGit(t, outer, "status", "--porcelain")

	fixturesDir := filepath.Join(outer, ".awo", "fixtures")
	if _, err := CreateSampleGoApp(fixturesDir, false, io.Discard); err != nil {
		t.Fatalf("CreateSampleGoApp: %v", err)
	}

	afterHash := runGit(t, outer, "rev-parse", "HEAD")
	if beforeHash != afterHash {
		t.Errorf("outer repo HEAD changed: %s -> %s", beforeHash, afterHash)
	}
	// `git status` in the outer repo will show the new untracked
	// .awo/fixtures path; that's fine. We only need to confirm no
	// modifications to tracked files were introduced.
	afterStatus := runGit(t, outer, "status", "--porcelain")
	if strings.Contains(afterStatus, " M ") || strings.Contains(afterStatus, " D ") {
		t.Errorf("outer working tree mutated:\nbefore=%q\nafter=%q",
			beforeStatus, afterStatus)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(out)
}
