//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach puts the child in its own session so it outlives the terminal
// and window that spawned it and never receives their signals.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
