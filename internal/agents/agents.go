// Package agents is the catalogue of AI coding agents terminalika can listen
// to, and the single event type every agent's activity is normalised into.
//
// Each agent's actual detection lives elsewhere (claudesession, pisession,
// aiderhistory, webhook); this package only says *who* an agent is and *what*
// happened, so the hub, the notifier, the home screen and the game engine can
// all speak the same language without knowing where an event came from.
package agents

import (
	"strings"
	"time"
)

// ID identifies an agent in config files, flags and webhook payloads.
type ID string

// The agents terminalika knows about.
const (
	Claude   ID = "claude"
	Pi       ID = "pi"
	Aider    ID = "aider"
	Cursor   ID = "cursor"
	OpenCode ID = "opencode"
)

// Agent describes one entry of the catalogue.
type Agent struct {
	ID   ID
	Name string

	// Native reports whether terminalika detects this agent's events on
	// its own by tailing the agent's session files. Agents without native
	// detection rely on the webhook ingest (see package webhook) fed by the
	// agent's own hook/notification mechanism.
	Native bool

	// Hint is the one-line description shown next to the agent in the
	// setup wizard.
	Hint string
}

// Catalog lists every supported agent in display order.
var Catalog = []Agent{
	{ID: Claude, Name: "Claude Code", Native: true, Hint: "tails ~/.claude/projects; hooks optional"},
	{ID: Pi, Name: "Pi Agent", Native: true, Hint: "tails ~/.pi/agent/sessions"},
	{ID: Aider, Name: "Aider", Native: true, Hint: "tails .aider.chat.history.md; or --notifications-command"},
	{ID: Cursor, Name: "Cursor CLI", Native: false, Hint: "via hooks -> terminalika notify"},
	{ID: OpenCode, Name: "OpenCode", Native: false, Hint: "via a plugin -> terminalika notify"},
}

// Lookup finds an agent by id. Unknown ids yield a synthetic agent named
// after the id itself, so a webhook from a tool terminalika has never heard
// of is still displayed sensibly; ok reports whether the id was catalogued.
func Lookup(id string) (Agent, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, a := range Catalog {
		if string(a.ID) == id {
			return a, true
		}
	}
	if id == "" {
		id = "agent"
	}
	return Agent{ID: ID(id), Name: id}, false
}

// EventKind says what an agent did.
type EventKind int

const (
	// Finished means the agent completed its run and is idle.
	Finished EventKind = iota

	// InputRequired means the agent stopped to ask the user something - a
	// question, a permission prompt, a tool approval - and is blocked until
	// they answer.
	InputRequired
)

// String returns the canonical wire name of the kind.
func (k EventKind) String() string {
	switch k {
	case InputRequired:
		return "input_required"
	default:
		return "finished"
	}
}

// ParseKind maps the many spellings agents use for the two kinds onto them.
func ParseKind(s string) (EventKind, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "finished", "finish", "settled", "done", "complete", "completed", "stop", "stopped", "idle", "end_turn":
		return Finished, true
	case "input_required", "input-required", "input", "prompt", "question", "question_asked", "user_input_required",
		"permission", "permission_prompt", "approval", "waiting", "ask":
		return InputRequired, true
	}
	return 0, false
}

// Event is one thing an agent did, as seen by terminalika.
type Event struct {
	Agent  Agent
	Kind   EventKind
	At     time.Time
	Detail string // free text from the source (a hook's message, say)
	Source string // where it was detected: "session", "webhook", "history"

	// Seq is the hub's serial number for the event, stamped on emit (0
	// before that). Screens use it to tell events apart, so that one event
	// is shown to the player once and never again (see hub.Current).
	Seq uint64
}

// Title is the short, notification-sized headline for the event, e.g.
// "Claude Code needs your input!" or "Pi Agent finished processing".
func (e Event) Title() string {
	if e.Kind == InputRequired {
		return e.Agent.Name + " needs your input!"
	}
	return e.Agent.Name + " finished processing"
}

// Body is the longer notification text.
func (e Event) Body() string {
	if e.Detail != "" {
		return e.Detail
	}
	if e.Kind == InputRequired {
		return "Your AI agent is waiting for your response or approval."
	}
	return "Your AI agent has finished and is idle."
}

// Message is the one line the player sees on screen for the event - the
// in-game pause overlay, the flash banner and the home-screen toast all
// show exactly this. It's short, names the agent, and stops there: what
// the keys do is the game's business, not the notice's. A custom
// per-agent message from config.json (Detail on a Finished event)
// replaces it.
func (e Event) Message() string {
	if e.Kind == InputRequired {
		return e.Agent.Name + " has a question - don't leave it hanging."
	}
	if e.Detail != "" {
		return e.Detail
	}
	return e.Agent.Name + "'s done - you're up."
}
