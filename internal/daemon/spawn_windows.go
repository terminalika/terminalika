//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

const detachedProcess = 0x00000008

// detach starts the child without a console window of its own and outside
// the parent's process group, so it outlives the window that spawned it.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess,
		HideWindow:    true,
	}
}
