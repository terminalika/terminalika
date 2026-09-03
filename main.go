// Command terminalika is an event-driven focus hub for people who work with
// CLI AI agents: it listens to the agents you pick, tells you the moment one
// finishes or needs your input, and keeps a library of retro games on hand
// for the wait - pausing whatever you're playing when an agent calls.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	core "github.com/terminalika/terminalika-core"
	"github.com/terminalika/terminalika-core/games"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/autostart"
	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/engine"
	"github.com/terminalika/terminalika/internal/home"
	"github.com/terminalika/terminalika/internal/hub"
	"github.com/terminalika/terminalika/internal/keystate"
	"github.com/terminalika/terminalika/internal/listener"
	"github.com/terminalika/terminalika/internal/notice"
	"github.com/terminalika/terminalika/internal/sidecar"
	"github.com/terminalika/terminalika/internal/sources"
	"github.com/terminalika/terminalika/internal/webhook"
	"github.com/terminalika/terminalika/internal/wizard"
	"github.com/terminalika/terminalika/internal/wsserver"
)

const wsPortTries = 100

// version is overridden at release time via
// -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

func main() {
	// Subcommands come before flag parsing: `terminalika notify ...` is run
	// by agents' hooks and must never touch the terminal.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "notify":
			os.Exit(runNotify(os.Args[2:]))
		case "daemon":
			// The background process of earlier versions. Its login entry
			// may still fire; take it down and go.
			os.Exit(runLegacyDaemon())
		case "setup":
			os.Args = append([]string{os.Args[0], "--setup"}, os.Args[2:]...)
		case "reset":
			os.Args = append([]string{os.Args[0], "--reset"}, os.Args[2:]...)
		}
	}

	gameFlag := flag.String("game", "", "skip the home screen and launch a game directly ("+strings.Join(games.Default().Names(), ", ")+")")
	wsFlag := flag.String("ws", "127.0.0.1:8080", "WebSocket base address for game events/commands (empty disables)")
	agentsFlag := flag.String("agents", "", "comma-separated agents to listen to for this run, overriding the config ("+agentIDList()+")")
	piFlag := flag.Bool("pi", false, "also listen to pi (shorthand for adding it to --agents)")
	claudeFlag := flag.Bool("claude", false, "also listen to Claude Code (shorthand for adding it to --agents)")
	setupFlag := flag.Bool("setup", false, "run the setup wizard again")
	resetFlag := flag.Bool("reset", false, "delete config.json and start over with the setup wizard")
	flag.BoolVar(resetFlag, "r", false, "shorthand for --reset")
	versionFlag := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println(version)
		return
	}

	registry := games.Default()

	// Validate the flag before taking over the terminal so a bad flag never
	// leaves the terminal in raw mode.
	if *gameFlag != "" && !registry.Has(*gameFlag) {
		fmt.Fprintf(os.Stderr, "unknown game %q; available games: %v\n", *gameFlag, registry.Names())
		os.Exit(1)
	}

	if *resetFlag {
		if err := config.Remove(); err != nil {
			fmt.Fprintf(os.Stderr, "terminalika: could not reset config: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "terminalika: config reset (%s); running setup\n", config.Path())
	}

	firstRun := !config.Exists()
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminalika: %v\n", err)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(os.Stderr, "terminalika: %v\n", err)
	}

	// Ask the terminal about key releases before tcell takes it over.
	support := keystate.Probe()

	screen, releases, err := newScreen(support)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not create screen: %v\n", err)
		os.Exit(1)
	}
	if err := screen.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "could not initialize screen: %v\n", err)
		os.Exit(1)
	}
	defer screen.Fini()

	if firstRun || *setupFlag {
		base := cfg
		if firstRun {
			base = config.Default()
		}
		saved, ok := wizard.New(screen, base).Run()
		if ok {
			cfg = saved
		} else if firstRun {
			// Cancelled on the very first run: play with nothing enabled,
			// and offer setup again next time.
			cfg = config.Config{}
		}
	}

	ids := resolveAgents(cfg, *agentsFlag, *piFlag, *claudeFlag)

	app := newApp(screen, cfg, ids)
	defer app.close()
	app.takeWindowSeat()
	removeLegacyDaemon()

	if *gameFlag != "" {
		app.runGame(releases, support, registry, *gameFlag, *wsFlag)
		return
	}

	h := home.New(screen, registry.Names(), app.hub, app.status)
	for !app.closing() {
		name, ok := h.Run()
		if !ok {
			return
		}

		app.runGame(releases, support, registry, name, *wsFlag)
	}
}

