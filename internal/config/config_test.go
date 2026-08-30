package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/terminalika/terminalika/internal/agents"
)

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	if Exists() {
		t.Fatal("Exists() = true for a missing file")
	}
	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(c.AgentIDs()) != 0 {
		t.Errorf("AgentIDs = %v, want none for a missing file", c.AgentIDs())
	}
	if !c.PauseOnEvent() {
		t.Error("PauseOnEvent() = false, want the default true")
	}
}

func TestLoadLegacyFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TERMINALIKA_CONFIG_DIR", dir)

	data := `{"pi":{"subscribe":true,"dir":"/home/u/proj","session":"/tmp/s.jsonl","message":"back to you, PI's out"}}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !c.PI.Subscribe {
		t.Errorf("PI.Subscribe = false, want true")
	}
	if c.PI.Dir != "/home/u/proj" || c.PI.Session != "/tmp/s.jsonl" || c.PI.Message != "back to you, PI's out" {
		t.Errorf("PI = %+v", c.PI)
	}
	if ids := c.AgentIDs(); len(ids) != 1 || ids[0] != agents.Pi {
		t.Errorf("AgentIDs = %v, want [pi] from the legacy subscribe flag", ids)
	}
	if c.Version != 0 || !c.PauseOnEvent() {
		t.Errorf("legacy file: version=%d pause=%v", c.Version, c.PauseOnEvent())
	}
}

func TestSaveRoundTrip(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", filepath.Join(t.TempDir(), "nested"))

	c := Default()
	c.SetAgents([]agents.ID{agents.Cursor, agents.Claude})
	c.Notify.Desktop = DesktopNoWindow
	off := false
	c.AutoPause = &off
	c.Background = false
	c.Aider.Dir = "/work"

	if err := Save(c); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if !Exists() {
		t.Fatal("Exists() = false after Save")
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Version != CurrentVersion {
		t.Errorf("Version = %d, want %d", got.Version, CurrentVersion)
	}
	if ids := got.AgentIDs(); len(ids) != 2 || ids[0] != agents.Claude || ids[1] != agents.Cursor {
		t.Errorf("AgentIDs = %v", ids)
	}
	if got.DesktopMode() != DesktopNoWindow {
		t.Errorf("Notify = %+v", got.Notify)
	}
	if got.PauseOnEvent() {
		t.Error("PauseOnEvent() = true, want false")
	}
	if got.Background {
		t.Error("Background = true, want false")
	}
	if got.Aider.Dir != "/work" {
		t.Errorf("Aider.Dir = %q", got.Aider.Dir)
	}
}

func TestVersion2BooleansMapOntoDesktopMode(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TERMINALIKA_CONFIG_DIR", dir)

	for _, c := range []struct {
		file string
		want DesktopMode
	}{
		{`{"version":2,"agents":["claude"],"notify":{"bell":true,"desktop":true}}`, DesktopAlways},
		{`{"version":2,"agents":["claude"],"notify":{"bell":true,"desktop":false}}`, DesktopNever},
		{`{"version":3,"agents":["claude"],"notify":{"desktop":"unfocused"}}`, DesktopUnfocused},
		{`{"version":2,"agents":["claude"]}`, DesktopNever},
	} {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(c.file), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := Load()
		if err != nil {
			t.Fatalf("Load(%s): %v", c.file, err)
		}
		if got.DesktopMode() != c.want {
			t.Errorf("%s: DesktopMode = %q, want %q", c.file, got.DesktopMode(), c.want)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("%s: Validate: %v", c.file, err)
		}
		if got.Background {
			t.Errorf("%s: Background should default to off for an existing file", c.file)
		}
	}

	if err := (Config{Notify: Notify{Desktop: "loudly"}}).Validate(); err == nil {
		t.Error("Validate() should flag an unknown desktop mode")
	}
	if d := Default(); d.DesktopMode() != DesktopUnfocused || !d.Background {
		t.Errorf("Default() = %+v, want unfocused desktop notifications and background on", d)
	}
}

func TestAgentIDsMergesAndDropsUnknown(t *testing.T) {
	c := Config{Agents: []string{"claude", "robot", "claude"}, PI: PI{Subscribe: true}}
	ids := c.AgentIDs()
	if len(ids) != 2 || ids[0] != agents.Claude || ids[1] != agents.Pi {
		t.Errorf("AgentIDs = %v", ids)
	}
	if err := c.Validate(); err == nil {
		t.Error("Validate() should flag the unknown agent")
	}
	if !c.HasAgent(agents.Pi) || c.HasAgent(agents.Aider) {
		t.Error("HasAgent mismatch")
	}
}

func TestRemoveDeletesConfigAndToleratesMissing(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	if err := Save(Default()); err != nil {
		t.Fatal(err)
	}
	if err := Remove(); err != nil || Exists() {
		t.Fatalf("Remove: err=%v exists=%v", err, Exists())
	}
	if err := Remove(); err != nil {
		t.Fatalf("Remove on a missing file: %v", err)
	}
}
