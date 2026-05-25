package safety

import (
	"reflect"
	"testing"
)

// ----- glob engine --------------------------------------------------------

func TestMatchProtectedPatternStarMatchesSingleSegment(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"*.pem", "server.pem", true},
		{"*.pem", "keys/server.pem", true}, // bare basename glob walks suffixes
		{"keys/*.pem", "keys/server.pem", true},
		{"keys/*.pem", "keys/sub/server.pem", false}, // * does not cross /
		{"keys/*.pem", "other/server.pem", false},
		{"*.go", "main.go", true},
		{"*.go", "main_test.go", true},
		{"*.go", "main.txt", false},
	}
	for _, c := range cases {
		if got := matchProtectedPattern(c.pattern, c.path); got != c.want {
			t.Errorf("matchProtectedPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchProtectedPatternDoubleStarSpansSegments(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"auth/**", "auth/login.go", true},
		{"auth/**", "auth/sub/dir/handler.go", true},
		{"auth/**", "auth", false}, // ** must consume something after /
		{"auth/**", "authentication/x.go", false},
		{"**/secrets.json", "secrets.json", true},
		{"**/secrets.json", "config/secrets.json", true},
		{"**/secrets.json", "deep/nested/path/secrets.json", true},
		{"**/secrets.json", "secrets.json.bak", false},
		{".github/workflows/**", ".github/workflows/ci.yml", true},
		{".github/workflows/**", ".github/workflows/sub/ci.yml", true},
		{".github/workflows/**", ".github/x.yml", false},
		{"**/*secret*", "config/my-secret.json", true},
		{"**/*secret*", "src/secrets.go", true},
		{"**/*secret*", "src/secret", true},
		{"**/*secret*", "src/safe.go", false},
		{"**/.env*", ".env", true},
		{"**/.env*", ".env.local", true},
		{"**/.env*", "config/.env.production", true},
		{"**/.env*", "envoy.go", false},
	}
	for _, c := range cases {
		if got := matchProtectedPattern(c.pattern, c.path); got != c.want {
			t.Errorf("matchProtectedPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchProtectedPatternQuestionMark(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"?.go", "a.go", true},
		{"?.go", "ab.go", false},
		{"a?.go", "ab.go", true},
		{"a?.go", "abc.go", false},
	}
	for _, c := range cases {
		if got := matchProtectedPattern(c.pattern, c.path); got != c.want {
			t.Errorf("matchProtectedPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchProtectedPatternLegacyDirPrefix(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{".github/", ".github/x.yml", true},
		{".github/", "src/.github/x.yml", true},
		{".github/", "main.go", false},
		{"vendor/", "vendor/foo/bar.go", true},
		{"vendor/", "src/vendor/foo.go", true},
		{"vendor/", "vendoring.go", false},
	}
	for _, c := range cases {
		if got := matchProtectedPattern(c.pattern, c.path); got != c.want {
			t.Errorf("matchProtectedPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchProtectedPatternBareBasename(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"go.mod", "go.mod", true},
		{"go.mod", "src/go.mod", true},
		{"go.mod", "go.modular", false},
		{"Makefile", "Makefile", true},
		{"Makefile", "build/Makefile", true},
		{"Makefile", "Makefile.in", false},
	}
	for _, c := range cases {
		if got := matchProtectedPattern(c.pattern, c.path); got != c.want {
			t.Errorf("matchProtectedPattern(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMatchProtectedPatternEmptyOrWhitespacePatternIsNoOp(t *testing.T) {
	for _, p := range []string{"", "   ", "\t"} {
		if matchProtectedPattern(p, "anything.go") {
			t.Errorf("empty/whitespace pattern %q should not match", p)
		}
	}
}

// ----- MatchProtectedPaths ------------------------------------------------

func TestMatchProtectedPathsFindsAllPatterns(t *testing.T) {
	changed := []string{
		"auth/login.go",
		"auth/login_test.go",
		"server/health.go",
		"config/.env.production",
		"src/credential_helper.go",
	}
	patterns := []string{
		"auth/**",
		"**/.env*",
		"**/*credential*",
	}
	got := MatchProtectedPaths(changed, patterns)

	wantPaths := []string{"auth/login.go", "auth/login_test.go", "config/.env.production", "src/credential_helper.go"}
	if len(got) != len(wantPaths) {
		t.Fatalf("hits=%d want %d (%v)", len(got), len(wantPaths), got)
	}
	// Output is sorted by path.
	for i, want := range wantPaths {
		if got[i].Path != want {
			t.Errorf("hit[%d].Path=%q want %q", i, got[i].Path, want)
		}
		if len(got[i].Patterns) == 0 {
			t.Errorf("hit %q has no patterns", got[i].Path)
		}
	}
}

func TestMatchProtectedPathsRecordsAllMatchingPatterns(t *testing.T) {
	// A single file matches both globs; both should be recorded.
	got := MatchProtectedPaths(
		[]string{"auth/secret-config.go"},
		[]string{"auth/**", "**/*secret*"},
	)
	if len(got) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(got))
	}
	if !reflect.DeepEqual(got[0].Patterns, []string{"auth/**", "**/*secret*"}) {
		t.Errorf("patterns=%v want both", got[0].Patterns)
	}
}

func TestMatchProtectedPathsDeduplicatesByPath(t *testing.T) {
	got := MatchProtectedPaths(
		[]string{"auth/x.go", "auth/x.go", "auth/y.go"},
		[]string{"auth/**"},
	)
	if len(got) != 2 {
		t.Fatalf("expected 2 hits (deduped), got %d (%v)", len(got), got)
	}
}

func TestMatchProtectedPathsNoChangedOrNoPatterns(t *testing.T) {
	if got := MatchProtectedPaths(nil, []string{"auth/**"}); got != nil {
		t.Errorf("expected nil for empty changed, got %v", got)
	}
	if got := MatchProtectedPaths([]string{"a.go"}, nil); got != nil {
		t.Errorf("expected nil for empty patterns, got %v", got)
	}
}

func TestMatchProtectedPathsNormalizesInputPaths(t *testing.T) {
	// "./" prefixes and redundant separators should not prevent matches;
	// git already produces forward-slash POSIX paths so we don't worry
	// about backslash conversion here.
	got := MatchProtectedPaths(
		[]string{"./auth/login.go", "auth//handler.go"},
		[]string{"auth/**"},
	)
	wantPaths := []string{"auth/handler.go", "auth/login.go"}
	if len(got) != 2 {
		t.Fatalf("hits=%d want 2 (%v)", len(got), got)
	}
	for i, want := range wantPaths {
		if got[i].Path != want {
			t.Errorf("hit[%d].Path=%q want %q", i, got[i].Path, want)
		}
	}
}

// ----- CountChangedFiles --------------------------------------------------

func TestCountChangedFiles(t *testing.T) {
	cases := []struct {
		in   []string
		want int
	}{
		{nil, 0},
		{[]string{}, 0},
		{[]string{"a.go"}, 1},
		{[]string{"a.go", "b.go"}, 2},
		{[]string{"a.go", "a.go"}, 1},   // dedup
		{[]string{"a.go", "./a.go"}, 1}, // normalize
		{[]string{"a.go", "  ", ""}, 1}, // whitespace dropped
		{[]string{"src/a.go", "src/a.go"}, 1},
	}
	for i, c := range cases {
		if got := CountChangedFiles(c.in); got != c.want {
			t.Errorf("case %d: CountChangedFiles(%v)=%d want %d", i, c.in, got, c.want)
		}
	}
}

// ----- CheckMaxChangedFiles -----------------------------------------------

func TestCheckMaxChangedFilesUnderLimit(t *testing.T) {
	out := CheckMaxChangedFiles([]string{"a.go", "b.go"}, 5)
	if out.ExceedsLimit {
		t.Errorf("under-limit run flagged as exceeding")
	}
	if out.Count != 2 {
		t.Errorf("count=%d want 2", out.Count)
	}
	if out.Limit != 5 {
		t.Errorf("limit=%d want 5", out.Limit)
	}
}

func TestCheckMaxChangedFilesOverLimit(t *testing.T) {
	out := CheckMaxChangedFiles([]string{"a.go", "b.go", "c.go"}, 2)
	if !out.ExceedsLimit {
		t.Errorf("expected ExceedsLimit, got false")
	}
	if out.Count != 3 || out.Limit != 2 {
		t.Errorf("got count=%d limit=%d, want 3/2", out.Count, out.Limit)
	}
}

func TestCheckMaxChangedFilesAtLimitIsAllowed(t *testing.T) {
	out := CheckMaxChangedFiles([]string{"a.go", "b.go"}, 2)
	if out.ExceedsLimit {
		t.Errorf("count == limit should not exceed")
	}
}

func TestCheckMaxChangedFilesZeroOrNegativeMeansUnlimited(t *testing.T) {
	for _, max := range []int{0, -1} {
		out := CheckMaxChangedFiles([]string{"a.go", "b.go", "c.go", "d.go"}, max)
		if out.ExceedsLimit {
			t.Errorf("max=%d should be unlimited; got ExceedsLimit", max)
		}
		if out.Count != 4 {
			t.Errorf("count=%d want 4", out.Count)
		}
	}
}

// ----- IsProtectedPath integration ----------------------------------------

func TestIsProtectedPathUsesNewMatcher(t *testing.T) {
	patterns := []string{"auth/**", "**/*secret*"}
	if !IsProtectedPath("auth/login.go", patterns) {
		t.Error("auth/login.go should match auth/**")
	}
	if !IsProtectedPath("config/my-secret.json", patterns) {
		t.Error("my-secret.json should match **/*secret*")
	}
	if IsProtectedPath("server/health.go", patterns) {
		t.Error("server/health.go should not match")
	}
}
