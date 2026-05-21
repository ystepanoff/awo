// Package artifacts persists structured per-run artifacts under
// <ArtifactDir>/<run-id>/. Run records are typed as domain.RunReport so the
// schema is shared across orchestrator, reports, and the CLI.
package artifacts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/awo-dev/awo/internal/domain"
)

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

// WriteReport serializes a RunReport to <id>/run.json.
func (s Store) WriteReport(r domain.RunReport) error {
	d, err := s.Dir(r.RunID)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d, "run.json"), b, 0o644)
}
