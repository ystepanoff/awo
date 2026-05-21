//go:build windows

package execx

import "os"

func extractSignal(state *os.ProcessState) string {
	// Windows does not expose POSIX-style termination signals; leave empty.
	_ = state
	return ""
}
