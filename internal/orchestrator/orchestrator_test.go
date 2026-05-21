package orchestrator

import (
	"testing"

	"github.com/awo-dev/awo/internal/domain"
)

func TestResolveModeDefaults(t *testing.T) {
	m, err := ResolveMode("", domain.ModeSingle)
	if err != nil {
		t.Fatal(err)
	}
	if m != domain.ModeSingle {
		t.Fatalf("got %q", m)
	}
}

func TestResolveModeKnown(t *testing.T) {
	for _, want := range []domain.RunMode{domain.ModeSingle, domain.ModeWriterReviewer, domain.ModeCompetitive} {
		got, err := ResolveMode(string(want), "")
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if got != want {
			t.Fatalf("got %q want %q", got, want)
		}
	}
}

func TestResolveModeUnknown(t *testing.T) {
	if _, err := ResolveMode("xyz", domain.ModeSingle); err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveModeEmptyAndNoFallback(t *testing.T) {
	if _, err := ResolveMode("", ""); err == nil {
		t.Fatal("expected error when both empty")
	}
}
