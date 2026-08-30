package hub

import (
	"context"
	"testing"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

func claudeEvent(kind agents.EventKind, at time.Time) agents.Event {
	a, _ := agents.Lookup("claude")
	return agents.Event{Agent: a, Kind: kind, At: at}
}

func TestEmitFansOutToSubscribers(t *testing.T) {
	h := New()
	a := h.Subscribe()
	b := h.Subscribe()
	defer h.Unsubscribe(a)
	defer h.Unsubscribe(b)

	h.Emit(claudeEvent(agents.Finished, time.Time{}))

	for _, ch := range []chan agents.Event{a, b} {
		select {
		case ev := <-ch:
			if ev.Kind != agents.Finished || ev.At.IsZero() {
				t.Errorf("got %+v", ev)
			}
		case <-time.After(time.Second):
			t.Fatal("subscriber never received the event")
		}
	}
	if ev, ok := h.Latest(); !ok || ev.Kind != agents.Finished {
		t.Errorf("Latest = %+v, %v", ev, ok)
	}
}

func TestEmitDedupesSameKindWithinWindow(t *testing.T) {
	h := New()
	ch := h.Subscribe()
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	h.Emit(claudeEvent(agents.Finished, base))
	h.Emit(claudeEvent(agents.Finished, base.Add(time.Second)))      // dropped
	h.Emit(claudeEvent(agents.InputRequired, base.Add(time.Second))) // different kind: kept
	h.Emit(claudeEvent(agents.Finished, base.Add(10*time.Second)))   // outside window: kept

	got := 0
	for {
		select {
		case <-ch:
			got++
		default:
			if got != 3 {
				t.Fatalf("received %d events, want 3", got)
			}
			return
		}
	}
}

func TestMuteSuppressesFanOut(t *testing.T) {
	h := New()
	ch := h.Subscribe()
	h.Mute(true)
	h.Emit(claudeEvent(agents.Finished, time.Time{}))
	select {
	case <-ch:
		t.Fatal("muted hub must not fan out")
	default:
	}
}

func TestStartRunsSourcesUntilStop(t *testing.T) {
	h := New()
	started := make(chan struct{})
	stopped := make(chan struct{})
	h.Add(SourceFunc(func(ctx context.Context, emit func(agents.Event)) {
		close(started)
		emit(claudeEvent(agents.InputRequired, time.Time{}))
		<-ctx.Done()
		close(stopped)
	}))
	ch := h.Subscribe()

	h.Start()
	if !h.Running() {
		t.Fatal("Running() = false after Start")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("source never started")
	}
	select {
	case ev := <-ch:
		if ev.Kind != agents.InputRequired {
			t.Errorf("event kind = %v", ev.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("event never delivered")
	}

	h.Stop()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("source never observed cancellation")
	}
	if h.Running() {
		t.Error("Running() = true after Stop")
	}
}

func TestCurrentIsOneEventShownOnce(t *testing.T) {
	h := New()
	base := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	if _, ok := h.Current(); ok {
		t.Fatal("Current on an empty hub should be empty")
	}

	h.Emit(claudeEvent(agents.Finished, base))
	ev, ok := h.Current()
	if !ok || ev.Kind != agents.Finished || ev.Seq == 0 {
		t.Fatalf("Current = %+v, %v; want the emitted event with a Seq", ev, ok)
	}

	// Shown somewhere (a game's pause overlay, say): gone for good, on
	// every screen, even though it is still Latest.
	h.MarkSeen(ev)
	if _, ok := h.Current(); ok {
		t.Fatal("a seen event must not be Current again")
	}
	if last, ok := h.Latest(); !ok || last.Seq != ev.Seq {
		t.Errorf("Latest = %+v, %v; want the seen event to remain the last one", last, ok)
	}

	// A newer event replaces it as the one current notice; marking an
	// older event seen doesn't retire the newer one.
	h.Emit(claudeEvent(agents.InputRequired, base.Add(10*time.Second)))
	h.MarkSeen(ev)
	cur, ok := h.Current()
	if !ok || cur.Kind != agents.InputRequired || cur.Seq <= ev.Seq {
		t.Fatalf("Current = %+v, %v; want the newer event", cur, ok)
	}
	h.MarkSeen(cur)
	if _, ok := h.Current(); ok {
		t.Fatal("nothing should be current after the newest event was seen")
	}
}
