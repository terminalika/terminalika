// Package autostart removes the login entry earlier versions registered for
// the background process (`terminalika daemon`), which no longer exists:
// terminalika is a game launcher, not a notification service. Each desktop's
// stock mechanism is covered so an upgrade leaves nothing behind:
//
//   - Linux/BSD: the XDG autostart entry (~/.config/autostart/terminalika.desktop);
//   - macOS: the launchd user agent (~/Library/LaunchAgents/dev.terminalika.daemon.plist);
//   - Windows: the HKCU ...\CurrentVersion\Run value, via reg.exe.
package autostart

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// name is the entry's identifier on every platform.
const name = "terminalika"

// launchdLabel is the macOS agent's label.
const launchdLabel = "dev.terminalika.daemon"

// Remove unregisters the login entry. A missing entry is not an error.
func Remove() error {
	switch runtime.GOOS {
	case "darwin":
		return removeLaunchd()
	case "windows":
		return removeWindows()
	default:
		return removeFile(xdgPath())
	}
}

// Path is where the entry lives, for status lines and docs.
func Path() string {
	switch runtime.GOOS {
	case "darwin":
		return launchdPath()
	case "windows":
		return windowsKey + `\` + name
	default:
		return xdgPath()
	}
}

// --- Linux / BSD: XDG autostart ---

func xdgPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "autostart", name+".desktop")
}

// --- macOS: launchd user agent ---

func launchdPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", launchdLabel+".plist")
}

func removeLaunchd() error {
	if _, err := os.Stat(launchdPath()); err == nil {
		_ = exec.Command("launchctl", "unload", "-w", launchdPath()).Run()
	}
	return removeFile(launchdPath())
}

// --- Windows: HKCU Run key ---

const windowsKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func removeWindows() error {
	out, err := exec.Command("reg", "delete", windowsKey, "/v", name, "/f").CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "unable to find") {
		return fmt.Errorf("reg delete: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- helpers ---

func removeFile(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
