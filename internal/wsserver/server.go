// Package wsserver exposes a running game's events and commands over a
// WebSocket. It is a thin transport adapter: it knows nothing about the games
// themselves, only the core.Event / core.Command envelopes.
package wsserver

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	core "github.com/terminalika/terminalika-core"
)

// Session is the part of the engine that the WebSocket server needs.
type Session interface {
	Bus() *core.Bus
	SendCommand(core.Command)
	Commands() []core.CommandSpec
}

// inMessage is a frame received from a client.
type inMessage struct {
	Kind    string          `json:"kind"`              // "command" | "list_commands" | "subscribe"
	ID      string          `json:"id,omitempty"`      // command correlation id
	Type    string          `json:"type,omitempty"`    // command type
	Payload json.RawMessage `json:"payload,omitempty"` // command payload
	Events  []string        `json:"events,omitempty"`  // subscribe filter (reserved)
}

// outMessage is a frame sent to a client.
type outMessage struct {
	Kind          string             `json:"kind"` // "event" | "command_list" | "error"
	Type          string             `json:"type,omitempty"`
	Game          string             `json:"game,omitempty"`
	At            *time.Time         `json:"at,omitempty"`
	CorrelationID string             `json:"correlation_id,omitempty"`
	Payload       json.RawMessage    `json:"payload,omitempty"`
	Commands      []core.CommandSpec `json:"commands,omitempty"`
	Code          string             `json:"code,omitempty"`
	Message       string             `json:"message,omitempty"`
}

// Server upgrades HTTP connections and bridges them to a game session.
type Server struct {
	session  Session
	upgrader websocket.Upgrader

	mu    sync.Mutex
	conns map[*websocket.Conn]struct{}
}

// New returns a WebSocket handler for the given session.
func New(session Session) *Server {
	return &Server{
		session: session,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			// Local tool: accept every origin.
			CheckOrigin: func(r *http.Request) bool { return true },
		},
		conns: make(map[*websocket.Conn]struct{}),
	}
}

// Close closes every active client connection. It is used when the game
// session ends so clients do not linger on a dead engine.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		_ = c.Close()
	}
}

// ServeHTTP upgrades the connection and starts serving it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	go s.handleConn(conn)
}

func (s *Server) handleConn(conn *websocket.Conn) {
	s.mu.Lock()
	s.conns[conn] = struct{}{}
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		_ = conn.Close()
	}()

	out := make(chan outMessage, 64)
	sub := s.session.Bus().Subscribe()

	// A single writer goroutine owns the socket, so concurrent writes cannot
	// race. It drains both direct responses and bus events.
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case m, ok := <-out:
				if !ok {
					return
				}
				if err := conn.WriteJSON(m); err != nil {
					return
				}
			case ev := <-sub:
				if err := conn.WriteJSON(eventMessage(ev)); err != nil {
					return
				}
			}
		}
	}()

	for {
		var msg inMessage
		if err := conn.ReadJSON(&msg); err != nil {
			break
		}

		switch msg.Kind {
		case "command":
			s.session.SendCommand(core.Command{ID: msg.ID, Type: msg.Type, Payload: msg.Payload})
		case "list_commands":
			s.send(out, writerDone, outMessage{Kind: "command_list", Commands: s.session.Commands()})
		case "subscribe":
			// Filtering is reserved for a later phase; every client currently
			// receives the full event stream.
			_ = msg.Events
		default:
			s.send(out, writerDone, outMessage{Kind: "error", Code: "unknown_kind", Message: "unknown message kind"})
		}
	}

	s.session.Bus().Unsubscribe(sub)
	close(out)
	<-writerDone
}

// send enqueues a message for the writer goroutine, giving up if it exited.
func (s *Server) send(out chan<- outMessage, done <-chan struct{}, m outMessage) {
	select {
	case out <- m:
	case <-done:
	}
}

func eventMessage(ev core.Event) outMessage {
	at := ev.At
	return outMessage{
		Kind:          "event",
		Type:          ev.Type,
		Game:          ev.Game,
		At:            &at,
		CorrelationID: ev.CorrelationID,
		Payload:       ev.Payload,
	}
}
