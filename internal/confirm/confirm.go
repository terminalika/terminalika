// Package confirm renders a minimal Yes/No confirmation screen, used to ask
// the player a one-off question before a game starts.
package confirm

import "github.com/gdamore/tcell/v2"

// Ask draws lines of text above a Yes/No choice and blocks until the player
// answers. Left/Right/Tab move the selection, Enter confirms it, Y/N answer
// directly, and Esc/Ctrl+C decline.
func Ask(screen tcell.Screen, lines []string) bool {
	yes := true

	draw := func() {
		s := screen
		s.Clear()
		w, h := s.Size()

		textStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
		hintStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)
		selectedStyle := tcell.StyleDefault.
			Foreground(tcell.ColorBlack).
			Background(tcell.ColorAqua)

		startY := h/2 - len(lines)/2 - 2
		for i, line := range lines {
			emitCentered(s, w/2, startY+i, textStyle, line)
		}

		optY := startY + len(lines) + 2
		if yes {
			emitCentered(s, w/2-6, optY, selectedStyle, " Yes ")
			emitCentered(s, w/2+6, optY, textStyle, " No ")
		} else {
			emitCentered(s, w/2-6, optY, textStyle, " Yes ")
			emitCentered(s, w/2+6, optY, selectedStyle, " No ")
		}

		emitCentered(s, w/2, h-2, hintStyle, "Left/Right: choose  Enter: confirm  Esc: no")
		s.Show()
	}

	draw()
	for {
		ev := screen.PollEvent()
		if ev == nil {
			return false
		}
		switch ev := ev.(type) {
		case *tcell.EventKey:
			switch ev.Key() {
			case tcell.KeyEscape, tcell.KeyCtrlC:
				return false
			case tcell.KeyLeft, tcell.KeyRight, tcell.KeyTab:
				yes = !yes
			case tcell.KeyEnter:
				return yes
			case tcell.KeyRune:
				switch ev.Rune() {
				case 'y', 'Y':
					return true
				case 'n', 'N':
					return false
				}
			}
		case *tcell.EventResize:
			screen.Sync()
		}
		draw()
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
