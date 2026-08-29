package engine

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/keystate"
)

type fakeGame struct {
	resetCalls  int
	pauseCalls  int
	resumeCalls int
	inputCalls  int
}

func (f *fakeGame) Init(tcell.Screen) error          { return nil }
func (f *fakeGame) Update()                          {}
func (f *fakeGame) Draw(tcell.Screen)                {}
func (f *fakeGame) Pause()                           { f.pauseCalls++ }
func (f *fakeGame) Resume()                          { f.resumeCalls++ }
func (f *fakeGame) Reset()                           { f.resetCalls++ }
func (f *fakeGame) HandleInput(*tcell.EventKey) bool { f.inputCalls++; return true }

// pausableGame implements core.PauseState so the engine can consult the
// game's real pause state instead of its own cached flag.
type pausableGame struct {
	fakeGame
	paused bool
}

func (p *pausableGame) Pause()         { p.paused = true; p.pauseCalls++ }
func (p *pausableGame) Resume()        { p.paused = false; p.resumeCalls++ }
func (p *pausableGame) IsPaused() bool { return p.paused }

func TestSpaceResumesExternallyPausedGame(t *testing.T) {
	p := &pausableGame{}
	e := New(nil, p)

	// Simulate an external pause (e.g. the pi subscription's <game>.pause
	// command) that did not go through the engine's SPACE toggle.
	p.paused = true

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
	if p.paused {
		t.Fatal("SPACE should resume the externally paused game")
	}
	if p.resumeCalls != 1 {
		t.Fatalf("resume calls = %d, want 1", p.resumeCalls)
	}
	if p.pauseCalls != 0 {
		t.Fatalf("pause calls = %d, want 0", p.pauseCalls)
	}
}

func TestGlobalKeysAreIntercepted(t *testing.T) {
	f := &fakeGame{}
	e := New(nil, f)

	e.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone))
	if !e.quit {
		t.Fatal("ESC should mark the engine as quitting")
	}
	if f.inputCalls != 0 {
		t.Fatal("ESC must not reach the game")
	}

	e.quit = false
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))
	if f.resetCalls != 1 {
		t.Fatal("R should reset the game")
	}
	if f.inputCalls != 0 {
		t.Fatal("R must not reach the game")
	}

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
	if f.pauseCalls != 1 {
		t.Fatal("SPACE should pause the game")
	}
	if e.paused != true {
		t.Fatal("engine should be paused after SPACE")
	}

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))
	if f.resumeCalls != 1 {
		t.Fatal("second SPACE should resume the game")
	}
	if e.paused != false {
		t.Fatal("engine should be resumed after second SPACE")
	}

	e.handleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))
	if f.inputCalls != 1 {
		t.Fatal("non-global keys should reach the game")
	}
}

// stateGame records key state notifications (core.KeyStateHandler).
type stateGame struct {
	fakeGame
	states []string // "w+" for press, "w-" for release
	claim  bool     // whether HandleKeyState consumes the key
}

func (s *stateGame) HandleKeyState(ev *tcell.EventKey, pressed bool) bool {
	if !s.claim {
		return false
	}
	suffix := "-"
	if pressed {
		suffix = "+"
	}
	if ev.Key() == tcell.KeyRune {
		s.states = append(s.states, string(ev.Rune())+suffix)
	} else {
		s.states = append(s.states, tcell.KeyNames[ev.Key()]+suffix)
	}
	return true
}

func TestKeyStatePressIsNotForwardedToHandleInput(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if g.inputCalls != 0 {
		t.Fatalf("HandleInput calls = %d, want 0 (press consumed by HandleKeyState)", g.inputCalls)
	}
	if len(g.states) != 1 || g.states[0] != "w+" {
		t.Fatalf("states = %v, want [w+]", g.states)
	}

	g.claim = false
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	if g.inputCalls != 1 {
		t.Fatalf("HandleInput calls = %d, want 1 (unclaimed key falls through)", g.inputCalls)
	}
}

func TestSynthesisedReleaseAfterHoldWindow(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)
	now := time.Unix(0, 0)
	e.now = func() time.Time { return now }

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'W', tcell.ModShift)) // shifted press
	now = now.Add(synthHold / 2)
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone)) // auto-repeat refreshes the hold
	e.expireHeld()
	if len(g.states) != 2 {
		t.Fatalf("states = %v, want two presses and no release yet", g.states)
	}

	now = now.Add(synthHold)
	e.expireHeld()
	if len(g.states) != 3 || g.states[2] != "w-" {
		t.Fatalf("states = %v, want a synthesised release of w", g.states)
	}
	if len(e.held) != 0 {
		t.Fatalf("held = %v, want empty after the release", e.held)
	}
}

