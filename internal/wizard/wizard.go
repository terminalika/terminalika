// Package wizard is the interactive first-run setup: which agents to
// listen to and whether games pause on agent events. The answers are
// written to config.json.
package wizard

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/agents"
	"github.com/terminalika/terminalika/internal/config"
	"github.com/terminalika/terminalika/internal/keystate"
	"github.com/terminalika/terminalika/internal/ui"
)

// DocsURL is where the wizard points for advanced configuration.
const DocsURL = "terminalika.dev/docs/events"

// step indexes the wizard's pages.
type step int

const (
	stepAgents step = iota
	stepPause
	stepSummary
	stepCount
)

// Wizard holds the pages' state.
type Wizard struct {
	screen tcell.Screen
	base   config.Config
	step   step

	agents ui.List
	pause  ui.List

	saveErr error
}

// New prepares a wizard pre-filled from base (typically config.Default(),
// or the current config when re-running setup).
func New(screen tcell.Screen, base config.Config) *Wizard {
	w := &Wizard{screen: screen, base: base}

	for _, a := range agents.Catalog {
		w.agents.Items = append(w.agents.Items, ui.Item{
			Label:   a.Name,
			Hint:    a.Hint,
			Checked: base.HasAgent(a.ID),
			Value:   string(a.ID),
		})
	}

	w.pause.Single = true
	w.pause.Items = []ui.Item{
		{Label: "Yes / Pause Game (Recommended)", Hint: "freeze the game and show who needs you", Value: "yes"},
		{Label: "No, just tell me", Hint: "keep playing; a banner shows the event", Value: "no"},
	}
	if base.PauseOnEvent() {
		selectValue(&w.pause, "yes")
	} else {
		selectValue(&w.pause, "no")
	}
	return w
}

// selectValue checks the radio row with the given value (the first row
// when none matches) and parks the cursor on it.
func selectValue(l *ui.List, value string) {
	idx := 0
	for i, it := range l.Items {
		if it.Value == value {
			idx = i
			break
		}
	}
	for i := range l.Items {
		l.Items[i].Checked = i == idx
	}
	l.Cursor = idx
}

// Run drives the wizard until the player saves or cancels. It returns the
// resulting config and whether it was saved to disk.
func (w *Wizard) Run() (config.Config, bool) {
	w.draw()
	for {
		ev := w.screen.PollEvent()
		if ev == nil {
			return w.result(), false
		}
		switch ev := ev.(type) {
		case *tcell.EventResize:
			w.screen.Sync()
		case *tcell.EventKey:
			if keystate.IsRelease(ev) {
				continue
			}
			switch w.handleKey(ev) {
			case actionCancel:
				return w.result(), false
			case actionSave:
				c := w.result()
				if err := config.Save(c); err != nil {
					w.saveErr = err
					break
				}
				return c, true
			}
		}
		w.draw()
	}
}

type action int

const (
	actionNone action = iota
	actionCancel
	actionSave
)

func (w *Wizard) handleKey(ev *tcell.EventKey) action {
	switch ev.Key() {
	case tcell.KeyCtrlC:
		return actionCancel
	case tcell.KeyEscape:
		if w.step == stepAgents {
			return actionCancel
		}
		w.step--
		return actionNone
	case tcell.KeyBackspace, tcell.KeyBackspace2, tcell.KeyLeft:
		if w.step > stepAgents {
			w.step--
		}
		return actionNone
	case tcell.KeyEnter, tcell.KeyRight, tcell.KeyTab:
		if w.step == stepSummary {
			return actionSave
		}
		w.step++
		return actionNone
	}
	if ev.Key() == tcell.KeyRune && ev.Rune() == 'q' && w.step == stepSummary {
		return actionCancel
	}
	if l := w.current(); l != nil {
		l.HandleKey(ev)
	}
	return actionNone
}

func (w *Wizard) current() *ui.List {
	switch w.step {
	case stepAgents:
		return &w.agents
	case stepPause:
		return &w.pause
	}
	return nil
}

// result assembles the config from the pages' state on top of base, so
// fields the wizard doesn't ask about (dir/session scopes, webhook
// address) survive a re-run.
func (w *Wizard) result() config.Config {
	c := w.base
	var ids []agents.ID
	for _, v := range w.agents.Checked() {
		ids = append(ids, agents.ID(v))
	}
	c.SetAgents(ids)
	pause := first(w.pause.Checked(), "yes") == "yes"
	c.AutoPause = &pause
	return c
}

func first(vals []string, fallback string) string {
	if len(vals) == 0 {
		return fallback
	}
	return vals[0]
}

