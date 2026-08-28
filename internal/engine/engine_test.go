package engine

import (
	"testing"

	"github.com/gdamore/tcell/v2"
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
