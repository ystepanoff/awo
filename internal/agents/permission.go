package agents

import "strings"

// PermissionFailure describes a structured reason why an agent run
// could not proceed because the CLI demanded an interactive approval
// AWO is not allowed (or willing) to grant.
//
// The orchestrator turns one of these into a "permission_required"
// failure on the AgentRunResult so the proof pack and recommendation
// can guide the operator toward a fix instead of misclassifying the
// run as "ready for human review".
type PermissionFailure struct {
	// Pattern is the lower-case substring that fired (kept verbatim
	// for diagnostic transparency).
	Pattern string
	// Sample is the original line containing Pattern, trimmed of
	// surrounding whitespace.
	Sample string
	// Source is "stdout" or "stderr".
	Source string
}

// permissionPatterns are the case-insensitive substrings AWO treats
// as evidence the agent CLI tried to ask for an interactive approval
// it could not get. They are deliberately worded as fragments instead
// of regex so a future CLI message rewording is more likely to still
// match. Update conservatively — false positives downgrade an agent
// run to needs_human_attention even when nothing is actually wrong.
var permissionPatterns = []string{
	"permission required",
	"approval required",
	"requires approval",
	"cannot proceed without approval",
	"not allowed",
	"permission denied",
	"sandbox denied",
	"operation not permitted",
	"tool use denied",
	"user approval",
	"approval prompt",
	"interactive approval",
	"blocked by sandbox",
	"not permitted in non-interactive mode",
}

// DetectPermissionFailure scans stdout / stderr for a known
// permission-denial fragment. Returns nil when nothing matches.
//
// The check is case-insensitive and substring-based: the patterns
// above are compared against a lower-cased copy of each line. The
// first match wins; the original (untransformed) line is returned in
// Sample so the proof pack can quote it verbatim.
//
// Both inputs are inspected so an agent that complains on either
// stream is detected. stderr is checked first because most CLIs
// surface permission errors there.
func DetectPermissionFailure(stdout, stderr string) *PermissionFailure {
	if f := scanForPermission(stderr, "stderr"); f != nil {
		return f
	}
	if f := scanForPermission(stdout, "stdout"); f != nil {
		return f
	}
	return nil
}

func scanForPermission(blob, source string) *PermissionFailure {
	if blob == "" {
		return nil
	}
	for _, raw := range strings.Split(blob, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		for _, p := range permissionPatterns {
			if strings.Contains(low, p) {
				return &PermissionFailure{
					Pattern: p,
					Sample:  line,
					Source:  source,
				}
			}
		}
	}
	return nil
}
