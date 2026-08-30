// Package hub is the multi-agent event hub: it runs every configured agent
// source concurrently in the background and fans their events out to
// whoever is interested - the notifier, the home screen, the game engine -
// without any of them knowing how an event was detected.
//
// The hub is deliberately independent of the game renderer: it keeps running
// while the player is on the home screen, inside a game, or in between.
package hub

import (
	"context"
	"sync"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

// Source is one way of detecting agent activity. Run blocks until ctx is
// cancelled and calls emit for every event it detects, from any goroutine.
type Source interface {
	Run(ctx context.Context, emit func(agents.Event))
}

// SourceFunc adapts a function to Source.
type SourceFunc func(ctx context.Context, emit func(agents.Event))

// Run calls f.
func (f SourceFunc) Run(ctx context.Context, emit func(agents.Event)) { f(ctx, emit) }

// dedupeWindow suppresses a repeat of the same (agent, kind) arriving within
// this long of the previous one: a native session watcher and a hook for
// the same agent both firing for one turn should produce one event, not
// two bells.
const dedupeWindow = 3 * time.Second

// Hub runs sources and fans out their events.
type Hub struct {
	mu      sync.Mutex
	sources []Source
	agents  []agents.Agent
	subs    map[chan agents.Event]struct{}
	last    map[agents.ID]agents.Event
	latest  *agents.Event
	seq     uint64
	seen    uint64 // Seq of the newest event a screen has shown (see Current)
	cancel  context.CancelFunc
	done    chan struct{}
	muted   bool
	now     func() time.Time
}

// New returns an empty, stopped hub.
func New() *Hub {
	return &Hub{
		subs: make(map[chan agents.Event]struct{}),
		last: make(map[agents.ID]agents.Event),
		now:  time.Now,
	}
}

// Add registers a source. Sources added after Start are not run.
func (h *Hub) Add(s Source) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sources = append(h.sources, s)
}

// Watch records that an agent is being listened to, for display purposes
// (see Agents).
func (h *Hub) Watch(a agents.Agent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range h.agents {
		if x.ID == a.ID {
			return
		}
	}
	h.agents = append(h.agents, a)
}

// Agents returns the agents being listened to, in the order added.
func (h *Hub) Agents() []agents.Agent {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]agents.Agent(nil), h.agents...)
}

// Start runs every source in its own goroutine until Stop.
func (h *Hub) Start() {
	h.mu.Lock()
	if h.cancel != nil {
		h.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	h.done = make(chan struct{})
	sources := append([]Source(nil), h.sources...)
	h.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			s.Run(ctx, h.Emit)
		}(s)
	}
	go func() {
		wg.Wait()
		close(h.done)
	}()
}

// Stop cancels every source and waits for them to return. Subscribers are
// left intact (they simply stop receiving).
func (h *Hub) Stop() {
	h.mu.Lock()
	cancel, done := h.cancel, h.done
	h.cancel = nil
	h.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Running reports whether Start has been called and Stop has not.
func (h *Hub) Running() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cancel != nil
}

// Mute suppresses fan-out without stopping the sources - used when another
// terminalika process takes over the listener seat: the files keep being
// tailed (so nothing is missed on un-mute) but nothing reacts.
func (h *Hub) Mute(on bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.muted = on
}

// Emit publishes an event to every subscriber, stamping At if unset and
// dropping duplicates inside dedupeWindow. It is safe from any goroutine
// and never blocks: a subscriber that has fallen behind misses the event.
func (h *Hub) Emit(ev agents.Event) {
	if ev.At.IsZero() {
		ev.At = h.now()
	}

	h.mu.Lock()
	if h.muted {
		h.mu.Unlock()
		return
	}
	if prev, ok := h.last[ev.Agent.ID]; ok && prev.Kind == ev.Kind && ev.At.Sub(prev.At) < dedupeWindow {
		h.mu.Unlock()
		return
	}
	h.seq++
	ev.Seq = h.seq
	h.last[ev.Agent.ID] = ev
	h.latest = &ev
	subs := make([]chan agents.Event, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe returns a buffered channel that receives every event emitted
// from now on.
func (h *Hub) Subscribe() chan agents.Event {
	ch := make(chan agents.Event, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

// Unsubscribe removes a channel returned by Subscribe.
func (h *Hub) Unsubscribe(ch chan agents.Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// Latest returns the most recent event, if any.
func (h *Hub) Latest() (agents.Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.latest == nil {
		return agents.Event{}, false
	}
	return *h.latest, true
}

// Current returns the one notification the player should be looking at:
// the most recent event, unless a screen has already shown it (see
// MarkSeen). There is only ever one - a newer event simply replaces an
// older unseen one, and an event that was shown anywhere is never offered
// again anywhere else, so closing the pause overlay in a game and walking
// back to the home screen doesn't produce a second toast for the same
// thing.
func (h *Hub) Current() (agents.Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.latest == nil || h.latest.Seq <= h.seen {
		return agents.Event{}, false
	}
	return *h.latest, true
}

// MarkSeen records that ev (and everything before it) has been shown to
// the player, retiring it from Current.
func (h *Hub) MarkSeen(ev agents.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ev.Seq > h.seen {
		h.seen = ev.Seq
	}
}
