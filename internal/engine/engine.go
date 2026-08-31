// Package engine runs a single core.Game and owns the global keybindings.
package engine

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"

	core "github.com/terminalika/terminalika-core"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/keystate"
)

const (
	// framePeriod paces the game loop (update + draw), and with it how often
	// expireHeld gets to check a key's timeout. Tuned tight (~120 Hz)
	// instead of a more conservative 60 Hz: this is the resolution floor
	// under every timeout below, so a low value trades CPU for shaving
	// worst-case release latency down closer to what real terminal release
	// events give for free.
	framePeriod = 8 * time.Millisecond

	// synthHold is how long a key counts as held after its first press when
	// the terminal does not report key releases and no repeat gap has been
	// measured for it yet: long enough to bridge a typical terminal's
	// auto-repeat gap, short enough that a single tap does not overshoot
	// much. Once a second press arrives for the same key, expireHeld
	// switches to a per-key timeout derived from the actual measured gap
	// (see dynamicHold) instead, so the OS's real keyboard repeat rate -
	// not a guess - drives how quickly a release is synthesised.
	synthHold = 5 * time.Millisecond

	// repeatGapMargin is the slack dynamicHold adds on top of a key's
	// measured auto-repeat interval, so ordinary jitter between repeats
	// never reads as a release. Kept close to 1 on purpose: a release
	// detected a bit too eagerly on an unlucky jitter spike reads as a
	// faster game, a release detected too late reads as a stuck key.
	repeatGapMargin = 1.15

	// minDynamicHold and maxDynamicHold bound dynamicHold's output: a floor
	// against a spuriously tiny measured gap (e.g. a burst of buffered
	// input arriving together) firing a release mid-hold, and a ceiling
	// against a spuriously large one (e.g. a scheduling hiccup) making a
	// released key feel stuck. Both trimmed down from more conservative
	// values in favour of snappier releases - accepting more sensitivity to
	// jitter as the trade-off.
	minDynamicHold = 5 * time.Millisecond
	maxDynamicHold = 120 * time.Millisecond

	// terminalHoldTimeout is the watchdog for a terminal that claimed key
	// release support (SetTerminalReleases(true)) but then never actually
	// delivers one for a held key - e.g. zellij, which can answer the kitty
	// keyboard protocol probe without relaying real releases from the outer
	// terminal through to the app, silently downgrading to presses-only. A
	// held key would otherwise never stop without ever being told to. It is
	// comfortably longer than any real repeat gap (a terminal's own delay
	// before the first auto-repeat rarely exceeds a second), so it never
	// fires for a terminal that actually honours what it claimed.
	terminalHoldTimeout = 1500 * time.Millisecond
)

// defaultGameOverNotice is used when a pause command that had no effect
// (see gameOverNotice) carries no reason of its own - a plain external pause
// with no payload, say. Named agent watches always supply one, so this is
// only a fallback.
const defaultGameOverNotice = "Game's done, and so am I - come look."

// noticeLifecycle says when a screenNotice clears itself automatically
// (see Engine.shouldDrawNotice); every case is also cleared early by R
// (reset) or by a newer notice simply replacing it.
type noticeLifecycle int

const (
	// noticeWhilePaused clears the moment the game reports itself no
	// longer paused - a manual SPACE pause or an agent's live pause/
	// question notice.
	noticeWhilePaused noticeLifecycle = iota

	// noticeUntilCleared persists with no condition of its own - a
	// natural, agent-independent game over (see wireEmitter), which has
	// no "no longer true" transition to watch for.
	noticeUntilCleared

	// noticeDismissOnKey blocks input and clears on the very next real
	// keypress, swallowing it - the game-over notice for a pause command
	// that arrived too late to actually pause anything (see
	// gameOverNotice), where missing the moment would mean missing it
	// entirely.
	noticeDismissOnKey
)

// screenNotice is the engine's one "text shown in the center of the screen"
// slot: a manual SPACE pause, an agent's pause/question, and any game-over
// notice all go through it (see Engine.notice), so there is only ever one
// of these on screen, and whichever set it most recently simply overwrites
// whatever was there before - no separate mechanisms to keep in sync, and
// nothing can stack.
type screenNotice struct {
	lines     []string
	style     tcell.Style
	lifecycle noticeLifecycle
}

