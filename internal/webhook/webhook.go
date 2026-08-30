// Package webhook is the generic event ingest: a tiny local HTTP endpoint
// any tool can POST to when an agent finishes or needs input. It's how
// agents without a session file terminalika can tail (Cursor CLI), or with
// their own notification hooks (Aider's --notifications-command, Claude
// Code's Notification/Stop hooks), reach the hub.
//
// The endpoint's address is published to hub.json in the config dir so the
// `terminalika notify` subcommand - and anything else - can find it without
// touching the terminal.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/sidecar"
)

// DefaultAddr is the base address the ingest binds to; a taken port is
// skipped forward like the WebSocket sidecar does.
const DefaultAddr = "127.0.0.1:7788"

// portTries is how many consecutive ports Listen tries from the base.
const portTries = 50

// maxBody bounds a request body; a hook payload is a few hundred bytes.
const maxBody = 64 << 10

// Info is the published ingest address.
type Info struct {
	PID  int    `json:"pid"`
	Addr string `json:"addr"`
	URL  string `json:"url"`
}

// InfoPath returns where the ingest address is published.
func InfoPath() string { return filepath.Join(sidecar.Dir(), "hub.json") }

// Request is the JSON body a client POSTs to /events.
type Request struct {
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

// Server is the ingest endpoint. It implements hub.Source: Run serves until
// the context is cancelled.
type Server struct {
	ln   net.Listener
	addr string

	mu   sync.Mutex
	emit func(agents.Event)
}

// Listen binds the ingest at base (DefaultAddr when empty), skipping taken
// ports, and publishes the resolved address to hub.json.
func Listen(base string) (*Server, error) {
	if base == "" {
		base = DefaultAddr
	}
	host, portStr, err := net.SplitHostPort(base)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook address %q: %w", base, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("invalid webhook port %q: %w", portStr, err)
	}
	var lastErr error
	for i := 0; i < portTries; i++ {
		addr := net.JoinHostPort(host, strconv.Itoa(port+i))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			lastErr = err
			continue
		}
		s := &Server{ln: ln, addr: ln.Addr().String()}
		_ = writeInfo(Info{PID: os.Getpid(), Addr: s.addr, URL: "http://" + s.addr + "/events"})
		return s, nil
	}
	return nil, fmt.Errorf("no free port for the webhook ingest starting at %s: %w", base, lastErr)
}

// Addr is the bound address.
func (s *Server) Addr() string { return s.addr }

// Publish (re)writes hub.json to point at this ingest. Listen does it once;
// a process that takes the listener seat back after another one held it
// (the daemon after a window closes) does it again, so `terminalika
// notify` always reaches the process that is actually reacting.
func (s *Server) Publish() error {
	return writeInfo(Info{PID: os.Getpid(), Addr: s.addr, URL: s.URL()})
}

// URL is the full ingest URL clients POST to.
func (s *Server) URL() string { return "http://" + s.addr + "/events" }

// Run serves the ingest until ctx is cancelled, handing every accepted
// event to emit.
func (s *Server) Run(ctx context.Context, emit func(agents.Event)) {
	s.mu.Lock()
	s.emit = emit
	s.mu.Unlock()

	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	done := make(chan struct{})
	go func() {
		_ = srv.Serve(s.ln)
		close(done)
	}()
	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	<-done
	if info, err := readInfo(); err == nil && info.PID == os.Getpid() {
		_ = os.Remove(InfoPath())
	}
}

// ServeHTTP handles POST /events (JSON body or agent/kind query params) and
// GET / (a small self-description).
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{
			"service": "terminalika",
			"events":  s.URL(),
			"agents":  agentIDs(),
			"kinds":   []string{agents.Finished.String(), agents.InputRequired.String()},
		})
	case r.Method == http.MethodPost:
		req, err := decodeRequest(r)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		ev, err := req.Event()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		s.mu.Lock()
		emit := s.emit
		s.mu.Unlock()
		if emit != nil {
			emit(ev)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "agent": string(ev.Agent.ID), "kind": ev.Kind.String()})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func decodeRequest(r *http.Request) (Request, error) {
	var req Request
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		return req, err
	}
	if len(bytes.TrimSpace(body)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return req, fmt.Errorf("invalid JSON body: %w", err)
		}
	}
	q := r.URL.Query()
	if req.Agent == "" {
		req.Agent = q.Get("agent")
	}
	if req.Kind == "" {
		req.Kind = q.Get("kind")
	}
	if req.Detail == "" {
		req.Detail = q.Get("detail")
	}
	return req, nil
}

// Event validates the request and turns it into an agent event.
func (req Request) Event() (agents.Event, error) {
	if req.Agent == "" {
		return agents.Event{}, errors.New("missing agent")
	}
	kind := agents.Finished
	if req.Kind != "" {
		k, ok := agents.ParseKind(req.Kind)
		if !ok {
			return agents.Event{}, fmt.Errorf("unknown kind %q (use finished or input_required)", req.Kind)
		}
		kind = k
	}
	a, _ := agents.Lookup(req.Agent)
	return agents.Event{Agent: a, Kind: kind, Detail: req.Detail, Source: "webhook"}, nil
}

// Post delivers a request to the running terminalika's ingest, found via
// hub.json. It is what `terminalika notify` does.
func Post(req Request) error {
	info, err := readInfo()
	if err != nil {
		return fmt.Errorf("no running terminalika found (%s): %w", InfoPath(), err)
	}
	body, _ := json.Marshal(req)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Post(info.URL, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	return nil
}

func agentIDs() []string {
	ids := make([]string, 0, len(agents.Catalog))
	for _, a := range agents.Catalog {
		ids = append(ids, string(a.ID))
	}
	return ids
}

func writeInfo(info Info) error {
	if err := os.MkdirAll(sidecar.Dir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	return os.WriteFile(InfoPath(), data, 0o644)
}

func readInfo() (Info, error) {
	data, err := os.ReadFile(InfoPath())
	if err != nil {
		return Info{}, err
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return Info{}, err
	}
	if info.URL == "" {
		return Info{}, errors.New("hub.json has no url")
	}
	return info, nil
}
