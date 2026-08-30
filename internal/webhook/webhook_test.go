package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
)

func TestListenServesAndPublishesInfo(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())

	s, err := Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	info, err := readInfo()
	if err != nil || info.URL != s.URL() {
		t.Fatalf("hub.json = %+v, %v; want url %s", info, err, s.URL())
	}

	got := make(chan agents.Event, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx, func(ev agents.Event) { got <- ev })
		close(done)
	}()

	// Wait for the server to accept connections, then post through the
	// same path `terminalika notify` uses.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if err = Post(Request{Agent: "aider", Kind: "settled", Detail: "all done"}); err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	select {
	case ev := <-got:
		if ev.Agent.ID != agents.Aider || ev.Kind != agents.Finished || ev.Detail != "all done" || ev.Source != "webhook" {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("event never emitted")
	}

	// Query parameters work too, for curl one-liners.
	resp, err := http.Post(s.URL()+"?agent=cursor&kind=input_required", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d", resp.StatusCode)
	}
	select {
	case ev := <-got:
		if ev.Agent.ID != agents.Cursor || ev.Kind != agents.InputRequired {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("event never emitted")
	}

	// Bad kinds are rejected.
	resp, err = http.Post(s.URL(), "application/json", strings.NewReader(`{"agent":"pi","kind":"dance"}`))
	if err != nil {
		t.Fatal(err)
	}
	var e struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&e)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(e.Error, "dance") {
		t.Errorf("bad kind: status %d, error %q", resp.StatusCode, e.Error)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run never returned after cancel")
	}
	if _, err := readInfo(); err == nil {
		t.Error("hub.json should be removed after Run returns")
	}
}

func TestPostWithoutRunningInstance(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	if err := Post(Request{Agent: "pi"}); err == nil {
		t.Fatal("expected an error without hub.json")
	}
}

func TestInferKind(t *testing.T) {
	cases := []struct {
		in   string
		kind agents.EventKind
		ok   bool
	}{
		{`{"hook_event_name":"Stop","stop_hook_active":false}`, agents.Finished, true},
		{`{"hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs permission"}`, agents.InputRequired, true},
		{`{"hook_event_name":"Notification","notification_type":"idle_prompt"}`, agents.InputRequired, true},
		{`{"hook_event_name":"Notification","notification_type":"auth_success"}`, 0, false},
		{`{"hook_event_name":"SubagentStop"}`, 0, false},
		{`{"hook_event_name":"stop"}`, agents.Finished, true},
		{`not json`, 0, false},
	}
	for _, c := range cases {
		kind, _, ok := InferKind([]byte(c.in))
		if ok != c.ok || (ok && kind != c.kind) {
			t.Errorf("InferKind(%s) = %v, %v; want %v, %v", c.in, kind, ok, c.kind, c.ok)
		}
	}
}
