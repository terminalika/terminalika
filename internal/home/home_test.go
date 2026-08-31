package home

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/agents"
)

func newSim(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	t.Cleanup(s.Fini)
	return s
}

func key(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModNone) }
func rn(r rune) *tcell.EventKey       { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }

func feed(s tcell.SimulationScreen, evs ...*tcell.EventKey) {
	go func() {
		for _, ev := range evs {
			for s.PostEvent(ev) != nil {
				time.Sleep(time.Millisecond)
			}
		}
	}()
}

func run(t *testing.T, h *Home) (string, bool) {
	t.Helper()
	type res struct {
		name string
		ok   bool
	}
	ch := make(chan res, 1)
	go func() {
		n, ok := h.Run()
		ch <- res{n, ok}
	}()
	select {
	case r := <-ch:
		return r.name, r.ok
	case <-time.After(5 * time.Second):
		t.Fatal("home screen did not finish")
		return "", false
	}
}

func TestTypeToLaunch(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 100, 30)
	h := New(s, games, nil, nil)
	feed(s, rn('t'), rn('e'), key(tcell.KeyEnter))
	name, ok := run(t, h)
	if !ok || name != "tetris" {
		t.Fatalf("Run = %q, %v; want tetris", name, ok)
	}
}

func TestDownArrowExploresAndEnterLaunchesSelection(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 100, 30)
	h := New(s, games, nil, nil)
	feed(s, key(tcell.KeyDown), key(tcell.KeyRight), key(tcell.KeyEnter))
	name, ok := run(t, h)
	if !ok || name != "invaders" {
		t.Fatalf("Run = %q, %v; want invaders (second card)", name, ok)
	}
}

func TestHeroModeHidesLibraryAndShowsPrompt(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 120, 36)
	h := New(s, games, nil, nil)
	h.loadScores()
	h.step()
	h.draw()

	text := screenText(s)
	if contains(text, "╭") || contains(text, "invaders") {
		t.Error("game cards visible in hero mode")
	}
	if !contains(text, "Press [↓] Down Arrow") {
		t.Error("hero prompt missing from bottom bar")
	}
	if !contains(text, "terminalika") && !contains(text, "█") && !contains(text, "▀") {
		t.Error("title not drawn")
	}
	if contains(text, "TERMINALIKA") {
		t.Error("title must be lowercase")
	}

	// Slide down: after enough frames the library is fully revealed.
	h.setMode(modeExplore)
	for i := 0; i < 40; i++ {
		h.step()
	}
	if h.heroShift != 1 {
		t.Fatalf("heroShift = %v after settling, want 1", h.heroShift)
	}
	h.draw()
	text = screenText(s)
	if !contains(text, "╭") || !contains(text, "snake") || !contains(text, "invaders") {
		t.Error("library not revealed in explore mode")
	}
}

func TestEscapeAndQQuit(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 80, 24)
	h := New(s, games, nil, nil)
	feed(s, key(tcell.KeyDown), key(tcell.KeyEscape), key(tcell.KeyEscape))
	if name, ok := run(t, h); ok || name != "" {
		t.Fatalf("Run = %q, %v; want quit", name, ok)
	}
}

// fakeFeed is a Feed with one event that may or may not have been seen.
type fakeFeed struct {
	ev   agents.Event
	seen uint64
}

func (f *fakeFeed) Current() (agents.Event, bool) {
	return f.ev, f.ev.Seq > f.seen
}
func (f *fakeFeed) MarkSeen(ev agents.Event) {
	if ev.Seq > f.seen {
		f.seen = ev.Seq
	}
}
func (f *fakeFeed) Latest() (agents.Event, bool) { return f.ev, true }

