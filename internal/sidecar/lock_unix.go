//go:build !windows

package sidecar

import (
	"os"
	"syscall"
)

// lockFile takes a non-blocking exclusive advisory lock on the open file.
// On non-Windows platforms this is flock(2); Windows uses LockFileEx
// (see lock_windows.go).
func lockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}
