//go:build !windows && !darwin

package autostart

import (
	"os"
	"path/filepath"
	"testing"
)

// An entry left by an earlier version is removed; removing again is fine.
func TestXDGRemoveClearsLeftoverEntry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if err := os.MkdirAll(filepath.Dir(Path()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte("[Desktop Entry]\nExec=/usr/bin/terminalika daemon\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(Path()); err == nil {
		t.Fatal("entry still present after Remove")
	}
	if err := Remove(); err != nil {
		t.Fatalf("Remove when absent: %v", err)
	}
}
