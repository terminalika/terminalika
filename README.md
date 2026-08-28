# terminalika

Standalone CLI launcher for the Terminalika terminal games. It consumes the
[`terminalika-core`](https://github.com/terminalika/terminalika-core) library.

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

The sidecar binds to the `-ws` base address. If that port is already taken
(e.g. by another project or Docker), it tries `+1`, `+2`, ... until a free port
is found. The resolved address is written to
`~/.config/terminalika/ws.json` (never printed to the terminal, which is in
fullscreen/raw mode while a game runs):

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
go 1.26.5

use (
	./terminalika
	./terminalika-core
)
```

From now on, `cd terminalika && go build ./...` uses the sibling
`terminalika-core` checkout instead of the published version.

The repos are private, so tell Go to fetch them directly over SSH and skip the
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
