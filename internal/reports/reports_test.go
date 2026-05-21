package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/artifacts"
)

func TestRenderProofPackContainsKeyFields(t *testing.T) {
	r := artifacts.Run{
		ID:        "20260521T120000-abcd",
		Mode:      "single",
		Status:    "succeeded",
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		Worktree:  ".awo/worktrees/run1",
		Branch:    "awo/run1",
	}
	out, err := RenderProofPack(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{r.ID, "single", "succeeded", "awo/run1", "auto-commits"} {
		if !strings.Contains(out, want) {
			t.Errorf("proof pack missing %q\n%s", want, out)
		}
	}
}
