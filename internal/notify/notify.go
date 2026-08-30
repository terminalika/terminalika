// Package notify delivers an agent event to the player as a native OS
// desktop notification, when the configured mode says so. Delivery is
// fire-and-forget; nothing here blocks the caller.
//
// The in-game overlay/banner is not this package's business: it is always
// on, so the desktop notification is the one channel with a choice to make
// - and the choice is *when*, not *whether*: always, only while no
// terminalika window has the terminal's focus, only while no window is open
// at all (i.e. only from the background process), or never.
package notify

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/config"
)

// desktopTimeout bounds the helper process a desktop notification spawns.
const desktopTimeout = 5 * time.Second

// Notifier sends desktop notifications according to a mode.
type Notifier struct {
	mode config.DesktopMode

	// focused reports whether this process's window currently has the
	// terminal's focus. nil means this process has no window at all (the
	// background daemon), which counts as "not focused" and "no window".
	focused func() bool

	// desktop shows a native notification; replaced in tests.
	desktop func(title, body string) error
}

// New builds a notifier. focused may be nil for a headless process.
func New(mode config.DesktopMode, focused func() bool) *Notifier {
	return &Notifier{mode: mode, focused: focused, desktop: desktopNotify}
}

// Mode returns the mode in use.
func (n *Notifier) Mode() config.DesktopMode { return n.mode }

// Wants reports whether an event arriving right now would be delivered.
func (n *Notifier) Wants() bool {
	switch n.mode {
	case config.DesktopAlways:
		return true
	case config.DesktopNoWindow:
		return n.focused == nil
	case config.DesktopUnfocused:
		return n.focused == nil || !n.focused()
	}
	return false
}

// Notify delivers ev if the mode says so.
func (n *Notifier) Notify(ev agents.Event) {
	if !n.Wants() {
		return
	}
	title, body := ev.Title(), ev.Body()
	go func() { _ = n.desktop(title, body) }()
}

// Describe is the mode for a status line ("desktop: when unfocused").
func (n *Notifier) Describe() string {
	switch n.mode {
	case config.DesktopAlways:
		return "desktop: always"
	case config.DesktopNoWindow:
		return "desktop: when no window is open"
	case config.DesktopUnfocused:
		return "desktop: when unfocused"
	}
	return "desktop: off"
}

// desktopNotify shows a native notification using the platform's stock
// tooling, so terminalika needs no cgo or extra dependencies:
//
//   - Linux/BSD: notify-send (libnotify), present on every desktop that has
//     a notification daemon;
//   - macOS: osascript's "display notification";
//   - Windows: a PowerShell balloon tip through System.Windows.Forms.
func desktopNotify(title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), desktopTimeout)
	defer cancel()

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := "display notification " + appleQuote(body) + " with title " + appleQuote("terminalika") + " subtitle " + appleQuote(title) + " sound name \"Glass\""
		cmd = exec.CommandContext(ctx, "osascript", "-e", script)
	case "windows":
		script := `Add-Type -AssemblyName System.Windows.Forms;` +
			`$n = New-Object System.Windows.Forms.NotifyIcon;` +
			`$n.Icon = [System.Drawing.SystemIcons]::Information;` +
			`$n.Visible = $true;` +
			`$n.ShowBalloonTip(8000, ` + psQuote(title) + `, ` + psQuote(body) + `, [System.Windows.Forms.ToolTipIcon]::Info);` +
			`Start-Sleep -Seconds 8; $n.Dispose()`
		cmd = exec.CommandContext(ctx, "powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	default:
		cmd = exec.CommandContext(ctx, "notify-send", "--app-name=terminalika", "--urgency=critical", "--expire-time=10000", title, body)
	}
	return cmd.Run()
}

// appleQuote quotes s for AppleScript.
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// psQuote quotes s as a PowerShell single-quoted string.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