// agentPauseInfo extracts the "reason", "lines" and "agent" fields an
// agent watch tags its pause commands with (see the hub bridge in main.go).
// "lines" is the full multi-line overlay text; "reason" the one-line
// summary used when there are no lines (older clients, a WebSocket client).
// A plain external pause has none of them.
func agentPauseInfo(payload json.RawMessage) (reason, agent string, lines []string) {
	var p struct {
		Reason string   `json:"reason"`
		Agent  string   `json:"agent"`
		Lines  []string `json:"lines"`
	}
	_ = json.Unmarshal(payload, &p)
	return p.Reason, p.Agent, p.Lines
}

// noticeStyleForAgent picks a style from a pause command's "agent" tag, not
// from its human-readable reason text - a user's own custom pi.message /
// claude.message (see config.go) need not mention the agent by name for
// this to still color correctly. Claude gets its own brand color, PI a
// dark, distinct purple, Aider a deep green, Cursor a blue, and anything
// untagged a neutral fallback; all are colors no in-game overlay
// (white-on-dark-red) uses, so an agent notice is unmistakable regardless
// of which game is on screen.
func noticeStyleForAgent(agent string) tcell.Style {
	switch agent {
	case "claude":
		return tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorOrange).Bold(true)
	case "pi":
		return tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorIndigo).Bold(true)
	case "aider":
		return tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorDarkGreen).Bold(true)
	case "cursor":
		return tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorNavy).Bold(true)
	default:
		// terminalika's own brand color (see notice.Show), for a notice
		// with no agent attribution - a plain external pause command, say.
		return tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorAqua).Bold(true)
	}
}

// gameOverNotice builds the notice shown when an external pause command (an
// agent settling, a WebSocket client) arrives for a game that already
// ended: Pause() is a no-op once a game is over, so nothing on screen would
// otherwise say the agent is done. It reuses the command's own "reason" -
// the same text that would have labeled a normal pause overlay, naming the
// agent that settled (e.g. "Claude's done, you're up.") - so this notice is
// attributed too, falling back to a generic line when there is none. It
// blocks input until any key dismisses it, so the moment isn't missed the
// way a normal pause overlay would be.
func gameOverNotice(payload json.RawMessage) *screenNotice {
	reason, agent, lines := agentPauseInfo(payload)

	if len(lines) == 0 {
		text := strings.TrimPrefix(reason, "Paused: ")
		if text == "" {
			text = defaultGameOverNotice
		}
		lines = []string{text}
	}
	return &screenNotice{lines: lines, style: noticeStyleForAgent(agent), lifecycle: noticeDismissOnKey}
}

// pauseOverlayNotice builds the overlay shown for as long as a game stays
// paused by an attributed external pause command, in the agent's color. A
// reason-less pause (a plain external client) returns nil, clearing any
// notice that was showing rather than leaving a stale one up.
func pauseOverlayNotice(payload json.RawMessage) *screenNotice {
	reason, agent, lines := agentPauseInfo(payload)
	if len(lines) == 0 {
		if reason == "" {
			return nil
		}
		lines = []string{reason}
	}
	return &screenNotice{lines: lines, style: noticeStyleForAgent(agent)}
}

// bannerDuration is how long a Flash banner stays on screen.
const bannerDuration = 6 * time.Second

