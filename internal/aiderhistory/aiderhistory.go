// Package aiderhistory tails Aider's chat history file. Aider appends the
// user's prompt (as a "#### " heading) when it's entered, the assistant's
// whole reply once it has finished streaming, and tool output as "> "
// quoted lines - so a fresh assistant block appearing after a prompt means
// the agent has finished its turn and is waiting for the user again.
//
// This is a best-effort signal: Aider's --notifications-command wired to
// `terminalika notify --agent aider` is the precise one, and the hub dedupes
// the two when both fire.
package aiderhistory

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

// HistoryFile is Aider's default chat history file name, written in the
// directory Aider runs in.
const HistoryFile = ".aider.chat.history.md"

// Options controls which history file is tailed.
type Options struct {
	// Dir is the directory Aider runs in. Empty means the current working
	// directory.
	Dir string

	// File is an explicit history file path; it wins over Dir.
	File string
}

// Resolve returns the history file path for the options.
func Resolve(opts Options) string {
	if opts.File != "" {
		return opts.File
	}
	dir := opts.Dir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return filepath.Join(dir, HistoryFile)
}

// requiet is how long after the last emitted turn an assistant line without
// a preceding user prompt counts as a new turn (Aider can answer twice in a
// row when it reflects on lint/test output).
const requiet = 5 * time.Second

// Watcher tails one history file and emits a Finished event per assistant
// turn. It implements hub.Source.
type Watcher struct {
	path     string
	interval time.Duration
	now      func() time.Time

	off      int64
	pending  []byte
	seen     bool
	armed    bool
	lastEmit time.Time
}

// NewWatcher returns a watcher for path.
func NewWatcher(path string) *Watcher {
	return &Watcher{path: path, interval: 500 * time.Millisecond, now: time.Now, armed: true}
}

// Run tails the file until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context, emit func(agents.Event)) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tick(emit)
		}
	}
}

// tick reads whatever was appended since the last tick. The first time the
// file is seen its existing content is skipped.
func (w *Watcher) tick(emit func(agents.Event)) {
	info, err := os.Stat(w.path)
	if err != nil {
		w.seen = false
		return
	}
	size := info.Size()
	if !w.seen {
		w.seen = true
		w.off = size
		w.pending = w.pending[:0]
		return
	}
	if size < w.off {
		w.off = 0
		w.pending = w.pending[:0]
	}
	if size == w.off {
		return
	}
	f, err := os.Open(w.path)
	if err != nil {
		return
	}
	defer f.Close()
	chunk := make([]byte, size-w.off)
	n, err := f.ReadAt(chunk, w.off)
	if err != nil && err != io.EOF {
		return
	}
	w.off += int64(n)
	w.pending = append(w.pending, chunk[:n]...)
	for {
		i := bytes.IndexByte(w.pending, '\n')
		if i < 0 {
			return
		}
		w.handleLine(string(w.pending[:i]), emit)
		w.pending = w.pending[i+1:]
	}
}

func (w *Watcher) handleLine(line string, emit func(agents.Event)) {
	trimmed := strings.TrimSpace(line)
	switch {
	case trimmed == "":
		return
	case strings.HasPrefix(line, "#### "), strings.HasPrefix(line, "# aider chat started"):
		// A user prompt (or a new session): the next assistant block is a
		// fresh turn.
		w.armed = true
		return
	case strings.HasPrefix(line, "> "), strings.HasPrefix(line, ">"):
		// Tool output / echoed commands.
		return
	}
	now := w.now()
	if !w.armed && now.Sub(w.lastEmit) < requiet {
		return
	}
	w.armed = false
	w.lastEmit = now
	a, _ := agents.Lookup(string(agents.Aider))
	emit(agents.Event{Agent: a, Kind: agents.Finished, At: now, Source: "history"})
}
