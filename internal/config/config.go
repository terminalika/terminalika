// Package config loads terminalika's optional configuration file
// (~/.config/terminalika/config.json). A missing file yields the zero Config,
// so the launcher keeps working without any configuration.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/terminalika/terminalika/internal/sidecar"
)

// Config is the root configuration object.
type Config struct {
	PI     PI     `json:"pi"`
	Claude Claude `json:"claude"`
}

// PI controls the pi session subscription: when enabled, the launcher watches
// the latest pi session file and pauses the game when the agent settles.
type PI struct {
	// Subscribe enables the subscription. It can also be forced with the
	// -pi flag; the flag and this field are OR-ed together.
	Subscribe bool `json:"subscribe"`

	// Dir is the project directory whose latest session to watch. Empty
	// means the launcher's current working directory.
	Dir string `json:"dir"`

	// Session is an explicit session file path. When non-empty it wins over
	// Dir.
	Session string `json:"session"`
}

// Claude controls the Claude Code session subscription: when enabled, the
// launcher watches the latest Claude Code session file and pauses the game
// when the agent settles.
type Claude struct {
	// Subscribe enables the subscription. It can also be forced with the
	// -claude flag; the flag and this field are OR-ed together.
	Subscribe bool `json:"subscribe"`

	// Dir is the project directory whose latest session to watch. Empty
	// means the launcher's current working directory.
	Dir string `json:"dir"`

	// Session is an explicit session file path. When non-empty it wins over
	// Dir.
	Session string `json:"session"`
}

// Path returns the config file path.
func Path() string { return filepath.Join(sidecar.Dir(), "config.json") }

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
