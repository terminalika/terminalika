// Package claudesession locates and tails Claude Code's session files. Claude
// Code appends entries to its session JSONL as they happen, so the watcher
// reacts to live activity the same way pisession does for pi.
package claudesession

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

// Options controls which Claude Code sessions the watcher subscribes to.
type Options struct {
	// Dir, when set, restricts the subscription to sessions of the Claude
	// Code instance running in this project directory.
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

	// Recursive scans one level of subdirectories of Dir (each project's own
	// directory), but never descends into a session's subagent-transcript
	// subfolder (see listFiles).
	Recursive bool
}

// sessionsRoot returns the directory Claude Code stores project sessions in:
// CLAUDE_CONFIG_DIR/projects when set (Claude Code's own override for its
// whole config directory), otherwise ~/.claude/projects.
func sessionsRoot() string {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(expandHome(d), "projects")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".claude", "projects")
}

// expandHome expands a leading "~", "~/" or "~\\" so env-var values like
// CLAUDE_CONFIG_DIR=~/foo resolve correctly.
func expandHome(path string) string {
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, "~\\") {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// sessionDirName converts a project directory into Claude Code's session
// subdirectory name. It resolves the path to an absolute form first, then
// applies the encoding: replace every '/', '\\' and ':' (the Windows drive
// letter) with '-'.
//
// Unlike pi's scheme, this is inferred empirically (observed against a real
// ~/.claude/projects directory: "/home/user/proj" -> "-home-user-proj")
// rather than from published documentation, since Claude Code's encoding
// isn't documented. It may need adjusting if Anthropic changes it.
//
//	"/home/user/proj"  -> "-home-user-proj"
func sessionDirName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	return encodeSessionDir(abs)
}

func encodeSessionDir(dir string) string {
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(dir)
}

// ResolveScope maps the options to a watch scope:
//
//   - Session set -> that single file
//   - Dir set     -> that project's session directory (non-recursive, so its
//     sibling subagent-transcript subfolder is never picked up)
//   - neither     -> the whole sessions root, one level deep, so any Claude
//     Code session of any project triggers the pause
func ResolveScope(opts Options) Scope {
	if opts.Session != "" {
		return Scope{File: opts.Session}
	}
	if opts.Dir != "" {
		return Scope{Dir: filepath.Join(sessionsRoot(), sessionDirName(opts.Dir))}
	}
	return Scope{Dir: sessionsRoot(), Recursive: true}
}

// SettleKind distinguishes why the agent settled, so the caller can react
// differently (e.g. a different pause message).
type SettleKind int

const (
	// SettleDone means the agent finished its turn: an assistant message
	// with a terminal stop_reason (e.g. "end_turn").
	SettleDone SettleKind = iota

	// SettleQuestion means the agent stopped to ask the user something: an
	// assistant message with stop_reason "tool_use" whose content includes
	// an AskUserQuestion tool call. Claude Code blocks on the user's answer
	// exactly like it does at the end of a turn, so it counts as settled
	// too even though stop_reason itself says "tool_use".
	SettleQuestion
)

// Watcher tails session files in a scope and calls onSettled whenever the
// agent settles: it finishes a run, or stops to ask the user a question.
//
// Only entries appended after a file is first seen are considered; existing
// history is ignored. Files that appear later (new sessions) are picked up
// automatically.
type Watcher struct {
	scope     Scope
	onSettled func(SettleKind)
	interval  time.Duration

	tails map[string]*tail
}

// tail tracks how much of a session file has already been consumed.
type tail struct {
	off     int64
	pending []byte
}

// NewWatcher returns a watcher for the given scope.
func NewWatcher(scope Scope, onSettled func(SettleKind)) *Watcher {
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
	if kind, ok := settleKind(line); ok {
		w.onSettled(kind)
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
			if path == scope.Dir {
				return nil
			}
			if !scope.Recursive {
				return fs.SkipDir
			}
			// scope.Dir is the projects root here: one level down are each
			// project's own directories (walk into those), two levels down
			// are a session's subagent-transcript subfolder, named after
			// that session's own file - never descend into those, since a
			// subagent settling doesn't mean the top-level session has.
			rel, relErr := filepath.Rel(scope.Dir, path)
			if relErr != nil || strings.ContainsRune(rel, filepath.Separator) {
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

// askUserQuestionTool is the name of the built-in tool Claude Code calls to
// ask the user a question and block on their answer.
const askUserQuestionTool = "AskUserQuestion"

// settleKind reports whether a session entry line means the agent settled,
// and why. An assistant message with a terminal stop_reason (e.g.
// "end_turn", "max_tokens", "stop_sequence", "refusal") means it finished a
// turn. A stop_reason of "tool_use" normally means it's still working,
// waiting on a tool result — except when the tool call is AskUserQuestion,
// which blocks on the user just like the end of a turn does.
func settleKind(line []byte) (SettleKind, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return 0, false
	}
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Role       string `json:"role"`
			StopReason string `json:"stop_reason"`
			Content    []struct {
				Type string `json:"type"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return 0, false
	}
	if e.Type != "assistant" || e.Message.Role != "assistant" {
		return 0, false
	}
	switch e.Message.StopReason {
	case "":
		return 0, false
	case "tool_use":
		for _, c := range e.Message.Content {
			if c.Type == "tool_use" && c.Name == askUserQuestionTool {
				return SettleQuestion, true
			}
		}
		return 0, false
	default:
		return SettleDone, true
	}
}
