// Package artifacts persists structured per-run artifacts under
// .awo/runs/<run-id>/. The MVP defines the schema and write helpers; the
// orchestrator will populate them as agents and verification execute.
package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/awo-dev/awo/internal/execx"
)

// Run is the top-level run record written to run.json.
type Run struct {
	ID         string            `json:"id"`
	Mode       string            `json:"mode"`
	StartedAt  time.Time         `json:"startedAt"`
	FinishedAt time.Time         `json:"finishedAt,omitempty"`
	Status     string            `json:"status"`
	Worktree   string            `json:"worktree,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Agents     []AgentRecord     `json:"agents,omitempty"`
	Verifies   []execx.Result    `json:"verifies,omitempty"`
	Notes      []string          `json:"notes,omitempty"`
	Meta       map[string]string `json:"meta,omitempty"`
}

// AgentRecord captures a single agent invocation within a run.
type AgentRecord struct {
	Name   string        `json:"name"`
	Kind   string        `json:"kind"`
	Role   string        `json:"role,omitempty"`
	Result execx.Result  `json:"result"`
}

// Store writes artifacts under a fixed root (typically .awo/runs).
type Store struct {
	Root string
}

// Dir returns the directory for a given run id, creating it if needed.
func (s Store) Dir(id string) (string, error) {
	d := filepath.Join(s.Root, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", fmt.Errorf("artifacts: mkdir: %w", err)
	}
	return d, nil
}

// WriteRun serializes a Run record to <id>/run.json.
func (s Store) WriteRun(r Run) error {
	d, err := s.Dir(r.ID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "run.json"), b, 0o644)
}