// removeLegacyDaemon clears what earlier versions left for their background
// process: the login entry and the daemon's seat and log files. terminalika
// is a game launcher, not a notification service; nothing runs when no
// window is open.
func removeLegacyDaemon() {
	_ = autostart.Remove()
	for _, f := range []string{"daemon.json", "daemon.log"} {
		_ = os.Remove(filepath.Join(sidecar.Dir(), f))
	}
}

// runLegacyDaemon answers `terminalika daemon` from a login entry an earlier
// version registered: remove the entry, say so, exit.
func runLegacyDaemon() int {
	removeLegacyDaemon()
	fmt.Fprintln(os.Stderr, "terminalika: the background process is gone (terminalika is a game launcher, not a notification service); its login entry has been removed")
	return 0
}

// agentIDList lists the catalogue ids for flag help.
func agentIDList() string {
	ids := make([]string, 0, len(agents.Catalog))
	for _, a := range agents.Catalog {
		ids = append(ids, string(a.ID))
	}
	return strings.Join(ids, ", ")
}

// resolveAgents decides which agents this run listens to: --agents replaces
// the config's list when given; --pi/--claude always add theirs.
func resolveAgents(cfg config.Config, agentsFlag string, pi, claude bool) []agents.ID {
	c := cfg
	if agentsFlag != "" {
		var ids []agents.ID
		for _, part := range strings.Split(agentsFlag, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			a, ok := agents.Lookup(part)
			if !ok {
				fmt.Fprintf(os.Stderr, "terminalika: unknown agent %q in --agents (known: %s)\n", part, agentIDList())
				continue
			}
			ids = append(ids, a.ID)
		}
		c.SetAgents(ids)
	}
	if pi {
		c.PI.Subscribe = true
	}
	if claude {
		c.Claude.Subscribe = true
	}
	return c.AgentIDs()
}

// app is the running launcher: the hub and everything subscribed to it.
// The hub runs for the whole session - on the home screen, inside a game,
// and in between - so no agent event is ever missed.
type app struct {
	screen tcell.Screen
	cfg    config.Config
	hub    *hub.Hub
	seat   *listener.Seat
	set    sources.Set

	// closeRequested is set when another terminalika window took the
	// listener seat: this window is on its way out (see takeWindowSeat).
	closeRequested atomic.Bool
}

func newApp(screen tcell.Screen, cfg config.Config, ids []agents.ID) *app {
	a := &app{
		screen: screen,
		cfg:    cfg,
		hub:    hub.New(),
	}

	if len(ids) == 0 {
		return a
	}

	a.set = sources.Build(cfg, ids)
	for _, src := range a.set.Sources {
		a.hub.Add(src)
	}
	for _, ag := range a.set.Agents {
		a.hub.Watch(ag)
	}

	a.hub.Start()
	return a
}

// takeWindowSeat claims the listener seat for this window, always: there is
// only ever one terminalika window. A window that held it before is told
// so and closes itself (it sees the takeover on its next heartbeat, see
// requestClose). The player is told when another window was closed for
// them.
func (a *app) takeWindowSeat() {
	closedOther := false
	if st := listener.Check(); st.Held && st.Kind == listener.Window {
		closedOther = true
	}
	seat, err := listener.Claim(listener.Window, a.requestClose)
	if err != nil {
		fmt.Fprintf(os.Stderr, "terminalika: %v\n", err)
		return
	}
	a.seat = seat
	if a.set.Webhook != nil {
		_ = a.set.Webhook.Publish()
	}
	if closedOther {
		notice.Show(a.screen, []string{
			"Closed your other terminalika window",
			"",
			"Only one terminalika window stays open at a time,",
			"so there's just one place to look. This one is it.",
		})
	}
}

// requestClose is called (from the seat's heartbeat goroutine) when another
// window took the listener seat: stop reacting to events and wake whichever
// screen loop is running so it returns - the home screen quits, a game
// leaves as if by ESC - and main falls through to exit.
func (a *app) requestClose() {
	a.hub.Mute(true)
	a.closeRequested.Store(true)
	_ = a.screen.PostEvent(tcell.NewEventInterrupt(nil))
}

