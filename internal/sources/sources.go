// Package sources builds the hub sources for the agents a configuration
// enables: the native session watchers (Claude Code, pi, Aider) and the
// webhook ingest every agent's own hooks can post to.
package sources

import (
	"context"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/aiderhistory"
	"github.com/terminalika/terminalika/internal/claudesession"
	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/hub"
	"github.com/terminalika/terminalika/internal/pisession"
	"github.com/terminalika/terminalika/internal/webhook"
)

// Set is the outcome of Build.
type Set struct {
	Sources []hub.Source
	Agents  []agents.Agent

	// Webhook is the bound ingest, or nil when disabled or unavailable.
	Webhook *webhook.Server

	// Warnings are non-fatal problems (the ingest port, say).
	Warnings []string
}

// Build creates the sources for the enabled agents. The webhook ingest is
// started whenever at least one agent is enabled (unless disabled in the
// config), since every agent can use it.
func Build(cfg config.Config, ids []agents.ID) Set {
	var set Set
	for _, id := range ids {
		a, _ := agents.Lookup(string(id))
		set.Agents = append(set.Agents, a)
		switch id {
		case agents.Claude:
			set.Sources = append(set.Sources, Claude(cfg.Claude))
		case agents.Pi:
			set.Sources = append(set.Sources, Pi(cfg.PI))
		case agents.Aider:
			set.Sources = append(set.Sources, Aider(cfg.Aider))
		}
	}
	if len(ids) > 0 && !cfg.Webhook.Disabled {
		srv, err := webhook.Listen(cfg.Webhook.Addr)
		if err != nil {
			set.Warnings = append(set.Warnings, err.Error())
		} else {
			set.Webhook = srv
			set.Sources = append(set.Sources, srv)
		}
	}
	return set
}

// Claude adapts the Claude Code session watcher.
func Claude(c config.Claude) hub.Source {
	agent, _ := agents.Lookup(string(agents.Claude))
	scope := claudesession.ResolveScope(claudesession.Options{Dir: c.Dir, Session: c.Session})
	return hub.SourceFunc(func(ctx context.Context, emit func(agents.Event)) {
		w := claudesession.NewWatcher(scope, func(kind claudesession.SettleKind) {
			ev := agents.Event{Agent: agent, Kind: agents.Finished, Source: "session", Detail: c.Message}
			if kind == claudesession.SettleQuestion {
				ev.Kind = agents.InputRequired
				ev.Detail = ""
			}
			emit(ev)
		})
		w.Run(ctx)
	})
}

// Pi adapts the pi session watcher.
func Pi(c config.PI) hub.Source {
	agent, _ := agents.Lookup(string(agents.Pi))
	scope := pisession.ResolveScope(pisession.Options{Dir: c.Dir, Session: c.Session})
	return hub.SourceFunc(func(ctx context.Context, emit func(agents.Event)) {
		w := pisession.NewWatcher(scope, func() {
			emit(agents.Event{Agent: agent, Kind: agents.Finished, Source: "session", Detail: c.Message})
		})
		w.Run(ctx)
	})
}

// Aider adapts the Aider chat history watcher.
func Aider(c config.Aider) hub.Source {
	path := aiderhistory.Resolve(aiderhistory.Options{Dir: c.Dir, File: c.History})
	return aiderhistory.NewWatcher(path)
}
