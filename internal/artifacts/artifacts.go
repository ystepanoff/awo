// Package artifacts owns the on-disk layout for a single AWO run.
//
// Every path returned by a Layout is anchored at <repo-root>/<artifact-dir>/
// <run-id> and is enforced by safety.MustBeUnder so callers cannot escape the
// run root by accident. Writers should go through WriteJSONAtomic /
// WriteFileAtomic to get crash-safe rename semantics.
package artifacts

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/awo-dev/awo/internal/runid"
	"github.com/awo-dev/awo/internal/safety"
)

// Layout describes the artifact directory tree for one run.
//
// Root is always absolute; all other paths returned by Layout methods are
// strictly under Root.
type Layout struct {
	RepoRoot    string
	ArtifactDir string
	RunID       string
	Root        string
}

// NewLayout validates inputs and returns a Layout rooted at
// <repoRoot>/<artifactDir>/<runID>.
//
// repoRoot must be a non-empty path. artifactDir is the directory holding
// per-run artifacts (typically ".awo/runs") and may be relative to the repo
// root. runID must be filesystem-safe (matches runid.Pattern).
func NewLayout(repoRoot, artifactDir, runID string) (*Layout, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return nil, errors.New("artifacts: empty repoRoot")
	}
	if strings.TrimSpace(artifactDir) == "" {
		return nil, errors.New("artifacts: empty artifactDir")
	}
	if err := runid.Validate(runID); err != nil {
		return nil, fmt.Errorf("artifacts: %w", err)
	}

	absRepo, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("artifacts: abs repoRoot: %w", err)
	}

	var artifactBase string
	if filepath.IsAbs(artifactDir) {
		artifactBase = filepath.Clean(artifactDir)
	} else {
		joined, jerr := safety.SafeJoin(absRepo, artifactDir)
		if jerr != nil {
			return nil, fmt.Errorf("artifacts: artifactDir: %w", jerr)
		}
		artifactBase = joined
	}

	root, err := safety.SafeJoin(artifactBase, runID)
	if err != nil {
		return nil, fmt.Errorf("artifacts: derive run root: %w", err)
	}

	return &Layout{
		RepoRoot:    absRepo,
		ArtifactDir: artifactBase,
		RunID:       runID,
		Root:        root,
	}, nil
}

// Ensure creates Root and the standard subdirectories (agents/, verify/).
// It is safe to call multiple times.
func (l *Layout) Ensure() error {
	for _, d := range []string{l.Root, filepath.Join(l.Root, "agents"), filepath.Join(l.Root, "verify")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("artifacts: mkdir %s: %w", d, err)
		}
	}
	return nil
}

// AgentDir returns the directory for one agent's artifacts:
// <root>/agents/<agent>-<role>.
func (l *Layout) AgentDir(agent, role string) string {
	return filepath.Join(l.Root, "agents", agent+"-"+role)
}

// VerificationDir returns <root>/verify/NNN where NNN is index zero-padded
// to three digits.
func (l *Layout) VerificationDir(index int) string {
	return filepath.Join(l.Root, "verify", fmt.Sprintf("%03d", index))
}

// RunJSONPath returns <root>/run.json.
func (l *Layout) RunJSONPath() string { return filepath.Join(l.Root, "run.json") }

// ProofPackPath returns <root>/proof-pack.md.
func (l *Layout) ProofPackPath() string { return filepath.Join(l.Root, "proof-pack.md") }

// SummaryPath returns <root>/summary.md.
func (l *Layout) SummaryPath() string { return filepath.Join(l.Root, "summary.md") }

// DiffPatchPath returns <root>/diff.patch.
func (l *Layout) DiffPatchPath() string { return filepath.Join(l.Root, "diff.patch") }

// ComparisonPath returns <root>/comparison.md.
func (l *Layout) ComparisonPath() string { return filepath.Join(l.Root, "comparison.md") }

// EnsureAgentDir creates and returns the agent directory.
func (l *Layout) EnsureAgentDir(agent, role string) (string, error) {
	d := l.AgentDir(agent, role)
	if err := l.guard(d); err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("artifacts: mkdir agent dir: %w", err)
	}
	return d, nil
}

// EnsureVerificationDir creates and returns the verification directory for index.
func (l *Layout) EnsureVerificationDir(index int) (string, error) {
	d := l.VerificationDir(index)
	if err := l.guard(d); err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("artifacts: mkdir verify dir: %w", err)
	}
	return d, nil
}

// WriteJSONAtomic encodes v as pretty-printed JSON and writes it to path
// atomically (temp file + rename). path must be inside the run root.
func (l *Layout) WriteJSONAtomic(path string, v any) error {
	if err := l.guard(path); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("artifacts: marshal json: %w", err)
	}
	b = append(b, '\n')
	return writeFileAtomic(path, b, 0o644)
}

// WriteFileAtomic writes data to path atomically (temp file + rename).
// path must be inside the run root.
func (l *Layout) WriteFileAtomic(path string, data []byte, mode os.FileMode) error {
	if err := l.guard(path); err != nil {
		return err
	}
	return writeFileAtomic(path, data, mode)
}

func (l *Layout) guard(path string) error {
	if err := safety.MustBeUnder(l.Root, path); err != nil {
		return fmt.Errorf("artifacts: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to path via a sibling temp file and a rename.
// The parent directory is created if needed. On error the temp file is
// removed.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("artifacts: mkdir parent: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".awo-write-*")
	if err != nil {
		return fmt.Errorf("artifacts: create temp: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("artifacts: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("artifacts: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("artifacts: close temp: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		cleanup()
		return fmt.Errorf("artifacts: chmod temp: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return fmt.Errorf("artifacts: rename: %w", err)
	}
	return nil
}
