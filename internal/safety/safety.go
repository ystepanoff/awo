// Package safety enforces the boundary between AWO and the user's repo.
//
// The most important invariant: AWO must never read, write, or delete
// outside the .awo/worktrees tree it owns. These helpers exist to make
// that invariant easy to enforce at every call site.
package safety

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrOutsideRoot is returned when a path escapes the allowed root.
var ErrOutsideRoot = errors.New("safety: path is outside allowed root")

// EnsureInside returns a cleaned absolute path if target resolves inside
// root. Otherwise it returns ErrOutsideRoot. Both root and target are
// expected to be absolute or resolvable relative to the same base.
func EnsureInside(root, target string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrOutsideRoot
	}
	if filepath.IsAbs(rel) {
		return "", ErrOutsideRoot
	}
	return absTarget, nil
}

// IsProtectedPath reports whether the given path matches any of the
// configured protected glob/prefix patterns. Patterns ending in "/" are
// treated as directory prefixes; others are matched with filepath.Match
// against the path's basename and tested as a literal suffix.
func IsProtectedPath(path string, patterns []string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/") {
			if strings.HasPrefix(clean+"/", p) || strings.Contains(clean+"/", "/"+p) {
				return true
			}
			continue
		}
		base := filepath.Base(clean)
		if ok, _ := filepath.Match(p, base); ok {
			return true
		}
		if clean == p || strings.HasSuffix(clean, "/"+p) {
			return true
		}
	}
	return false
}

// Redact returns s with substrings matching any of the given regexp
// patterns replaced by "[REDACTED]". Invalid patterns are skipped.
func Redact(s string, patterns []string) string {
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
