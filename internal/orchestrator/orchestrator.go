// Package orchestrator wires modes (single, writer-reviewer, competitive)
// to agents and verification. The MVP exposes only the mode-resolution API;
// the run loop lands with the `awo run` command.
package orchestrator

import (
	"errors"
	"fmt"

	"github.com/awo-dev/awo/internal/domain"
)

// ResolveMode validates a user-supplied mode string. If s is empty, the
// fallback is used. An empty fallback with empty s is an error.
func ResolveMode(s string, fallback domain.RunMode) (domain.RunMode, error) {
	if s == "" {
		if fallback == "" {
			return "", errors.New("orchestrator: no mode supplied and no fallback")
		}
		s = string(fallback)
	}
	m := domain.RunMode(s)
	if err := m.Validate(); err != nil {
		return "", fmt.Errorf("orchestrator: %w", err)
	}
	return m, nil
}
