package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	core "github.com/terminalika/terminalika-core"
	"github.com/terminalika/terminalika-core/games"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/engine"
	"github.com/terminalika/terminalika/internal/menu"
	"github.com/terminalika/terminalika/internal/wsserver"
)

const wsPortTries = 100

func main() {
	gameFlag := flag.String("game", "", "skip the menu and launch a game directly (snake or tetris)")
	wsFlag := flag.String("ws", "127.0.0.1:8080", "WebSocket base address for events/commands (empty disables)")
	flag.Parse()

	registry := games.Default()

	// Validate the flag before taking over the terminal so a bad flag never
	// leaves the terminal in raw mode.
	if *gameFlag != "" && !registry.Has(*gameFlag) {
		fmt.Fprintf(os.Stderr, "unknown game %q; available games: %v\n", *gameFlag, registry.Names())
		os.Exit(1)
	}

	screen, err := tcell.NewScreen()
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not create screen: %v\n", err)
		os.Exit(1)
	}
	if err := screen.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "could not initialize screen: %v\n", err)
		os.Exit(1)
	}
	defer screen.Fini()

	if *gameFlag != "" {
		runGame(screen, registry, *gameFlag, *wsFlag)
		return
	}

	for {
		m := menu.New(screen, registry.Names())
		name, ok := m.Run()
		if !ok {
			return
		}
		runGame(screen, registry, name, *wsFlag)
	}
}

// wsInfo is written to a file so external tools can discover the sidecar's
// address. The terminal is in raw/fullscreen mode while the game runs, so we
// never print to it; the file is the single source of truth.
type wsInfo struct {
	Game  string `json:"game"`
	Addr  string `json:"addr,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty"`
}

func wsInfoPath() string {
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "terminalika", "ws.json")
}

func writeWSInfo(info wsInfo) {
	path := wsInfoPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(info)
	if err != nil {
		return
	}
	_ = os.WriteFile(path, data, 0o644)
}

func removeWSInfo() {
	_ = os.Remove(wsInfoPath())
}

// resolveWSAddr binds a TCP listener for the sidecar. It starts at the base
// address and increments the port until it finds a free one (up to tries
// attempts), so a port claimed by another process (e.g. Docker) is skipped
// instead of disabling the sidecar.
func resolveWSAddr(base string, tries int) (net.Listener, string, error) {
	host, portStr, err := net.SplitHostPort(base)
	if err != nil {
		return nil, "", fmt.Errorf("invalid -ws address %q: %w", base, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, "", fmt.Errorf("invalid -ws port %q: %w", portStr, err)
	}

	var lastErr error
	for i := 0; i < tries; i++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port+i))
		ln, err := net.Listen("tcp", addr)
		if err == nil {
			return ln, addr, nil
		}
		lastErr = err
	}
	return nil, "", fmt.Errorf("no free port starting at %s after %d tries: %w", base, tries, lastErr)
}

func runGame(screen tcell.Screen, registry *core.Registry, name, wsAddr string) {
	game, ok := registry.Get(name)
	if !ok {
		return
	}

	eng := engine.New(screen, game)

	// Optional WebSocket sidecar: terminal stays in charge, remote clients can
	// observe events and send commands.
	var ws *wsserver.Server
	var srv *http.Server

	if wsAddr == "" {
		writeWSInfo(wsInfo{Game: name, Error: "disabled"})
	} else {
		ln, addr, err := resolveWSAddr(wsAddr, wsPortTries)
		if err != nil {
			writeWSInfo(wsInfo{Game: name, Error: err.Error()})
		} else {
			ws = wsserver.New(eng)
			srv = &http.Server{Handler: ws}
			go func() {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					writeWSInfo(wsInfo{Game: name, Error: err.Error()})
				}
			}()
			writeWSInfo(wsInfo{Game: name, Addr: addr, URL: "ws://" + addr})
		}
	}

	eng.Run()

	// The game ended: drop connected clients and stop the listener.
	if ws != nil {
		ws.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	removeWSInfo()
}
