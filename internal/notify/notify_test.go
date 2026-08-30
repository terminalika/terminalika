package notify

import (
	"sync"
	"testing"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

func TestNotifyUsesSelectedChannels(t *testing.T) {
	claude, _ := agents.Lookup("claude")
	ev := agents.Event{Agent: claude, Kind: agents.InputRequired}

	beeps := 0
	n := New(Options{Bell: true, Desktop: true}, func() error { beeps++; return nil })
	var mu sync.Mutex
	var gotTitle, gotBody string
	shown := make(chan struct{}, 1)
	n.desktop = func(title, body string) error {
		mu.Lock()
		gotTitle, gotBody = title, body
		mu.Unlock()
		shown <- struct{}{}
		return nil
	}

	n.Notify(ev)
	if beeps != 1 {
		t.Errorf("beeps = %d, want 1", beeps)
	}
	select {
	case <-shown:
	case <-time.After(time.Second):
		t.Fatal("desktop notification never sent")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotTitle != "Claude Code needs your input!" || gotBody == "" {
		t.Errorf("desktop(%q, %q)", gotTitle, gotBody)
	}
}

func TestNotifySilentWhenNothingSelected(t *testing.T) {
	beeps := 0
	n := New(Options{}, func() error { beeps++; return nil })
	n.desktop = func(string, string) error { t.Error("desktop called"); return nil }
	pi, _ := agents.Lookup("pi")
	n.Notify(agents.Event{Agent: pi})
	if beeps != 0 || n.Enabled() || n.Describe() != "silent" {
		t.Errorf("beeps=%d enabled=%v describe=%q", beeps, n.Enabled(), n.Describe())
	}
}

func TestQuoting(t *testing.T) {
	if got := appleQuote(`say "hi"`); got != `"say \"hi\""` {
		t.Errorf("appleQuote = %s", got)
	}
	if got := psQuote("it's"); got != "'it''s'" {
		t.Errorf("psQuote = %s", got)
	}
}
