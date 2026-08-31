package engine

import (
	"strings"
	"testing"
	"time"

	core "github.com/terminalika/terminalika-core"

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

// pauseNoOpGame implements core.Commandable and core.PauseState so apply()
// can be exercised directly. pauseIsNoOp simulates a game that's already
// over: HandleCommand succeeds (as every game's cmdPause case does) but
// Pause() itself is a no-op, so IsPaused() stays false.
type pauseNoOpGame struct {
	fakeGame
	paused      bool
	pauseIsNoOp bool
	handled     []core.Command
}

func (c *pauseNoOpGame) HandleCommand(cmd core.Command) error {
	c.handled = append(c.handled, cmd)
	if strings.HasSuffix(cmd.Type, ".pause") && !c.pauseIsNoOp {
		c.paused = true
	}
	return nil
}

func (c *pauseNoOpGame) Commands() []core.CommandSpec { return nil }
func (c *pauseNoOpGame) IsPaused() bool               { return c.paused }

func TestPauseCommandOnFinishedGameShowsNotice(t *testing.T) {
	g := &pauseNoOpGame{pauseIsNoOp: true}
	e := New(nil, g)

	e.apply(core.Command{Type: "snake.pause"})

	if e.notice == nil || e.notice.lifecycle != noticeDismissOnKey {
		t.Fatalf("notice = %+v, want a dismiss-on-key notice when the pause command had no effect", e.notice)
	}
}

func TestGameOverNoticeIsAttributedToTheAgent(t *testing.T) {
	g := &pauseNoOpGame{pauseIsNoOp: true}
	e := New(nil, g)

	e.apply(core.Command{
		Type:    "snake.pause",
		Payload: core.MustJSON(map[string]string{"reason": "Paused: Claude's done, you're up."}),
	})

	if e.notice == nil || len(e.notice.lines) == 0 || e.notice.lines[0] != "Claude's done, you're up." {
		t.Fatalf("notice = %+v, want the command's reason (Paused: prefix stripped) naming Claude", e.notice)
	}
}

func TestGameOverNoticeFallsBackWithoutAReason(t *testing.T) {
	g := &pauseNoOpGame{pauseIsNoOp: true}
	e := New(nil, g)

	e.apply(core.Command{Type: "snake.pause"})

	if e.notice == nil || len(e.notice.lines) == 0 || e.notice.lines[0] != defaultGameOverNotice {
		t.Fatalf("notice = %+v, want the fallback %q when no reason was given", e.notice, defaultGameOverNotice)
	}
}

func TestPauseCommandOnRunningGameShowsNoDismissOnKeyNotice(t *testing.T) {
	g := &pauseNoOpGame{}
	e := New(nil, g)

	e.apply(core.Command{Type: "snake.pause"})

	if !g.paused {
		t.Fatal("expected the game to actually pause")
	}
	if e.notice != nil && e.notice.lifecycle == noticeDismissOnKey {
		t.Fatal("expected no dismiss-on-key notice when the pause succeeded")
	}
}

func TestNonPauseCommandNeverShowsNotice(t *testing.T) {
	g := &pauseNoOpGame{pauseIsNoOp: true}
	e := New(nil, g)

	e.apply(core.Command{Type: "snake.move"})

	if e.notice != nil {
		t.Fatal("expected no notice for a non-pause command")
	}
}

func TestDismissOnKeyNoticeSwallowsNextPressAndClears(t *testing.T) {
	f := &fakeGame{}
	e := New(nil, f)
	e.notice = &screenNotice{lines: []string{"notice"}, lifecycle: noticeDismissOnKey}

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))

	if e.notice != nil {
		t.Fatal("expected the notice to be cleared by the dismissing keypress")
	}
	if f.resetCalls != 0 || f.inputCalls != 0 {
		t.Fatalf("resetCalls=%d inputCalls=%d; the dismissing keypress must be swallowed, not forwarded", f.resetCalls, f.inputCalls)
	}
}

