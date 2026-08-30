package ui

import "github.com/gdamore/tcell/v2"

// Item is one row of a List.
type Item struct {
	Label   string
	Hint    string
	Checked bool

	// Value is an opaque identifier for the caller's use.
	Value string
}

// List is a keyboard-driven checkbox (or radio) list: arrows/j/k move the
// cursor, Space (or x) toggles the row under it.
type List struct {
	Items  []Item
	Cursor int

	// Single makes the list a radio group: toggling a row checks it and
	// unchecks the rest, and exactly one row is always checked.
	Single bool

	// HideHints drops the hint column (small terminals).
	HideHints bool
}

// Move shifts the cursor by delta, clamped to the list.
func (l *List) Move(delta int) {
	if len(l.Items) == 0 {
		return
	}
	l.Cursor += delta
	if l.Cursor < 0 {
		l.Cursor = 0
	}
	if l.Cursor >= len(l.Items) {
		l.Cursor = len(l.Items) - 1
	}
}

// Toggle flips the row under the cursor (or selects it for a radio list).
func (l *List) Toggle() {
	if len(l.Items) == 0 {
		return
	}
	if l.Single {
		for i := range l.Items {
			l.Items[i].Checked = i == l.Cursor
		}
		return
	}
	l.Items[l.Cursor].Checked = !l.Items[l.Cursor].Checked
}

// Checked returns the values of the checked rows, in list order.
func (l *List) Checked() []string {
	var out []string
	for _, it := range l.Items {
		if it.Checked {
			out = append(out, it.Value)
		}
	}
	return out
}

// HandleKey applies a navigation/toggle key and reports whether it was one.
func (l *List) HandleKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyUp:
		l.Move(-1)
	case tcell.KeyDown:
		l.Move(1)
	case tcell.KeyRune:
		switch ev.Rune() {
		case 'k':
			l.Move(-1)
		case 'j':
			l.Move(1)
		case ' ', 'x':
			l.Toggle()
		default:
			return false
		}
	default:
		return false
	}
	return true
}

// Draw renders the list with its top-left corner at (x, y) inside width w,
// and returns the number of rows used. Each row is "› [x] Label   hint",
// with the hints aligned in a column after the widest label.
func (l *List) Draw(s tcell.Screen, x, y, w int) int {
	labelW := 0
	for _, it := range l.Items {
		if n := Width(it.Label); n > labelW {
			labelW = n
		}
	}
	for i, it := range l.Items {
		row := y + i
		cursor := i == l.Cursor
		mark := "[ ]"
		if it.Checked {
			mark = "[x]"
			if l.Single {
				mark = "(•)"
			}
		} else if l.Single {
			mark = "( )"
		}

		labelStyle := StyleText
		if cursor {
			labelStyle = StyleSelected
		}
		markStyle := StyleDim
		if it.Checked {
			markStyle = StyleChecked
		}
		if cursor {
			markStyle = StyleSelected
		}

		xx := x
		if cursor {
			xx = Print(s, xx, row, StyleAccent, "› ")
		} else {
			xx = Print(s, xx, row, StyleDim, "  ")
		}
		xx = Print(s, xx, row, markStyle, mark)
		xx = Print(s, xx, row, labelStyle, " "+it.Label+" ")
		if it.Hint != "" && !l.HideHints {
			hx := x + 2 + 3 + 1 + labelW + 3
			room := x + w - hx
			if room > 4 {
				Print(s, hx, row, StyleDim, Truncate(it.Hint, room))
			}
		}
	}
	return len(l.Items)
}
