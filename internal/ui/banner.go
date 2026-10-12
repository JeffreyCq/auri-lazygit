package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"auri-lazygit/internal/report"
)

// auriFont es una pequeña fuente de bloques 5×5 embebida, justo lo
// suficiente para escribir "AURI" en el banner de la pantalla de menú. Cada
// glifo mide 5 columnas de ancho; buildBanner une los glifos con una sola
// columna de espacio entre ellos, de modo que la palabra entera quede lo
// bastante compacta como para caber incluso en una terminal de 80×24 con
// espacio de sobra.
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

// buildBanner escribe word (sus letras deben existir en auriFont) como 5
// filas de texto hechas de caracteres de bloque.
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

// bannerColors se degrada desde el verde más brillante de Endor Labs
// (arriba) hasta el más oscuro (abajo), dándole al banner un sutil brillo
// "iluminado desde arriba" en vez de un bloque de color plano.
var bannerColors = []lipgloss.Color{report.ColAccent, report.ColOK, report.ColLow, report.ColLow, report.ColBrand}

// renderBanner dibuja el banner del título "AURI", con la misma sangría de
// dos espacios que usa el resto de la UI como margen izquierdo.
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
