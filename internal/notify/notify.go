// Package notify delivers an agent event to the player through the channels
// they picked in setup: the terminal bell and/or a native OS desktop
// notification. Both are fire-and-forget; nothing here blocks the caller.
package notify

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

// Options selects the channels.
type Options struct {
	Bell    bool
	Desktop bool
}

// Notifier sends notifications.
type Notifier struct {
	opts Options

	// beep rings the terminal bell. It's the screen's Beep when tcell owns
	// the terminal (writing "\a" straight to stdout would corrupt the
	// screen's own output stream), and nil when there is no screen.
	beep func() error

	// desktop shows a native notification; replaced in tests.
	desktop func(title, body string) error

	mu   sync.Mutex
	last time.Time
}

// New builds a notifier. beep may be nil, in which case the bell channel is
// silently unavailable.
func New(opts Options, beep func() error) *Notifier {
	return &Notifier{opts: opts, beep: beep, desktop: desktopNotify}
}

// Options returns the channels in use.
func (n *Notifier) Options() Options { return n.opts }

// Enabled reports whether any channel is on.
func (n *Notifier) Enabled() bool { return n.opts.Bell || n.opts.Desktop }

// Notify delivers ev through every selected channel.
func (n *Notifier) Notify(ev agents.Event) {
	if n.opts.Bell && n.beep != nil {
		_ = n.beep()
	}
	if n.opts.Desktop {
		title, body := ev.Title(), ev.Body()
		go func() { _ = n.desktop(title, body) }()
	}
	n.mu.Lock()
	n.last = time.Now()
	n.mu.Unlock()
}

// Describe lists the active channels for a status line ("bell · desktop").
func (n *Notifier) Describe() string {
	var parts []string
	if n.opts.Bell {
		parts = append(parts, "bell")
	}
	if n.opts.Desktop {
		parts = append(parts, "desktop")
	}
	if len(parts) == 0 {
		return "silent"
	}
	return strings.Join(parts, " · ")
}

// desktopTimeout bounds the helper process a desktop notification spawns.
const desktopTimeout = 5 * time.Second

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
