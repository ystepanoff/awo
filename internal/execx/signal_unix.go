//go:build !windows

package execx

import (
	"os"
	"syscall"
)

func extractSignal(state *os.ProcessState) string {
	if state == nil {
		return ""
	}
	ws, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return ""
	}
	if ws.Signaled() {
		return ws.Signal().String()
	}
	return ""
}
