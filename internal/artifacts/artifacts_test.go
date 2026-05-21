package artifacts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/awo-dev/awo/internal/safety"
)

const validRunID = "20260521-143022-a1b2c3"

func newLayoutForTest(t *testing.T) *Layout {
	t.Helper()
	repo := t.TempDir()
	l, err := NewLayout(repo, ".awo/runs", validRunID)
	if err != nil {
		t.Fatalf("NewLayout: %v", err)
	}
	return l
}

func TestNewLayoutPaths(t *testing.T) {
	l := newLayoutForTest(t)

	wantSuffix := filepath.Join(".awo", "runs", validRunID)
	if !strings.HasSuffix(l.Root, wantSuffix) {
		t.Errorf("Root=%q does not end with %q", l.Root, wantSuffix)
	}
	if got, want := l.RunJSONPath(), filepath.Join(l.Root, "run.json"); got != want {
		t.Errorf("RunJSONPath=%q want %q", got, want)
	}
	if got, want := l.ProofPackPath(), filepath.Join(l.Root, "proof-pack.md"); got != want {
		t.Errorf("ProofPackPath=%q want %q", got, want)
	}
	if got, want := l.SummaryPath(), filepath.Join(l.Root, "summary.md"); got != want {
		t.Errorf("SummaryPath=%q want %q", got, want)
	}
	if got, want := l.DiffPatchPath(), filepath.Join(l.Root, "diff.patch"); got != want {
		t.Errorf("DiffPatchPath=%q want %q", got, want)
	}
	if got, want := l.ComparisonPath(), filepath.Join(l.Root, "comparison.md"); got != want {
		t.Errorf("ComparisonPath=%q want %q", got, want)
	}
	if got, want := l.AgentDir("claude", "writer"), filepath.Join(l.Root, "agents", "claude-writer"); got != want {
		t.Errorf("AgentDir=%q want %q", got, want)
	}
	if got, want := l.VerificationDir(1), filepath.Join(l.Root, "verify", "001"); got != want {
		t.Errorf("VerificationDir=%q want %q", got, want)
	}
	if got, want := l.VerificationDir(42), filepath.Join(l.Root, "verify", "042"); got != want {
		t.Errorf("VerificationDir(42)=%q want %q", got, want)
	}
}

func TestNewLayoutRejectsBadInput(t *testing.T) {
	if _, err := NewLayout("", ".awo/runs", validRunID); err == nil {
		t.Error("expected error for empty repoRoot")
	}
	if _, err := NewLayout(t.TempDir(), "", validRunID); err == nil {
		t.Error("expected error for empty artifactDir")
	}
	if _, err := NewLayout(t.TempDir(), ".awo/runs", "not-a-runid"); err == nil {
		t.Error("expected error for invalid runID")
	}
	if _, err := NewLayout(t.TempDir(), "../escape", validRunID); err == nil {
		t.Error("expected error for traversal in artifactDir")
	}
}

func TestEnsureCreatesStandardDirs(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, sub := range []string{"", "agents", "verify"} {
		p := filepath.Join(l.Root, sub)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing %q: %v", p, err)
		}
		if !fi.IsDir() {
			t.Fatalf("%q is not a directory", p)
		}
	}
	if err := l.Ensure(); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}

func TestEnsureAgentAndVerificationDirs(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	d, err := l.EnsureAgentDir("claude", "writer")
	if err != nil {
		t.Fatalf("EnsureAgentDir: %v", err)
	}
	if !strings.HasSuffix(d, filepath.Join("agents", "claude-writer")) {
		t.Errorf("unexpected agent dir: %s", d)
	}
	if fi, err := os.Stat(d); err != nil || !fi.IsDir() {
		t.Fatalf("agent dir missing: %v", err)
	}

	v, err := l.EnsureVerificationDir(7)
	if err != nil {
		t.Fatalf("EnsureVerificationDir: %v", err)
	}
	if !strings.HasSuffix(v, filepath.Join("verify", "007")) {
		t.Errorf("unexpected verify dir: %s", v)
	}
	if fi, err := os.Stat(v); err != nil || !fi.IsDir() {
		t.Fatalf("verify dir missing: %v", err)
	}
}

// ----- path safety --------------------------------------------------------

func TestWriteJSONRefusesPathOutsideRoot(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}

	outside := filepath.Join(filepath.Dir(l.Root), "evil.json")
	err := l.WriteJSONAtomic(outside, map[string]string{"x": "y"})
	if err == nil {
		t.Fatal("expected refusal for path outside root")
	}
	if !errors.Is(err, safety.ErrOutsideRoot) {
		t.Fatalf("want ErrOutsideRoot, got %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("outside file should not exist: %v", err)
	}
}

func TestWriteFileRefusesParentTraversal(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	traversal := filepath.Join(l.Root, "..", "..", "etc", "passwd")
	err := l.WriteFileAtomic(traversal, []byte("nope"), 0o644)
	if err == nil {
		t.Fatal("expected refusal for parent traversal")
	}
	if !errors.Is(err, safety.ErrOutsideRoot) {
		t.Fatalf("want ErrOutsideRoot, got %v", err)
	}
}

// ----- atomic JSON write --------------------------------------------------

func TestWriteJSONAtomicHappy(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}

	type sample struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	want := sample{Name: "claude", Count: 3}
	if err := l.WriteJSONAtomic(l.RunJSONPath(), want); err != nil {
		t.Fatalf("WriteJSONAtomic: %v", err)
	}

	data, err := os.ReadFile(l.RunJSONPath())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "\n  \"name\": \"claude\"") {
		t.Errorf("expected pretty-printed JSON, got:\n%s", string(data))
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Errorf("expected trailing newline, got:\n%q", string(data))
	}
	var got sample
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != want {
		t.Errorf("roundtrip got %+v want %+v", got, want)
	}
}

func TestWriteJSONAtomicNoTempLeftBehind(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := l.WriteJSONAtomic(l.RunJSONPath(), map[string]int{"i": i}); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}
	entries, err := os.ReadDir(l.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".awo-write-") {
			t.Errorf("temp file leaked: %s", e.Name())
		}
	}
}

func TestWriteFileAtomicOverwritesIdempotently(t *testing.T) {
	l := newLayoutForTest(t)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(l.Root, "agents", "claude-writer", "stdout.log")
	if err := l.WriteFileAtomic(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := l.WriteFileAtomic(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("second write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "second" {
		t.Errorf("got %q want %q", string(got), "second")
	}
}

// ----- absolute artifactDir -----------------------------------------------

func TestNewLayoutAcceptsAbsoluteArtifactDir(t *testing.T) {
	repo := t.TempDir()
	abs := filepath.Join(t.TempDir(), "external-artifacts")
	l, err := NewLayout(repo, abs, validRunID)
	if err != nil {
		t.Fatalf("NewLayout abs: %v", err)
	}
	want := filepath.Join(abs, validRunID)
	if l.Root != want {
		t.Errorf("Root=%q want %q", l.Root, want)
	}
}
