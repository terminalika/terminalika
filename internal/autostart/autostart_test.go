//go:build !windows && !darwin

package autostart

import (
	"os"
	"strings"
	"testing"
)

func TestXDGInstallRemove(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if Installed() {
		t.Fatal("Installed() = true before Install")
	}
	if err := Install(); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !Installed() {
		t.Fatal("Installed() = false after Install")
	}
	data, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	exe, _ := executable()
	for _, want := range []string{"[Desktop Entry]", "Exec=" + desktopQuote(exe) + " daemon", "Terminal=false"} {
		if !strings.Contains(text, want) {
			t.Errorf("entry missing %q:\n%s", want, text)
		}
	}

	if err := Remove(); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if Installed() {
		t.Fatal("Installed() = true after Remove")
	}
	if err := Remove(); err != nil {
		t.Fatalf("Remove when absent: %v", err)
	}
}

func TestDesktopQuote(t *testing.T) {
	if got := desktopQuote("/usr/bin/terminalika"); got != "/usr/bin/terminalika" {
		t.Errorf("plain path quoted: %q", got)
	}
	if got := desktopQuote(`/home/me/my apps/terminalika`); got != `"/home/me/my apps/terminalika"` {
		t.Errorf("path with space: %q", got)
	}
}
