// Package orchestrator wires modes (single, writer-reviewer, competitive)
// to agents and verification. The MVP exposes only the mode selection API
// and stubs for the run loop; concrete execution lands in subsequent steps.
package orchestrator

import (
	"errors"

	"github.com/awo-dev/awo/internal/config"
)

// Mode is re-exported for callers that import only this package.
type Mode = config.Mode

// ResolveMode validates a user-supplied mode string against config.
func ResolveMode(s string, fallback config.Mode) (config.Mode, error) {
	if s == "" {
		if fallback == "" {
			return "", errors.New("orchestrator: no mode supplied and no default configured")
		}
		s = string(fallback)
	}
	m := config.Mode(s)
	switch m {
	case config.ModeSingle, config.ModeWriterReviewer, config.ModeCompetitive:
		return m, nil
	default:
		return "", errors.New("orchestrator: unknown mode " + string(m))
	}
}
