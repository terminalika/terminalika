// Package config loads and saves terminalika's configuration file
// (~/.config/terminalika/config.json). A missing file yields the zero Config
// and means the first-run setup wizard has not been completed yet.
package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/sidecar"
)

// CurrentVersion is the schema version the setup wizard writes. Files
// without a version are the pre-hub schema (only the "pi"/"claude" blocks)
// and are still honoured through their "subscribe" fields; version 2 files
// carry a boolean "desktop" (and a "bell" that is now ignored), mapped onto
// DesktopMode on load.
const CurrentVersion = 3

// Config is the root configuration object.
type Config struct {
	// Version is the schema version; set by the wizard (see CurrentVersion).
	Version int `json:"version,omitempty"`

	// Agents lists the ids of the agents to listen to (see agents.Catalog).
	Agents []string `json:"agents"`

	// Notify selects the notification channels.
	Notify Notify `json:"notify"`

	// AutoPause, when nil, defaults to true: a running game is paused the
	// moment any watched agent needs the player.
	AutoPause *bool `json:"auto_pause,omitempty"`

	// Background keeps terminalika running as a background process
	// (`terminalika daemon`), started at login, so agent events are
	// delivered even when no terminalika window is open.
	Background bool `json:"background"`

	// Webhook configures the local event ingest.
	Webhook Webhook `json:"webhook"`

	// PI, Claude and Aider carry per-agent scoping. The legacy "subscribe"
	// fields of PI and Claude still enable those agents (OR-ed with Agents).
	PI     PI     `json:"pi"`
	Claude Claude `json:"claude"`
	Aider  Aider  `json:"aider"`
}

// Notify selects how the player is told an agent needs them, beyond the
// in-game overlay/banner (which is always on: the way to silence
// terminalika entirely is to listen to no agents).
type Notify struct {
	// Desktop says when a native OS notification is shown.
	Desktop DesktopMode `json:"desktop"`
}

// DesktopMode says when a desktop notification is sent for an agent event.
type DesktopMode string

const (
	// DesktopNever sends none.
	DesktopNever DesktopMode = "never"

	// DesktopNoWindow sends one only when no terminalika window is open -
	// i.e. only from the background process, which makes it pointless
	// unless Background is on.
	DesktopNoWindow DesktopMode = "no_window"

	// DesktopUnfocused sends one unless a terminalika window has the
	// terminal's focus (the overlay is already in front of the player).
	DesktopUnfocused DesktopMode = "unfocused"

	// DesktopAlways sends one for every event.
	DesktopAlways DesktopMode = "always"
)

// DesktopModes lists the modes in the order the wizard offers them.
var DesktopModes = []DesktopMode{DesktopUnfocused, DesktopAlways, DesktopNoWindow, DesktopNever}

// Valid reports whether m is one of the known modes.
func (m DesktopMode) Valid() bool {
	for _, x := range DesktopModes {
		if x == m {
			return true
		}
	}
	return false
}

// UnmarshalJSON accepts the mode's name, or the version-2 boolean: true
// was "always notify" (the only behaviour there was), false is never.
func (m *DesktopMode) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			*m = DesktopAlways
		} else {
			*m = DesktopNever
		}
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*m = DesktopMode(s)
	return nil
}

// Label is the human-readable form used in the wizard and status lines.
func (m DesktopMode) Label() string {
	switch m {
	case DesktopAlways:
		return "always"
	case DesktopNoWindow:
		return "only when no window is open"
	case DesktopUnfocused:
		return "only when the window isn't focused"
	case DesktopNever:
		return "never"
	}
	return string(m)
}

// Webhook configures the local HTTP ingest that agents' hooks post to.
type Webhook struct {
	// Disabled turns the ingest off entirely.
	Disabled bool `json:"disabled,omitempty"`

	// Addr is the base address to bind; empty means webhook.DefaultAddr.
	// A taken port is skipped forward.
	Addr string `json:"addr,omitempty"`
}

// PI controls the pi session subscription: when enabled, the launcher watches
// the latest pi session file and pauses the game when the agent settles.
type PI struct {
	// Subscribe enables the subscription (legacy; listing "pi" in Agents is
	// the same). It can also be forced with the -pi flag; all are OR-ed.
	Subscribe bool `json:"subscribe,omitempty"`

	// Dir is the project directory whose latest session to watch. Empty
	// means every project.
	Dir string `json:"dir,omitempty"`

	// Session is an explicit session file path. When non-empty it wins over
	// Dir.
	Session string `json:"session,omitempty"`

	// Message overrides the one-line in-game notice shown when the agent
	// settles. Empty falls back to the built-in text.
	Message string `json:"message,omitempty"`
}

