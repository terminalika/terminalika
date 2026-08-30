// Package autostart registers `terminalika daemon` to start at login, using
// each desktop's stock mechanism so terminalika needs no extra tooling:
//
//   - Linux/BSD: an XDG autostart entry (~/.config/autostart/terminalika.desktop);
//   - macOS: a launchd user agent (~/Library/LaunchAgents/dev.terminalika.daemon.plist);
//   - Windows: a HKCU ...\CurrentVersion\Run value, via reg.exe.
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

// Supported reports whether this platform has an autostart mechanism here.
func Supported() bool {
	switch runtime.GOOS {
	case "linux", "freebsd", "openbsd", "netbsd", "darwin", "windows":
		return true
	}
	return false
}

// Install registers the running executable's `daemon` subcommand to start
// at login, replacing any previous entry (so a moved binary is fixed up by
// re-running setup).
func Install() error {
	exe, err := executable()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		return installLaunchd(exe)
	case "windows":
		return installWindows(exe)
	default:
		return installXDG(exe)
	}
}

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

// Installed reports whether a login entry is present.
func Installed() bool {
	switch runtime.GOOS {
	case "darwin":
		_, err := os.Stat(launchdPath())
		return err == nil
	case "windows":
		out, err := exec.Command("reg", "query", windowsKey, "/v", name).Output()
		return err == nil && strings.Contains(string(out), name)
	default:
		_, err := os.Stat(xdgPath())
		return err == nil
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

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return exe, nil
}

// --- Linux / BSD: XDG autostart ---

func xdgPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = filepath.Join(os.Getenv("HOME"), ".config")
	}
	return filepath.Join(dir, "autostart", name+".desktop")
}

func installXDG(exe string) error {
	entry := "[Desktop Entry]\n" +
		"Type=Application\n" +
		"Name=terminalika\n" +
		"Comment=AI agent notification hub (background listener)\n" +
		"Exec=" + desktopQuote(exe) + " daemon\n" +
		"Terminal=false\n" +
		"NoDisplay=true\n" +
		"X-GNOME-Autostart-enabled=true\n"
	return writeFile(xdgPath(), entry)
}

// desktopQuote quotes an Exec argument per the Desktop Entry spec.
func desktopQuote(s string) string {
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// --- macOS: launchd user agent ---

func launchdPath() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", launchdLabel+".plist")
}

func installLaunchd(exe string) error {
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>` + launchdLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + xmlEscape(exe) + `</string>
		<string>daemon</string>
	</array>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><false/>
</dict>
</plist>
`
	if err := writeFile(launchdPath(), plist); err != nil {
		return err
	}
	// Best effort: make it active now, not only at next login.
	_ = exec.Command("launchctl", "load", "-w", launchdPath()).Run()
	return nil
}

func removeLaunchd() error {
	if _, err := os.Stat(launchdPath()); err == nil {
		_ = exec.Command("launchctl", "unload", "-w", launchdPath()).Run()
	}
	return removeFile(launchdPath())
}

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// --- Windows: HKCU Run key ---

const windowsKey = `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`

func installWindows(exe string) error {
	// Launched through a hidden PowerShell so no console window sticks
	// around for a process that never draws anything.
	cmd := `powershell -NoProfile -WindowStyle Hidden -Command "& '` + strings.ReplaceAll(exe, "'", "''") + `' daemon"`
	out, err := exec.Command("reg", "add", windowsKey, "/v", name, "/t", "REG_SZ", "/d", cmd, "/f").CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeWindows() error {
	out, err := exec.Command("reg", "delete", windowsKey, "/v", name, "/f").CombinedOutput()
	if err != nil && !strings.Contains(strings.ToLower(string(out)), "unable to find") {
		return fmt.Errorf("reg delete: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- helpers ---

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0o644)
}

func removeFile(path string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
