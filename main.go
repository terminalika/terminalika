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

	"github.com/terminalika/terminalika/internal/claudesession"
	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/confirm"
	"github.com/terminalika/terminalika/internal/engine"
	"github.com/terminalika/terminalika/internal/listener"
	"github.com/terminalika/terminalika/internal/menu"
	"github.com/terminalika/terminalika/internal/pisession"
	"github.com/terminalika/terminalika/internal/sidecar"
	"github.com/terminalika/terminalika/internal/wsserver"
)

const wsPortTries = 100

// version is overridden at release time via
// -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	gameFlag := flag.String("game", "", "skip the menu and launch a game directly (snake or tetris)")
	wsFlag := flag.String("ws", "127.0.0.1:8080", "WebSocket base address for events/commands (empty disables)")
	piFlag := flag.Bool("pi", false, "subscribe to the latest pi session and pause the game when the agent settles")
	claudeFlag := flag.Bool("claude", false, "subscribe to the latest Claude Code session and pause the game when the agent settles")
	versionFlag := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminalika: %v\n", err)
	}
	pi := piWatch{
		enabled: *piFlag || cfg.PI.Subscribe,
		dir:     cfg.PI.Dir,
		session: cfg.PI.Session,
	}
	claude := claudeWatch{
		enabled: *claudeFlag || cfg.Claude.Subscribe,
		dir:     cfg.Claude.Dir,
		session: cfg.Claude.Session,
	}

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

	seat, err := resolveListenerSeat(screen, &pi, &claude)
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminalika: %v\n", err)
	}
	if seat != nil {
		defer seat.Release()
	}

	if *gameFlag != "" {
		runGame(screen, registry, *gameFlag, *wsFlag, pi, claude, seat)
		return
	}

	for {
		m := menu.New(screen, registry.Names())
		name, ok := m.Run()
		if !ok {
			return
		}
		runGame(screen, registry, name, *wsFlag, pi, claude, seat)
	}
}

// resolveListenerSeat decides whether this process may run the pi/Claude
// Code watchers: only one terminalika process may hold the listener seat at
// a time, since a single agent's events should only ever pause one screen.
//
// A process that doesn't want to listen at all (neither -pi nor -claude, nor
// their config equivalents) never touches the seat. When the seat is free -
// or its holder is gone without releasing it - it's claimed silently. When a
// live process already holds it, the player is asked whether to move
// listening here; declining disables both watches for this process without
// asking again.
func resolveListenerSeat(screen tcell.Screen, pi *piWatch, claude *claudeWatch) (*listener.Seat, error) {
	if !pi.enabled && !claude.enabled {
		return nil, nil
	}

	if status := listener.Check(); status.Held {
		if !confirm.Ask(screen, []string{
			"Another terminalika window is currently listening",
			"for agent events (pi / Claude Code).",
			"",
			"Move event listening to this window instead?",
		}) {
			pi.enabled = false
			claude.enabled = false
			return nil, nil
		}
	}

	return listener.Claim(nil)
}

// piWatch is the resolved pi subscription configuration: the -pi flag and the
// config file are OR-ed together.
type piWatch struct {
	enabled bool
	dir     string
	session string
}

// claudeWatch is the resolved Claude Code subscription configuration: the
// -claude flag and the config file are OR-ed together.
type claudeWatch struct {
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

func runGame(screen tcell.Screen, registry *core.Registry, name, wsAddr string, pi piWatch, claude claudeWatch, seat *listener.Seat) {
	game, ok := registry.Get(name)
	if !ok {
		return
	}

	eng := engine.New(screen, game)

	// The listener seat may have been taken over by another process since it
	// was claimed (or since the last game was played from this menu loop);
	// don't start watchers this process no longer has the right to run.
	if seat != nil && !seat.Held() {
		pi.enabled = false
		claude.enabled = false
	}

	stopPi := startPiWatch(name, eng, pi)
	defer stopPi()

	stopClaude := startClaudeWatch(name, eng, claude)
	defer stopClaude()

	// While this session's watchers are active, keep confirming the seat is
	// still ours; if another process takes over mid-session, stop reacting
	// to agent events immediately instead of waiting for the next game.
	stopSeatWatch := func() {}
	if seat != nil && (pi.enabled || claude.enabled) {
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if !seat.Held() {
						stopPi()
						stopClaude()
						return
					}
				}
			}
		}()
		stopSeatWatch = cancel
	}
	defer stopSeatWatch()

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

// startClaudeWatch subscribes the game to the latest Claude Code session:
// when the agent settles, the game is paused. It returns a stop function that
// cancels the watcher. When the subscription is disabled or no session can be
// found it returns a no-op stop.
func startClaudeWatch(game string, eng *engine.Engine, claude claudeWatch) func() {
	if !claude.enabled {
		return func() {}
	}
	scope := claudesession.ResolveScope(claudesession.Options{Dir: claude.dir, Session: claude.session})
	ctx, cancel := context.WithCancel(context.Background())
	w := claudesession.NewWatcher(scope, func() {
		eng.SendCommand(core.Command{
			Type:    game + ".pause",
			Payload: core.MustJSON(map[string]string{"reason": "Paused by Claude"}),
		})
	})
	go w.Run(ctx)
	return cancel
}
