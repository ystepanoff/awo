package safety

import (
	"path/filepath"
	"sort"
	"strings"
)

// ProtectedPathMatch records a single changed file that matched at least
// one protected-path pattern. Patterns is the deduplicated list of
// patterns that flagged the path, in the order they were configured.
type ProtectedPathMatch struct {
	Path     string   `json:"path"`
	Patterns []string `json:"patterns"`
}

// SafetyFinding is the result of a max-changed-files check. Limit == 0
// means "unlimited"; in that case ExceedsLimit is always false.
type SafetyFinding struct {
	ChangedFiles []string `json:"changedFiles"`
	Count        int      `json:"count"`
	Limit        int      `json:"limit"`
	ExceedsLimit bool     `json:"exceedsLimit"`
}

// MatchProtectedPaths returns one ProtectedPathMatch per changed file
// that matches any of the given patterns. The output is deduplicated by
// path and sorted by path for deterministic rendering.
//
// Path normalization: every input path is converted to a slash-separated
// form via filepath.ToSlash + filepath.Clean before matching, so callers
// can pass either OS-native or git-style paths.
func MatchProtectedPaths(changedFiles []string, patterns []string) []ProtectedPathMatch {
	if len(changedFiles) == 0 || len(patterns) == 0 {
		return nil
	}
	// Dedup the patterns up front; matching against duplicates wastes
	// work and would surface duplicates in the .Patterns list.
	uniqPatterns := make([]string, 0, len(patterns))
	seenPat := map[string]struct{}{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, ok := seenPat[p]; ok {
			continue
		}
		seenPat[p] = struct{}{}
		uniqPatterns = append(uniqPatterns, p)
	}

	byPath := map[string]*ProtectedPathMatch{}
	for _, raw := range changedFiles {
		path := normalizePath(raw)
		if path == "" {
			continue
		}
		var hit *ProtectedPathMatch
		for _, pat := range uniqPatterns {
			if !matchProtectedPattern(pat, path) {
				continue
			}
			if hit == nil {
				if existing, ok := byPath[path]; ok {
					hit = existing
				} else {
					hit = &ProtectedPathMatch{Path: path}
					byPath[path] = hit
				}
			}
			if !contains(hit.Patterns, pat) {
				hit.Patterns = append(hit.Patterns, pat)
			}
		}
	}
	if len(byPath) == 0 {
		return nil
	}
	out := make([]ProtectedPathMatch, 0, len(byPath))
	for _, m := range byPath {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// CountChangedFiles returns the number of unique, non-empty changed
// files (after path normalization). Empty / whitespace entries and
// duplicates are dropped so the count is comparable across callers.
func CountChangedFiles(changedFiles []string) int {
	if len(changedFiles) == 0 {
		return 0
	}
	seen := map[string]struct{}{}
	n := 0
	for _, f := range changedFiles {
		p := normalizePath(f)
		if p == "" {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		n++
	}
	return n
}

// CheckMaxChangedFiles compares the number of changed files against
// max. A max of 0 (or negative) is treated as "no limit": ExceedsLimit
// is always false in that case, but the count and file list are still
// populated for proof-pack rendering.
func CheckMaxChangedFiles(changedFiles []string, max int) SafetyFinding {
	count := CountChangedFiles(changedFiles)
	out := SafetyFinding{
		ChangedFiles: append([]string(nil), changedFiles...),
		Count:        count,
		Limit:        max,
	}
	if max > 0 && count > max {
		out.ExceedsLimit = true
	}
	return out
}

// ----- glob engine --------------------------------------------------------
//
// The supported pattern dialect is the small subset we actually need:
//
//   *      — matches any run of characters within a single path segment
//   **     — matches zero or more path segments (including none)
//   ?      — matches a single non-separator character
//   prefix/path — anchored at the path root
//
// Patterns that contain none of those metacharacters and end in "/" are
// treated as directory prefixes for backward compatibility with the
// pre-existing IsProtectedPath behavior. Patterns without metacharacters
// and without a "/" are matched against the basename or as a literal
// suffix, which keeps the legacy "go.mod" / "Makefile" defaults working.

func matchProtectedPattern(pattern, path string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}

	// Trailing-slash directory prefix: legacy form, plus a natural way
	// to spell "everything under this dir". ".github/" matches both
	// ".github/x" and "any/sub/.github/x".
	if !containsGlobMeta(pattern) && strings.HasSuffix(pattern, "/") {
		needle := pattern
		if strings.HasPrefix(path+"/", needle) {
			return true
		}
		if strings.Contains("/"+path+"/", "/"+needle) {
			return true
		}
		return false
	}

	// No glob metacharacters and no slash → match against basename or
	// allow as a literal trailing path segment. Keeps "go.mod" working.
	if !containsGlobMeta(pattern) && !strings.Contains(pattern, "/") {
		base := filepath.Base(path)
		if base == pattern {
			return true
		}
		if path == pattern || strings.HasSuffix(path, "/"+pattern) {
			return true
		}
		return false
	}

	// Otherwise: a glob. We anchor against the full path; if the glob
	// has no leading "**/" we still allow a basename-only pattern (e.g.
	// "*.pem") to match anywhere by walking suffixes.
	if globMatch(pattern, path) {
		return true
	}
	if !strings.Contains(pattern, "/") {
		// Bare basename glob like "*.pem" — also match the basename.
		if globMatch(pattern, filepath.Base(path)) {
			return true
		}
	}
	return false
}

// globMatch matches pattern against path with support for *, ?, and
// **. The matching is anchored: the entire path must match.
func globMatch(pattern, path string) bool {
	patSegs := splitSegs(pattern)
	pathSegs := splitSegs(path)
	return matchSegments(patSegs, pathSegs)
}

// matchSegments matches a sequence of pattern segments against a
// sequence of path segments. "**" is a wildcard that consumes one or
// more path segments — that's the gitignore-style interpretation that
// matches user expectations: "auth/**" matches things under auth/, not
// auth itself. A leading "**" (e.g. "**/*secret*") is allowed to
// consume zero or more segments so it can match at the repo root.
func matchSegments(pat, path []string) bool {
	for i, p := range pat {
		if p == "**" {
			rest := pat[i+1:]
			minSplit := 1
			if i == 0 {
				minSplit = 0
			}
			for j := minSplit; j <= len(path); j++ {
				if matchSegments(rest, path[j:]) {
					return true
				}
			}
			return false
		}
		if len(path) == 0 {
			return false
		}
		if !matchSegmentGlob(p, path[0]) {
			return false
		}
		path = path[1:]
	}
	return len(path) == 0
}

// matchSegmentGlob matches a single pattern segment against a single
// path segment. "*" matches any run of characters within the segment;
// "?" matches a single character. Backslash escapes are not honored —
// path segments don't typically need them.
func matchSegmentGlob(pat, seg string) bool {
	// Fast path: no metacharacters → exact match.
	if !strings.ContainsAny(pat, "*?") {
		return pat == seg
	}
	// Two-pointer with backtracking for "*". This is the standard
	// non-recursive glob matcher.
	pi, si := 0, 0
	starPi, starSi := -1, 0
	for si < len(seg) {
		switch {
		case pi < len(pat) && pat[pi] == '*':
			starPi = pi
			starSi = si
			pi++
		case pi < len(pat) && (pat[pi] == '?' || pat[pi] == seg[si]):
			pi++
			si++
		case starPi != -1:
			pi = starPi + 1
			starSi++
			si = starSi
		default:
			return false
		}
	}
	for pi < len(pat) && pat[pi] == '*' {
		pi++
	}
	return pi == len(pat)
}

// ----- helpers ------------------------------------------------------------

func splitSegs(p string) []string {
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}

func containsGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?")
}

func normalizePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.ToSlash(p)
	p = filepath.Clean(p)
	p = strings.TrimPrefix(p, "./")
	if p == "." {
		return ""
	}
	return p
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}
