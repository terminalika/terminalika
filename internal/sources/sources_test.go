package sources

import (
	"testing"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/config"
)

func TestBuildCreatesOneSourcePerNativeAgentPlusWebhook(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	cfg := config.Default()
	cfg.Webhook.Addr = "127.0.0.1:0"
	set := Build(cfg, []agents.ID{agents.Claude, agents.Pi, agents.Cursor})
	if len(set.Agents) != 3 {
		t.Errorf("Agents = %v", set.Agents)
	}
	// claude + pi + webhook; cursor has no native source.
	if len(set.Sources) != 3 || set.Webhook == nil {
		t.Errorf("Sources = %d, webhook = %v", len(set.Sources), set.Webhook != nil)
	}
	if set.Webhook != nil {
		set.Webhook.Addr() // bound
	}
}

func TestBuildWithoutAgentsStartsNothing(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	set := Build(config.Config{}, nil)
	if len(set.Sources) != 0 || set.Webhook != nil {
		t.Errorf("Set = %+v", set)
	}
}

func TestBuildHonoursDisabledWebhook(t *testing.T) {
	t.Setenv("TERMINALIKA_CONFIG_DIR", t.TempDir())
	cfg := config.Config{Webhook: config.Webhook{Disabled: true}}
	set := Build(cfg, []agents.ID{agents.Aider})
	if len(set.Sources) != 1 || set.Webhook != nil {
		t.Errorf("Set = %+v", set)
	}
}
