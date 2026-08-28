package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	c, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if c.PI.Subscribe {
		t.Errorf("PI.Subscribe = true, want false for a missing file")
	}
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TERMINALIKA_CONFIG_DIR", dir)

	data := `{"pi":{"subscribe":true,"dir":"/home/u/proj","session":"/tmp/s.jsonl"}}`
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
	if c.PI.Dir != "/home/u/proj" {
		t.Errorf("PI.Dir = %q, want %q", c.PI.Dir, "/home/u/proj")
	}
	if c.PI.Session != "/tmp/s.jsonl" {
		t.Errorf("PI.Session = %q, want %q", c.PI.Session, "/tmp/s.jsonl")
	}
}
