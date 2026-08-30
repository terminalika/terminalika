package home

import (
	"strings"
	"testing"
)

func TestTitleFontCoversTheWordInLowercase(t *testing.T) {
	for _, r := range titleWord {
		g, ok := glyphs[r]
		if !ok {
			t.Fatalf("no glyph for %q", r)
		}
		if len(g) != glyphRows {
			t.Errorf("%q: %d rows, want %d", r, len(g), glyphRows)
		}
		for i, row := range g {
			if len(row) != len(g[0]) {
				t.Errorf("%q row %d: width %d, want %d", r, i, len(row), len(g[0]))
			}
		}
		if r >= 'A' && r <= 'Z' {
			t.Errorf("uppercase %q in the title", r)
		}
	}
	if _, ok := glyphs['T']; ok {
		t.Error("the font must not carry uppercase glyphs")
	}
}

func TestBigWidthSumsVariableWidths(t *testing.T) {
	// i and l are one pixel wide, m seven: a fixed-width count would be
	// wrong either way.
	if got := bigWidth("il"); got != 1+glyphGap+1 {
		t.Errorf("bigWidth(il) = %d", got)
	}
	if got := bigWidth("m"); got != 7 {
		t.Errorf("bigWidth(m) = %d", got)
	}
	if got, rows := bigWidth(titleWord), renderBig(titleWord); got != len(rows[0]) {
		t.Errorf("bigWidth = %d but rendered rows are %d wide", got, len(rows[0]))
	}
}

func TestTitleCellsPacksTwoPixelRowsPerCell(t *testing.T) {
	cells := titleCells("i")
	if len(cells) != titleRows {
		t.Fatalf("%d cells rows, want %d", len(cells), titleRows)
	}
	// i: dot on pixel row 0, gap on row 1, stem rows 2-6. With a blank
	// pad row on top: (pad,dot)=▄ (gap,stem)=▄ (stem,stem)=█ (stem,stem)=█.
	got := ""
	for _, row := range cells {
		got += string(row)
	}
	if got != "▄▄██" {
		t.Errorf("titleCells(i) = %q, want ▄▄██", got)
	}

	// Nothing but the four block runes ever comes out.
	for _, row := range titleCells(titleWord) {
		if strings.Trim(string(row), " ▀▄█") != "" {
			t.Errorf("unexpected rune in %q", string(row))
		}
	}
}
