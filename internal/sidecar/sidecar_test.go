package sidecar

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteReadInfo(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	info := Info{Game: "snake", Addr: "127.0.0.1:8081", URL: "ws://127.0.0.1:8081"}
	if err := WriteInfo(info); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}

	got, err := ReadInfo()
	if err != nil {
		t.Fatalf("ReadInfo: %v", err)
	}
	if got != info {
		t.Fatalf("ReadInfo = %+v, want %+v", got, info)
	}
}

func TestReadInfoMissingFile(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	if _, err := ReadInfo(); err == nil {
		t.Fatal("expected error for missing ws.json")
	}
}

func TestRemoveInfo(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	if err := WriteInfo(Info{Game: "tetris"}); err != nil {
		t.Fatalf("WriteInfo: %v", err)
	}
	RemoveInfo()
	if _, err := os.Stat(InfoPath()); !os.IsNotExist(err) {
		t.Fatalf("ws.json should be removed, stat err = %v", err)
	}
}

func TestAcquireLockExcludesSecondInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer release()

	if _, err := AcquireLock(path); err == nil {
		t.Fatal("second AcquireLock should fail while the first holds the lock")
	}
}

func TestAcquireLockReleaseAllowsReacquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instance.lock")

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	release()

	release2, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock after release: %v", err)
	}
	release2()
}