// Engine drives the game loop and intercepts the global keys before they can
// reach the game:
//
//	ESC   -> return to the launcher menu
//	R     -> reset the game
//	SPACE -> toggle pause/resume
//
// The engine also owns the event bus and the external command queue, so
// network transports (e.g. WebSocket) never touch the game directly.
//
// Games that implement core.KeyStateHandler get press and release
// notifications for their keys. Releases come from the terminal when it
// reports them (see SetTerminalReleases; they arrive as key events marked
// with keystate.ReleaseMod, in order with the presses) and the engine
// forwards those; either way it also runs a watchdog (expireHeld) that
// synthesises a release once a key has gone quiet for a while - synthHold on
// the first press and dynamicHold's measured-repeat-gap-based value after
// that when the terminal doesn't claim release support (bridging the OS's
// actual auto-repeat gaps instead of guessing one), terminalHoldTimeout when
// it does (a safety net for a terminal, or a multiplexer in front of it,
// that claimed support it doesn't actually deliver).
type Engine struct {
	screen   tcell.Screen
	game     core.Game
	bus      *core.Bus
	commands chan core.Command
	corrID   string
	paused   bool
	quit     bool

	terminalReleases bool
	held             map[keyID]heldKey
	now              func() time.Time

	// notice, when non-nil, is drawn in the center of the screen every
	// frame - a manual SPACE pause, an agent's pause/question, or this
	// game-over notice (see screenNotice, apply and togglePause). Only one
	// is ever shown: setting it always replaces whatever was there before.
	notice *screenNotice

	// flashes carries banners from other goroutines (see Flash) onto the
	// game loop; banner is the one currently showing, until bannerUntil.
	flashes     chan *screenNotice
	banner      *screenNotice
	bannerUntil time.Time
}

// keyID identifies a physical key regardless of the shift state of its rune.
type keyID struct {
	key tcell.Key
	r   rune
}

// heldKey is a key the engine considers held, with its last press and the
// timeout (from last) that will make expireHeld synthesise its release.
type heldKey struct {
	ev      *tcell.EventKey
	last    time.Time
	timeout time.Duration
}

// New creates an engine for the given game.
func New(screen tcell.Screen, game core.Game) *Engine {
	// The engine owns these keys (handleKey); the game only names them.
	core.SetGlobalKeys(game, core.GlobalKeys{Pause: "Space", Reset: "R", Leave: "Esc", LeaveAction: "menu"})
	return &Engine{
		screen:   screen,
		game:     game,
		bus:      core.NewBus(),
		commands: make(chan core.Command, 64),
		flashes:  make(chan *screenNotice, 8),
		held:     make(map[keyID]heldKey),
		now:      time.Now,
	}
}

// Flash shows a transient banner along the top of the screen, styled for
// the agent, without touching the game's state - the "notify only" way of
// surfacing an agent event when auto-pause is off. It is safe to call from
// any goroutine and never blocks.
func (e *Engine) Flash(lines []string, agent string) {
	n := &screenNotice{lines: lines, style: noticeStyleForAgent(agent)}
	select {
	case e.flashes <- n:
	default:
	}
}

// drainFlashes adopts the newest queued banner, if any.
func (e *Engine) drainFlashes() {
	for {
		select {
		case n := <-e.flashes:
			e.banner = n
			e.bannerUntil = e.now().Add(bannerDuration)
		default:
			return
		}
	}
}

// drawBanner paints the current banner as a small card in the top-right
// corner - out of the way of the board, the way the home screen shows its
// toast - while it lasts.
func (e *Engine) drawBanner() {
	if e.banner == nil {
		return
	}
	if e.now().After(e.bannerUntil) {
		e.banner = nil
		return
	}
	w, _ := e.screen.Size()
	width := 0
	for _, l := range e.banner.lines {
		if n := len([]rune(l)); n > width {
			width = n
		}
	}
	bw := width + 4
	if bw > w-2 {
		bw = w - 2
	}
	x := w - bw - 1
	if x < 0 {
		x = 0
	}
	for row := 0; row < len(e.banner.lines)+2; row++ {
		for i := 0; i < bw; i++ {
			e.screen.SetContent(x+i, row, ' ', nil, e.banner.style)
		}
	}
	for i, l := range e.banner.lines {
		runes := []rune(l)
		if len(runes) > bw-4 {
			runes = runes[:bw-4]
		}
		for j, r := range runes {
			e.screen.SetContent(x+2+j, 1+i, r, nil, e.banner.style)
		}
	}
	e.screen.Show()
}

// Bus returns the engine's event bus.
func (e *Engine) Bus() *core.Bus { return e.bus }

// SetTerminalReleases tells the engine whether the terminal reports key
// releases (as keystate.ReleaseMod-marked key events). When it does, the
// engine forwards those instead of synthesising one from auto-repeat gaps -
// but it still keeps a much longer watchdog running (terminalHoldTimeout),
// in case the terminal (or a multiplexer in front of it) doesn't actually
// deliver on the claim.
func (e *Engine) SetTerminalReleases(on bool) {
	e.terminalReleases = on
}

