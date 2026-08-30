package notify

import (
	"sync"
	"testing"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/config"
)

// capture replaces the desktop channel with one that records the call.
func capture(n *Notifier) (wait func(t *testing.T) (title, body string)) {
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
	return func(t *testing.T) (string, string) {
		t.Helper()
		select {
		case <-shown:
		case <-time.After(time.Second):
			t.Fatal("desktop notification never sent")
		}
		mu.Lock()
		defer mu.Unlock()
		return gotTitle, gotBody
	}
}

func TestAlwaysDelivers(t *testing.T) {
	claude, _ := agents.Lookup("claude")
	n := New(config.DesktopAlways, func() bool { return true })
	wait := capture(n)
	n.Notify(agents.Event{Agent: claude, Kind: agents.InputRequired})
	if title, body := wait(t); title != "Claude Code needs your input!" || body == "" {
		t.Errorf("desktop(%q, %q)", title, body)
	}
}

func TestModesAgainstFocus(t *testing.T) {
	focused := true
	window := func() bool { return focused }
	cases := []struct {
		mode      config.DesktopMode
		focused   func() bool
		isFocused bool
		want      bool
	}{
		{config.DesktopNever, window, true, false},
		{config.DesktopNever, nil, false, false},
		{config.DesktopAlways, window, true, true},
		{config.DesktopAlways, nil, false, true},
		{config.DesktopUnfocused, window, true, false},
		{config.DesktopUnfocused, window, false, true},
		{config.DesktopUnfocused, nil, false, true},
		{config.DesktopNoWindow, window, true, false},
		{config.DesktopNoWindow, window, false, false},
		{config.DesktopNoWindow, nil, false, true},
		{"", window, false, false},
	}
	for _, c := range cases {
		focused = c.isFocused
		n := New(c.mode, c.focused)
		if got := n.Wants(); got != c.want {
			t.Errorf("mode=%q headless=%v focused=%v: Wants = %v, want %v", c.mode, c.focused == nil, c.isFocused, got, c.want)
		}
	}
}

func TestSilentModeNeverCallsDesktop(t *testing.T) {
	n := New(config.DesktopNever, nil)
	n.desktop = func(string, string) error { t.Error("desktop called"); return nil }
	pi, _ := agents.Lookup("pi")
	n.Notify(agents.Event{Agent: pi})
	time.Sleep(20 * time.Millisecond)
	if n.Describe() != "desktop: off" {
		t.Errorf("Describe = %q", n.Describe())
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
