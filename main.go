package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	core "github.com/terminalika/terminalika-core"
	"github.com/terminalika/terminalika-core/games"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/engine"
	"github.com/terminalika/terminalika/internal/menu"
	"github.com/terminalika/terminalika/internal/pisession"
	"github.com/terminalika/terminalika/internal/sidecar"
	"github.com/terminalika/terminalika/internal/wsserver"
)

const wsPortTries = 100

func main() {
	gameFlag := flag.String("game", "", "skip the menu and launch a game directly (snake or tetris)")
	wsFlag := flag.String("ws", "127.0.0.1:8080", "WebSocket base address for events/commands (empty disables)")
	piFlag := flag.Bool("pi", false, "subscribe to the latest pi session and pause the game when the agent settles")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminalika: %v\n", err)
	}
	pi := piWatch{
		enabled: *piFlag || cfg.PI.Subscribe,
		dir:     cfg.PI.Dir,
		session: cfg.PI.Session,
	}

	// Single instance: only one terminalika process may run per machine. This
	// runs before the terminal goes raw, so the error is visible on screen.
	release, err := sidecar.AcquireLock(sidecar.LockPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer release()

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
		runGame(screen, registry, *gameFlag, *wsFlag, pi)
		return
	}

	for {
		m := menu.New(screen, registry.Names())
		name, ok := m.Run()
		if !ok {
			return
		}
		runGame(screen, registry, name, *wsFlag, pi)
	}
}

// piWatch is the resolved pi subscription configuration: the -pi flag and the
// config file are OR-ed together.
type piWatch struct {
	enabled bool
	dir     string
	session string
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

func runGame(screen tcell.Screen, registry *core.Registry, name, wsAddr string, pi piWatch) {
	game, ok := registry.Get(name)
	if !ok {
		return
	}

	eng := engine.New(screen, game)

	stopPi := startPiWatch(name, eng, pi)
	defer stopPi()

	// Optional WebSocket sidecar: terminal stays in charge, remote clients can
	// observe events and send commands.
	var ws *wsserver.Server
	var srv *http.Server

	if wsAddr == "" {
		_ = sidecar.WriteInfo(sidecar.Info{Game: name, Error: "disabled"})
	} else {
		ln, addr, err := resolveWSAddr(wsAddr, wsPortTries)
		if err != nil {
			_ = sidecar.WriteInfo(sidecar.Info{Game: name, Error: err.Error()})
		} else {
			ws = wsserver.New(eng)
			srv = &http.Server{Handler: ws}
			go func() {
				if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
					_ = sidecar.WriteInfo(sidecar.Info{Game: name, Error: err.Error()})
				}
			}()
			_ = sidecar.WriteInfo(sidecar.Info{Game: name, Addr: addr, URL: "ws://" + addr})
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
	sidecar.RemoveInfo()
}

// startPiWatch subscribes the game to the latest pi session: when the agent
// settles, the game is paused. It returns a stop function that cancels the
// watcher. When the subscription is disabled or no session can be found it
// returns a no-op stop.
func startPiWatch(game string, eng *engine.Engine, pi piWatch) func() {
	if !pi.enabled {
		return func() {}
	}
	scope := pisession.ResolveScope(pisession.Options{Dir: pi.dir, Session: pi.session})
	ctx, cancel := context.WithCancel(context.Background())
	w := pisession.NewWatcher(scope, func() {
		eng.SendCommand(core.Command{
			Type:    game + ".pause",
			Payload: core.MustJSON(map[string]string{"reason": "Paused by PI"}),
		})
	})
	go w.Run(ctx)
	return cancel
}
