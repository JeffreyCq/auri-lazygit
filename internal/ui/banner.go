package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"auri-lazygit/internal/report"
)

// auriFont is a tiny embedded 5×5 block font, just large enough to spell
// "AURI" for the menu screen's title banner. Each glyph is 5 columns wide;
// buildBanner joins glyphs with a single column of space between them, so
// the whole word stays compact enough to fit even an 80×24 terminal with
// room to spare.
var auriFont = map[byte][5]string{
	'A': {
		"  █  ",
		" █ █ ",
		"█████",
		"█   █",
		"█   █",
	},
	'U': {
		"█   █",
		"█   █",
		"█   █",
		"█   █",
		" ███ ",
	},
	'R': {
		"████ ",
		"█   █",
		"████ ",
		"█  █ ",
		"█   █",
	},
	'I': {
		"█████",
		"  █  ",
		"  █  ",
		"  █  ",
		"█████",
	},
}

// buildBanner spells word (letters must exist in auriFont) as 5 text rows
// of block characters.
func buildBanner(word string) []string {
	rows := make([]string, 5)
	for i := range rows {
		var b strings.Builder
		for j := 0; j < len(word); j++ {
			if j > 0 {
				b.WriteByte(' ')
			}
			if glyph, ok := auriFont[word[j]]; ok {
				b.WriteString(glyph[i])
			}
		}
		rows[i] = b.String()
	}
	return rows
}

// bannerColors fades from Endor Labs' brightest green (top) to its darkest
// (bottom), giving the banner a subtle lit-from-above glow instead of a
// flat block of color.
var bannerColors = []lipgloss.Color{report.ColAccent, report.ColOK, report.ColLow, report.ColLow, report.ColBrand}

// renderBanner renders the "AURI" title banner, indented to match the
// rest of the UI's two-space left margin.
func renderBanner() string {
	rows := buildBanner("AURI")
	var b strings.Builder
	for i, row := range rows {
		c := bannerColors[i%len(bannerColors)]
		b.WriteString("  " + lipgloss.NewStyle().Bold(true).Foreground(c).Render(row))
		if i < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