func TestDismissOnKeyNoticeIgnoresReleaseEvents(t *testing.T) {
	f := &fakeGame{}
	e := New(nil, f)
	e.notice = &screenNotice{lines: []string{"notice"}, lifecycle: noticeDismissOnKey}

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'r', keystate.ReleaseMod))

	if e.notice == nil {
		t.Fatal("a release event must not dismiss the notice")
	}
}

func TestPersistentNoticeIsNotDismissedByAKeypress(t *testing.T) {
	g := &pausableGame{paused: true}
	e := New(nil, g)
	e.notice = &screenNotice{lines: []string{"Paused: Claude's done, you're up."}, style: noticeStyleForAgent("claude")}

	// A key other than SPACE must leave a persistent (non-dismiss-on-key)
	// pause notice alone - only togglePause (SPACE) or a reset clears it.
	e.handleKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone))

	if e.notice == nil {
		t.Fatal("expected the persistent pause notice to survive a non-SPACE keypress")
	}
}

func TestNoticeStyleForAgentIsDistinctPerAgent(t *testing.T) {
	claude := noticeStyleForAgent("claude")
	pi := noticeStyleForAgent("pi")
	other := noticeStyleForAgent("")

	if claude == pi || claude == other || pi == other {
		t.Fatalf("expected three distinct styles, got claude=%v pi=%v other=%v", claude, pi, other)
	}
}

func TestPauseCommandAttributesLiveOverlayToTheAgent(t *testing.T) {
	g := &pauseNoOpGame{}
	e := New(nil, g)

	e.apply(core.Command{
		Type:    "snake.pause",
		Payload: core.MustJSON(map[string]string{"reason": "Paused: Claude's done, you're up.", "agent": "claude"}),
	})

	if e.notice == nil || e.notice.style != noticeStyleForAgent("claude") || e.notice.lifecycle != noticeWhilePaused {
		t.Fatalf("notice = %+v, want a persistent overlay styled for claude", e.notice)
	}
}

func TestPauseCommandWithoutReasonLeavesNoLiveOverlay(t *testing.T) {
	g := &pauseNoOpGame{}
	e := New(nil, g)

	e.apply(core.Command{Type: "snake.pause"})

	if e.notice != nil {
		t.Fatalf("notice = %+v, want nil so the game's own default overlay shows through", e.notice)
	}
}

func TestManualPauseSetsAquaBrandNotice(t *testing.T) {
	g := &pausableGame{}
	e := New(nil, g)

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))

	if e.notice == nil || e.notice.style != noticeStyleForAgent("") {
		t.Fatalf("notice = %+v, want the aqua brand style for a manual SPACE pause", e.notice)
	}
}

func TestManualSpaceClearsAttributedPauseNotice(t *testing.T) {
	g := &pausableGame{}
	e := New(nil, g)
	e.notice = &screenNotice{lines: []string{"Paused: Claude's done, you're up."}, style: noticeStyleForAgent("claude")}

	// SPACE while the game reports itself paused (e.g. by that same agent
	// command) must resume it and drop the leftover attribution.
	g.paused = true
	e.handleKey(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone))

	if e.notice != nil {
		t.Fatal("expected a manual SPACE to clear the agent-attributed pause overlay")
	}
}

func TestResetClearsAttributedPauseNotice(t *testing.T) {
	f := &fakeGame{}
	e := New(nil, f)
	e.notice = &screenNotice{lines: []string{"Paused: PI's done, you're up."}, style: noticeStyleForAgent("pi")}

	e.handleKey(tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone))

	if e.notice != nil {
		t.Fatal("expected R (reset) to clear the agent-attributed pause overlay")
	}
}

func TestResumeCommandClearsNotice(t *testing.T) {
	g := &pauseNoOpGame{paused: true}
	e := New(nil, g)
	e.notice = &screenNotice{lines: []string{"Paused: PI's done, you're up."}, style: noticeStyleForAgent("pi")}

	e.apply(core.Command{Type: "snake.resume"})

	if e.notice != nil {
		t.Fatal("expected an external resume command to clear the pause notice")
	}
}