var stepTitles = [stepCount]string{
	"Which AI agents should terminalika listen to?",
	"Pause the game automatically when an agent event arrives?",
	"All set.",
}

func (w *Wizard) draw() {
	s := w.screen
	s.Clear()
	sw, sh := s.Size()

	// Frame: a centered panel, shrunk to a compact form on a small
	// terminal (a quarter of a laptop screen, say): the notes and the hint
	// column go first, then the blank spacer rows.
	pw := 84
	if pw > sw-2 {
		pw = sw - 2
	}
	inner := pw - 6
	items := 0
	if l := w.current(); l != nil {
		items = len(l.Items)
	} else {
		items = 3 // summary rows
	}
	compact := inner < 56 || sh < 16
	notes := 2
	if compact {
		notes = 0
	}
	spacer := 1
	// border(2) + title(1) + spacer + question(1) + spacer + items + notes + spacer + hint(1)
	need := func() int { return 2 + 1 + spacer + 1 + spacer + items + notes + spacer + 1 }
	if need() > sh {
		spacer = 0
	}
	ph := need()
	if ph > sh {
		ph = sh
	}
	px := (sw - pw) / 2
	py := (sh - ph) / 2
	if px < 0 {
		px = 0
	}
	if py < 0 {
		py = 0
	}
	ui.Box(s, px, py, pw, ph, ui.StyleDim)

	x := px + 3
	y := py + 1

	stepLabel := fmt.Sprintf("step %d of %d", int(w.step)+1, int(stepCount))
	title := "terminalika · first-run setup"
	if ui.Width(title)+ui.Width(stepLabel)+2 > inner {
		title = "terminalika setup"
	}
	if ui.Width(title)+ui.Width(stepLabel)+2 > inner {
		title = "setup"
	}
	ui.Print(s, x, y, ui.StyleTitle, title)
	ui.Print(s, px+pw-3-ui.Width(stepLabel), y, ui.StyleDim, stepLabel)
	y += 1 + spacer
	ui.Print(s, x, y, ui.StyleText.Bold(true), ui.Truncate(stepTitles[w.step], inner))
	y += 1 + spacer

	for _, l := range []*ui.List{&w.agents, &w.pause} {
		l.HideHints = compact
	}

	switch w.step {
	case stepAgents:
		y += w.agents.Draw(s, x, y, inner)
		if notes > 0 {
			y++
			ui.Print(s, x, y, ui.StyleDim, ui.Truncate("Select one or more - every selected agent is monitored at once.", inner))
			y++
			ui.Print(s, x, y, ui.StyleDim, ui.Truncate("Advanced (directory scopes, custom webhooks): "+DocsURL, inner))
		}
	case stepPause:
		y += w.pause.Draw(s, x, y, inner)
		if notes > 0 {
			y++
			ui.Print(s, x, y, ui.StyleDim, ui.Truncate("Paused: a notice in the middle of the board. Not paused: a banner in the corner.", inner))
		}
	case stepSummary:
		c := w.result()
		names := "none"
		if ids := c.AgentIDs(); len(ids) > 0 {
			names = ""
			for i, id := range ids {
				a, _ := agents.Lookup(string(id))
				if i > 0 {
					names += ", "
				}
				names += a.Name
			}
		}
		pause := "pause the game"
		if !c.PauseOnEvent() {
			pause = "banner only, keep playing"
		}
		rows := [][2]string{
			{"agents", names},
			{"on event", pause},
			{"saved to", config.Path()},
		}
		for _, r := range rows {
			ui.Print(s, x, y, ui.StyleDim, fmt.Sprintf("%-11s", r[0]))
			ui.Print(s, x+12, y, ui.StyleText, ui.Truncate(r[1], inner-12))
			y++
		}
		if w.saveErr != nil && notes > 0 {
			y++
			ui.Print(s, x, y, tcell.StyleDefault.Foreground(tcell.ColorRed), ui.Truncate("could not save: "+w.saveErr.Error(), inner))
		}
	}

	hint := "↑/↓ move   Space toggle   Enter next   Esc back"
	switch w.step {
	case stepAgents:
		hint = "↑/↓ move   Space toggle   Enter next   Esc quit"
	case stepSummary:
		hint = "Enter save & continue   Esc back   q quit without saving"
	}
	if compact {
		hint = strings.ReplaceAll(hint, "   ", "  ")
	}
	ui.Print(s, x, py+ph-2, ui.StyleDim, ui.Truncate(hint, inner))
	s.Show()
}
