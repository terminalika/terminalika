# terminalika

Standalone CLI launcher for the Terminalika terminal games. It consumes the
[`terminalika-core`](https://github.com/terminalika/terminalika-core) library.

## Install

### Linux: direct downloads (.deb / .rpm / pkg.tar.zst)

Every release ships prebuilt `.deb`, `.rpm` and `.pkg.tar.zst (for arch)` packages on the
[releases page](https://github.com/terminalika/terminalika/releases) —
install them directly

```sh
# Debian / Ubuntu (amd64) — pick the latest version from the releases page
wget https://github.com/terminalika/terminalika/releases/download/v0.3.2/terminalika_0.3.2_amd64.deb
sudo apt install ./terminalika_0.3.2_amd64.deb

# Fedora / RHEL / openSUSE (x86_64)
wget https://github.com/terminalika/terminalika/releases/download/v0.3.2/terminalika-0.3.2-1.x86_64.rpm
sudo dnf install ./terminalika-0.3.2-1.x86_64.rpm

# Arch Linux — pacman installs the release's .pkg.tar.zst directly
wget https://github.com/terminalika/terminalika/releases/download/v0.3.2/terminalika-0.3.2-1-x86_64.pkg.tar.zst
sudo pacman -U terminalika-0.3.2-1-x86_64.pkg.tar.zst
```

`arm64`/`aarch64` variants of every package are attached too, alongside raw
binaries (`tar.gz` for Linux/macOS, `zip` for Windows).

### Windows and MacOS

```sh
# Homebrew (macOS/Linux)
brew tap terminalika/tap && brew install --cask terminalika

# Scoop (Windows)
scoop bucket add terminalika https://github.com/terminalika/scoop-bucket
scoop install terminalika
```

### From source

```sh
go install github.com/terminalika/terminalika@latest   # Go 1.24+
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

# also open the WebSocket sidecar (default 127.0.0.1:8080; use -ws="" to disable)
go run . --game=snake --ws=127.0.0.1:8080
```

Only **one terminalika instance** can run per machine: a second launch prints
an error and exits. The lock lives in `instance.lock` in the user config dir
(`~/.config/terminalika` on Linux, `~/Library/Application Support/terminalika`
on macOS, `%AppData%\terminalika` on Windows) and is released automatically
when the process exits (`flock` on Unix, `LockFileEx` on Windows).

The sidecar binds to the `-ws` base address. If that port is already taken
(e.g. by another project or Docker), it tries `+1`, `+2`, ... until a free port
is found. The resolved address is written to `ws.json` in the user config dir
(never printed to the terminal, which is in fullscreen/raw mode while a game
runs):

```json
{"game":"snake","addr":"127.0.0.1:8081","url":"ws://127.0.0.1:8081"}
```

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
reads `Paused by PI`. The `snake.pause` / `tetris.pause` commands also accept
an optional `reason` field for any other client:

```json
{"kind":"command", "type":"snake.pause", "payload":{"reason":"Paused by PI"}}
```

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