// Commands returns the commands supported by the running game, if any.
func (e *Engine) Commands() []core.CommandSpec {
	if c, ok := e.game.(core.Commandable); ok {
		return c.Commands()
	}
	return nil
}

// SendCommand enqueues a command to be applied on the game loop goroutine.
// It never blocks; a full queue produces a command.rejected event instead.
func (e *Engine) SendCommand(cmd core.Command) {
	select {
	case e.commands <- cmd:
	default:
		e.bus.Emit(core.Event{
			Type:          "command.rejected",
			CorrelationID: cmd.ID,
			Payload:       core.MustJSON(map[string]string{"reason": "command queue full"}),
		})
	}
}

// Run starts the game loop and returns when ESC is pressed.
func (e *Engine) Run() {
	if err := e.game.Init(e.screen); err != nil {
		return
	}

	e.wireEmitter()

	e.paused = false
	e.quit = false

	ticker := time.NewTicker(framePeriod)
	defer ticker.Stop()

	for !e.quit {
		e.drainCommands()
		e.drainFlashes()
		e.handleEvents()
		e.expireHeld()
		if e.quit {
			break
		}

		e.game.Update()
		e.game.Draw(e.screen)
		if e.shouldDrawNotice() {
			e.drawScreenNotice(e.notice)
		}
		e.drawBanner()

		<-ticker.C
	}
}

// shouldDrawNotice reports whether e.notice is still current: the one-shot
// game-over notice always is (it's cleared by the dismissing keypress
// itself, see handleKey), while a pause notice only lasts as long as the
// game actually reports itself paused.
func (e *Engine) shouldDrawNotice() bool {
	if e.notice == nil {
		return false
	}
	switch e.notice.lifecycle {
	case noticeDismissOnKey, noticeUntilCleared:
		return true
	default: // noticeWhilePaused
		ps, ok := e.game.(core.PauseState)
		return ok && ps.IsPaused()
	}
}

// naturalGameOverNotice replaces every game's own native "GAME OVER" text -
// win or lose, whichever way a game ends on its own - the moment it emits
// its own "<name>.game_over" event, so a game over is a system message like
// any other pause, in the same brand color, without needing an agent or an
// external command involved at all (see wireEmitter).
var naturalGameOverNotice = &screenNotice{
	lines:     []string{"GAME OVER"},
	style:     noticeStyleForAgent(""),
	lifecycle: noticeUntilCleared,
}

// wireEmitter gives the game an emitter that stamps the active command's
// correlation ID onto every event before publishing it to the bus, and -
// since every screen message is meant to go through the engine's own
// notice now, not a game's own hardcoded rendering - reacts to the game's
// own "<name>.game_over" event by showing naturalGameOverNotice.
func (e *Engine) wireEmitter() {
	if s, ok := e.game.(core.EmitterSetter); ok {
		s.SetEmitter(core.EmitterFunc(func(ev core.Event) {
			ev.CorrelationID = e.corrID
			e.bus.Emit(ev)
			if strings.HasSuffix(ev.Type, ".game_over") {
				e.notice = naturalGameOverNotice
			}
		}))
	}
}

// drainCommands applies all currently queued external commands.
func (e *Engine) drainCommands() {
	for {
		select {
		case cmd := <-e.commands:
			e.apply(cmd)
		default:
			return
		}
	}
}

