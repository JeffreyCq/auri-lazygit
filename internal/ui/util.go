package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"auri-lazygit/internal/report"
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

// centerText centra s (puede tener ANSI) en un ancho w, rellenando con
// espacios a la izquierda. Si s ya ocupa w columnas o más, se devuelve tal cual.
func centerText(s string, w int) string {
	sw := lipgloss.Width(s)
	if sw >= w {
		return s
	}
	return strings.Repeat(" ", (w-sw)/2) + s
}

// indeterminateBar dibuja una barra de carga indeterminada: un bloque que
// rebota de un lado a otro dentro de un riel de ancho width. Se usa para
// esperas sin progreso medible, como la conexión al servidor MCP.
func indeterminateBar(frame, width int) string {
	if width < 10 {
		width = 10
	}
	block := width / 5
	if block < 3 {
		block = 3
	}
	track := width - block
	period := track * 2
	if period < 1 {
		period = 1
	}
	pos := frame % period
	if pos > track {
		pos = period - pos
	}
	accent := lipgloss.NewStyle().Foreground(report.ColAccent)
	dim := lipgloss.NewStyle().Foreground(report.ColDim)
	var b strings.Builder
	b.WriteString(dim.Render("["))
	for i := 0; i < width; i++ {
		if i >= pos && i < pos+block {
			b.WriteString(accent.Render("█"))
		} else {
			b.WriteString(dim.Render("·"))
		}
	}
	b.WriteString(dim.Render("]"))
	return b.String()
}

// mailAnimation dibuja un sobre viajando entre tu equipo y Endor Labs, para
// visualizar la comunicación en curso con el servidor MCP.
func mailAnimation(frame, width int) string {
	if width < 16 {
		width = 16
	}
	track := width - 6
	if track < 2 {
		track = 2
	}
	period := 2 * (track - 1)
	if period < 1 {
		period = 1
	}
	pos := frame % period
	if pos >= track {
		pos = period - pos
	}
	dim := lipgloss.NewStyle().Foreground(report.ColDim)
	cells := make([]string, track)
	for i := range cells {
		if i == pos {
			cells[i] = "✉"
		} else {
			cells[i] = dim.Render("·")
		}
	}
	return "💻 " + strings.Join(cells, "") + " ☁"
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
