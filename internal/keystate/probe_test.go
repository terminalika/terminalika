package keystate

import (
	"runtime"
	"testing"
)

// TestProbeDistrustsZellij checks that Probe never claims release support
// under zellij, which answers the kitty query without actually relaying
// releases: trusting it would leave the engine waiting on its long
// terminalHoldTimeout watchdog on every release instead of reacting to
// auto-repeat gaps immediately.
func TestProbeDistrustsZellij(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zellij override only applies to the non-Windows probe path")
	}
	t.Setenv("ZELLIJ", "0")
	if s := Probe(); s.Releases() {
		t.Fatalf("Probe() under zellij = %+v, want no release support claimed", s)
	}
}

// TestProbeDistrustsTmux checks that Probe never claims release support
// under tmux: tmux does not implement the kitty keyboard protocol (no
// push/pop, no release/repeat event types) - only modifyOtherKeys/fixterms
// modified-key encoding - regardless of the outer terminal or tmux config.
func TestProbeDistrustsTmux(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("tmux detection only applies to the non-Windows probe path")
	}
	t.Setenv("ZELLIJ", "")
	t.Setenv("TMUX", "/tmp/tmux-1000/default,1234,0")
	if s := Probe(); s.Releases() {
		t.Fatalf("Probe() under tmux = %+v, want no release support claimed", s)
	}
}
