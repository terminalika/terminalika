// Package sidecar manages the shared state of the terminalika WebSocket
// sidecar: the published address file (ws.json).
package sidecar

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Info is the WebSocket sidecar's published address, written to ws.json so
// external tools can discover the running game without touching the terminal
// (which is in fullscreen/raw mode while a game runs).
type Info struct {
	Game  string `json:"game"`
	Addr  string `json:"addr,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

// Dir returns the directory used for sidecar state. TERMINALIKA_CONFIG_DIR
// overrides the default (~/.config/terminalika) for testing and custom setups.
func Dir() string {
	if d := os.Getenv("TERMINALIKA_CONFIG_DIR"); d != "" {
		return d
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "terminalika")
}

// InfoPath returns the path of the published address file.
func InfoPath() string { return filepath.Join(Dir(), "ws.json") }

// WriteInfo publishes the sidecar address (or an error) to ws.json.
func WriteInfo(info Info) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(InfoPath(), data, 0o644)
}

// RemoveInfo deletes ws.json.
func RemoveInfo() { _ = os.Remove(InfoPath()) }

// ReadInfo reads ws.json. It returns an error when the file is missing or
// malformed.
func ReadInfo() (Info, error) {
	data, err := os.ReadFile(InfoPath())
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, err
	}
	return info, nil
}
