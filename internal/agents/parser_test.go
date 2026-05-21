package agents

import (
	"errors"
	"strings"
	"testing"
)

// ----- ParseAgentResult ---------------------------------------------------

func TestParseAgentResultCleanJSON(t *testing.T) {
	stdout := `Some narrative.
AWO_RESULT_JSON
{
  "summary": "Added /health endpoint",
  "changed_files_intended": ["server/health.go", "server/health_test.go"],
  "tests_run": ["go test ./..."],
  "risks": ["no risk"],
  "follow_up": ["add /ready"],
  "confidence": "high"
}
`
	got, err := ParseAgentResult(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected result")
	}
	if got.Summary != "Added /health endpoint" {
		t.Errorf("summary=%q", got.Summary)
	}
	if len(got.FilesTouched) != 2 || got.FilesTouched[0] != "server/health.go" {
		t.Errorf("files=%v", got.FilesTouched)
	}
	joined := strings.Join(got.Notes, "|")
	for _, want := range []string{"tests_run: go test ./...", "follow_up: add /ready", "confidence: high"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing note %q in %v", want, got.Notes)
		}
	}
}

func TestParseAgentResultFencedJSON(t *testing.T) {
	stdout := "Hello.\n\n```json\nAWO_RESULT_JSON\n{\n  \"summary\": \"ok\",\n  \"confidence\": \"medium\"\n}\n```\n"
	got, err := ParseAgentResult(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.Summary != "ok" {
		t.Fatalf("got=%+v", got)
	}
}

func TestParseAgentResultPicksLastBlock(t *testing.T) {
	stdout := `First attempt:
AWO_RESULT_JSON
{"summary": "first", "confidence": "low"}

But actually I changed my mind:

` + "```json\nAWO_RESULT_JSON\n{\n  \"summary\": \"second\",\n  \"confidence\": \"high\"\n}\n```\n"
	got, err := ParseAgentResult(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil || got.Summary != "second" {
		t.Fatalf("expected last block to win, got=%+v", got)
	}
}

func TestParseAgentResultMissingBlock(t *testing.T) {
	got, err := ParseAgentResult("just narrative, no json block here\n")
	if err != nil {
		t.Fatalf("missing block must not error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil result, got %+v", got)
	}
}

func TestParseAgentResultMalformedJSONIsWarning(t *testing.T) {
	stdout := "AWO_RESULT_JSON\n{\n  \"summary\": \"oops,\n  this is not valid json\n}\n"
	got, err := ParseAgentResult(stdout)
	if got != nil {
		t.Errorf("expected nil result, got %+v", got)
	}
	if err == nil {
		t.Fatal("expected parse warning")
	}
	if !errors.Is(err, ErrParseWarning) {
		t.Fatalf("want ErrParseWarning, got %v", err)
	}
}

func TestParseAgentResultMalformedFenced(t *testing.T) {
	stdout := "```json\nAWO_RESULT_JSON\n{not really json}\n```\n"
	got, err := ParseAgentResult(stdout)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
	if !errors.Is(err, ErrParseWarning) {
		t.Fatalf("want ErrParseWarning, got %v", err)
	}
}

func TestParseAgentResultDoesNotCrashOnNoise(t *testing.T) {
	for _, in := range []string{
		"",
		"```json\n```\n",
		"AWO_RESULT_JSON\n",
		"AWO_RESULT_JSON\n{",
		"```\n```",
		strings.Repeat("AWO_RESULT_JSON\n", 100),
	} {
		_, _ = ParseAgentResult(in)
	}
}

// ----- ParseReviewResult --------------------------------------------------

func TestParseReviewResultCleanJSON(t *testing.T) {
	stdout := `Reviewing diff...
AWO_REVIEW_JSON
{
  "blocking": ["missing nil check on req.Body"],
  "non_blocking": ["consider extracting helper"],
  "suggested_tests": ["TestHealthHandlerWhenContextCancelled"],
  "risk_summary": "small surface area, low risk",
  "recommendation": "needs_revision"
}
`
	got, err := ParseReviewResult(stdout)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got == nil {
		t.Fatal("expected result")
	}
	if got.Recommendation != "needs_revision" {
		t.Errorf("recommendation=%q", got.Recommendation)
	}
	if len(got.Blocking) != 1 || got.Blocking[0] != "missing nil check on req.Body" {
		t.Errorf("blocking=%v", got.Blocking)
	}
	if len(got.SuggestedTests) != 1 {
		t.Errorf("suggestedTests=%v", got.SuggestedTests)
	}
}

func TestParseReviewResultFenced(t *testing.T) {
	stdout := "```json\nAWO_REVIEW_JSON\n{\n  \"recommendation\": \"approve_for_human_review\"\n}\n```\n"
	got, err := ParseReviewResult(stdout)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if got == nil || got.Recommendation != "approve_for_human_review" {
		t.Fatalf("got=%+v", got)
	}
}

func TestParseReviewResultMissingBlock(t *testing.T) {
	got, err := ParseReviewResult("nothing structured here")
	if err != nil {
		t.Fatalf("missing block must not error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %+v", got)
	}
}

func TestParseReviewResultMalformed(t *testing.T) {
	stdout := "AWO_REVIEW_JSON\n{\n  \"recommendation\": \"rej\n}\n"
	got, err := ParseReviewResult(stdout)
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
	if !errors.Is(err, ErrParseWarning) {
		t.Fatalf("want ErrParseWarning, got %v", err)
	}
}

// ----- helpers ------------------------------------------------------------

func TestExtractFirstJSONObjectHandlesNestedAndStrings(t *testing.T) {
	in := `prefix {"a": "b}c", "n": {"x": 1}} suffix`
	got, ok := extractFirstJSONObject(in)
	if !ok {
		t.Fatal("expected match")
	}
	want := `{"a": "b}c", "n": {"x": 1}}`
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestExtractFirstJSONObjectUnclosed(t *testing.T) {
	if _, ok := extractFirstJSONObject("{ unclosed"); ok {
		t.Fatal("expected no match for unclosed object")
	}
}
