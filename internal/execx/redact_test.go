package execx

import (
	"strings"
	"testing"
)

func TestRedactKeyValue(t *testing.T) {
	cases := []string{
		"OPENAI_API_KEY=sk-abc123def456ghi",
		"ANTHROPIC_API_KEY=sk-ant-api03-abcdef",
		"GITHUB_TOKEN=ghp_abcdef1234567890",
	}
	for _, in := range cases {
		out := Redact(in)
		if strings.Contains(out, "sk-") || strings.Contains(out, "ghp_") {
			t.Errorf("redact(%q) leaked: %q", in, out)
		}
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("redact(%q) missing placeholder: %q", in, out)
		}
		// The variable name should remain so logs stay greppable.
		head := strings.SplitN(in, "=", 2)[0]
		if !strings.Contains(out, head+"=") {
			t.Errorf("redact(%q) dropped variable name: %q", in, out)
		}
	}
}

func TestRedactBearerToken(t *testing.T) {
	in := "Authorization: Bearer abcdef.1234567890.token"
	out := Redact(in)
	if strings.Contains(out, "abcdef.1234567890.token") {
		t.Fatalf("redact leaked token: %q", out)
	}
	if !strings.Contains(out, "Authorization: Bearer [REDACTED]") {
		t.Fatalf("unexpected redaction: %q", out)
	}
}

func TestRedactProviderPrefixedTokens(t *testing.T) {
	cases := map[string]string{
		"value=sk-aBcDeF1234567":   "sk-",
		"X ghp_abcdefghij Y":       "ghp_",
		"slack=xoxb-1111-2222-aaa": "xoxb-",
		"env npm_aBcDeFgHiJ token": "npm_",
	}
	for in, prefix := range cases {
		out := Redact(in)
		if strings.Contains(out, prefix+"a") || strings.Contains(out, prefix+"1") {
			t.Errorf("redact(%q) leaked prefix=%q output=%q", in, prefix, out)
		}
		if !strings.Contains(out, "[REDACTED]") {
			t.Errorf("redact(%q) missing placeholder: %q", in, out)
		}
	}
}

// Real CLI flags that share a prefix-shaped substring (e.g.
// `--ask-for-approval` contains `sk-for-approval`) must not be
// touched, otherwise log lines from agent CLIs become unreadable.
func TestRedactDoesNotEatCliFlags(t *testing.T) {
	cases := []string{
		"error: unexpected argument '--ask-for-approval' found",
		"codex exec --sandbox read-only --ask-for-approval never",
		"--no-skip-checks",
	}
	for _, in := range cases {
		if out := Redact(in); out != in {
			t.Errorf("Redact mutated CLI flag line: %q -> %q", in, out)
		}
	}
}

func TestRedactPreservesUnrelatedText(t *testing.T) {
	in := "ok status=200 path=/healthz"
	if out := Redact(in); out != in {
		t.Fatalf("redact mutated unrelated text: %q -> %q", in, out)
	}
}

func TestRedactCaseInsensitiveKey(t *testing.T) {
	out := Redact("openai_api_key=sk-shouldhide123")
	if strings.Contains(out, "sk-shouldhide123") {
		t.Fatalf("case-insensitive match failed: %q", out)
	}
}
