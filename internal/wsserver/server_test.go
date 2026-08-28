package wsserver

import (
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	core "github.com/terminalika/terminalika-core"
)

type fakeSession struct {
	bus  *core.Bus
	mu   sync.Mutex
	cmds []core.Command
}

func (f *fakeSession) Bus() *core.Bus { return f.bus }
func (f *fakeSession) SendCommand(c core.Command) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, c)
}
func (f *fakeSession) Commands() []core.CommandSpec {
	return []core.CommandSpec{{Name: "test.run", Description: "test command"}}
}
func (f *fakeSession) received() []core.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.Command(nil), f.cmds...)
}

func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestCommandListAndEventFlow(t *testing.T) {
	bus := core.NewBus()
	session := &fakeSession{bus: bus}
	srv := httptest.NewServer(New(session))
	defer srv.Close()

	conn := dial(t, srv)

	// Requesting the command list proves the handler is up and subscribed.
	if err := conn.WriteJSON(inMessage{Kind: "list_commands"}); err != nil {
		t.Fatalf("write list_commands: %v", err)
	}
	var list outMessage
	if err := conn.ReadJSON(&list); err != nil {
		t.Fatalf("read command_list: %v", err)
	}
	if list.Kind != "command_list" || len(list.Commands) != 1 || list.Commands[0].Name != "test.run" {
		t.Fatalf("list = %+v, want command_list with test.run", list)
	}

	// An event emitted on the bus must reach the client.
	bus.Emit(core.Event{Type: "test.event", Game: "snake", At: time.Now()})

	var ev outMessage
	if err := conn.ReadJSON(&ev); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if ev.Kind != "event" || ev.Type != "test.event" || ev.Game != "snake" {
		t.Fatalf("event = %+v, want event test.event snake", ev)
	}
	if ev.At == nil {
		t.Fatal("event at should be set")
	}
}

func TestCommandReachesSession(t *testing.T) {
	bus := core.NewBus()
	session := &fakeSession{bus: bus}
	srv := httptest.NewServer(New(session))
	defer srv.Close()

	conn := dial(t, srv)

	if err := conn.WriteJSON(inMessage{Kind: "command", ID: "c1", Type: "test.run"}); err != nil {
		t.Fatalf("write command: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(session.received()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	cmds := session.received()
	if len(cmds) != 1 {
		t.Fatalf("commands = %d, want 1", len(cmds))
	}
	if cmds[0].ID != "c1" || cmds[0].Type != "test.run" {
		t.Fatalf("command = %+v, want id=c1 type=test.run", cmds[0])
	}
}

func TestUnknownKindReturnsError(t *testing.T) {
	bus := core.NewBus()
	session := &fakeSession{bus: bus}
	srv := httptest.NewServer(New(session))
	defer srv.Close()

	conn := dial(t, srv)

	if err := conn.WriteJSON(inMessage{Kind: "bogus"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	var got outMessage
	if err := conn.ReadJSON(&got); err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Kind != "error" || got.Code != "unknown_kind" {
		t.Fatalf("got = %+v, want error unknown_kind", got)
	}
}
