// Package safety enforces the boundary between AWO and the user's repo.
//
// The most important invariant: AWO must never read, write, or delete
// outside the .awo/worktrees tree it owns. These helpers exist to make
// that invariant easy to enforce at every call site.
package safety

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrOutsideRoot is returned when a path escapes the allowed root.
var ErrOutsideRoot = errors.New("safety: path is outside allowed root")

// EnsureInside returns a cleaned absolute path if target resolves inside
// root. Otherwise it returns ErrOutsideRoot.
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

// IsSubpath reports whether child is strictly contained inside parent.
// Equal paths return false — a directory is not a sub-path of itself.
// Both paths are resolved with filepath.Abs before comparison.
func IsSubpath(parent, child string) bool {
	p, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	c, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	p = filepath.Clean(p)
	c = filepath.Clean(c)
	if p == c {
		return false
	}
	rel, err := filepath.Rel(p, c)
	if err != nil {
		return false
	}
	if rel == "." || rel == ".." {
		return false
	}
	if strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if filepath.IsAbs(rel) {
		return false
	}
	return true
}

// MustBeUnder returns nil if child is strictly under parent, else an error.
func MustBeUnder(parent, child string) error {
	if !IsSubpath(parent, child) {
		return fmt.Errorf("%w: %q not under %q", ErrOutsideRoot, child, parent)
	}
	return nil
}

// SafeJoin joins parts onto base, rejecting empty components, absolute
// components, and any "..": a structural guard against path traversal in
// caller-supplied identifiers like run ids or agent names.
func SafeJoin(base string, parts ...string) (string, error) {
	if strings.TrimSpace(base) == "" {
		return "", errors.New("safety: SafeJoin: empty base")
	}
	if len(parts) == 0 {
		return "", errors.New("safety: SafeJoin: at least one path component required")
	}
	for _, p := range parts {
		if strings.TrimSpace(p) == "" {
			return "", errors.New("safety: SafeJoin: empty path component")
		}
		if filepath.IsAbs(p) {
			return "", fmt.Errorf("safety: SafeJoin: absolute path component %q", p)
		}
		for _, seg := range strings.Split(filepath.ToSlash(p), "/") {
			if seg == ".." {
				return "", fmt.Errorf("safety: SafeJoin: parent traversal in %q", p)
			}
		}
	}
	joined := filepath.Join(append([]string{base}, parts...)...)
	if err := MustBeUnder(base, joined); err != nil {
		return "", err
	}
	return joined, nil
}

// IsProtectedPath reports whether the given path matches any of the
// configured protected patterns. Patterns are interpreted by the glob
// engine in protected.go: "*" within a single path segment, "**" across
// segments, "?" for a single character, trailing-slash directory
// prefixes, and bare names (matched against basename or as a path
// suffix). Path normalization is identical to MatchProtectedPaths.
func IsProtectedPath(path string, patterns []string) bool {
	clean := normalizePath(path)
	if clean == "" {
		return false
	}
	for _, p := range patterns {
		if matchProtectedPattern(p, clean) {
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
