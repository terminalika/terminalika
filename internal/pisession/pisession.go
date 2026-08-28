// Package pisession locates and tails pi's session files. Pi appends entries
// to its session JSONL as they happen, so the watcher reacts to live activity
// without pi needing a server mode.
package pisession

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Options controls which pi sessions the watcher subscribes to.
type Options struct {
	// Dir, when set, restricts the subscription to sessions of the pi
	// running in this project directory.
	Dir string

	// Session, when set, watches a single explicit session file. It wins
	// over Dir.
	Session string
}

// Scope describes what the watcher tails.
type Scope struct {
	// File is a single session file. When set, Dir is ignored.
	File string

	// Dir is a session directory to watch for .jsonl files.
	Dir string

	// Recursive scans subdirectories of Dir.
	Recursive bool
}

// sessionsRoot returns the directory pi stores sessions in. PI_SESSIONS_DIR
// overrides it for tests and custom setups.
func sessionsRoot() string {
	if d := os.Getenv("PI_SESSIONS_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".pi", "agent", "sessions")
}

// sessionDirName converts a project directory into pi's session subdirectory
// name: "/home/user/proj" -> "--home-user-proj--".
func sessionDirName(dir string) string {
	dir = filepath.Clean(dir)
	dir = strings.TrimPrefix(dir, string(filepath.Separator))
	dir = strings.ReplaceAll(dir, string(filepath.Separator), "-")
	return "--" + dir + "--"
}

// ResolveScope maps the options to a watch scope:
//
//   - Session set -> that single file
//   - Dir set     -> that project's session directory (non-recursive)
//   - neither     -> the whole sessions root (recursive), so any session of
//     any project triggers the pause
func ResolveScope(opts Options) Scope {
	if opts.Session != "" {
		return Scope{File: opts.Session}
	}
	if opts.Dir != "" {
		return Scope{Dir: filepath.Join(sessionsRoot(), sessionDirName(opts.Dir))}
	}
	return Scope{Dir: sessionsRoot(), Recursive: true}
}

// Watcher tails session files in a scope and calls onSettled whenever the
// agent finishes a run: a new assistant message whose stopReason is not
// "toolUse" (or "pending", which is never persisted but is guarded against
// anyway).
//
// Only entries appended after a file is first seen are considered; existing
// history is ignored. Files that appear later (new sessions) are picked up
// automatically.
type Watcher struct {
	scope     Scope
	onSettled func()
	interval  time.Duration

	tails map[string]*tail
}

// tail tracks how much of a session file has already been consumed.
type tail struct {
	off     int64
	pending []byte
}

// NewWatcher returns a watcher for the given scope.
func NewWatcher(scope Scope, onSettled func()) *Watcher {
	return &Watcher{
		scope:     scope,
		onSettled: onSettled,
		interval:  500 * time.Millisecond,
		tails:     make(map[string]*tail),
	}
}

// Run tails the scope until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.scanTick()
		}
	}
}

// scanTick re-lists the scope, updates every file, and drops files that have
// disappeared (or been excluded by a scope change).
func (w *Watcher) scanTick() {
	files := listFiles(w.scope)
	seen := make(map[string]bool, len(files))
	for _, path := range files {
		seen[path] = true
		w.updateTail(path)
	}
	for path := range w.tails {
		if !seen[path] {
			delete(w.tails, path)
		}
	}
}

// updateTail reads anything appended to path after its recorded offset and
// dispatches complete lines. The first time a file is seen its existing
// content is skipped.
func (w *Watcher) updateTail(path string) {
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	size := info.Size()

	t, ok := w.tails[path]
	if !ok {
		w.tails[path] = &tail{off: size}
		return
	}
	if size <= t.off {
		if size < t.off {
			// Truncated: restart from the beginning.
			t.off = 0
			t.pending = t.pending[:0]
		}
		return
	}

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	chunk := make([]byte, size-t.off)
	n, err := f.ReadAt(chunk, t.off)
	if err != nil && err != io.EOF {
		return
	}
	t.off += int64(n)
	t.pending = append(t.pending, chunk[:n]...)

	for {
		i := bytes.IndexByte(t.pending, '\n')
		if i < 0 {
			break
		}
		w.handleLine(t.pending[:i])
		t.pending = t.pending[i+1:]
	}
}

func (w *Watcher) handleLine(line []byte) {
	if settledEvent(line) {
		w.onSettled()
	}
}

// listFiles returns the session files covered by the scope.
func listFiles(scope Scope) []string {
	if scope.File != "" {
		if _, err := os.Stat(scope.File); err == nil {
			return []string{scope.File}
		}
		return nil
	}

	var files []string
	_ = filepath.WalkDir(scope.Dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if !scope.Recursive && path != scope.Dir {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".jsonl") {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// settledEvent reports whether a session entry line means the agent settled:
// an assistant message whose stopReason is a terminal one ("stop", "length",
// "error", "aborted"). "toolUse" and "pending" mean the agent is still working.
func settledEvent(line []byte) bool {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return false
	}
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Role       string `json:"role"`
			StopReason string `json:"stopReason"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return false
	}
	if e.Type != "message" || e.Message.Role != "assistant" {
		return false
	}
	switch e.Message.StopReason {
	case "", "toolUse", "pending":
		return false
	}
	return true
}
