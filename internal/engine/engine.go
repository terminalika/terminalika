// Package engine runs a single core.Game and owns the global keybindings.
package engine

import (
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
	return &Engine{
		screen:   screen,
		game:     game,
		bus:      core.NewBus(),
		commands: make(chan core.Command, 64),
		held:     make(map[keyID]heldKey),
		now:      time.Now,
	}
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
		e.handleEvents()
		e.expireHeld()
		if e.quit {
			break
		}

		e.game.Update()
		e.game.Draw(e.screen)

		<-ticker.C
	}
}

// wireEmitter gives the game an emitter that stamps the active command's
// correlation ID onto every event before publishing it to the bus.
func (e *Engine) wireEmitter() {
	if s, ok := e.game.(core.EmitterSetter); ok {
		s.SetEmitter(core.EmitterFunc(func(ev core.Event) {
			ev.CorrelationID = e.corrID
			e.bus.Emit(ev)
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

	// Global keybindings are intercepted here and never forwarded to the game.
	if ev.Key() == tcell.KeyEscape {
		e.quit = true
		return
	}

	if ev.Key() == tcell.KeyRune {
		switch ev.Rune() {
		case 'r', 'R':
			e.paused = false
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

func (e *Engine) togglePause() {
	// Prefer the game's real pause state so that pauses triggered by external
	// commands (WebSocket, pi subscription) are picked up and a single SPACE
	// resumes them.
	if ps, ok := e.game.(core.PauseState); ok {
		if ps.IsPaused() {
			e.game.Resume()
			return
		}
		e.game.Pause()
		return
	}

	// Fallback for games that do not report their pause state.
	if e.paused {
		e.paused = false
		e.game.Resume()
		return
	}

	e.paused = true
	e.game.Pause()
}
