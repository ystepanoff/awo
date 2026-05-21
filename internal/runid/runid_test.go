package runid

import (
	"testing"
	"time"
)

func TestNewMatchesPattern(t *testing.T) {
	id := New()
	if !Pattern.MatchString(id) {
		t.Fatalf("unexpected id %q", id)
	}
	if err := Validate(id); err != nil {
		t.Fatalf("Validate(%q): %v", id, err)
	}
}

func TestNewAtFormat(t *testing.T) {
	got := newAt(time.Date(2026, 5, 21, 14, 30, 22, 0, time.UTC))
	want := "20260521-143022-"
	if got[:len(want)] != want {
		t.Fatalf("prefix=%q want %q", got[:len(want)], want)
	}
	if len(got) != len(want)+6 {
		t.Fatalf("length=%d want %d", len(got), len(want)+6)
	}
}

func TestUniqueIDs(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 64; i++ {
		id := New()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestValidateRejects(t *testing.T) {
	bad := []string{
		"",
		"20260521-143022",                   // missing suffix
		"20260521T143022-a1b2c3",            // wrong separator
		"20260521-143022-a1b2c",             // suffix too short
		"20260521-143022-a1b2c3d",           // suffix too long
		"20260521-143022-A1B2C3",            // uppercase suffix
		"20260521-143022-a1b2c3/etc/passwd", // path injection
		"../../etc",                         // pure traversal
	}
	for _, id := range bad {
		if err := Validate(id); err == nil {
			t.Errorf("Validate(%q) = nil, want error", id)
		}
	}
}

func TestValidateAccepts(t *testing.T) {
	good := []string{
		"20260521-143022-a1b2c3",
		"19700101-000000-000000",
		"99991231-235959-ffffff",
	}
	for _, id := range good {
		if err := Validate(id); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", id, err)
		}
	}
}