func TestDrawScreenNoticeCentersOnScreen(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(40, 40)

	e := &Engine{screen: screen}
	claudeStyle := noticeStyleForAgent("claude")
	e.drawScreenNotice(&screenNotice{lines: []string{"hi"}, style: claudeStyle})

	x := 40/2 - len("hi")/2
	_, _, gotStyle, _ := screen.GetContent(x, 40/2)
	if gotStyle != claudeStyle {
		t.Fatalf("style at (%d, %d) = %v, want claude's style", x, 40/2, gotStyle)
	}
}

// bandGame is a game that reports where its own PAUSED band is
// (core.OverlayReporter); the notice must land on it, wherever it says.
type bandGame struct {
	core.Game
	band core.Rect
}

func (g *bandGame) OverlayArea() (core.Rect, bool) { return g.band, true }

// TestDrawScreenNoticeFollowsOddBoardOverlayOneRowUp reproduces a game with
// an odd row count: its own "PAUSED" band lands one row above h/2, and
// the notice must follow it there instead of landing on the h/2 guess and
// leaving both visible.
func TestDrawScreenNoticeFollowsOddBoardOverlayOneRowUp(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(40, 40)

	gameStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkRed)
	realRow := 40/2 - 1
	for i, r := range "PAUSED" {
		screen.SetContent(17+i, realRow, r, nil, gameStyle)
	}
	screen.Show()

	e := &Engine{screen: screen, game: &bandGame{band: core.Rect{X: 17, Y: realRow, W: len("PAUSED"), H: 1}}}
	aqua := noticeStyleForAgent("")
	e.drawScreenNotice(&screenNotice{lines: []string{"PAUSED"}, style: aqua})

	x := 40/2 - len("PAUSED")/2
	_, _, gotStyle, _ := screen.GetContent(x, realRow)
	if gotStyle != aqua {
		t.Fatalf("style at (%d, %d) = %v, want the notice to have followed the game's overlay one row up", x, realRow, gotStyle)
	}
	// The h/2 guess itself must be left alone - nothing should leak there.
	_, _, guessStyle, _ := screen.GetContent(x, 40/2)
	if guessStyle == aqua {
		t.Fatalf("notice also drawn at the h/2 guess (%d, %d); it must land on exactly one row", x, 40/2)
	}
}

// TestNaturalGameOverSetsAquaNotice checks that a game's own "<name>.game_over"
// event - fired with no external pause command or agent involved, e.g. the
// player just lost or won on their own - still shows the same brand-colored
// notice as everything else, not the game's plain default rendering.
func TestNaturalGameOverSetsAquaNotice(t *testing.T) {
	g := &commandableGame{}
	e := New(nil, g)
	e.wireEmitter()

	g.emitter.Emit(core.Event{Type: "snake.game_over"})

	if e.notice != naturalGameOverNotice {
		t.Fatalf("notice = %+v, want naturalGameOverNotice", e.notice)
	}
	if e.notice.style != noticeStyleForAgent("") {
		t.Fatalf("notice style = %v, want the aqua brand color", e.notice.style)
	}
	if e.notice.lifecycle != noticeUntilCleared {
		t.Fatalf("notice lifecycle = %v, want noticeUntilCleared (no pause state to watch)", e.notice.lifecycle)
	}
}

func TestNonGameOverEventsLeaveNoticeAlone(t *testing.T) {
	g := &commandableGame{}
	e := New(nil, g)
	e.wireEmitter()

	g.emitter.Emit(core.Event{Type: "snake.moved"})

	if e.notice != nil {
		t.Fatalf("notice = %+v, want nil for an unrelated event", e.notice)
	}
}

func TestPauseCommandLinesBecomeTheOverlay(t *testing.T) {
	g := &pauseNoOpGame{}
	e := New(nil, g)

	lines := []string{"[INPUT REQUIRED: Claude Code]", "Your AI Agent is waiting for your response/approval!", "Press [ESC] to switch back."}
	e.apply(core.Command{
		Type:    "snake.pause",
		Payload: core.MustJSON(map[string]any{"reason": "Paused: Claude needs you", "agent": "claude", "lines": lines}),
	})

	if e.notice == nil || len(e.notice.lines) != 3 || e.notice.lines[0] != lines[0] {
		t.Fatalf("notice = %+v, want the three overlay lines", e.notice)
	}
	if e.notice.style != noticeStyleForAgent("claude") {
		t.Errorf("notice style = %v, want claude's", e.notice.style)
	}
}

