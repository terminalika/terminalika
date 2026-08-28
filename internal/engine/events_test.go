package engine

import (
	"fmt"
	"testing"
	"time"

	core "github.com/terminalika/terminalika-core"
)

// commandableGame embeds fakeGame and adds command support.
type commandableGame struct {
	fakeGame
	emitter core.Emitter
	cmds    []core.Command
}

func (g *commandableGame) SetEmitter(e core.Emitter) { g.emitter = e }

func (g *commandableGame) HandleCommand(cmd core.Command) error {
	g.cmds = append(g.cmds, cmd)
	if cmd.Type != "test.run" {
		return fmt.Errorf("unknown command %q", cmd.Type)
	}
	g.emitter.Emit(core.Event{Type: "test.ran", Game: "test", At: time.Now()})
	return nil
}

func (g *commandableGame) Commands() []core.CommandSpec {
	return []core.CommandSpec{{Name: "test.run", Description: "test command"}}
}

func TestEngineAppliesCommandAndCorrelates(t *testing.T) {
	g := &commandableGame{}
	e := New(nil, g)
	sub := e.Bus().Subscribe()
	defer e.Bus().Unsubscribe(sub)

	e.wireEmitter()
	e.apply(core.Command{ID: "c1", Type: "test.run"})

	if len(g.cmds) != 1 {
		t.Fatalf("cmds = %d, want 1", len(g.cmds))
	}

	ev := <-sub
	if ev.Type != "test.ran" {
		t.Fatalf("event type = %q, want test.ran", ev.Type)
	}
	if ev.CorrelationID != "c1" {
		t.Fatalf("correlation = %q, want c1", ev.CorrelationID)
	}
}

func TestEngineRejectsUnknownCommand(t *testing.T) {
	g := &commandableGame{}
	e := New(nil, g)
	sub := e.Bus().Subscribe()
	defer e.Bus().Unsubscribe(sub)

	e.wireEmitter()
	e.apply(core.Command{ID: "c2", Type: "nope"})

	ev := <-sub
	if ev.Type != "command.rejected" {
		t.Fatalf("event type = %q, want command.rejected", ev.Type)
	}
	if ev.CorrelationID != "c2" {
		t.Fatalf("correlation = %q, want c2", ev.CorrelationID)
	}
}

func TestEngineRejectsNonCommandableGame(t *testing.T) {
	e := New(nil, &fakeGame{})
	sub := e.Bus().Subscribe()
	defer e.Bus().Unsubscribe(sub)

	e.apply(core.Command{ID: "c3", Type: "x"})

	ev := <-sub
	if ev.Type != "command.rejected" {
		t.Fatalf("event type = %q, want command.rejected", ev.Type)
	}
	if ev.CorrelationID != "c3" {
		t.Fatalf("correlation = %q, want c3", ev.CorrelationID)
	}
}

func TestSendCommandQueuesAndDrains(t *testing.T) {
	g := &commandableGame{}
	e := New(nil, g)
	e.wireEmitter()

	e.SendCommand(core.Command{ID: "c4", Type: "test.run"})
	e.drainCommands()

	if len(g.cmds) != 1 {
		t.Fatalf("cmds = %d, want 1", len(g.cmds))
	}
}

func TestCommandsReflectsGameCapabilities(t *testing.T) {
	g := &commandableGame{}
	e := New(nil, g)

	specs := e.Commands()
	if len(specs) != 1 || specs[0].Name != "test.run" {
		t.Fatalf("Commands() = %+v, want test.run", specs)
	}
}
