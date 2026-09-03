// Package listener coordinates terminalika's processes through two
// heartbeat-refreshed seat files in the user config dir:
//
//   - the listener seat (listener.json): whoever holds it is the one process
//     reacting to agent events. Every terminalika window takes it when it
//     opens - a window that loses it closes itself, so there is only ever
//     one window - while the background daemon takes it only when it is
//     free, and gives way to any window;
//   - the daemon seat (daemon.json): the single background daemon. A second
//     daemon finding a live holder simply exits; removing the file is how
//     a running daemon is asked to stop.
package listener

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/terminalika/terminalika/internal/sidecar"
)

// Kind says what sort of process holds a seat.
type Kind string

const (
	// Window is an interactive terminalika (home screen or a game).
	Window Kind = "window"
)

// staleAfter is how long a held seat is trusted without a heartbeat refresh
// before a new claimant treats its holder as gone (e.g. crashed without
// releasing) and claims it without asking.
var staleAfter = 5 * time.Second

// heartbeatInterval is how often a held seat refreshes its file, and how
// often it checks whether it has been taken over.
var heartbeatInterval = 2 * time.Second

// Path returns the listener seat file's location.
func Path() string { return filepath.Join(sidecar.Dir(), "listener.json") }

// record is the on-disk seat: who holds it and when they last proved they're
// still alive.
type record struct {
	PID       int       `json:"pid"`
	Kind      Kind      `json:"kind,omitempty"`
	Heartbeat time.Time `json:"heartbeat"`
}

// Status reports whether a seat is currently held by a live process other
// than the caller, and by what.
type Status struct {
	Held bool
	PID  int
	Kind Kind
}

// Check reports the listener seat's status without claiming or changing
// anything.
func Check() Status { return check(Path()) }

func check(path string) Status {
	r, ok := read(path)
	if !ok || stale(r) || r.PID == os.Getpid() {
		return Status{}
	}
	kind := r.Kind
	if kind == "" {
		kind = Window // a seat file from before kinds existed: a window
	}
	return Status{Held: true, PID: r.PID, Kind: kind}
}

func read(path string) (record, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return record{}, false
	}
	var r record
	if err := json.Unmarshal(data, &r); err != nil {
		return record{}, false
	}
	return r, true
}

func stale(r record) bool { return time.Since(r.Heartbeat) > staleAfter }

func write(path string, r record) error {
	if err := os.MkdirAll(sidecar.Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// Seat is a claimed seat: it refreshes its heartbeat until Release, so
// other processes see this one as the live holder.
type Seat struct {
	path string
	kind Kind
	stop chan struct{}
	done chan struct{}
}

// Claim takes the listener seat unconditionally, as kind: the caller
// decides beforehand whether that's appropriate (a window always may; the
// daemon only when the seat is free). onLost, if non-nil, is called once -
// from a background goroutine - the first time another process claims the
// seat away from this one.
func Claim(kind Kind, onLost func()) (*Seat, error) {
	return claim(Path(), kind, onLost)
}

func claim(path string, kind Kind, onLost func()) (*Seat, error) {
	if err := write(path, record{PID: os.Getpid(), Kind: kind, Heartbeat: time.Now()}); err != nil {
		return nil, err
	}

	s := &Seat{path: path, kind: kind, stop: make(chan struct{}), done: make(chan struct{})}
	go s.run(onLost)
	return s, nil
}

func (s *Seat) run(onLost func()) {
	defer close(s.done)
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			r, ok := read(s.path)
			if ok && r.PID != os.Getpid() {
				if onLost != nil {
					onLost()
				}
				return
			}
			_ = write(s.path, record{PID: os.Getpid(), Kind: s.kind, Heartbeat: time.Now()})
		}
	}
}

// Held reports whether this process still holds the seat (another process
// may have taken it over).
func (s *Seat) Held() bool {
	select {
	case <-s.done:
		return false
	default:
	}
	r, ok := read(s.path)
	return ok && r.PID == os.Getpid()
}

// Lost returns a channel closed once the seat has been lost or released.
func (s *Seat) Lost() <-chan struct{} { return s.done }

// Release gives up the seat, if still held, and stops the heartbeat. It's a
// no-op on a seat that has already been taken over by someone else.
func (s *Seat) Release() {
	select {
	case <-s.done:
	default:
		close(s.stop)
		<-s.done
	}
	if r, ok := read(s.path); ok && r.PID == os.Getpid() {
		_ = os.Remove(s.path)
	}
}
