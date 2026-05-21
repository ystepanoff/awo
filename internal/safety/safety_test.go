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
