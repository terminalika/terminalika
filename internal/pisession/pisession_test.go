package pisession

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSessionDirName(t *testing.T) {
	cases := map[string]string{
		"/home/hyvin/terminalika":  "--home-hyvin-terminalika--",
		"/home/hyvin/terminalika/": "--home-hyvin-terminalika--",
		"/":                        "----",
		"rel/path":                 "--rel-path--",
	}
	for in, want := range cases {
		if got := sessionDirName(in); got != want {
			t.Errorf("sessionDirName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSettledEvent(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`{"type":"message","message":{"role":"assistant","stopReason":"stop"}}`, true},
		{`{"type":"message","message":{"role":"assistant","stopReason":"length"}}`, true},
		{`{"type":"message","message":{"role":"assistant","stopReason":"error"}}`, true},
		{`{"type":"message","message":{"role":"assistant","stopReason":"toolUse"}}`, false},
		{`{"type":"message","message":{"role":"assistant","stopReason":"pending"}}`, false},
		{`{"type":"message","message":{"role":"assistant"}}`, false}, // no stopReason
		{`{"type":"message","message":{"role":"user"}}`, false},
		{`{"type":"message","message":{"role":"toolResult"}}`, false},
		{`{"type":"session","version":3}`, false},
		{`not json`, false},
		{``, false},
	}
	for _, c := range cases {
		if got := settledEvent([]byte(c.line)); got != c.want {
			t.Errorf("settledEvent(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestResolveScope(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PI_SESSIONS_DIR", root)

	// Default: every session under the sessions root, recursively.
	got := ResolveScope(Options{})
	if got.File != "" || got.Dir != root || !got.Recursive {
		t.Errorf("ResolveScope(default) = %+v, want Dir=%q Recursive=true", got, root)
	}

	// Dir: only that project's session directory, non-recursive.
	got = ResolveScope(Options{Dir: "/home/u/proj"})
	wantDir := filepath.Join(root, "--home-u-proj--")
	if got.File != "" || got.Dir != wantDir || got.Recursive {
		t.Errorf("ResolveScope(dir) = %+v, want Dir=%q Recursive=false", got, wantDir)
	}

	// Session: an explicit file.
	got = ResolveScope(Options{Session: "/tmp/x.jsonl"})
	if got.File != "/tmp/x.jsonl" {
		t.Errorf("ResolveScope(session) = %+v, want File=/tmp/x.jsonl", got)
	}
}

func TestWatcherIgnoresHistory(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	history := `{"type":"message","message":{"role":"assistant","stopReason":"stop"}}` + "\n"
	if err := os.WriteFile(p, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{File: p}, func() { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	time.Sleep(60 * time.Millisecond)

	mu.Lock()
	n := got
	mu.Unlock()
	if n != 0 {
		t.Errorf("history triggered onSettled %d times, want 0", n)
	}
}

func TestWatcherDetectsSettled(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{File: p}, func() { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A tool-calling assistant message must be ignored.
	appendLine(t, p, `{"type":"message","message":{"role":"assistant","stopReason":"toolUse"}}`)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if n := got; n != 0 {
		t.Fatalf("toolUse triggered onSettled %d times, want 0", n)
	}
	mu.Unlock()

	// A final assistant message must trigger exactly once.
	appendLine(t, p, `{"type":"message","message":{"role":"assistant","stopReason":"stop"}}`)
	waitFor(t, &mu, &got, 1)
}

func TestWatcherDirectoryPicksUpNewSessions(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{Dir: dir}, func() { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A session file created after the watcher started must be picked up.
	time.Sleep(40 * time.Millisecond)
	p := filepath.Join(dir, "new.jsonl")
	appendLine(t, p, `{"type":"session","version":3}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, p, `{"type":"message","message":{"role":"assistant","stopReason":"stop"}}`)
	waitFor(t, &mu, &got, 1)
}

func TestWatcherNonRecursiveSkipsSubdirs(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{Dir: root, Recursive: false}, func() { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A settled message in a subdirectory must be ignored (non-recursive).
	subp := filepath.Join(sub, "s.jsonl")
	appendLine(t, subp, `{"type":"session","version":3}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, subp, `{"type":"message","message":{"role":"assistant","stopReason":"stop"}}`)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if n := got; n != 0 {
		t.Fatalf("subdir session triggered onSettled %d times, want 0", n)
	}
	mu.Unlock()

	// A settled message at the top level must trigger.
	topp := filepath.Join(root, "t.jsonl")
	appendLine(t, topp, `{"type":"session","version":3}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, topp, `{"type":"message","message":{"role":"assistant","stopReason":"stop"}}`)
	waitFor(t, &mu, &got, 1)
}

// appendLine appends a JSONL line to path, creating the file if needed.
func appendLine(t *testing.T, path, line string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(line + "\n"); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls got until it equals want or times out.
func waitFor(t *testing.T, mu *sync.Mutex, got *int, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := *got
		mu.Unlock()
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: got %d, want %d", n, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
