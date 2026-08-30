package home

import "strings"

// glyphs is a 5-row block font covering the letters of "TERMINALIKA".
// '#' cells are painted, ' ' cells left transparent.
var glyphs = map[rune][]string{
	'T': {"#####", "  #  ", "  #  ", "  #  ", "  #  "},
	'E': {"#####", "#    ", "#### ", "#    ", "#####"},
	'R': {"#### ", "#   #", "#### ", "#  # ", "#   #"},
	'M': {"#   #", "## ##", "# # #", "#   #", "#   #"},
	'I': {"#####", "  #  ", "  #  ", "  #  ", "#####"},
	'N': {"#   #", "##  #", "# # #", "#  ##", "#   #"},
	'A': {" ### ", "#   #", "#####", "#   #", "#   #"},
	'L': {"#    ", "#    ", "#    ", "#    ", "#####"},
	'K': {"#   #", "#  # ", "###  ", "#  # ", "#   #"},
}

const (
	glyphRows = 5
	glyphCols = 5
	glyphGap  = 1
)

// renderBig lays out word in the block font and returns its rows. Letters
// without a glyph are skipped.
func renderBig(word string) []string {
	rows := make([]string, glyphRows)
	first := true
	for _, r := range word {
		g, ok := glyphs[r]
		if !ok {
			continue
		}
		for i := 0; i < glyphRows; i++ {
			if !first {
				rows[i] += strings.Repeat(" ", glyphGap)
			}
			rows[i] += g[i]
		}
		first = false
	}
	return rows
}

// bigWidth is the rendered width of word in the block font.
func bigWidth(word string) int {
	n := 0
	for _, r := range word {
		if _, ok := glyphs[r]; ok {
			n++
		}
	}
	if n == 0 {
		return 0
	}
	return n*glyphCols + (n-1)*glyphGap
}

// previews are the little ASCII thumbnails shown on the explore cards,
// each previewRows tall and at most previewCols wide.
const (
	previewRows = 5
	previewCols = 14
)

var previews = map[string][]string{
	"snake": {
		"              ",
		"  ●●●●●●▶     ",
		"          ◆   ",
		"      ●●●     ",
		"              ",
	},
	"tetris": {
		"     ▟█▙      ",
		"              ",
		"  ██  ▄▄  ▐▌  ",
		"  ██▄▄██▄▄▐▌▄ ",
		"  ████████████",
	},
	"invaders": {
		"  ▙▟  ▙▟  ▙▟  ",
		"   ▙▟  ▙▟  ▙▟ ",
		"              ",
		"       |      ",
		"      ▟█▙     ",
	},
	"pong": {
		" ▌            ",
		" ▌     •    ▐ ",
		" ▌          ▐ ",
		"            ▐ ",
		"  3  ·····  1 ",
	},
}

// previewFor returns a game's thumbnail, or a generic one.
func previewFor(name string) []string {
	if p, ok := previews[name]; ok {
		return p
	}
	return []string{
		"              ",
		"   ╭──────╮   ",
		"   │ play │   ",
		"   ╰──────╯   ",
		"              ",
	}
}