// apply routes one command to the game and publishes the outcome. A command
// that succeeds produces the game's own domain event (with the command's
// correlation ID); a failure produces command.rejected.
func (e *Engine) apply(cmd core.Command) {
	c, ok := e.game.(core.Commandable)
	if !ok {
		e.bus.Emit(core.Event{
			Type:          "command.rejected",
			CorrelationID: cmd.ID,
			Payload:       core.MustJSON(map[string]string{"reason": "game does not accept commands"}),
		})
		return
	}

	e.corrID = cmd.ID
	defer func() { e.corrID = "" }()

	if err := c.HandleCommand(cmd); err != nil {
		e.bus.Emit(core.Event{
			Type:          "command.rejected",
			CorrelationID: cmd.ID,
			Payload:       core.MustJSON(map[string]string{"reason": err.Error()}),
		})
		return
	}

	// A "<game>.pause" command that didn't actually pause the game means it
	// was already over: Pause() no-ops once gameOver is set (see each
	// game's implementation), silently, with no event of its own. Without
	// this, an agent settling after the player already lost would produce
	// no feedback at all. Otherwise, a pause that did succeed gets its own
	// attributed overlay for as long as it lasts.
	if strings.HasSuffix(cmd.Type, ".pause") {
		if ps, ok := e.game.(core.PauseState); ok {
			if ps.IsPaused() {
				e.notice = pauseOverlayNotice(cmd.Payload)
			} else {
				e.notice = gameOverNotice(cmd.Payload)
			}
		}
		return
	}
	if strings.HasSuffix(cmd.Type, ".resume") {
		e.notice = nil
	}
}

func (e *Engine) handleEvents() {
	for e.screen.HasPendingEvent() {
		ev := e.screen.PollEvent()
		if ev == nil {
			continue
		}

		switch ev := ev.(type) {
		case *tcell.EventResize:
			e.screen.Sync()
		case *tcell.EventKey:
			e.handleKey(ev)
		case *tcell.EventInterrupt:
			// The launcher asking this window to close (another
			// terminalika window took over; see main.go): leave the game
			// as if ESC had been pressed.
			e.quit = true
		}
	}
}

func (e *Engine) handleKey(ev *tcell.EventKey) {
	// A key release reported by the terminal only matters to games that
	// track key state; it must never trigger a global key or HandleInput.
	// It also retires the key's watchdog entry: the terminal is doing its
	// job for this key, so expireHeld must not also fire for it later.
	if keystate.IsRelease(ev) {
		unmarked := keystate.Unmark(ev)
		if ks, ok := e.game.(core.KeyStateHandler); ok {
			ks.HandleKeyState(unmarked, false)
		}
		delete(e.held, idOf(unmarked))
		return
	}

	// A one-shot notice (a pause command that arrived too late to pause
	// anything) swallows the next real press to dismiss itself, before it
	// can reach a global keybinding or the game: the player must
	// acknowledge it before doing anything else. Persistent notices
	// (pause, game over) are not dismissed this way - SPACE/R below handle
	// those normally.
	if e.notice != nil && e.notice.lifecycle == noticeDismissOnKey {
		e.notice = nil
		return
	}

	// Global keybindings are intercepted here and never forwarded to the game.
	if ev.Key() == tcell.KeyEscape {
		e.quit = true
		return
	}

	if ev.Key() == tcell.KeyRune {
		switch ev.Rune() {
		case 'r', 'R':
			e.paused = false
			e.notice = nil
			e.game.Reset()
			return
		case ' ':
			e.togglePause()
			return
		}
	}

	if ks, ok := e.game.(core.KeyStateHandler); ok && ks.HandleKeyState(ev, true) {
		// Tracked regardless of terminalReleases: expireHeld is the
		// watchdog either way, just with a much longer fuse when the
		// terminal claims it will send the real release itself.
		id := idOf(ev)
		now := e.now()
		prev, held := e.held[id]

		timeout := synthHold
		switch {
		case e.terminalReleases:
			timeout = terminalHoldTimeout
		case held:
			if gap := now.Sub(prev.last); gap > 0 {
				timeout = dynamicHold(gap)
			}
		}
		e.held[id] = heldKey{ev: ev, last: now, timeout: timeout}
		return
	}

	e.game.HandleInput(ev)
}

// expireHeld synthesises a release for every key that has gone quiet (no
// press, auto-repeat, or terminal release) for longer than its timeout: set
// per key at press time (see handleKey) - synthHold on the first press,
// dynamicHold's measured-gap-based value from the second press on, or the
// much longer terminalHoldTimeout when the terminal claims to report
// releases itself but a stuck key means it hasn't (see terminalHoldTimeout).
func (e *Engine) expireHeld() {
	if len(e.held) == 0 {
		return
	}
	ks, ok := e.game.(core.KeyStateHandler)
	now := e.now()
	for id, h := range e.held {
		if now.Sub(h.last) < h.timeout {
			continue
		}
		delete(e.held, id)
		if ok {
			ks.HandleKeyState(h.ev, false)
		}
	}
}

