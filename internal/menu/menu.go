// Package menu renders the minimalist game selection screen.
package menu

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// Menu lists the games registered in terminalika-core.
type Menu struct {
	screen   tcell.Screen
	games    []string
	selected int
}

// New creates a menu for the given game names.
func New(screen tcell.Screen, games []string) *Menu {
	return &Menu{screen: screen, games: games}
}

// Run blocks until the user selects a game or quits. It returns the selected
// game name and true on selection, or "" and false when the user quits.
func (m *Menu) Run() (string, bool) {
	if len(m.games) == 0 {
		return "", false
	}

	m.selected = 0
	m.draw()

	for {
		ev := m.screen.PollEvent()
		if ev == nil {
			return "", false
		}

		switch ev := ev.(type) {
		case *tcell.EventKey:
			switch ev.Key() {
			case tcell.KeyEscape, tcell.KeyCtrlC:
				return "", false
			case tcell.KeyUp:
				m.move(-1)
			case tcell.KeyDown:
				m.move(1)
			case tcell.KeyEnter:
				return m.games[m.selected], true
			case tcell.KeyRune:
				switch ev.Rune() {
				case 'q', 'Q':
					return "", false
				case 'j':
					m.move(1)
				case 'k':
					m.move(-1)
				}
			}
		case *tcell.EventResize:
			m.screen.Sync()
		}

		m.draw()
	}
}

func (m *Menu) move(delta int) {
	m.selected = (m.selected + delta + len(m.games)) % len(m.games)
}

func (m *Menu) draw() {
	s := m.screen
	s.Clear()

	w, h := s.Size()

	titleStyle := tcell.StyleDefault.Foreground(tcell.ColorAqua).Bold(true)
	subtitleStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	itemStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
	selectedStyle := tcell.StyleDefault.
		Foreground(tcell.ColorBlack).
		Background(tcell.ColorAqua)

	emitCentered(s, w/2, h/2-4, titleStyle, "TERMINALIKA")
	emitCentered(s, w/2, h/2-3, subtitleStyle, "select a game")

	startY := h/2 - 1
	for i, name := range m.games {
		y := startY + i*2
		if i == m.selected {
			emitCentered(s, w/2, y, selectedStyle, fmt.Sprintf("> %s <", name))
		} else {
			emitCentered(s, w/2, y, itemStyle, fmt.Sprintf("  %s  ", name))
		}
	}

	emitCentered(s, w/2, h-2, subtitleStyle, "Arrows: navigate  Enter: play  Esc/Q: quit")

	s.Show()
}

func emitCentered(s tcell.Screen, centerX, y int, style tcell.Style, str string) {
	emitStr(s, centerX-len(str)/2, y, style, str)
}

func emitStr(s tcell.Screen, x, y int, style tcell.Style, str string) {
	for _, r := range str {
		s.SetContent(x, y, r, nil, style)
		x++
	}
}
