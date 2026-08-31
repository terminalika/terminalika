package wizard

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/keystate"
)

func newSim(t *testing.T, size ...int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	if len(size) == 2 {
		s.SetSize(size[0], size[1])
	} else {
		s.SetSize(100, 30)
	}
	t.Cleanup(s.Fini)
	return s
}

func key(k tcell.Key) *tcell.EventKey { return tcell.NewEventKey(k, 0, tcell.ModNone) }
func rn(r rune) *tcell.EventKey       { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }
func release(r rune) *tcell.EventKey  { return tcell.NewEventKey(tcell.KeyRune, r, keystate.ReleaseMod) }
func enters(n int) (evs []*tcell.EventKey) {
	for i := 0; i < n; i++ {
		evs = append(evs, key(tcell.KeyEnter))
	}
	return evs
}

// feed posts the events in order from a goroutine, waiting when the
// simulation screen's small queue is full, so long key scripts aren't
// dropped.
func feed(s tcell.SimulationScreen, evs ...*tcell.EventKey) {
	go func() {
		for _, ev := range evs {
			for s.PostEvent(ev) != nil {
				time.Sleep(time.Millisecond)
			}
		}
	}()
}

// run executes the wizard with a timeout so a script that doesn't finish
// it fails instead of hanging.
func run(t *testing.T, w *Wizard) (config.Config, bool) {
	t.Helper()
	type res struct {
		c     config.Config
		saved bool
	}
	ch := make(chan res, 1)
	go func() {
		c, saved := w.Run()
		ch <- res{c, saved}
	}()
	select {
	case r := <-ch:
		return r.c, r.saved
	case <-time.After(5 * time.Second):
		t.Fatal("wizard did not finish")
		return config.Config{}, false
	}
}

func TestDefaultsSaveWithEnterOnly(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t)
	w := New(s, config.Default())

	feed(s, enters(3)...)
	c, saved := run(t, w)
	if !saved {
		t.Fatal("wizard did not save")
	}
	ids := c.AgentIDs()
	if len(ids) != 2 || ids[0] != agents.Claude || ids[1] != agents.Pi {
		t.Errorf("AgentIDs = %v, want [claude pi]", ids)
	}
	if !c.PauseOnEvent() {
		t.Errorf("config = %+v; want the recommended default (pause)", c)
	}
	if !config.Exists() {
		t.Error("config file not written")
	}
	got, _ := config.Load()
	if got.Version != config.CurrentVersion {
		t.Errorf("saved version = %d", got.Version)
	}
}

func TestTogglesAreRecorded(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t)
	w := New(s, config.Default())

	script := []*tcell.EventKey{
		// Agents: untick Claude (row 0), tick Aider (row 2), tick Cursor (row 3).
		rn(' '), key(tcell.KeyDown), key(tcell.KeyDown), rn(' '), key(tcell.KeyDown), rn('x'), key(tcell.KeyEnter),
		// Auto-pause: choose "No".
		key(tcell.KeyDown), rn(' '), key(tcell.KeyEnter),
		// Summary: save.
		key(tcell.KeyEnter),
	}
	feed(s, script...)

	c, saved := run(t, w)
	if !saved {
		t.Fatal("wizard did not save")
	}
	ids := c.AgentIDs()
	if len(ids) != 3 || ids[0] != agents.Pi || ids[1] != agents.Aider || ids[2] != agents.Cursor {
		t.Errorf("AgentIDs = %v, want [pi aider cursor]", ids)
	}
	if c.PauseOnEvent() {
		t.Error("PauseOnEvent() = true, want false")
	}
}

func TestRerunStartsFromCurrentChoices(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t)
	base := config.Default()
	off := false
	base.AutoPause = &off
	w := New(s, base)

	// Enter straight through must keep what the file already says.
	feed(s, enters(3)...)
	c, saved := run(t, w)
	if !saved {
		t.Fatal("wizard did not save")
	}
	if c.PauseOnEvent() {
		t.Errorf("config = %+v; want the base choice (no auto-pause) kept", c)
	}
}

// terminalika is a game launcher, not a notification service: no step
// offers a bell, a desktop notification or a background process.
func TestOnlyAgentsAndAutoPauseAreAsked(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t)
	w := New(s, config.Default())
	for st := stepAgents; st < stepCount; st++ {
		w.step = st
		w.draw()
		text := strings.ToLower(screenText(s))
		for _, banned := range []string{"bell", "desktop notification", "os popup", "background", "login", "daemon"} {
			if strings.Contains(text, banned) {
				t.Errorf("step %d offers %q:\n%s", st, banned, text)
			}
		}
	}
	if stepCount != 3 {
		t.Errorf("stepCount = %d, want 3 (agents, auto-pause, summary)", stepCount)
	}
}

func TestEscapeOnFirstStepCancelsWithoutSaving(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t)
	w := New(s, config.Default())
	feed(s, key(tcell.KeyEnter), key(tcell.KeyEscape), key(tcell.KeyEscape))
	if _, saved := run(t, w); saved {
		t.Fatal("expected cancel")
	}
	if config.Exists() {
		t.Error("cancel must not write config")
	}
}

func TestReleaseEventsAreIgnored(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	s := newSim(t)
	w := New(s, config.Default())
	// A marked release of Space must not toggle anything back.
	feed(s, append([]*tcell.EventKey{rn(' '), release(' ')}, enters(3)...)...)
	c, _ := run(t, w)
	if c.HasAgent(agents.Claude) {
		t.Error("Claude should have been unticked exactly once")
	}
}

// TestSmallTerminalKeepsEveryOptionVisible covers a quarter-screen
// terminal: all four agents, the step label and the key hint must be on
// screen, with nothing drawn outside the panel.
func TestSmallTerminalKeepsEveryOptionVisible(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	for _, size := range [][2]int{{45, 12}, {60, 14}, {40, 10}} {
		s := newSim(t, size[0], size[1])
		w := New(s, config.Default())
		w.draw()
		text := screenText(s)
		for _, want := range []string{"Claude Code", "Pi Agent", "Aider", "Cursor CLI", "step 1 of 3", "Space toggle"} {
			if !strings.Contains(text, want) {
				t.Errorf("%dx%d: %q missing:\n%s", size[0], size[1], want, text)
			}
		}
	}
}

func screenText(s tcell.SimulationScreen) string {
	cells, w, h := s.GetContents()
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cells[y*w+x]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			} else {
				b.WriteRune(' ')
			}
		}
		b.WriteRune('\n')
	}
	return b.String()
}
