package daemon

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/listener"
)

func TestRunNeedsAgents(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	if err := Run(context.Background(), config.Config{}, nil); err == nil {
		t.Fatal("Run with no agents should fail instead of idling forever")
	}
	if Running() {
		t.Fatal("Running() = true after a failed Run")
	}
}

// seatHolder reads the listener seat file raw (the daemon and the test
// share a PID, so listener.Check can't tell them apart).
func seatHolder(t *testing.T) (pid int, kind string, ok bool) {
	t.Helper()
	data, err := os.ReadFile(listener.Path())
	if err != nil {
		return 0, "", false
	}
	var r struct {
		PID  int    `json:"pid"`
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatalf("bad seat file: %v", err)
	}
	return r.PID, r.Kind, true
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(msg)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestDaemonYieldsListenerSeatToWindows is the daemon's whole contract:
// it holds the listener seat while nobody else does, steps aside the
// moment a window takes it, and comes back once the window is gone.
func TestDaemonYieldsListenerSeatToWindows(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	orig := seatPoll
	seatPoll = 50 * time.Millisecond
	t.Cleanup(func() { seatPoll = orig })

	cfg := config.Config{Agents: []string{"cursor"}, Webhook: config.Webhook{Disabled: true}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg, nil) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Run did not return after cancel")
		}
	}()

	me := os.Getpid()
	waitFor(t, 3*time.Second, func() bool {
		pid, kind, ok := seatHolder(t)
		return ok && pid == me && kind == string(listener.Daemon)
	}, "daemon never took the free listener seat")
	if !Running() && !listener.CheckDaemon().Held {
		// Own PID: CheckDaemon reports Held=false for ourselves, so only
		// the file's existence can be checked here.
		if _, err := os.Stat(listener.DaemonPath()); err != nil {
			t.Fatalf("daemon seat file missing: %v", err)
		}
	}

	// A window opens: it takes the seat. The daemon must leave it alone.
	window, _ := json.Marshal(map[string]any{"pid": me + 1, "kind": "window", "heartbeat": time.Now()})
	if err := os.WriteFile(listener.Path(), window, 0o644); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond) // > one heartbeat: the daemon has noticed
	if pid, _, ok := seatHolder(t); !ok || pid != me+1 {
		t.Fatalf("daemon overwrote a window's seat (pid %d)", pid)
	}

	// The window closes (releases its seat): the daemon takes it back.
	if err := os.Remove(listener.Path()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		pid, kind, ok := seatHolder(t)
		return ok && pid == me && kind == string(listener.Daemon)
	}, "daemon never reclaimed the listener seat after the window closed")
}
