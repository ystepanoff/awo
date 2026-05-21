package orchestrator

import (
	"testing"

	"github.com/awo-dev/awo/internal/config"
)

func TestResolveModeDefaults(t *testing.T) {
	m, err := ResolveMode("", config.ModeSingle)
	if err != nil {
		t.Fatal(err)
	}
	if m != config.ModeSingle {
		t.Fatalf("got %q", m)
	}
}

func TestResolveModeKnown(t *testing.T) {
	for _, want := range []config.Mode{config.ModeSingle, config.ModeWriterReviewer, config.ModeCompetitive} {
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
	if _, err := ResolveMode("xyz", config.ModeSingle); err == nil {
		t.Fatal("expected error")
	}
}
