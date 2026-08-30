package agents

import (
	"strings"
	"testing"
)

func TestLookupKnownAndUnknown(t *testing.T) {
	a, ok := Lookup("Claude")
	if !ok || a.Name != "Claude Code" {
		t.Fatalf("Lookup(claude) = %+v, %v", a, ok)
	}
	u, ok := Lookup("robo")
	if ok || u.ID != "robo" || u.Name != "robo" {
		t.Fatalf("Lookup(robo) = %+v, %v; want synthetic agent", u, ok)
	}
}

func TestParseKindSpellings(t *testing.T) {
	for _, s := range []string{"settled", "finished", "done", "Stop"} {
		if k, ok := ParseKind(s); !ok || k != Finished {
			t.Errorf("ParseKind(%q) = %v, %v; want Finished", s, k, ok)
		}
	}
	for _, s := range []string{"prompt", "question_asked", "user_input_required", "permission_prompt"} {
		if k, ok := ParseKind(s); !ok || k != InputRequired {
			t.Errorf("ParseKind(%q) = %v, %v; want InputRequired", s, k, ok)
		}
	}
	if _, ok := ParseKind("dance"); ok {
		t.Error("ParseKind(dance) should fail")
	}
}

func TestEventTexts(t *testing.T) {
	claude, _ := Lookup("claude")
	q := Event{Agent: claude, Kind: InputRequired}
	if q.Title() != "Claude Code needs your input!" {
		t.Errorf("Title = %q", q.Title())
	}
	// The on-screen message is one short line naming the agent - no key
	// hints, no "[INPUT REQUIRED]" tag, nothing that reads like an
	// instruction from the game itself.
	if m := q.Message(); !strings.HasPrefix(m, "Claude Code has a question") || strings.Contains(m, "SPACE") || strings.Contains(m, "ESC") || strings.Contains(m, "[") {
		t.Errorf("Message = %q", m)
	}

	pi, _ := Lookup("pi")
	d := Event{Agent: pi, Kind: Finished}
	if d.Title() != "Pi Agent finished processing" {
		t.Errorf("Title = %q", d.Title())
	}
	if m := d.Message(); m != "Pi Agent's done - you're up." {
		t.Errorf("Message = %q", m)
	}
	// A custom per-agent message from config.json replaces the Finished
	// line outright; a question keeps its own wording.
	if m := (Event{Agent: pi, Kind: Finished, Detail: "PI's out, you're up"}).Message(); m != "PI's out, you're up" {
		t.Errorf("Message with Detail = %q", m)
	}
	if m := (Event{Agent: pi, Kind: InputRequired, Detail: "needs permission"}).Message(); !strings.HasPrefix(m, "Pi Agent has a question") {
		t.Errorf("InputRequired Message with Detail = %q", m)
	}
}
