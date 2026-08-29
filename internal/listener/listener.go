// Package listener coordinates the single global "agent event listener"
// seat across concurrent terminalika instances: only one process may have
// its pi/Claude Code session watchers active and pausing a game at a time,
// regardless of which agent(s) it watches.
package listener

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/terminalika/terminalika/internal/sidecar"
)

// staleAfter is how long a held seat is trusted without a heartbeat refresh
// before a new claimant treats its holder as gone (e.g. crashed without
// releasing) and claims it without asking.
var staleAfter = 5 * time.Second

// heartbeatInterval is how often a held seat refreshes its file, and how
// often it checks whether it has been taken over.
var heartbeatInterval = 2 * time.Second

// Path returns the seat file's location.
func Path() string { return filepath.Join(sidecar.Dir(), "listener.json") }

// record is the on-disk seat: who holds it and when they last proved they're
// still alive.
type record struct {
	PID       int       `json:"pid"`
	Heartbeat time.Time `json:"heartbeat"`
}

// Status reports whether the seat is currently held by a live process other
// than the caller.
type Status struct {
	Held bool
	PID  int
}

// Check reports the current seat status without claiming or changing
// anything.
func Check() Status {
	r, ok := read()
	if !ok || stale(r) || r.PID == os.Getpid() {
		return Status{}
	}
	return Status{Held: true, PID: r.PID}
}

func read() (record, bool) {
	data, err := os.ReadFile(Path())
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

func write(r record) error {
	if err := os.MkdirAll(sidecar.Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), data, 0o644)
}

// Seat is a claimed listener seat: it refreshes its heartbeat until Release,
// so other processes see this one as the live holder.
type Seat struct {
	stop chan struct{}
	done chan struct{}
}

// Claim takes the seat unconditionally: the caller decides beforehand
// whether that's appropriate (the seat was free/stale, or the player agreed
// to move listening here). onLost, if non-nil, is called once - from a
// background goroutine - the first time another process claims the seat
// away from this one.
func Claim(onLost func()) (*Seat, error) {
	if err := write(record{PID: os.Getpid(), Heartbeat: time.Now()}); err != nil {
		return nil, err
	}
	s := &Seat{stop: make(chan struct{}), done: make(chan struct{})}
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
			r, ok := read()
			if ok && r.PID != os.Getpid() {
				if onLost != nil {
					onLost()
				}
				return
			}
			_ = write(record{PID: os.Getpid(), Heartbeat: time.Now()})
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
	r, ok := read()
	return ok && r.PID == os.Getpid()
}

// Release gives up the seat, if still held, and stops the heartbeat. It's a
// no-op on a seat that has already been taken over by someone else.
func (s *Seat) Release() {
	select {
	case <-s.done:
	default:
		close(s.stop)
		<-s.done
	}
	if r, ok := read(); ok && r.PID == os.Getpid() {
		_ = os.Remove(Path())
	}
}
