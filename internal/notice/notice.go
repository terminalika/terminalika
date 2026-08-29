// Package notice renders a minimal one-off message screen that waits for a
// keypress, used to warn the player about their terminal before a game
// starts.
package notice

import "github.com/gdamore/tcell/v2"

// Show draws lines of text and blocks until the player presses a key. The
// first line is drawn as a title.
func Show(screen tcell.Screen, lines []string) {
	draw := func() {
		s := screen
		s.Clear()
		w, h := s.Size()

		titleStyle := tcell.StyleDefault.Foreground(tcell.ColorOrange).Bold(true)
		textStyle := tcell.StyleDefault.Foreground(tcell.ColorWhite)
		hintStyle := tcell.StyleDefault.Foreground(tcell.ColorGray)

		startY := h/2 - len(lines)/2 - 1
		for i, line := range lines {
			style := textStyle
			if i == 0 {
				style = titleStyle
			}
			emitCentered(s, w/2, startY+i, style, line)
		}

		emitCentered(s, w/2, h-2, hintStyle, "Press any key to continue")
		s.Show()
	}

	draw()
	for {
		ev := screen.PollEvent()
		if ev == nil {
			return
		}
		switch ev.(type) {
		case *tcell.EventKey:
			return
		case *tcell.EventResize:
			screen.Sync()
		}
		draw()
	}
}

func emitCentered(s tcell.Screen, centerX, y int, style tcell.Style, str string) {
	x := centerX - len(str)/2
	for _, r := range str {
		s.SetContent(x, y, r, nil, style)
		x++
	}
}
