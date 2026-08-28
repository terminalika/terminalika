package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	core "github.com/terminalika/terminalika-core"
	"github.com/terminalika/terminalika-core/games"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/engine"
	"github.com/terminalika/terminalika/internal/menu"
	"github.com/terminalika/terminalika/internal/wsserver"
)

func main() {
	gameFlag := flag.String("game", "", "skip the menu and launch a game directly (snake or tetris)")
	wsFlag := flag.String("ws", "127.0.0.1:8080", "WebSocket listen address for events/commands (empty disables)")
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
	if wsAddr != "" {
		ws = wsserver.New(eng)
		srv = &http.Server{Addr: wsAddr, Handler: ws}
		go func() {
			if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "ws server: %v\n", err)
			}
		}()
	}

	eng.Run()

	// The game ended: drop connected clients and stop the listener.
	if ws != nil {
		ws.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
