// Package daemon is terminalika's headless background process: the same
// agent hub and desktop notifier as a window, minus the screen, so agent
// events keep reaching the player after every terminalika window is
// closed - and from login on, when autostart is on.
//
// It is deliberately deferential: it reacts to events only while it holds
// the listener seat, which it takes only when free and hands to any window
// that opens (see package listener). There is at most one daemon; a second
// one finding a live one exits at once.
package daemon

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/hub"
	"github.com/terminalika/terminalika/internal/listener"
	"github.com/terminalika/terminalika/internal/notify"
	"github.com/terminalika/terminalika/internal/sidecar"
	"github.com/terminalika/terminalika/internal/sources"
)

// seatPoll is how often a muted daemon checks whether the listener seat has
// become free again (a window closed).
var seatPoll = time.Second

// LogPath is where the daemon writes its few log lines.
func LogPath() string { return filepath.Join(sidecar.Dir(), "daemon.log") }

// Running reports whether a live daemon holds its seat.
func Running() bool { return listener.CheckDaemon().Held }

// Stop asks the running daemon, if any, to exit. It has gone within a
// heartbeat; Stop does not wait for that.
func Stop() error { return listener.StopDaemon() }

// Spawn starts `terminalika daemon` as a detached process using this
// executable, unless one is already running.
func Spawn() error {
	if Running() {
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "daemon")
	cmd.Dir = filepath.Dir(exe)
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// Restart stops a running daemon and starts a fresh one, so a changed
// config takes effect. The new one waits for the old one's seat to clear.
func Restart() error {
	if Running() {
		if err := Stop(); err != nil {
			return err
		}
		deadline := time.Now().Add(10 * time.Second)
		for Running() && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return Spawn()
}

// Run is the daemon's main loop: claim the daemon seat, run the hub, and
// hold the listener seat whenever no window does. It returns when ctx is
// done, the daemon is asked to stop (Stop, or a newer daemon), or there is
// nothing to listen to.
func Run(ctx context.Context, cfg config.Config, logf func(format string, args ...any)) error {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ids := cfg.AgentIDs()
	if len(ids) == 0 {
		return fmt.Errorf("no agents enabled in %s; nothing to listen to", config.Path())
	}
	if listener.CheckDaemon().Held {
		return fmt.Errorf("another daemon is already running (pid %d)", listener.CheckDaemon().PID)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	seat, err := listener.ClaimDaemon(func() {
		logf("daemon seat lost; exiting")
		cancel()
	})
	if err != nil {
		return err
	}
	defer seat.Release()

	set := sources.Build(cfg, ids)
	for _, w := range set.Warnings {
		logf("warning: %s", w)
	}
	h := hub.New()
	for _, src := range set.Sources {
		h.Add(src)
	}
	for _, a := range set.Agents {
		h.Watch(a)
	}
	n := notify.New(cfg.DesktopMode(), nil)
	ch := h.Subscribe()
	go func() {
		for ev := range ch {
			logf("event: %s", ev.Title())
			n.Notify(ev)
		}
	}()
	h.Mute(true)
	h.Start()
	defer func() {
		h.Stop()
		h.Unsubscribe(ch)
		close(ch)
	}()
	logf("daemon up (pid %d): agents %v, %s", os.Getpid(), ids, n.Describe())

	// Hold the listener seat whenever it's free; give it up to any window.
	for {
		if st := listener.Check(); st.Held {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(seatPoll):
			}
			continue
		}
		ls, err := listener.Claim(listener.Daemon, nil)
		if err != nil {
			logf("cannot claim listener seat: %v", err)
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(seatPoll):
			}
			continue
		}
		if set.Webhook != nil {
			_ = set.Webhook.Publish()
		}
		h.Mute(false)
		logf("listening (no window open)")
		select {
		case <-ls.Lost():
			h.Mute(true)
			logf("a window took over; standing by")
		case <-ctx.Done():
			ls.Release()
			return nil
		}
	}
}