// closing reports whether this window has been asked to close.
func (a *app) closing() bool { return a.closeRequested.Load() }

// status is what the home screen shows about the hub.
func (a *app) status() home.Status {
	st := home.Status{
		Agents:    a.hub.Agents(),
		AutoPause: a.cfg.PauseOnEvent(),
		Listening: a.hub.Running() && (a.seat == nil || a.seat.Held()),
	}
	if a.set.Webhook != nil {
		st.Webhook = a.set.Webhook.URL()
	}
	return st
}

func (a *app) close() {
	a.hub.Stop()
	if a.seat != nil {
		a.seat.Release()
	}
}

// newScreen opens the terminal screen. When the terminal reports key
// releases (kitty keyboard protocol, win32-input-mode) tcell's tty is
// wrapped so the releases reach the engine as marked key events; it reports
// whether that is the case. Otherwise a plain screen is used and the engine
// synthesises releases from auto-repeat.
func newScreen(support keystate.Support) (tcell.Screen, bool, error) {
	if support.Releases() {
		if tty, err := tcell.NewDevTty(); err == nil {
			if s, err := tcell.NewTerminfoScreenFromTty(keystate.Wrap(tty, support)); err == nil {
				return s, true, nil
			}
		}
	}
	s, err := tcell.NewScreen()
	return s, false, err
}

// docsURL is the documentation site; the key-release notices point at the
// page explaining the player's particular situation.
const docsURL = "terminalika.dev"

// heldControlLabel names what a game's continuously-held key drives, for the
// key-release warning below. Only games that implement
// core.KeyStateHandler are affected by a terminal's inability to report key
// releases - everything else moves one step per keypress and never reads
// held state, so it's unaffected regardless of the terminal. Keep this in
// sync with which games implement that interface - none of the built-in
// ones do at the moment (pong, which had paddles here, was dropped).
var heldControlLabel = map[string]string{}

