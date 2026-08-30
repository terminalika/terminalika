package aiderhistory

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

func TestResolveDefaultsToCwdHistoryFile(t *testing.T) {
	p := Resolve(Options{Dir: "/tmp/proj"})
	if p != filepath.Join("/tmp/proj", HistoryFile) {
		t.Errorf("Resolve = %q", p)
	}
	if p := Resolve(Options{File: "/x/h.md"}); p != "/x/h.md" {
		t.Errorf("Resolve(File) = %q", p)
	}
}

func TestWatcherEmitsOncePerAssistantTurn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, HistoryFile)
	if err := os.WriteFile(path, []byte("# aider chat started at 2026\n\n#### old prompt\n\nold reply\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := NewWatcher(path)
	clock := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return clock }

	var got []agents.Event
	emit := func(ev agents.Event) { got = append(got, ev) }

	w.tick(emit) // first sight: history skipped
	if len(got) != 0 {
		t.Fatalf("existing history emitted %d events", len(got))
	}

	appendTo(t, path, "\n#### make it faster\n\n")
	w.tick(emit)
	if len(got) != 0 {
		t.Fatalf("a user prompt emitted %d events", len(got))
	}

	appendTo(t, path, "Sure, here is the change.\n\n```python\nx = 1\n```\n\n> Applied edit to main.py\n")
	w.tick(emit)
	if len(got) != 1 || got[0].Agent.ID != agents.Aider || got[0].Kind != agents.Finished {
		t.Fatalf("assistant reply emitted %+v, want one Finished event", got)
	}

	// More assistant lines right away are the same turn.
	appendTo(t, path, "Anything else?\n")
	w.tick(emit)
	if len(got) != 1 {
		t.Fatalf("continuation emitted %d events, want 1", len(got))
	}

	// After a quiet period a reply without a prompt (aider reflecting on
	// lint output) counts as a new turn.
	clock = clock.Add(10 * time.Second)
	appendTo(t, path, "Fixed the lint error too.\n")
	w.tick(emit)
	if len(got) != 2 {
		t.Fatalf("post-quiet reply emitted %d events, want 2", len(got))
	}
}

func appendTo(t *testing.T, path, s string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}
