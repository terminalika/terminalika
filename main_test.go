package main

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	core "github.com/terminalika/terminalika-core"
	"github.com/terminalika/terminalika-core/games"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/keystate"
)

func TestResolveWSAddrUsesFreeBasePort(t *testing.T) {
	// Grab a free port, then release it so the base address is available.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	base := probe.Addr().String()
	probe.Close()

	ln, addr, err := resolveWSAddr(base, 10)
	if err != nil {
		t.Fatalf("resolveWSAddr: %v", err)
	}
	defer ln.Close()

	if addr != base {
		t.Fatalf("addr = %q, want %q (free base port should be used as-is)", addr, base)
	}
}

func TestResolveWSAddrIncrementsWhenTaken(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupied: %v", err)
	}
	defer occupied.Close()
	base := occupied.Addr().String()

	ln, addr, err := resolveWSAddr(base, 100)
	if err != nil {
		t.Fatalf("resolveWSAddr: %v", err)
	}
	defer ln.Close()

	if addr == base {
		t.Fatalf("addr = %q, expected a different port (base is occupied)", addr)
	}
}

func TestResolveWSAddrInvalidAddress(t *testing.T) {
	if _, _, err := resolveWSAddr("not-an-address", 10); err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestResolveWSAddrInvalidPort(t *testing.T) {
	if _, _, err := resolveWSAddr("127.0.0.1:notaport", 10); err == nil {
		t.Fatal("expected error for invalid port")
	}
}

// TestHeldControlLabelMatchesKeyStateHandlers guards the invariant the
// heldControlLabel doc comment asks for: exactly the games that implement
// core.KeyStateHandler (and are therefore affected by a terminal's inability
// to report key releases) have an entry, so the key-release warning is
// never shown for - or silently missing from - the wrong game.
func TestHeldControlLabelMatchesKeyStateHandlers(t *testing.T) {
	registry := games.Default()
	for _, name := range registry.Names() {
		game, ok := registry.Get(name)
		if !ok {
			t.Fatalf("registry.Get(%q) = false", name)
		}
		_, affected := game.(core.KeyStateHandler)
		_, labeled := heldControlLabel[name]
		if affected != labeled {
			t.Errorf("%s: implements core.KeyStateHandler=%v, but has heldControlLabel entry=%v", name, affected, labeled)
		}
	}
}

func TestKeyReleaseWarningMentionsControl(t *testing.T) {
	lines := keyReleaseWarning(keystate.Support{}, "paddles")
	found := false
	for _, l := range lines {
		if l == "Held keys (paddles) will feel sticky." {
			found = true
		}
	}
	if !found {
		t.Errorf("keyReleaseWarning lines = %v, want a line naming the held control", lines)
	}
}

func TestResolveAgentsFlagOverridesConfigAndShorthandsAdd(t *testing.T) {
	cfg := config.Config{Agents: []string{"aider"}}
	ids := resolveAgents(cfg, "", false, false)
	if len(ids) != 1 || ids[0] != agents.Aider {
		t.Errorf("config only: %v", ids)
	}
	ids = resolveAgents(cfg, "cursor,claude", true, false)
	if len(ids) != 3 || ids[0] != agents.Claude || ids[1] != agents.Pi || ids[2] != agents.Cursor {
		t.Errorf("--agents + --pi: %v", ids)
	}
	ids = resolveAgents(cfg, "", false, true)
	if len(ids) != 2 || ids[0] != agents.Claude || ids[1] != agents.Aider {
		t.Errorf("--claude adds: %v", ids)
	}
}

func TestPauseCommandCarriesOverlayLinesAndAgent(t *testing.T) {
	claude, _ := agents.Lookup("claude")
	cmd := pauseCommand("snake", agents.Event{Agent: claude, Kind: agents.InputRequired})
	if cmd.Type != "snake.pause" {
		t.Errorf("Type = %q", cmd.Type)
	}
	var p struct {
		Reason string   `json:"reason"`
		Agent  string   `json:"agent"`
		Kind   string   `json:"kind"`
		Lines  []string `json:"lines"`
	}
	if err := json.Unmarshal(cmd.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Agent != "claude" || p.Kind != "input_required" || p.Reason != "Claude Code needs your input!" {
		t.Errorf("payload = %+v", p)
	}
	if len(p.Lines) != 3 || p.Lines[0] != "[INPUT REQUIRED: Claude Code]" {
		t.Errorf("lines = %q", p.Lines)
	}

	pi, _ := agents.Lookup("pi")
	cmd = pauseCommand("pong", agents.Event{Agent: pi, Kind: agents.Finished, Detail: "PI's out, you're up"})
	_ = json.Unmarshal(cmd.Payload, &p)
	if p.Lines[0] != "[AGENT READY: Pi Agent]" || p.Lines[1] != "PI's out, you're up" {
		t.Errorf("custom message lines = %q", p.Lines)
	}
}

// TestReadHookInputDoesNotHangOnAnOpenPipe guards the `terminalika notify`
// hang: a hook that leaves stdin open must not block the notification.
func TestReadHookInputDoesNotHangOnAnOpenPipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close() // never closed before the read: simulates a stuck pipe

	start := time.Now()
	if data := readHookInput(r, 100*time.Millisecond); data != nil {
		t.Errorf("data = %q, want nil on timeout", data)
	}
	if time.Since(start) > 2*time.Second {
		t.Error("readHookInput blocked past its timeout")
	}
}

func TestReadHookInputReturnsPipedPayload(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	payload := `{"hook_event_name":"Stop"}`
	if _, err := w.WriteString(payload); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if got := string(readHookInput(r, time.Second)); got != payload {
		t.Errorf("readHookInput = %q, want %q", got, payload)
	}
}
