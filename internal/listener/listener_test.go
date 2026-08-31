package listener

import (
	"os"
	"testing"
	"time"
)

func TestStale(t *testing.T) {
	if stale(record{Heartbeat: time.Now()}) {
		t.Error("fresh heartbeat reported stale")
	}
	if !stale(record{Heartbeat: time.Now().Add(-time.Hour)}) {
		t.Error("hour-old heartbeat not reported stale")
	}
}

func TestCheckUnheld(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	if got := Check(); got.Held {
		t.Errorf("Check() = %+v, want Held=false", got)
	}
}

func TestCheckIgnoresOwnSeat(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	seat, err := Claim(Window, nil)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	defer seat.Release()

	if got := Check(); got.Held {
		t.Errorf("Check() = %+v, want Held=false (own seat)", got)
	}
	if !seat.Held() {
		t.Error("seat.Held() = false right after Claim")
	}
}

func TestCheckReportsOtherLiveHolderAndKind(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	other := os.Getpid() + 1
	if err := write(Path(), record{PID: other, Kind: Window, Heartbeat: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got := Check()
	if !got.Held || got.PID != other || got.Kind != Window {
		t.Errorf("Check() = %+v, want Held=true PID=%d Kind=window", got, other)
	}

	// A seat file from before kinds existed is a window's.
	if err := write(Path(), record{PID: other, Heartbeat: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := Check(); !got.Held || got.Kind != Window {
		t.Errorf("Check() = %+v, want an untagged seat reported as a window", got)
	}
}

func TestCheckIgnoresStaleHolder(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	if err := write(Path(), record{PID: os.Getpid() + 1, Heartbeat: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	if got := Check(); got.Held {
		t.Errorf("Check() = %+v, want Held=false (stale)", got)
	}
}

func TestReleaseRemovesOwnSeat(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	seat, err := Claim(Window, nil)
	if err != nil {
		t.Fatal(err)
	}
	seat.Release()

	if _, err := os.Stat(Path()); !os.IsNotExist(err) {
		t.Fatalf("seat file should be removed, stat err = %v", err)
	}
}

func TestReleaseLeavesOtherHoldersSeat(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	seat, err := Claim(Window, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Someone else takes over before we release.
	if err := write(Path(), record{PID: os.Getpid() + 1, Heartbeat: time.Now()}); err != nil {
		t.Fatal(err)
	}
	seat.Release()

	if _, err := os.Stat(Path()); err != nil {
		t.Fatalf("other holder's seat should survive Release, stat err = %v", err)
	}
}

func TestSeatDetectsTakeover(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	withFastHeartbeat(t)

	seat, err := Claim(Window, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer seat.Release()

	if !seat.Held() {
		t.Fatal("expected to hold the seat right after Claim")
	}

	if err := write(Path(), record{PID: os.Getpid() + 1, Heartbeat: time.Now()}); err != nil {
		t.Fatal(err)
	}

	waitUntil(t, func() bool { return !seat.Held() }, "seat.Held() still true long after takeover")
	select {
	case <-seat.Lost():
	case <-time.After(time.Second):
		t.Fatal("Lost() not closed after takeover")
	}
}

func TestSeatCallsOnLostOnTakeover(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	withFastHeartbeat(t)

	lost := make(chan struct{})
	seat, err := Claim(Window, func() { close(lost) })
	if err != nil {
		t.Fatal(err)
	}
	defer seat.Release()

	if err := write(Path(), record{PID: os.Getpid() + 1, Heartbeat: time.Now()}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-lost:
	case <-time.After(2 * time.Second):
		t.Fatal("onLost was not called after takeover")
	}
}

func TestListenerSeatSurvivesMissingFile(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	withFastHeartbeat(t)

	seat, err := Claim(Window, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer seat.Release()

	// A vanished listener file is re-written by the heartbeat, not treated
	// as a loss (only the daemon seat stops on removal).
	_ = os.Remove(Path())
	time.Sleep(5 * heartbeatInterval)
	if !seat.Held() {
		t.Fatal("listener seat should have re-written its file")
	}
}

func withFastHeartbeat(t *testing.T) {
	t.Helper()
	origHeartbeat, origStale := heartbeatInterval, staleAfter
	heartbeatInterval = 10 * time.Millisecond
	staleAfter = 100 * time.Millisecond
	t.Cleanup(func() {
		heartbeatInterval, staleAfter = origHeartbeat, origStale
	})
}

func waitUntil(t *testing.T, done func() bool, timeoutMsg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatal(timeoutMsg)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
