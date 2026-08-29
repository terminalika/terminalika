# terminalika

Terminalika is a tiny terminal game launcher for developers who want something
nostalgic to do while their AI agents are working: fire it up alongside a
coding agent, and it can pause your game automatically the moment the agent
needs your attention, so you never miss a turn-taking cue while lost in Snake
or Tetris.

This repo is the standalone CLI — the menu, the game loop/engine, keybindings,
the optional WebSocket sidecar, and the pi/Claude Code session watchers. The
games themselves (Snake, Tetris, Space Invaders, Pong) and the engine contract
live in the separate
[`terminalika-core`](https://github.com/terminalika/terminalika-core) library,
which this CLI imports as a normal Go dependency.

## Install

Prebuilt packages and binaries (Homebrew, Scoop, `.deb`/`.rpm`/`.pkg.tar.zst`/
`.apk`, raw `tar.gz`/`zip`) are documented on
**[terminalika.dev/install](https://terminalika.dev/install/)**.

To build from source instead (Go 1.24+):

```sh
go install github.com/terminalika/terminalika@latest
```

On Windows you need Windows 10+ and a VT-capable terminal (Windows
Terminal). See [RELEASING.md](RELEASING.md) for the release pipeline.

## Run

```sh
# open the game selection menu
go run .

# skip the menu and launch a game directly
go run . --game=snake
go run . --game=tetris
go run . --game=invaders
go run . --game=pong

# also open the WebSocket sidecar (default 127.0.0.1:8080; use -ws="" to disable)
go run . --game=snake --ws=127.0.0.1:8080
```

Multiple terminalika instances can run at once, but only one may listen for
agent events at a time - regardless of which agent(s) it watches. Launching a
second instance with `-pi`/`-claude` (or `subscribe` in config) while another
live instance already holds that seat asks the player whether to move
listening to this window; declining leaves that instance running the game
without pausing on any agent events. An instance started with neither flag
never asks and never pauses on its own. The seat is tracked in
`listener.json` in the user config dir, with a heartbeat so a crashed
holder's seat is reclaimed automatically instead of asking forever.

The sidecar binds to the `-ws` base address. If that port is already taken
(e.g. by another project or Docker), it tries `+1`, `+2`, ... until a free port
is found. The resolved address is written to `ws.json` in the user config dir
(never printed to the terminal, which is in fullscreen/raw mode while a game
runs):

```json
{"game":"snake","addr":"127.0.0.1:8081","url":"ws://127.0.0.1:8081"}
```

`ws.json` is a single shared file, so running several instances at once with
the sidecar enabled makes each overwrite the others' entry; if you need to
observe more than one instance's sidecar, run the rest with `-ws=""` or
discover their ports another way.

### Key releases

Terminals normally only report key presses; holding a key just produces the
terminal's auto-repeat (one event, a pause, then a burst), which makes
continuous movement feel sticky. At start the launcher asks the terminal
(`CSI ? u`, before tcell takes over) whether it speaks the
[kitty keyboard protocol](https://sw.kovidgoyal.net/kitty/keyboard-protocol/).
If it does, tcell's tty is wrapped (`internal/keystate`): the wrapper asks
for the protocol's *report event types* flag, pulls the key release
sequences out of the input stream before tcell (which doesn't understand
them) sees them, rewrites repeats into plain presses and re-injects each
release as a press of the same key marked with a Hyper modifier
(`keystate.ReleaseMod`), so it goes through tcell's own queue in order with
the presses; the engine turns those back into releases for games implementing
`core.KeyStateHandler`. On Windows, Windows Terminal's win32-input-mode
key-ups are used the same way.

Terminals with real key releases: kitty, foot, Ghostty, Alacritty, WezTerm
(`enable_kitty_keyboard = true`), iTerm2, Konsole, Rio, Windows Terminal;
tmux passes the protocol through with `extended-keys` on. Without support
(GNOME Terminal and other VTE terminals, xterm, Terminal.app) a warning is
shown once at start and the engine synthesises a release ~120 ms after a
key's last press or auto-repeat.

### WebSocket protocol

Connect to `/` on the `-ws` address. Both directions use JSON text frames with
a top-level `kind` field.

Client → server:

```json
{"kind":"list_commands"}
{"kind":"command", "id":"c1", "type":"snake.set_direction", "payload":{"direction":"up"}}
```

Server → client:

```json
{"kind":"command_list", "commands":[{"name":"snake.set_direction", "description":"..."}]}
{"kind":"event", "type":"snake.direction_changed", "game":"snake", "correlation_id":"c1", "payload":{"from":"right","to":"up"}}
{"kind":"error", "code":"unknown_kind", "message":"..."}
```

Commands that fail produce a `command.rejected` event carrying the command's
correlation id. Spontaneous events (keyboard/timer driven) carry no
`correlation_id`.

## pi subscription

The launcher can subscribe to pi sessions and pause the game when the agent
settles. Pi appends entries to its session files live, so terminalika tails
them — no separate bridge process and no pi server mode needed. It works on
Linux, macOS and Windows.

The session directory follows pi's own resolution: `~/.pi/agent/sessions` by
default, `PI_CODING_AGENT_SESSION_DIR` overrides it directly, and
`PI_CODING_AGENT_DIR` moves the whole agent dir (sessions then live in
`<dir>/sessions`).

By default **any** session of **any** running pi triggers the pause. To
restrict it, set `dir` or `session` in the config.

Enable it with the `-pi` flag or in `~/.config/terminalika/config.json`:

```sh
# enable via flag
go run . --game=snake -pi

# or via config (either one is enough)
cat > ~/.config/terminalika/config.json <<'EOF'
{"pi":{"subscribe":true}}
EOF
go run . --game=snake
```

Config fields under `pi`:

- `subscribe` (bool): enable the subscription (OR-ed with `-pi`).
- `dir` (string, optional): restrict to sessions of the pi running in this
  project directory. Unset = every project.
- `session` (string, optional): explicit session file path; overrides `dir`.

```jsonc
// example: only react to pi running in this one directory
{"pi": {"subscribe": true, "dir": "/home/me/my-project"}}
```

Event watched: a new assistant message with a terminal `stopReason` (e.g.
`stop`) → `<game>.pause`. Assistant messages that are still calling tools
(`stopReason: "toolUse"`) are ignored. Only entries appended after the game
starts count; existing history is skipped.

The pause command is sent with a `reason` payload, so the game's pause overlay
reads `Paused by PI`. The `<game>.pause` commands (`snake.pause`, `pong.pause`, ...) also accept
an optional `reason` field for any other client:

```json
{"kind":"command", "type":"snake.pause", "payload":{"reason":"Paused by PI"}}
```

## Claude Code subscription

The launcher can also subscribe to Claude Code sessions, the same way it does
for pi: it tails Claude Code's own session files and pauses the game when the
agent settles, no separate bridge process needed.

The session directory follows Claude Code's own layout:
`~/.claude/projects` by default, or `<dir>/projects` when
`CLAUDE_CONFIG_DIR` is set.

By default **any** session of **any** running Claude Code instance triggers
the pause (a session's own subagent transcripts are never watched directly).
To restrict it, set `dir` or `session` in the config.

Enable it with the `-claude` flag or in `~/.config/terminalika/config.json`:

```sh
# enable via flag
go run . --game=snake -claude

# or via config (either one is enough)
cat > ~/.config/terminalika/config.json <<'EOF'
{"claude":{"subscribe":true}}
EOF
go run . --game=snake
```

Config fields under `claude`:

- `subscribe` (bool): enable the subscription (OR-ed with `-claude`).
- `dir` (string, optional): restrict to sessions of the Claude Code instance
  running in this project directory. Unset = every project.
- `session` (string, optional): explicit session file path; overrides `dir`.

```jsonc
// example: only react to Claude Code running in this one directory
{"claude": {"subscribe": true, "dir": "/home/me/my-project"}}
```

Event watched: a new assistant message with a terminal `stop_reason` (e.g.
`end_turn`) → `<game>.pause`. Assistant messages that are still calling tools
(`stop_reason: "tool_use"`) are ignored. Only entries appended after the game
starts count; existing history is skipped.

The pause command is sent with a `reason` payload, so the game's pause overlay
reads `Paused by Claude`:

```json
{"kind":"command", "type":"snake.pause", "payload":{"reason":"Paused by Claude"}}
```

Both subscriptions can be enabled at the same time (`-pi -claude`); either
agent settling pauses the game.

## Local development

`terminalika` depends on `github.com/terminalika/terminalika-core` as a
published module, so it builds standalone. To work on both at once, clone the
two repos side by side and use a Go workspace:

```sh
mkdir terminalika-dev && cd terminalika-dev
git clone git@github.com:terminalika/terminalika-core.git
git clone git@github.com:terminalika/terminalika.git

# create a workspace that links the local core into the launcher
go work init ./terminalika ./terminalika-core
```

The generated `go.work` looks like this (and is local-only, don't commit it):

```
go 1.24.0

use (
	./terminalika
	./terminalika-core
)
```

From now on, `cd terminalika && go build ./...` uses the sibling
`terminalika-core` checkout instead of the published version.

The repos are public, so `go get` / `go install` work out of the box. For
local development over SSH, configure Go to fetch them directly and skip the
public module proxy:

```sh
git config --global url."git@github.com:".insteadOf "https://github.com/"
go env -w GOPRIVATE=github.com/terminalika/*
```

## Test

```sh
cd terminalika
go test ./...
```
