package home

import "strings"

// glyphs is the title's pixel font: the lowercase letters of "terminalika"
// in Pixelify Sans (fonts.google.com/specimen/Pixelify+Sans), sampled
// straight from the font's own pixel grid - an ~11-cell em, 7 pixels from
// baseline to ascender, no descenders in this word. Widths vary per
// letter; '#' cells are painted, ' ' cells left transparent.
var glyphs = map[rune][]string{
	't': {
		" #    ",
		" #    ",
		"##### ",
		" #    ",
		" #    ",
		" #   #",
		"  ### ",
	},
	'e': {
		"     ",
		"     ",
		" ### ",
		"#   #",
		"###  ",
		"#   #",
		" ### ",
	},
	'r': {
		"     ",
		"     ",
		" ### ",
		"#   #",
		"#    ",
		"#    ",
		"#    ",
	},
	'm': {
		"       ",
		"       ",
		"### ## ",
		"#  #  #",
		"#  #  #",
		"#  #  #",
		"#  #  #",
	},
	'i': {
		"#",
		" ",
		"#",
		"#",
		"#",
		"#",
		"#",
	},
	'n': {
		"     ",
		"     ",
		"#### ",
		"#   #",
		"#   #",
		"#   #",
		"#   #",
	},
	'a': {
		"      ",
		"      ",
		" ###  ",
		"#   # ",
		"#   # ",
		"#   # ",
		" ### #",
	},
	'l': {
		"#",
		"#",
		"#",
		"#",
		"#",
		"#",
		"#",
	},
	'k': {
		"#    ",
		"#    ",
		"#   #",
		"# ## ",
		"##   ",
		"# ## ",
		"#   #",
	},
}

const (
	// glyphRows is the pixel height of every glyph.
	glyphRows = 7

	// glyphGap is the blank columns between letters (the font's own
	// sidebearings add up to about one cell).
	glyphGap = 1

	// titleRows is the terminal rows the title occupies: two pixel rows
	// per terminal cell (see titleCells), so the pixels come out roughly
	// square instead of twice as tall as they are wide.
	titleRows = (glyphRows + 1) / 2
)

// renderBig lays out word in the pixel font and returns its pixel rows.
// Letters without a glyph are skipped.
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

// bigWidth is the rendered width of word in the pixel font, in cells.
func bigWidth(word string) int {
	n, w := 0, 0
	for _, r := range word {
		if g, ok := glyphs[r]; ok {
			n++
			w += len(g[0])
		}
	}
	if n == 0 {
		return 0
	}
	return w + (n-1)*glyphGap
}

// titleCells turns the pixel rows into terminal rows, packing two pixel
// rows into each cell with the half-block characters: '▀' upper pixel
// only, '▄' lower only, '█' both, ' ' neither. An odd pixel height is
// padded with a blank row on top, so the baseline lands on a full cell
// edge.
func titleCells(word string) [][]rune {
	pix := renderBig(word)
	if len(pix)%2 == 1 {
		pix = append([]string{strings.Repeat(" ", len(pix[0]))}, pix...)
	}
	out := make([][]rune, 0, len(pix)/2)
	for i := 0; i < len(pix); i += 2 {
		top, bottom := pix[i], pix[i+1]
		row := make([]rune, len(top))
		for c := range top {
			up := top[c] == '#'
			down := c < len(bottom) && bottom[c] == '#'
			switch {
			case up && down:
				row[c] = '█'
			case up:
				row[c] = '▀'
			case down:
				row[c] = '▄'
			default:
				row[c] = ' '
			}
		}
		out = append(out, row)
	}
	return out
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
