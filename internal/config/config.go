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
// and are still honoured through their "subscribe" fields.
const CurrentVersion = 2

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

	// Webhook configures the local event ingest.
	Webhook Webhook `json:"webhook"`

	// PI, Claude and Aider carry per-agent scoping. The legacy "subscribe"
	// fields of PI and Claude still enable those agents (OR-ed with Agents).
	PI     PI     `json:"pi"`
	Claude Claude `json:"claude"`
	Aider  Aider  `json:"aider"`
}

// Notify selects how the player is told an agent needs them.
type Notify struct {
	// Bell rings the terminal bell (BEL / the terminal's visual bell).
	Bell bool `json:"bell"`

	// Desktop shows a native OS notification.
	Desktop bool `json:"desktop"`
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

	// Message overrides the second line of the in-game overlay shown when
	// the agent settles. Empty falls back to the built-in text.
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

	// Message overrides the second line of the in-game overlay shown when
	// the agent settles. Empty falls back to the built-in text.
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
// selected, both notification channels on, auto-pause on.
func Default() Config {
	on := true
	return Config{
		Agents:    []string{string(agents.Claude), string(agents.Pi)},
		Notify:    Notify{Bell: true, Desktop: true},
		AutoPause: &on,
	}
}

// PauseOnEvent reports whether games pause automatically on agent events
// (true unless explicitly disabled).
func (c Config) PauseOnEvent() bool {
	return c.AutoPause == nil || *c.AutoPause
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
	return nil
}
