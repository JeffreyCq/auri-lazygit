package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// trunc recorta s (puede tener ANSI) a w columnas.
func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// pad rellena s con espacios hasta w columnas (o lo recorta).
func pad(s string, w int) string {
	s = trunc(s, w)
	if d := w - lipgloss.Width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// wrap ajusta texto plano a w columnas.
func wrap(s string, w int) string {
	if w < 10 {
		w = 10
	}
	var result []string
	words := strings.Fields(s)
	var line string
	for _, word := range words {
		if lipgloss.Width(line)+lipgloss.Width(word)+1 > w {
			if line != "" {
				result = append(result, line)
				line = word
			} else {
				result = append(result, word)
			}
		} else {
			if line != "" {
				line += " "
			}
			line += word
		}
	}
	if line != "" {
		result = append(result, line)
	}
	return strings.Join(result, "\n")
}

// fmtElapsed formatea un tiempo transcurrido, derivado del contador de ticks
// del spinner (150ms) de la pantalla de escaneo, como "12s" o "1m05s", para
// escaneos (como el motor de secretos) que no tienen un conteo de progreso
// por ítem que mostrar en su lugar.
func fmtElapsed(spinnerFrame int) string {
	secs := spinnerFrame * 150 / 1000
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	return fmt.Sprintf("%dm%02ds", secs/60, secs%60)
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		hi = lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