func TestGameOverNoticeUsesLinesWhenPresent(t *testing.T) {
	g := &pauseNoOpGame{pauseIsNoOp: true}
	e := New(nil, g)

	e.apply(core.Command{
		Type:    "snake.pause",
		Payload: core.MustJSON(map[string]any{"agent": "aider", "lines": []string{"[AGENT READY: Aider]", "done"}}),
	})
	if e.notice == nil || e.notice.lifecycle != noticeDismissOnKey || e.notice.lines[0] != "[AGENT READY: Aider]" {
		t.Fatalf("notice = %+v", e.notice)
	}
	if e.notice.style != noticeStyleForAgent("aider") || noticeStyleForAgent("aider") == noticeStyleForAgent("cursor") {
		t.Errorf("aider/cursor styles must be their own")
	}
}

func TestFlashShowsBannerOnTopRowThenExpires(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)

	e := New(screen, &fakeGame{})
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return now }

	e.Flash([]string{"[AGENT READY: Pi Agent]"}, "pi")
	e.drainFlashes()
	e.drawBanner()

	// Top-right card: styled cells on rows 0-2 near the right edge, none on
	// the left half.
	_, _, right, _ := screen.GetContent(80-3, 1)
	_, _, left, _ := screen.GetContent(2, 1)
	if right != noticeStyleForAgent("pi") || left == noticeStyleForAgent("pi") {
		t.Fatalf("banner not drawn as a top-right card in pi's style (right=%v left=%v)", right, left)
	}

	now = now.Add(bannerDuration + time.Second)
	e.drawBanner()
	if e.banner != nil {
		t.Error("banner should expire after bannerDuration")
	}
}

func TestMultiLineNoticeIsDrawnAsASolidBlock(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(60, 20)

	e := &Engine{screen: screen}
	style := noticeStyleForAgent("cursor")
	e.drawScreenNotice(&screenNotice{lines: []string{"[AGENT READY: Cursor CLI]", "done"}, style: style})

	// The short second line must be padded to the first line's width.
	x := 60/2 - (len("[AGENT READY: Cursor CLI]")+2)/2
	_, _, got, _ := screen.GetContent(x, 20/2+1)
	if got != style {
		t.Fatalf("second line not padded to block width at x=%d", x)
	}
}

// TestNoticeCoversTheGamesOwnOverlayRow reproduces the "red and orange side
// by side" glitch: a game paints its long reason text white-on-dark-red on
// the center row, and a narrower agent block must still cover all of it.
func TestNoticeCoversTheGamesOwnOverlayRow(t *testing.T) {
	screen := tcell.NewSimulationScreen("")
	if err := screen.Init(); err != nil {
		t.Fatalf("screen.Init: %v", err)
	}
	defer screen.Fini()
	screen.SetSize(80, 24)

	red := tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkRed)
	reason := "Claude Code needs your input! (and some more red text)"
	x0 := 80/2 - len(reason)/2
	for i, r := range reason {
		screen.SetContent(x0+i, 24/2, r, nil, red)
	}
	screen.Show()

	e := &Engine{screen: screen, game: &bandGame{band: core.Rect{X: x0, Y: 24 / 2, W: len(reason), H: 1}}}
	claude := noticeStyleForAgent("claude")
	e.drawScreenNotice(&screenNotice{lines: []string{"[INPUT REQUIRED: Claude Code]", "short", "[SPACE] resume"}, style: claude})

	for x := 0; x < 80; x++ {
		_, _, style, _ := screen.GetContent(x, 24/2)
		_, bg, _ := style.Decompose()
		if bg == tcell.ColorDarkRed {
			t.Fatalf("dark red still visible at x=%d on the overlay row", x)
		}
	}
	// And the block is one solid rectangle: every line spans the same columns.
	for row := 24/2 + 1; row <= 24/2+2; row++ {
		_, _, style, _ := screen.GetContent(x0, row)
		if style != claude {
			t.Fatalf("row %d not widened to the block's left edge", row)
		}
	}
}