// drawScreenNotice overlays n on the game's own pause/game-over band - the
// game says where it is (core.OverlayReporter) - widened to cover it
// completely, so the two never show side by side; with no band up it is
// centered on the screen. It's the only thing the engine itself draws
// outside of the game, so it never has to clear anything first. A
// multi-line notice is drawn as one solid block (every line padded to the
// widest) so it reads as a single card over the board rather than ragged
// text.
func (e *Engine) drawScreenNotice(n *screenNotice) {
	w, h := e.screen.Size()
	startY := h / 2
	left, right := -1, -1
	if e.game != nil {
		if band, ok := core.OverlayAreaOf(e.game); ok {
			startY = band.Y
			left, right = band.X, band.X+band.W-1
		}
	}
	lines := n.lines
	if len(lines) > 1 {
		lines = padBlock(lines)
	}
	for i, line := range lines {
		x := w/2 - len([]rune(line))/2
		if i == 0 || len(lines) > 1 {
			if left >= 0 && left < x {
				line = strings.Repeat(" ", x-left) + line
				x = left
			}
			if end := x + len([]rune(line)); right >= 0 && right+1 > end {
				line += strings.Repeat(" ", right+1-end)
			}
		}
		for j, r := range []rune(line) {
			e.screen.SetContent(x+j, startY+i, r, nil, n.style)
		}
	}
	e.screen.Show()
}

// padBlock centers every line inside the width of the widest one, with a
// one-cell margin, so a multi-line notice paints a solid rectangle.
func padBlock(lines []string) []string {
	width := 0
	for _, l := range lines {
		if n := len([]rune(l)); n > width {
			width = n
		}
	}
	width += 2
	out := make([]string, len(lines))
	for i, l := range lines {
		n := len([]rune(l))
		left := (width - n) / 2
		right := width - n - left
		out[i] = strings.Repeat(" ", left) + l + strings.Repeat(" ", right)
	}
	return out
}

// dynamicHold turns a measured gap between a held key's auto-repeat presses
// into a release timeout: the gap plus repeatGapMargin slack, clamped to
// [minDynamicHold, maxDynamicHold] so a burst of buffered input or a
// scheduling hiccup can't produce an unreasonably short or long one.
func dynamicHold(gap time.Duration) time.Duration {
	t := time.Duration(float64(gap) * repeatGapMargin)
	if t < minDynamicHold {
		return minDynamicHold
	}
	if t > maxDynamicHold {
		return maxDynamicHold
	}
	return t
}

// idOf identifies the key of an event; runes are folded to lower case so a
// press with shift and its release without it are the same key.
func idOf(ev *tcell.EventKey) keyID {
	if ev.Key() == tcell.KeyRune {
		return keyID{key: tcell.KeyRune, r: unicode.ToLower(ev.Rune())}
	}
	return keyID{key: ev.Key()}
}

// manualPauseNotice is shown for a pause SPACE itself triggered: aqua,
// terminalika's own brand color (see noticeStyleForAgent's "" case, and
// notice.Show), the same way an agent's pause gets its own color - so any
// key-triggered pause is visually ours, not just the game's plain default.
var manualPauseNotice = &screenNotice{lines: []string{"PAUSED"}, style: noticeStyleForAgent("")}

func (e *Engine) togglePause() {
	// Prefer the game's real pause state so that pauses triggered by external
	// commands (WebSocket, pi subscription) are picked up and a single SPACE
	// resumes them.
	if ps, ok := e.game.(core.PauseState); ok {
		if ps.IsPaused() {
			e.game.Resume()
			e.notice = nil
			return
		}
		e.game.Pause()
		e.notice = manualPauseNotice
		return
	}

	// Fallback for games that do not report their pause state.
	if e.paused {
		e.paused = false
		e.game.Resume()
		e.notice = nil
		return
	}

	e.paused = true
	e.game.Pause()
	e.notice = manualPauseNotice
}
