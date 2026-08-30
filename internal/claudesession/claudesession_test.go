package claudesession

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSessionDirName(t *testing.T) {
	// sessionDirName must encode the absolute, cleaned path on every platform.
	root := t.TempDir()
	dir := filepath.Join(root, "proj")
	want := encodeSessionDir(filepath.Clean(dir))

	if got := sessionDirName(dir); got != want {
		t.Errorf("sessionDirName(%q) = %q, want %q", dir, got, want)
	}

	// Trailing separators must be cleaned away before encoding.
	if got := sessionDirName(dir + string(filepath.Separator)); got != want {
		t.Errorf("sessionDirName(%q) = %q, want %q", dir+string(filepath.Separator), got, want)
	}
}

func TestEncodeSessionDir(t *testing.T) {
	cases := map[string]string{
		"/home/user/proj": "-home-user-proj",
		"/":               "-",
		`C:\Users\x\proj`: "C--Users-x-proj",
		`C:/Users/x/proj`: "C--Users-x-proj",
		`\home\x`:         "-home-x",
	}
	for in, want := range cases {
		if got := encodeSessionDir(in); got != want {
			t.Errorf("encodeSessionDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionsRootEnvOverride(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	if got := sessionsRoot(); got != filepath.Join(dir, "projects") {
		t.Errorf("sessionsRoot() = %q, want %q", got, filepath.Join(dir, "projects"))
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home dir")
	}
	cases := map[string]string{
		"~":        home,
		"~/foo":    filepath.Join(home, "foo"),
		"~\\foo":   filepath.Join(home, "foo"),
		"/abs/x":   "/abs/x",
		"no/tilde": "no/tilde",
	}
	for in, want := range cases {
		if got := expandHome(in); got != want {
			t.Errorf("expandHome(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSettleKind(t *testing.T) {
	cases := []struct {
		line string
		ok   bool
		want SettleKind
	}{
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`, true, SettleDone},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"max_tokens"}}`, true, SettleDone},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"stop_sequence"}}`, true, SettleDone},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"refusal"}}`, true, SettleDone},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash"}]}}`, false, 0},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use"}}`, false, 0},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"AskUserQuestion"}]}}`, true, SettleQuestion},
		{`{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"text","text":"hmm"},{"type":"tool_use","name":"AskUserQuestion"}]}}`, true, SettleQuestion},
		{`{"type":"assistant","message":{"role":"assistant"}}`, false, 0}, // no stop_reason
		{`{"type":"user","message":{"role":"user"}}`, false, 0},
		{`{"type":"system","subtype":"init"}`, false, 0},
		{`not json`, false, 0},
		{``, false, 0},
	}
	for _, c := range cases {
		kind, ok := settleKind([]byte(c.line))
		if ok != c.ok || (ok && kind != c.want) {
			t.Errorf("settleKind(%q) = (%v, %v), want (%v, %v)", c.line, kind, ok, c.want, c.ok)
		}
	}
}