func TestAgentEventBecomesToastOnce(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 100, 30)
	claude, _ := agents.Lookup("claude")
	ev := agents.Event{Agent: claude, Kind: agents.InputRequired, At: time.Now(), Seq: 1}
	f := &fakeFeed{ev: ev}
	h := New(s, games, f, nil)
	h.loadScores()

	h.pollHub()
	h.step()
	h.draw()

	text := screenText(s)
	if !contains(text, ev.Message()) {
		t.Fatalf("toast missing; screen:\n%s", text)
	}
	if contains(text, "[INPUT REQUIRED") {
		t.Errorf("toast should carry only the one-line message; screen:\n%s", text)
	}
	if !contains(text, "last event") {
		t.Error("last-event line missing")
	}
	// Showing the toast marks the event seen on the feed...
	if f.seen != 1 {
		t.Fatalf("seen = %d, want 1 after the toast was shown", f.seen)
	}
	// ...so once dismissed it doesn't come back on the next poll.
	h.handleKey(key(tcell.KeyDown))
	h.pollHub()
	if h.toast != nil {
		t.Fatal("a dismissed event must not become a toast again")
	}
}

func TestEventSeenElsewhereIsNotToasted(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 100, 30)
	claude, _ := agents.Lookup("claude")
	ev := agents.Event{Agent: claude, Kind: agents.Finished, At: time.Now(), Seq: 1}
	// The player already met this event inside a game (the engine bridge
	// marked it seen): back on the home screen it must stay quiet.
	f := &fakeFeed{ev: ev, seen: 1}
	h := New(s, games, f, nil)
	h.loadScores()

	h.pollHub()
	h.step()
	h.draw()

	if h.toast != nil || contains(screenText(s), ev.Message()) {
		t.Fatalf("event seen in-game resurfaced as a toast; screen:\n%s", screenText(s))
	}
}

func TestSmallTerminalStillRenders(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t, 40, 12)
	h := New(s, games, nil, nil)
	h.loadScores()
	h.setMode(modeExplore)
	for i := 0; i < 40; i++ {
		h.step()
	}
	h.draw() // must not panic
	text := screenText(s)
	if !contains(text, "terminalika") {
		t.Error("plain title fallback missing on a narrow terminal")
	}
	for _, g := range games {
		if !contains(text, g) {
			t.Errorf("%s not listed on a 40x12 terminal:\n%s", g, text)
		}
	}
}

// TestQuarterScreenShowsEveryGame covers a terminal that's a quarter of a
// laptop screen: the library must still be fully navigable.
func TestQuarterScreenShowsEveryGame(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	for _, size := range [][2]int{{60, 16}, {80, 20}, {50, 10}, {40, 9}} {
		s := newSim(t, size[0], size[1])
		h := New(s, games, nil, nil)
		h.loadScores()
		h.setMode(modeExplore)
		for i := 0; i < 40; i++ {
			h.step()
		}
		h.draw()
		text := screenText(s)
		for _, g := range games {
			if !contains(text, g) {
				t.Errorf("%dx%d: %s not visible:\n%s", size[0], size[1], g, text)
			}
		}
	}
	// Navigation in list layout moves one game per keypress, and an
	// absurdly small terminal still scrolls the list to the selection.
	s := newSim(t, 30, 8)
	h := New(s, games, nil, nil)
	h.setMode(modeExplore)
	for i := 0; i < 40; i++ {
		h.step()
	}
	for i := 0; i < 3; i++ {
		h.handleExploreKey(key(tcell.KeyDown))
	}
	if h.sel != 3 {
		t.Errorf("sel = %d after 3x Down in list layout, want 3", h.sel)
	}
	h.draw()
	if !contains(screenText(s), "tetris") {
		t.Errorf("30x8: selected game not scrolled into view:\n%s", screenText(s))
	}
}

func screenText(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()
	var b []rune
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b = append(b, c.Runes[0])
			} else {
				b = append(b, ' ')
			}
		}
		b = append(b, '\n')
	}
	return string(b)
}

func contains(text, sub string) bool {
	return len(sub) > 0 && len(text) >= len(sub) && indexOf(text, sub) >= 0
}

func indexOf(text, sub string) int {
	for i := 0; i+len(sub) <= len(text); i++ {
		if text[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
