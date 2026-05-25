package reports

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/awo-dev/awo/internal/artifacts"
	"github.com/awo-dev/awo/internal/domain"
	"github.com/awo-dev/awo/internal/runid"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files in testdata/")

// fixedTime is used in fixtures so snapshot output is deterministic.
var fixedTime = time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)

// ----- fixtures -----------------------------------------------------------

func happyInputs() Inputs {
	return Inputs{
		Report: domain.RunReport{
			RunID: "20260521-120000-abc123",
			Spec: domain.RunSpec{
				Task: "add /health endpoint",
				Mode: domain.ModeSingle,
			},
			Status:     domain.StatusCompleted,
			StartedAt:  fixedTime,
			FinishedAt: fixedTime.Add(2 * time.Second),
			AgentResults: []domain.AgentRunResult{{
				Agent:        domain.AgentClaude,
				Role:         domain.RoleWriter,
				WorktreePath: ".awo/worktrees/run/claude-writer",
				BranchName:   "awo/run/claude-writer",
				Status:       "ok",
				ChangedFiles: []string{"server/health.go", "server/health_test.go"},
				ParsedResult: &domain.ParsedAgentResult{
					Summary: "Added /health endpoint and a unit test.",
					Notes:   []string{"depends on net/http", "no auth required"},
				},
			}},
			VerificationResults: []domain.VerificationResult{{
				Command:        "go test ./...",
				ExitCode:       0,
				Passed:         true,
				DurationMillis: 1234,
			}},
			Recommendation: domain.RecReadyForHumanReview,
		},
		ProtectedPaths: []string{"go.mod", "go.sum"},
		DiffPatchPath:  ".awo/runs/20260521-120000-abc123/diff.patch",
	}
}

func failedVerifyInputs() Inputs {
	in := happyInputs()
	in.Report.Status = domain.StatusFailed
	in.Report.Recommendation = domain.RecFailedVerification
	in.Report.VerificationResults = []domain.VerificationResult{{
		Command:        "go test ./...",
		ExitCode:       1,
		Passed:         false,
		DurationMillis: 870,
	}}
	return in
}

func noVerifyNoParseInputs() Inputs {
	in := happyInputs()
	in.Report.VerificationResults = nil
	in.Report.AgentResults[0].ParsedResult = nil
	in.Report.Spec.DryRun = true
	return in
}

func protectedHitsInputs() Inputs {
	in := happyInputs()
	in.Report.AgentResults[0].ChangedFiles = []string{"go.mod", "server/health.go"}
	in.Report.Recommendation = domain.RecNeedsHumanAttention
	return in
}

func permissionFailureInputs() Inputs {
	in := happyInputs()
	in.Report.AgentResults[0].ChangedFiles = nil
	in.Report.AgentResults[0].Status = "failed"
	in.Report.AgentResults[0].FailureKind = "permission_required"
	in.Report.AgentResults[0].FailureReason = `agent appears to have hit an interactive permission/approval prompt (stderr: "Error: permission required to edit /etc/passwd")`
	in.Report.AgentResults[0].StdoutPath = ".awo/runs/20260521-120000-abc123/agents/claude-writer/stdout.log"
	in.Report.AgentResults[0].StderrPath = ".awo/runs/20260521-120000-abc123/agents/claude-writer/stderr.log"
	in.Report.AgentResults[0].ParsedResult = nil
	in.Report.VerificationResults = nil
	in.Report.Recommendation = domain.RecNeedsHumanAttention
	in.Report.Status = domain.StatusFailed
	return in
}

// ----- snapshot helper ----------------------------------------------------

func goldenCheck(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("update golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run with -update to create)", path, err)
	}
	if string(want) != got {
		t.Errorf("snapshot %s mismatch.\n--- want ---\n%s\n--- got ---\n%s", name, string(want), got)
	}
}

// ----- proof pack ---------------------------------------------------------

func TestRenderProofPackHappyPath(t *testing.T) {
	out, err := RenderProofPack(happyInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "proof_pack_happy.md", out)
}

func TestRenderProofPackFailedVerification(t *testing.T) {
	out, err := RenderProofPack(failedVerifyInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "proof_pack_failed_verification.md", out)
	for _, want := range []string{"failed_verification", "**failed**"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output", want)
		}
	}
}

