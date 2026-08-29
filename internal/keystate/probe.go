// Package keystate teaches the launcher about key releases.
//
// A terminal normally only tells an application which keys were pressed;
// holding a key down shows up as the terminal's own auto-repeat (one event,
// a pause, then a burst), which makes continuous movement feel sticky. Two
// terminal extensions fix that:
//
//   - the kitty keyboard protocol (kitty, foot, Ghostty, Alacritty, WezTerm,
//     iTerm2, Konsole, Rio, ...): with its "report event types" flag the
//     terminal sends CSI-u sequences for key repeats and releases;
//   - win32-input-mode (Windows Terminal): every key event, key-ups
//     included, is sent as a CSI _ sequence.
//
// Probe asks the terminal which of these it speaks, and Wrap turns a
// tcell.Tty into one that extracts the release events from the input stream
// (before tcell, which does not understand them, sees them) and hands them
// out on a channel.
package keystate

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"golang.org/x/term"
)

// Kitty keyboard protocol flags.
const (
	FlagDisambiguate = 1 << iota
	FlagEventTypes
	FlagAlternateKeys
	FlagAllKeysAsEscapes
	FlagText
)

// Support is what the terminal was found to speak.
type Support struct {
	// Kitty is set when the terminal answered the kitty keyboard protocol
	// query; Flags holds the flags it reported as active at the time.
	Kitty bool
	Flags int
	// Win32 is set when the terminal speaks win32-input-mode (Windows
	// Terminal), which reports key-ups on its own.
	Win32 bool
	// Zellij is set when the multiplexer itself - not the outer terminal -
	// is the reason no release support is claimed, so callers can steer
	// players away from blaming (or replacing) a perfectly capable terminal.
	Zellij bool
	// Tmux is set for the same reason: tmux does not implement the kitty
	// keyboard protocol (no push/pop, no release/repeat event types) - only
	// xterm's modifyOtherKeys/fixterms encoding, which has no release
	// concept at all - regardless of the outer terminal or tmux config.
	Tmux bool
	// Detail is a short human-readable account of the finding.
	Detail string
}

// Releases reports whether the terminal can tell us when a key is released.
func (s Support) Releases() bool { return s.Kitty || s.Win32 }

// probeTimeout is how long Probe waits for the terminal's answers. Terminals
// answer within a few milliseconds; a remote session over SSH takes longer.
const probeTimeout = 400 * time.Millisecond

var (
	kittyReply = regexp.MustCompile(`\x1b\[\?(\d+)u`)
	da1Reply   = regexp.MustCompile(`\x1b\[\?[\d;]*c`)
)

// Probe asks the controlling terminal whether it speaks the kitty keyboard
// protocol. It must run before tcell takes over the terminal. On Windows the
// query is skipped and Windows Terminal is recognised by its environment.
// Zellij and tmux are special-cased below instead of being probed directly.
func Probe() Support {
	if runtime.GOOS == "windows" {
		if os.Getenv("WT_SESSION") != "" {
			return Support{Win32: true, Detail: "Windows Terminal (win32-input-mode)"}
		}
		return Support{Detail: "not Windows Terminal; no key release support"}
	}
	// Zellij answers the kitty keyboard protocol query itself - claiming
	// support - without actually relaying the outer terminal's real release
	// events through to the app. Trusting that answer would leave the engine
	// waiting on its long terminalHoldTimeout watchdog for every release,
	// which reads as the key never releasing (movement keeps going ~1.5s
	// past the actual key-up). Auto-repeat gap detection reacts far faster
	// and Zellij forwards plain auto-repeat just fine.
	if os.Getenv("ZELLIJ") != "" {
		return Support{Zellij: true, Detail: "zellij does not relay key releases to the app; using auto-repeat instead"}
	}
	// tmux does not implement the kitty keyboard protocol at all (confirmed
	// against tmux/tmux#3335, #4158, #4196: it only forwards modifyOtherKeys
	// / fixterms-style modified-key encoding, which has no press/repeat/
	// release event type field). No tmux config - extended-keys, terminal-
	// features, nothing - can produce release events through it, so trust
	// it even less than probing would: skip probing (which tmux swallows
	// anyway) and go straight to auto-repeat.
	if os.Getenv("TMUX") != "" {
		return Support{Tmux: true, Detail: "tmux does not implement the kitty keyboard protocol (no release events); using auto-repeat instead"}
	}
	return probeTTY("/dev/tty", probeTimeout)
}

// probeTTY sends the kitty keyboard query followed by a primary device
// attributes query to dev and reads the answers. Every terminal answers
// DA1, so its reply marks the end of the terminal's response: a kitty reply
// before it means support, no kitty reply means none.
func probeTTY(dev string, timeout time.Duration) Support {
	f, err := os.OpenFile(dev, os.O_RDWR, 0)
	if err != nil {
		return Support{Detail: fmt.Sprintf("cannot open %s: %v", dev, err)}
	}
	defer f.Close()

	fd := int(f.Fd())
	if !term.IsTerminal(fd) {
		return Support{Detail: dev + " is not a terminal"}
	}
	saved, err := term.MakeRaw(fd)
	if err != nil {
		return Support{Detail: fmt.Sprintf("cannot switch %s to raw mode: %v", dev, err)}
	}
	defer term.Restore(fd, saved) //nolint:errcheck

	if _, err := f.WriteString("\x1b[?u\x1b[c"); err != nil {
		return Support{Detail: fmt.Sprintf("cannot write to %s: %v", dev, err)}
	}

	deadline := time.Now().Add(timeout)
	_ = f.SetReadDeadline(deadline)
	var reply []byte
	buf := make([]byte, 256)
	for !da1Reply.Match(reply) && time.Now().Before(deadline) {
		n, err := f.Read(buf)
		reply = append(reply, buf[:n]...)
		if err != nil {
			break
		}
	}

	return parseReply(reply)
}

// parseReply interprets the terminal's answer to the probe.
func parseReply(reply []byte) Support {
	if m := kittyReply.FindSubmatch(reply); m != nil {
		flags, _ := strconv.Atoi(string(m[1]))
		return Support{Kitty: true, Flags: flags, Detail: fmt.Sprintf("kitty keyboard protocol (flags %d)", flags)}
	}
	return Support{Detail: "terminal does not speak the kitty keyboard protocol"}
}