func TestResolveScope(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", configDir)
	projectsRoot := filepath.Join(configDir, "projects")

	// Default: every session under the projects root, one level deep.
	got := ResolveScope(Options{})
	if got.File != "" || got.Dir != projectsRoot || !got.Recursive {
		t.Errorf("ResolveScope(default) = %+v, want Dir=%q Recursive=true", got, projectsRoot)
	}

	// Dir: only that project's session directory, non-recursive.
	got = ResolveScope(Options{Dir: "/home/u/proj"})
	wantDir := filepath.Join(projectsRoot, "-home-u-proj")
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
	history := `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}` + "\n"
	if err := os.WriteFile(p, []byte(history), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{File: p}, func(SettleKind) { mu.Lock(); got++; mu.Unlock() })
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
	w := NewWatcher(Scope{File: p}, func(SettleKind) { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A tool-calling assistant message must be ignored.
	appendLine(t, p, `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use"}}`)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if n := got; n != 0 {
		t.Fatalf("tool_use triggered onSettled %d times, want 0", n)
	}
	mu.Unlock()

	// A final assistant message must trigger exactly once.
	appendLine(t, p, `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`)
	waitFor(t, &mu, &got, 1)
}

func TestWatcherDetectsAskUserQuestion(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got int
	var kinds []SettleKind
	w := NewWatcher(Scope{File: p}, func(k SettleKind) {
		mu.Lock()
		got++
		kinds = append(kinds, k)
		mu.Unlock()
	})
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A Bash tool call must still be ignored: the agent is working, not
	// waiting on the user.
	appendLine(t, p, `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"Bash"}]}}`)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if n := got; n != 0 {
		t.Fatalf("Bash tool_use triggered onSettled %d times, want 0", n)
	}
	mu.Unlock()

	// An AskUserQuestion tool call must trigger, as SettleQuestion.
	appendLine(t, p, `{"type":"assistant","message":{"role":"assistant","stop_reason":"tool_use","content":[{"type":"tool_use","name":"AskUserQuestion"}]}}`)
	waitFor(t, &mu, &got, 1)
	mu.Lock()
	if len(kinds) != 1 || kinds[0] != SettleQuestion {
		t.Errorf("kinds = %v, want [SettleQuestion]", kinds)
	}
	mu.Unlock()
}

func TestWatcherDirectoryPicksUpNewSessions(t *testing.T) {
	dir := t.TempDir()

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{Dir: dir}, func(SettleKind) { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A session file created after the watcher started must be picked up.
	time.Sleep(40 * time.Millisecond)
	p := filepath.Join(dir, "new.jsonl")
	appendLine(t, p, `{"type":"system","subtype":"init"}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, p, `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`)
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
	w := NewWatcher(Scope{Dir: root, Recursive: false}, func(SettleKind) { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A settled message in a subdirectory must be ignored (non-recursive).
	subp := filepath.Join(sub, "s.jsonl")
	appendLine(t, subp, `{"type":"system","subtype":"init"}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, subp, `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if n := got; n != 0 {
		t.Fatalf("subdir session triggered onSettled %d times, want 0", n)
	}
	mu.Unlock()

	// A settled message at the top level must trigger.
	topp := filepath.Join(root, "t.jsonl")
	appendLine(t, topp, `{"type":"system","subtype":"init"}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, topp, `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`)
	waitFor(t, &mu, &got, 1)
}

// TestWatcherRecursiveSkipsSubagentSubdir mirrors the layout Claude Code
// produces: <projectsRoot>/<project>/<sessionID>.jsonl next to a
// <projectsRoot>/<project>/<sessionID>/ subfolder holding that session's
// subagent transcripts. A recursive (projects-root) scope must watch the
// project-level session files but never descend into the subagent subfolder.
func TestWatcherRecursiveSkipsSubagentSubdir(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "-home-me-proj")
	subagentDir := filepath.Join(project, "session-id")
	if err := os.MkdirAll(subagentDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var got int
	w := NewWatcher(Scope{Dir: root, Recursive: true}, func(SettleKind) { mu.Lock(); got++; mu.Unlock() })
	w.interval = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	// A settled message in the subagent subfolder must be ignored.
	subp := filepath.Join(subagentDir, "sub.jsonl")
	appendLine(t, subp, `{"type":"system","subtype":"init"}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, subp, `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if n := got; n != 0 {
		t.Fatalf("subagent subfolder session triggered onSettled %d times, want 0", n)
	}
	mu.Unlock()

	// A settled message in the project's own session file must trigger.
	topp := filepath.Join(project, "session-id.jsonl")
	appendLine(t, topp, `{"type":"system","subtype":"init"}`)
	time.Sleep(40 * time.Millisecond)
	appendLine(t, topp, `{"type":"assistant","message":{"role":"assistant","stop_reason":"end_turn"}}`)
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
