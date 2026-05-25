package safety

import (
	"path/filepath"
	"testing"
)

func TestEnsureInsideAllowsChild(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "a", "b")
	got, err := EnsureInside(root, child)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if got == "" {
		t.Fatal("expected non-empty path")
	}
}

func TestEnsureInsideRejectsEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "..", "evil")
	if _, err := EnsureInside(root, outside); err == nil {
		t.Fatal("expected error for escaped path")
	}
}

func TestEnsureInsideRejectsDotDot(t *testing.T) {
	root := t.TempDir()
	if _, err := EnsureInside(root, filepath.Join(root, "..")); err == nil {
		t.Fatal("expected error for parent path")
	}
}

func TestIsSubpath(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		parent, child string
		want          bool
	}{
		{root, filepath.Join(root, "a"), true},
		{root, filepath.Join(root, "a", "b", "c"), true},
		{root, root, false},                                 // equal: strict child
		{root, filepath.Join(root, ".."), false},            // parent
		{root, filepath.Join(root, "..", "sibling"), false}, // sibling
		{filepath.Join(root, "a"), filepath.Join(root, "ab"), false},
		{filepath.Join(root, "a"), filepath.Join(root, "a", "b"), true},
	}
	for _, c := range cases {
		if got := IsSubpath(c.parent, c.child); got != c.want {
			t.Errorf("IsSubpath(%q, %q)=%v want %v", c.parent, c.child, got, c.want)
		}
	}
}

func TestMustBeUnder(t *testing.T) {
	root := t.TempDir()
	if err := MustBeUnder(root, filepath.Join(root, "x")); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if err := MustBeUnder(root, root); err == nil {
		t.Fatal("expected error for equal paths")
	}
	if err := MustBeUnder(root, filepath.Join(root, "..", "evil")); err == nil {
		t.Fatal("expected error for path outside root")
	}
}

func TestSafeJoinHappyPath(t *testing.T) {
	root := t.TempDir()
	got, err := SafeJoin(root, "a", "b", "c")
	if err != nil {
		t.Fatalf("safe join: %v", err)
	}
	want := filepath.Join(root, "a", "b", "c")
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSafeJoinRejectsEmpty(t *testing.T) {
	root := t.TempDir()
	if _, err := SafeJoin(root); err == nil {
		t.Fatal("expected error for no parts")
	}
	if _, err := SafeJoin(root, ""); err == nil {
		t.Fatal("expected error for empty part")
	}
	if _, err := SafeJoin("", "a"); err == nil {
		t.Fatal("expected error for empty base")
	}
}

func TestSafeJoinRejectsAbsoluteComponent(t *testing.T) {
	root := t.TempDir()
	if _, err := SafeJoin(root, "/etc/passwd"); err == nil {
		t.Fatal("expected error for absolute component")
	}
}

func TestSafeJoinRejectsParentTraversal(t *testing.T) {
	root := t.TempDir()
	cases := []string{"..", "../etc", "a/../b", "a/.."}
	for _, p := range cases {
		if _, err := SafeJoin(root, p); err == nil {
			t.Errorf("expected error for traversal %q", p)
		}
	}
}

func TestIsProtectedPath(t *testing.T) {
	patterns := []string{".github/", "Makefile", "go.mod", "*.pem"}
	cases := []struct {
		path string
		want bool
	}{
		{".github/workflows/ci.yml", true},
		{"sub/.github/x.yml", true},
		{"Makefile", true},
		{"src/Makefile", true},
		{"go.mod", true},
		{"keys/server.pem", true},
		{"main.go", false},
		{"docs/readme.md", false},
	}
	for _, c := range cases {
		if got := IsProtectedPath(c.path, patterns); got != c.want {
			t.Errorf("IsProtectedPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestRedact(t *testing.T) {
	in := "API_KEY=abc123 secret=sshhh other=ok"
	out := Redact(in, []string{`(?i)api[_-]?key=\S+`, `(?i)secret=\S+`})
	if out == in {
		t.Fatalf("expected redaction, got %q", out)
	}
	if want := "[REDACTED] [REDACTED] other=ok"; out != want {
		t.Fatalf("redact got %q want %q", out, want)
	}
}
