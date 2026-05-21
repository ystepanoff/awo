package agents

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/awo-dev/awo/internal/domain"
)

// ParseWarning is returned when a result block is found but cannot be
// decoded. It is non-fatal: the caller still gets a nil result and can
// surface the warning to the user.
var ErrParseWarning = errors.New("agents: malformed result block")

// ParsedReviewResult mirrors AWO_REVIEW_JSON.
type ParsedReviewResult struct {
	Blocking       []string `json:"blocking,omitempty"`
	NonBlocking    []string `json:"non_blocking,omitempty"`
	SuggestedTests []string `json:"suggested_tests,omitempty"`
	RiskSummary    string   `json:"risk_summary,omitempty"`
	Recommendation string   `json:"recommendation,omitempty"`
}

// ParseAgentResult extracts the last AWO_RESULT_JSON block from stdout.
// Returns (nil, nil) when no block is present. Returns (nil, warning)
// when a block exists but is malformed.
func ParseAgentResult(stdout string) (*domain.ParsedAgentResult, error) {
	raw, found, malformed := extractBlock(stdout, "AWO_RESULT_JSON")
	if !found {
		return nil, nil
	}
	if malformed {
		return nil, fmt.Errorf("%w: AWO_RESULT_JSON: could not extract JSON object", ErrParseWarning)
	}
	var p struct {
		Summary              string   `json:"summary"`
		ChangedFilesIntended []string `json:"changed_files_intended"`
		TestsRun             []string `json:"tests_run"`
		Risks                []string `json:"risks"`
		FollowUp             []string `json:"follow_up"`
		Confidence           string   `json:"confidence"`
		// Tolerate the writer / competitor prompt's "selfReportedSuccess"
		// even though the schema doesn't ask for it; agents have been known
		// to add extra fields and we don't want strict decode to drop the
		// whole block.
		SelfReportedSuccess *bool `json:"self_reported_success,omitempty"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("%w: AWO_RESULT_JSON: %v", ErrParseWarning, err)
	}
	notes := mergeNotes(p.TestsRun, p.FollowUp, p.Confidence)
	return &domain.ParsedAgentResult{
		Summary:             strings.TrimSpace(p.Summary),
		FilesTouched:        cleanList(p.ChangedFilesIntended),
		SelfReportedSuccess: p.SelfReportedSuccess,
		Notes:               notes,
	}, nil
}

// ParseReviewResult extracts the last AWO_REVIEW_JSON block from stdout.
// Returns (nil, nil) when no block is present. Returns (nil, warning)
// when a block exists but is malformed.
func ParseReviewResult(stdout string) (*ParsedReviewResult, error) {
	raw, found, malformed := extractBlock(stdout, "AWO_REVIEW_JSON")
	if !found {
		return nil, nil
	}
	if malformed {
		return nil, fmt.Errorf("%w: AWO_REVIEW_JSON: could not extract JSON object", ErrParseWarning)
	}
	var p ParsedReviewResult
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, fmt.Errorf("%w: AWO_REVIEW_JSON: %v", ErrParseWarning, err)
	}
	p.Blocking = cleanList(p.Blocking)
	p.NonBlocking = cleanList(p.NonBlocking)
	p.SuggestedTests = cleanList(p.SuggestedTests)
	p.RiskSummary = strings.TrimSpace(p.RiskSummary)
	p.Recommendation = strings.TrimSpace(p.Recommendation)
	return &p, nil
}

// fenceRe matches a fenced markdown block, optionally tagged with a
// language identifier.
var fenceRe = regexp.MustCompile("(?s)```[a-zA-Z0-9_+-]*\\n(.*?)```")

// extractBlock scans stdout for the last block whose contents include
// the given marker (e.g. "AWO_RESULT_JSON") and returns the JSON object
// within. The block may be wrapped in markdown fences; the marker may
// appear on its own line above the JSON body. The returned string is
// the substring from the first '{' through its matching '}'.
//
// found is true when at least one candidate containing the marker was
// located. malformed is true when a candidate existed but no balanced
// JSON object could be extracted — callers surface that as a warning
// rather than silently treating the block as missing.
func extractBlock(stdout, marker string) (raw string, found, malformed bool) {
	candidates := collectCandidates(stdout, marker)
	if len(candidates) == 0 {
		return "", false, false
	}
	last := candidates[len(candidates)-1]
	body := stripMarker(last, marker)
	obj, ok := extractFirstJSONObject(body)
	if !ok {
		return "", true, true
	}
	return obj, true, false
}

func collectCandidates(stdout, marker string) []string {
	var out []string
	for _, m := range fenceRe.FindAllStringSubmatch(stdout, -1) {
		body := m[1]
		if strings.Contains(body, marker) {
			out = append(out, body)
		}
	}
	if len(out) > 0 {
		return out
	}
	// No fenced blocks contained the marker. Fall back to scanning for
	// the marker in the raw text and treating the surrounding lines as
	// the candidate.
	idx := 0
	for {
		i := strings.Index(stdout[idx:], marker)
		if i < 0 {
			break
		}
		out = append(out, stdout[idx+i:])
		idx += i + len(marker)
	}
	return out
}

func stripMarker(s, marker string) string {
	if i := strings.Index(s, marker); i >= 0 {
		return s[i+len(marker):]
	}
	return s
}

// extractFirstJSONObject returns the substring from the first '{' to its
// matching '}', accounting for braces inside strings.
func extractFirstJSONObject(s string) (string, bool) {
	start := strings.Index(s, "{")
	if start < 0 {
		return "", false
	}
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inStr {
			switch c {
			case '\\':
				escape = true
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], true
			}
		}
	}
	return "", false
}

func cleanList(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeNotes(tests []string, followUp []string, confidence string) []string {
	var notes []string
	for _, t := range cleanList(tests) {
		notes = append(notes, "tests_run: "+t)
	}
	for _, f := range cleanList(followUp) {
		notes = append(notes, "follow_up: "+f)
	}
	if c := strings.TrimSpace(confidence); c != "" {
		notes = append(notes, "confidence: "+c)
	}
	return notes
}
