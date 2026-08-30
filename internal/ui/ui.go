// Package ui holds the small drawing helpers and widgets shared by the
// launcher's own screens (setup wizard, home screen): text, boxes, and a
// keyboard-driven checkbox list.
package ui

import (
	"github.com/gdamore/tcell/v2"
)

// Brand colors. Aqua is terminalika's own system color (see engine and
// notice), green/lime the snake logo's, gray for hints.
var (
	Accent = tcell.ColorAqua
	Green  = tcell.ColorGreen
	Lime   = tcell.ColorLime
	Dim    = tcell.ColorGray
	Text   = tcell.ColorWhite
)

// Styles used across screens.
var (
	StyleText     = tcell.StyleDefault.Foreground(Text)
	StyleDim      = tcell.StyleDefault.Foreground(Dim)
	StyleAccent   = tcell.StyleDefault.Foreground(Accent)
	StyleTitle    = tcell.StyleDefault.Foreground(Accent).Bold(true)
	StyleSelected = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(Accent).Bold(true)
	StyleChecked  = tcell.StyleDefault.Foreground(Lime).Bold(true)
)

// Print writes str at (x, y) and returns the x after the last rune.
func Print(s tcell.Screen, x, y int, style tcell.Style, str string) int {
	for _, r := range str {
		s.SetContent(x, y, r, nil, style)
		x++
	}
	return x
}

// Width is the number of cells str occupies (one per rune; the launcher's
// own text is ASCII and single-width symbols).
func Width(str string) int {
	n := 0
	for range str {
		n++
	}
	return n
}

// PrintCentered writes str centered on centerX.
func PrintCentered(s tcell.Screen, centerX, y int, style tcell.Style, str string) {
	Print(s, centerX-Width(str)/2, y, style, str)
}

// Truncate cuts str to at most w cells, ending in "…" when cut.
func Truncate(str string, w int) string {
	if w <= 0 {
		return ""
	}
	runes := []rune(str)
	if len(runes) <= w {
		return str
	}
	if w == 1 {
		return "…"
	}
	return string(runes[:w-1]) + "…"
}

// Fill paints a rectangle with a style (spaces).
func Fill(s tcell.Screen, x, y, w, h int, style tcell.Style) {
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			s.SetContent(xx, yy, ' ', nil, style)
		}
	}
}

// Box draws a rounded border around the rectangle (x, y, w, h).
func Box(s tcell.Screen, x, y, w, h int, style tcell.Style) {
	if w < 2 || h < 2 {
		return
	}
	s.SetContent(x, y, '╭', nil, style)
	s.SetContent(x+w-1, y, '╮', nil, style)
	s.SetContent(x, y+h-1, '╰', nil, style)
	s.SetContent(x+w-1, y+h-1, '╯', nil, style)
	for xx := x + 1; xx < x+w-1; xx++ {
		s.SetContent(xx, y, '─', nil, style)
		s.SetContent(xx, y+h-1, '─', nil, style)
	}
	for yy := y + 1; yy < y+h-1; yy++ {
		s.SetContent(x, yy, '│', nil, style)
		s.SetContent(x+w-1, yy, '│', nil, style)
	}
}

// HLine draws a horizontal rule.
func HLine(s tcell.Screen, x, y, w int, style tcell.Style) {
	for xx := x; xx < x+w; xx++ {
		s.SetContent(xx, y, '─', nil, style)
	}
}
