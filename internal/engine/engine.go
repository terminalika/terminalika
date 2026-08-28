// Package engine runs a single core.Game and owns the global keybindings.
package engine

import (
	"time"

	core "github.com/terminalika/terminalika-core"

	"github.com/gdamore/tcell/v2"
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
type Engine struct {
	screen   tcell.Screen
	game     core.Game
	bus      *core.Bus
	commands chan core.Command
	corrID   string
	paused   bool
	quit     bool
}

// New creates an engine for the given game.
func New(screen tcell.Screen, game core.Game) *Engine {
	return &Engine{
		screen:   screen,
		game:     game,
		bus:      core.NewBus(),
		commands: make(chan core.Command, 64),
	}
}

// Bus returns the engine's event bus.
func (e *Engine) Bus() *core.Bus { return e.bus }

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

	for !e.quit {
		e.drainCommands()
		e.handleEvents()
		if e.quit {
			break
		}

		e.game.Update()
		e.game.Draw(e.screen)

		time.Sleep(16 * time.Millisecond)
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

	e.game.HandleInput(ev)
}

func (e *Engine) togglePause() {
	if e.paused {
		e.paused = false
		e.game.Resume()
		return
	}

	e.paused = true
	e.game.Pause()
}
