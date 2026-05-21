package execx

import "regexp"

// redactionPlaceholder is the token written in place of detected secrets.
const redactionPlaceholder = "[REDACTED]"

// First-pass secret patterns. This is a safety net, not a credential
// scanner — keep the list short and avoid over-fitting.
var redactRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	// KEY=VALUE pairs. Replace just the value so the variable name
	// remains greppable.
	{regexp.MustCompile(`(?i)(OPENAI_API_KEY|ANTHROPIC_API_KEY|GITHUB_TOKEN)=\S+`), "${1}=" + redactionPlaceholder},
	// HTTP bearer tokens.
	{regexp.MustCompile(`(?i)(Authorization:\s*Bearer)\s+\S+`), "${1} " + redactionPlaceholder},
	// Provider-prefixed tokens.
	{regexp.MustCompile(`sk-[A-Za-z0-9_\-]{8,}`), redactionPlaceholder},
	{regexp.MustCompile(`ghp_[A-Za-z0-9]{8,}`), redactionPlaceholder},
	{regexp.MustCompile(`xoxb-[A-Za-z0-9-]{8,}`), redactionPlaceholder},
	{regexp.MustCompile(`npm_[A-Za-z0-9]{8,}`), redactionPlaceholder},
}

// Redact returns s with obvious secret-like substrings replaced by the
// redaction placeholder.
func Redact(s string) string {
	for _, r := range redactRules {
		s = r.re.ReplaceAllString(s, r.repl)
	}
	return s
}