// TestDynamicHoldTracksMeasuredRepeatGap checks that once a key's second
// press arrives, its release timeout switches from the synthHold default to
// one derived from the actual measured gap between presses (dynamicHold) -
// so a key's own auto-repeat rate, not the first-press default, drives how
// quickly a release is synthesised from the second press on.
func TestDynamicHoldTracksMeasuredRepeatGap(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)
	now := time.Unix(0, 0)
	e.now = func() time.Time { return now }
	id := idOf(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if got := e.held[id].timeout; got != synthHold {
		t.Fatalf("first press timeout = %v, want synthHold (%v)", got, synthHold)
	}

	// Chosen so dynamicHold(gap) lands strictly between minDynamicHold and
	// maxDynamicHold, unclamped - proving it tracks the measured gap rather
	// than just hitting a bound (see TestDynamicHoldIsClamped for that).
	gap := 20 * time.Millisecond
	now = now.Add(gap)
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	want := dynamicHold(gap)
	if got := e.held[id].timeout; got != want {
		t.Fatalf("second press timeout = %v, want dynamicHold(gap) = %v", got, want)
	}
	if want <= minDynamicHold || want >= maxDynamicHold {
		t.Fatalf("dynamicHold(%v) = %v is clamped to a bound; test is not meaningful", gap, want)
	}

	now = now.Add(want - time.Millisecond)
	e.expireHeld()
	if len(g.states) != 2 {
		t.Fatalf("states = %v, want no release yet", g.states)
	}
	now = now.Add(2 * time.Millisecond)
	e.expireHeld()
	if len(g.states) != 3 || g.states[2] != "w-" {
		t.Fatalf("states = %v, want a synthesised release using the dynamic timeout", g.states)
	}
}

// TestDynamicHoldIsClamped checks that dynamicHold never lets a stray
// measurement produce an unusable timeout: a near-zero gap (a burst of
// buffered repeats arriving together) still gets the floor, and a huge one
// (a scheduling hiccup) still gets the ceiling.
func TestDynamicHoldIsClamped(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)
	now := time.Unix(0, 0)
	e.now = func() time.Time { return now }
	id := idOf(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))

	now = now.Add(time.Microsecond)
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if got := e.held[id].timeout; got != minDynamicHold {
		t.Fatalf("timeout after a tiny gap = %v, want the floor (%v)", got, minDynamicHold)
	}

	now = now.Add(2 * time.Second)
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone))
	if got := e.held[id].timeout; got != maxDynamicHold {
		t.Fatalf("timeout after a huge gap = %v, want the ceiling (%v)", got, maxDynamicHold)
	}
}

func TestTerminalReleasesArriveAsMarkedKeyEvents(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)
	now := time.Unix(0, 0)
	e.now = func() time.Time { return now }
	e.SetTerminalReleases(true)

	e.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))
	now = now.Add(10 * synthHold)
	e.expireHeld()
	if len(g.states) != 1 {
		t.Fatalf("states = %v, want no synthesised release when the terminal reports them", g.states)
	}

	e.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, keystate.ReleaseMod))
	if len(g.states) != 2 || g.states[1] != "Up-" {
		t.Fatalf("states = %v, want the marked release forwarded as a release", g.states)
	}
	if g.inputCalls != 0 {
		t.Fatal("a release must never reach HandleInput")
	}
}

func TestTerminalHoldWatchdogSynthesisesReleaseWhenTerminalNeverDoes(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)
	now := time.Unix(0, 0)
	e.now = func() time.Time { return now }
	e.SetTerminalReleases(true)

	// The terminal claimed release support (e.g. zellij answering the kitty
	// probe on the outer terminal's behalf) but, unlike a working terminal,
	// never actually sends one for this key: no auto-repeat, no marked
	// release, ever again.
	e.handleKey(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone))

	now = now.Add(terminalHoldTimeout - time.Millisecond)
	e.expireHeld()
	if len(g.states) != 1 {
		t.Fatalf("states = %v, want no release before the watchdog fires", g.states)
	}

	now = now.Add(2 * time.Millisecond)
	e.expireHeld()
	if len(g.states) != 2 || g.states[1] != "Up-" {
		t.Fatalf("states = %v, want the watchdog to synthesise a release", g.states)
	}
	if len(e.held) != 0 {
		t.Fatalf("held = %v, want empty after the watchdog release", e.held)
	}
}

func TestMarkedReleasesNeverTriggerGlobalKeys(t *testing.T) {
	g := &stateGame{claim: true}
	e := New(nil, g)
	e.SetTerminalReleases(true)

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'r', keystate.ReleaseMod))
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', keystate.ReleaseMod))
	e.handleKey(tcell.NewEventKey(tcell.KeyEscape, 0, keystate.ReleaseMod))
	if g.resetCalls != 0 || g.pauseCalls != 0 || e.quit {
		t.Fatalf("reset=%d pause=%d quit=%v; releases of R/SPACE/ESC must be inert", g.resetCalls, g.pauseCalls, e.quit)
	}
}