// keyReleaseWarning is shown when the terminal cannot report key releases:
// holding a key then only produces the terminal's auto-repeat, which makes
// this game's continuous movement (named by control) feel sticky. The
// notice stays short and defers the full explanation to the website.
func keyReleaseWarning(support keystate.Support, control string) []string {
	held := fmt.Sprintf("Held keys (%s) will feel sticky.", control)
	if support.Zellij {
		return []string{
			"Zellij does not relay key releases",
			"",
			held,
			"Your terminal is fine; run outside zellij for smooth movement.",
			"",
			docsURL + "/terminals/zellij",
		}
	}
	if support.Tmux {
		return []string{
			"tmux does not support the kitty keyboard protocol",
			"",
			held,
			"Your terminal is fine; run outside tmux for smooth movement.",
			"",
			docsURL + "/terminals/tmux",
		}
	}
	return []string{
		"Your terminal does not report key releases",
		"",
		held,
		"Use a terminal that speaks the kitty keyboard protocol",
		"(kitty, foot, Ghostty, Alacritty, WezTerm, iTerm2, Konsole, Rio;",
		"Windows Terminal on Windows).",
		"",
		docsURL + "/terminals",
		"",
		"Detected: " + support.Detail,
	}
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

// runGame plays one game to completion (ESC), with the hub bridged into
// the engine for the duration.
func (a *app) runGame(releases bool, support keystate.Support, registry *core.Registry, name, wsAddr string) {
	game, ok := registry.Get(name)
	if !ok {
		return
	}

	if !releases {
		if _, ok := game.(core.KeyStateHandler); ok {
			control := heldControlLabel[name]
			if control == "" {
				control = "keys"
			}
			notice.Show(a.screen, keyReleaseWarning(support, control))
		}
	}

	eng := engine.New(a.screen, game)
	eng.SetTerminalReleases(releases)

	stopBridge := a.bridge(name, eng)
	defer stopBridge()

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

// bridge subscribes the running game to the hub: every agent event pauses
// the game with an attributed overlay (auto-pause on) or flashes a banner
// over it (auto-pause off). Showing an event in the game marks it seen on
// the hub, so it's the one place the player meets it: leaving the game
// afterwards doesn't bring it back as a toast on the home screen. An event
// that arrived in the gap between the home screen and the game starting
// is delivered first, for the same reason. It returns a stop function.
func (a *app) bridge(game string, eng *engine.Engine) func() {
	if !a.hub.Running() {
		return func() {}
	}
	autoPause := a.cfg.PauseOnEvent()
	var shown uint64 // Seq of the last event shown, so the catch-up below and the subscription can't both show one
	show := func(ev agents.Event) {
		if ev.Seq <= shown {
			return
		}
		shown = ev.Seq
		if autoPause {
			eng.SendCommand(pauseCommand(game, ev))
		} else {
			eng.Flash([]string{ev.Message()}, string(ev.Agent.ID))
		}
		a.hub.MarkSeen(ev)
	}

	ch := a.hub.Subscribe()
	if ev, ok := a.hub.Current(); ok {
		show(ev)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range ch {
			show(ev)
		}
	}()
	return func() {
		a.hub.Unsubscribe(ch)
		close(ch)
		<-done
	}
}

// pauseCommand is the "<game>.pause" command an agent event turns into:
// the one overlay line the engine shows, the agent (for its color) and the
// kind. It deliberately carries no "reason": games append that to their
// own status bar ("SCORE: 3 - Claude Code needs your input!"), which would
// be a second copy of the same notice - the overlay is the one and only.
func pauseCommand(game string, ev agents.Event) core.Command {
	return core.Command{
		Type: game + ".pause",
		Payload: core.MustJSON(map[string]any{
			"agent": string(ev.Agent.ID),
			"kind":  ev.Kind.String(),
			"lines": []string{ev.Message()},
		}),
	}
}

// hookInputTimeout bounds how long `terminalika notify` waits for hook JSON
// on stdin. A hook pipes its payload and closes the pipe immediately, so
// this only matters when stdin is some pipe that never closes (a
// notifications-command run with an inherited pipe, say) - the notification
// must go out anyway rather than hang forever.
const hookInputTimeout = 500 * time.Millisecond

// readHookInput returns stdin's contents when stdin is a pipe or file (a
// hook's JSON payload), nil when it's a terminal or nothing arrives within
// timeout.
func readHookInput(f *os.File, timeout time.Duration) []byte {
	stat, err := f.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice != 0 {
		return nil
	}
	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(f, 1<<20))
		ch <- result{data, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil
		}
		return r.data
	case <-time.After(timeout):
		return nil
	}
}

// runNotify implements `terminalika notify`: deliver one agent event to the
// running terminalika through its webhook ingest. It's meant to be wired
// into agents' own hooks; when a hook pipes its JSON on stdin (Claude
// Code, Cursor) the kind is inferred from it unless --kind says otherwise.
func runNotify(args []string) int {
	fs := flag.NewFlagSet("terminalika notify", flag.ContinueOnError)
	agent := fs.String("agent", "", "agent id ("+agentIDList()+"), or any name")
	kind := fs.String("kind", "", "finished or input_required (inferred from hook JSON on stdin when empty; defaults to finished)")
	detail := fs.String("detail", "", "optional free text shown in the notification")
	quiet := fs.Bool("quiet", true, "exit 0 even when no terminalika is running (hooks must never fail the agent)")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: terminalika notify --agent <id> [--kind finished|input_required] [--detail text]")
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Delivers an agent event to the running terminalika (see terminalika.dev/docs/events).")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *agent == "" {
		fs.Usage()
		return 2
	}

	req := webhook.Request{Agent: *agent, Kind: *kind, Detail: *detail}
	if data := readHookInput(os.Stdin, hookInputTimeout); data != nil {
		if k, d, ok := webhook.InferKind(data); ok {
			if req.Kind == "" {
				req.Kind = k.String()
			}
			if req.Detail == "" {
				req.Detail = d
			}
		} else if len(strings.TrimSpace(string(data))) > 0 && req.Kind == "" {
			// A hook we don't map to either kind (a subagent stop, a tool
			// hook): nothing to notify about.
			return 0
		}
	}

	if err := webhook.Post(req); err != nil {
		if *quiet {
			return 0
		}
		fmt.Fprintf(os.Stderr, "terminalika notify: %v\n", err)
		return 1
	}
	return 0
}
