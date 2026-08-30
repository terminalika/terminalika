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
	if lines := q.OverlayLines(); lines[0] != "[INPUT REQUIRED: Claude Code]" || !strings.Contains(lines[2], "[SPACE]") || !strings.Contains(lines[2], "[ESC]") {
		t.Errorf("OverlayLines = %q", lines)
	}

	pi, _ := Lookup("pi")
	d := Event{Agent: pi, Kind: Finished}
	if d.Title() != "Pi Agent finished processing" {
		t.Errorf("Title = %q", d.Title())
	}
	if lines := d.OverlayLines(); lines[0] != "[AGENT READY: Pi Agent]" {
		t.Errorf("OverlayLines = %q", lines)
	}
}