// Claude controls the Claude Code session subscription.
type Claude struct {
	// Subscribe enables the subscription (legacy; listing "claude" in
	// Agents is the same). It can also be forced with the -claude flag.
	Subscribe bool `json:"subscribe,omitempty"`

	// Dir is the project directory whose latest session to watch. Empty
	// means every project.
	Dir string `json:"dir,omitempty"`

	// Session is an explicit session file path. When non-empty it wins over
	// Dir.
	Session string `json:"session,omitempty"`

	// Message overrides the one-line in-game notice shown when the agent
	// settles. Empty falls back to the built-in text.
	Message string `json:"message,omitempty"`
}

// Aider controls the Aider history subscription.
type Aider struct {
	// Dir is the directory Aider runs in (where .aider.chat.history.md
	// lives). Empty means the launcher's working directory.
	Dir string `json:"dir,omitempty"`

	// History is an explicit history file path; it wins over Dir.
	History string `json:"history,omitempty"`
}

// Path returns the config file path.
func Path() string { return filepath.Join(sidecar.Dir(), "config.json") }

// Exists reports whether a config file is present - i.e. whether setup has
// been completed (or a file was written by hand).
func Exists() bool {
	_, err := os.Stat(Path())
	return err == nil
}

// Remove deletes the config file, so the next run starts from the setup
// wizard again. A missing file is not an error.
func Remove() error {
	err := os.Remove(Path())
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// Load reads the config file. A missing file is not an error.
func Load() (Config, error) {
	data, err := os.ReadFile(Path())
	if os.IsNotExist(err) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Save writes the config file, creating the directory if needed. The
// written file always carries CurrentVersion.
func Save(c Config) error {
	c.Version = CurrentVersion
	if c.Agents == nil {
		c.Agents = []string{}
	}
	if err := os.MkdirAll(sidecar.Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(data, '\n'), 0o644)
}

// Default is the configuration the wizard starts from: Claude Code and pi
// selected, desktop notifications when the window isn't focused, auto-pause
// on, background process on.
func Default() Config {
	on := true
	return Config{
		Agents:     []string{string(agents.Claude), string(agents.Pi)},
		Notify:     Notify{Desktop: DesktopUnfocused},
		AutoPause:  &on,
		Background: true,
	}
}

// PauseOnEvent reports whether games pause automatically on agent events
// (true unless explicitly disabled).
func (c Config) PauseOnEvent() bool {
	return c.AutoPause == nil || *c.AutoPause
}

// DesktopMode returns the desktop notification mode, DesktopNever when the
// file doesn't say.
func (c Config) DesktopMode() DesktopMode {
	if c.Notify.Desktop == "" {
		return DesktopNever
	}
	return c.Notify.Desktop
}

// AgentIDs returns the set of enabled agents: the Agents list merged with
// the legacy subscribe flags, deduplicated, unknown ids dropped, in
// catalogue order.
func (c Config) AgentIDs() []agents.ID {
	want := make(map[agents.ID]bool)
	for _, id := range c.Agents {
		if a, ok := agents.Lookup(id); ok {
			want[a.ID] = true
		}
	}
	if c.PI.Subscribe {
		want[agents.Pi] = true
	}
	if c.Claude.Subscribe {
		want[agents.Claude] = true
	}
	var ids []agents.ID
	for _, a := range agents.Catalog {
		if want[a.ID] {
			ids = append(ids, a.ID)
		}
	}
	return ids
}

// HasAgent reports whether id is enabled.
func (c Config) HasAgent(id agents.ID) bool {
	for _, x := range c.AgentIDs() {
		if x == id {
			return true
		}
	}
	return false
}

// SetAgents replaces the Agents list (sorted for a stable file) and clears
// the legacy subscribe flags so the list is the single source of truth.
func (c *Config) SetAgents(ids []agents.ID) {
	c.Agents = make([]string, 0, len(ids))
	for _, id := range ids {
		c.Agents = append(c.Agents, string(id))
	}
	sort.Strings(c.Agents)
	c.PI.Subscribe = false
	c.Claude.Subscribe = false
}

// Validate reports the first problem with the file's contents.
func (c Config) Validate() error {
	for _, id := range c.Agents {
		if _, ok := agents.Lookup(id); !ok {
			return errors.New("unknown agent " + id + " in config.json")
		}
	}
	if m := c.Notify.Desktop; m != "" && !m.Valid() {
		return errors.New("unknown notify.desktop mode " + string(m) + " in config.json (never, no_window, unfocused, always)")
	}
	return nil
}
