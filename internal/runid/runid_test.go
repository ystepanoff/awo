package runid

import (
	"regexp"
	"testing"
)

func TestNewFormat(t *testing.T) {
	id := New()
	re := regexp.MustCompile(`^\d{8}T\d{6}-[0-9a-f]{4}$`)
	if !re.MatchString(id) {
		t.Fatalf("unexpected id %q", id)
	}
}

func TestUniqueIDs(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 32; i++ {
		id := New()
		if _, ok := seen[id]; ok {
			t.Fatalf("duplicate id: %s", id)
		}
		seen[id] = struct{}{}
	}
}
