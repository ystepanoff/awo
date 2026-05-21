package reports

import (
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/domain"
)

func TestRenderProofPackContainsKeyFields(t *testing.T) {
	r := domain.RunReport{
		RunID: "20260521T120000-abcd",
		Spec: domain.RunSpec{
			Task: "add a hello world endpoint",
			Mode: domain.ModeSingle,
		},
		Status:    domain.StatusCompleted,
		StartedAt: time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC),
		AgentResults: []domain.AgentRunResult{{
			Agent:        domain.AgentClaude,
			Role:         domain.RoleWriter,
			WorktreePath: ".awo/worktrees/run1",
			BranchName:   "awo/run1",
			Status:       "ok",
			ExitCode:     0,
			ChangedFiles: []string{"main.go"},
		}},
		VerificationResults: []domain.VerificationResult{{
			Command:        "go test ./...",
			ExitCode:       0,
			Passed:         true,
			DurationMillis: 12345,
		}},
		Recommendation: domain.RecReadyForHumanReview,
	}
	out, err := RenderProofPack(r)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	for _, want := range []string{
		r.RunID,
		"single",
		"completed",
		"awo/run1",
		"go test ./...",
		"ready_for_human_review",
		"add a hello world endpoint",
		"auto-commits",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("proof pack missing %q\n%s", want, out)
		}
	}
}
