//go:build windows

package sidecar

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes a non-blocking exclusive lock using LockFileEx, the Windows
// equivalent of flock(2). It locks the whole file (length 0 means "to EOF"
// for the first byte here) and fails immediately if already held.
func lockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, // reserved
		1, // lock 1 byte
		0,
		&ol,
	)
}

func unlockFile(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