func TestRenderProofPackMissingVerificationAndParsed(t *testing.T) {
	out, err := RenderProofPack(noVerifyNoParseInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "proof_pack_no_verify_no_parsed.md", out)
	if !strings.Contains(out, "_not verified_") {
		t.Errorf("missing 'not verified' marker:\n%s", out)
	}
	if strings.Contains(out, "## Agent summary") {
		t.Errorf("agent summary section should be omitted when ParsedResult is nil:\n%s", out)
	}
	if strings.Contains(out, "## Agent-reported risks") {
		t.Errorf("risks section should be omitted when ParsedResult is nil:\n%s", out)
	}
}

func TestRenderProofPackProtectedHits(t *testing.T) {
	out, err := RenderProofPack(protectedHitsInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "proof_pack_protected_hits.md", out)
	if !strings.Contains(out, "Protected path warnings") {
		t.Errorf("protected hits section missing:\n%s", out)
	}
	if !strings.Contains(out, "`go.mod`") {
		t.Errorf("protected file not listed:\n%s", out)
	}
}

func TestRenderProofPackPermissionFailure(t *testing.T) {
	out, err := RenderProofPack(permissionFailureInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "proof_pack_permission_failure.md", out)
	for _, want := range []string{
		"Agent failure (permission_required)",
		"non-interactive",
		"awo.config.json",
		"bypassPermissions",
		"danger-full-access",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in permission-failure proof pack", want)
		}
	}
}

func TestRenderSummaryPermissionFailure(t *testing.T) {
	out, err := RenderSummary(permissionFailureInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "summary_permission_failure.md", out)
	if !strings.Contains(out, "agent failure: `permission_required`") {
		t.Errorf("summary missing failure cue:\n%s", out)
	}
}

func TestRenderProofPackNeverAdvertisesAutoCommits(t *testing.T) {
	out, err := RenderProofPack(happyInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(out, "AWO did not commit, push, merge, or auto-approve this change.") {
		t.Errorf("missing the no-auto-commits guarantee statement")
	}
}

// ----- summary ------------------------------------------------------------

func TestRenderSummaryHappyPath(t *testing.T) {
	out, err := RenderSummary(happyInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "summary_happy.md", out)
}

func TestRenderSummaryFailedVerification(t *testing.T) {
	out, err := RenderSummary(failedVerifyInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "summary_failed_verification.md", out)
}

func TestRenderSummaryMissingVerification(t *testing.T) {
	out, err := RenderSummary(noVerifyNoParseInputs())
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	goldenCheck(t, "summary_not_verified.md", out)
	if !strings.Contains(out, "not verified") {
		t.Errorf("expected 'not verified' status:\n%s", out)
	}
}

// ----- WriteRunReportFiles ------------------------------------------------

func TestWriteRunReportFilesPersistsBoth(t *testing.T) {
	repo := t.TempDir()
	rid := "20260521-120000-abc123"
	if err := runid.Validate(rid); err != nil {
		t.Fatalf("seed run id: %v", err)
	}
	layout, err := artifacts.NewLayout(repo, ".awo/runs", rid)
	if err != nil {
		t.Fatalf("layout: %v", err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	if err := WriteRunReportFiles(happyInputs(), layout); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, p := range []string{layout.ProofPackPath(), layout.SummaryPath()} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", p, err)
		}
		if fi.Size() == 0 {
			t.Errorf("%s is empty", p)
		}
	}
	body, err := os.ReadFile(layout.ProofPackPath())
	if err != nil {
		t.Fatalf("read proof pack: %v", err)
	}
	if !strings.Contains(string(body), rid) {
		t.Errorf("proof pack missing run id")
	}
}

func TestWriteRunReportFilesRefusesNilLayout(t *testing.T) {
	if err := WriteRunReportFiles(happyInputs(), nil); err == nil {
		t.Fatal("expected error on nil layout")
	}
}

func TestRenderRejectsUnsupportedInput(t *testing.T) {
	if _, err := RenderProofPack(123); err == nil {
		t.Error("expected error for unsupported input type")
	}
	if _, err := RenderSummary(struct{}{}); err == nil {
		t.Error("expected error for unsupported input type")
	}
}
