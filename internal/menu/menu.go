// Package menu renders the minimalist game selection screen.
package menu

import (
	"fmt"

	"github.com/gdamore/tcell/v2"

	"github.com/terminalika/terminalika/internal/keystate"
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
			// A key release reported by the terminal travels through as a
			// marked press (see keystate.Wrap) so it survives tcell's own
			// parser; the menu has no use for key state and must ignore it,
			// or every navigation key would fire twice per tap.
			if keystate.IsRelease(ev) {
				continue
			}
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

	titleStyle := tcell.StyleDefault.Foreground(tcell.ColorGreen).Bold(true)
	subtitleStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
	itemStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
	selectedStyle := tcell.StyleDefault.
		Foreground(tcell.ColorBlack).
		Background(tcell.ColorAqua)

	drawLogo(s, w/2-logoWidth/2, h/2-8)
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

// logoCellWidth/logoSize/logoWidth describe the terminalika logo: a 3x3
// snake board -
//
//	[0 0 g]
//	[r 0 g]
//	[0 H g]
//
// g = snake body (green), H = snake head (lime, matches games/snake/draw.go
// in terminalika-core), r = food (red), 0 = empty - the same board as the
// website's logo.svg/favicon.svg. Each logical pixel is drawn two columns
// wide so it reads roughly square in a terminal cell grid.
const (
	logoCellWidth = 2
	logoSize      = 3
	logoWidth     = logoSize * logoCellWidth
)

type logoPixel struct {
	x, y  int
	color tcell.Color
}

var logoPixels = []logoPixel{
	{2, 0, tcell.ColorGreen},
	{0, 1, tcell.ColorRed},
	{2, 1, tcell.ColorGreen},
	{1, 2, tcell.ColorLime},
	{2, 2, tcell.ColorGreen},
}

// drawLogo draws the terminalika logo with its top-left corner at (x, y).
func drawLogo(s tcell.Screen, x, y int) {
	for _, p := range logoPixels {
		style := tcell.StyleDefault.Background(p.color)
		px, py := x+p.x*logoCellWidth, y+p.y
		for i := 0; i < logoCellWidth; i++ {
			s.SetContent(px+i, py, ' ', nil, style)
		}
	}
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
