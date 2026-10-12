// Package report dibuja gráficos con caracteres de bloque para la terminal:
// barras por severidad, barra apilada, desglose por categoría, medidor de riesgo
// y sparklines de tendencia. Todo usa solo texto + colores ANSI (Lip Gloss), de
// modo que se degrada bien en terminales sin color.
package report

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"auri-lazygit/internal/findings"
)

// Paleta por severidad. Critical/High/Medium se mantienen en rojo/naranja/
// amarillo (semántica de severidad, no debe "verdearse"). Low, OK y los
// acentos neutros usan el verde de marca de Endor Labs (tomado de
// endorlabs.com: #31FF94 "spring", #00F078 "vivid", #26D07C "medium",
// #009F50 "dark"), para que la paleta se sienta on-brand sin perder la
// escala roja→verde que comunica riesgo.
var (
	ColCritical = lipgloss.Color("196")
	ColHigh     = lipgloss.Color("208")
	ColMedium   = lipgloss.Color("220")
	ColLow      = lipgloss.Color("#26D07C") // Endor green (medium)
	ColOK       = lipgloss.Color("#00F078") // Endor green (vivid)
	ColAccent   = lipgloss.Color("#31FF94") // Endor green (spring) — UI accents/highlights
	ColBrand    = lipgloss.Color("#009F50") // Endor green (dark) — high-contrast backgrounds
	ColDim      = lipgloss.Color("240")
	ColText     = lipgloss.Color("252")
)

// SevColor devuelve el color de una severidad.
func SevColor(s findings.Severity) lipgloss.Color {
	switch s {
	case findings.SevCritical:
		return ColCritical
	case findings.SevHigh:
		return ColHigh
	case findings.SevMedium:
		return ColMedium
	default:
		return ColLow
	}
}

// SevBadge devuelve una etiqueta coloreada tipo " CRIT ".
func SevBadge(s findings.Severity) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(SevColor(s)).
		Render(fmt.Sprintf(" %-4s ", s.Short()))
}

func fg(c lipgloss.Color, s string) string { return lipgloss.NewStyle().Foreground(c).Render(s) }

// eighths son los bloques parciales de 1/8 a 7/8 para barras de resolución fina.
var eighths = []rune{' ', '▏', '▎', '▍', '▌', '▋', '▊', '▉'}

// bar dibuja una barra horizontal de ancho w (en celdas) que representa val/max,
// con resolución de 1/8 de celda. Un valor > 0 siempre se ve (mínimo 1/8).
func bar(val, max, w int) (full int, part rune, empty int) {
	if w < 1 {
		w = 1
	}
	if max <= 0 || val <= 0 {
		return 0, 0, w
	}
	units := int(math.Round(float64(val) / float64(max) * float64(w) * 8))
	if units < 1 {
		units = 1
	}
	if units > w*8 {
		units = w * 8
	}
	full = units / 8
	if r := units % 8; r > 0 {
		part = eighths[r]
	}
	empty = w - full
	if part != 0 {
		empty--
	}
	return
}

func renderBar(val, max, w int, c lipgloss.Color) string {
	full, part, empty := bar(val, max, w)
	s := strings.Repeat("█", full)
	if part != 0 {
		s += string(part)
	}
	return fg(c, s) + fg(ColDim, strings.Repeat("░", empty))
}

// SeverityBars dibuja una barra por severidad con conteo y porcentaje:
//
//	CRIT  ████████████████░░░░░░░░   4  31%
func SeverityBars(c findings.Counts, width int) string {
	if width < 8 {
		width = 8
	}
	max := 0
	for _, n := range []int{c.Critical, c.High, c.Medium, c.Low} {
		if n > max {
			max = n
		}
	}
	tot := c.Total()
	rows := []struct {
		s findings.Severity
		n int
	}{
		{findings.SevCritical, c.Critical}, {findings.SevHigh, c.High},
		{findings.SevMedium, c.Medium}, {findings.SevLow, c.Low},
	}
	var b strings.Builder
	for i, r := range rows {
		pct := 0
		if tot > 0 {
			pct = int(math.Round(float64(r.n) * 100 / float64(tot)))
		}
		lbl := lipgloss.NewStyle().Bold(true).Foreground(SevColor(r.s)).Render(fmt.Sprintf("%-4s", r.s.Short()))
		b.WriteString(fmt.Sprintf("%s %s %s %s", lbl, renderBar(r.n, max, width, SevColor(r.s)),
			lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%3d", r.n)), fg(ColDim, fmt.Sprintf("%3d%%", pct))))
		if i < len(rows)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// StackedBar dibuja una sola barra con segmentos proporcionales por severidad.
func StackedBar(c findings.Counts, width int) string {
	tot := c.Total()
	if width < 4 {
		width = 4
	}
	if tot == 0 {
		return fg(ColOK, strings.Repeat("█", width))
	}
	vals := []struct {
		n   int
		col lipgloss.Color
	}{{c.Critical, ColCritical}, {c.High, ColHigh}, {c.Medium, ColMedium}, {c.Low, ColLow}}

	// método del mayor resto: la suma de celdas es exactamente width y todo valor > 0 ocupa ≥ 1 celda
	cells := make([]int, len(vals))
	type rem struct {
		i int
		r float64
	}
	var rems []rem
	used := 0
	for i, v := range vals {
		exact := float64(v.n) * float64(width) / float64(tot)
		cells[i] = int(exact)
		if v.n > 0 && cells[i] == 0 {
			cells[i] = 1
		}
		used += cells[i]
		rems = append(rems, rem{i, exact - math.Floor(exact)})
	}
	for used < width {
		best := -1
		for k, r := range rems {
			if vals[r.i].n > 0 && (best < 0 || r.r > rems[best].r) {
				best = k
			}
		}
		cells[rems[best].i]++
		rems[best].r = -1
		used++
	}
	for used > width {
		big := 0
		for i := range cells {
			if cells[i] > cells[big] {
				big = i
			}
		}
		cells[big]--
		used--
	}
	var b strings.Builder
	for i, v := range vals {
		b.WriteString(fg(v.col, strings.Repeat("█", cells[i])))
	}
	return b.String()
}

// Legend devuelve la leyenda de colores de la barra apilada.
func Legend() string {
	return fg(ColCritical, "■") + " critical  " + fg(ColHigh, "■") + " high  " +
		fg(ColMedium, "■") + " medium  " + fg(ColLow, "■") + " low"
}

// CategoryBars dibuja el desglose por categoría (SCA, Secrets, SAST, Malware, Other).
func CategoryBars(fs []findings.Finding, width int) string {
	m := findings.ByCategory(fs)
	max := 0
	for _, n := range m {
		if n > max {
			max = n
		}
	}
	icons := map[findings.Category]string{
		findings.CatSCA: "📦", findings.CatSecrets: "🔑", findings.CatSAST: "🧬",
		findings.CatMalware: "☠ ", findings.CatOther: "• ",
	}
	var lines []string
	for _, cat := range findings.Categories {
		n := m[cat]
		if n == 0 {
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %-8s %s %s", icons[cat], string(cat),
			renderBar(n, max, width, ColAccent), lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d", n))))
	}
	if len(lines) == 0 {
		return fg(ColDim, "(no findings)")
	}
	return strings.Join(lines, "\n")
}

// RiskLevel traduce el índice 0..100 a etiqueta y color.
func RiskLevel(score int) (string, lipgloss.Color) {
	switch {
	case score == 0:
		return "CLEAN", ColOK
	case score < 25:
		return "LOW", ColLow
	case score < 50:
		return "MODERATE", ColMedium
	case score < 75:
		return "HIGH", ColHigh
	default:
		return "CRITICAL", ColCritical
	}
}

// RiskMeter dibuja un medidor con degradado verde→rojo y un marcador:
//
//	RIESGO  ███████████▌░░░░░░░░░░░  64/100 ALTO
//
// El índice es una heurística local (crit×10 + alta×5 + media×2 + baja), no un score de Endor Labs.
func RiskMeter(score, width int) string {
	if width < 10 {
		width = 10
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	filled := int(math.Round(float64(score) / 100 * float64(width)))
	if score > 0 && filled == 0 {
		filled = 1
	}
	var b strings.Builder
	for i := 0; i < width; i++ {
		if i < filled {
			// el color depende de la posición, no del valor: el degradado es fijo
			pos := float64(i) / float64(width-1)
			b.WriteString(fg(gradient(pos), "█"))
		} else {
			b.WriteString(fg(ColDim, "░"))
		}
	}
	lbl, col := RiskLevel(score)
	return fmt.Sprintf("%s  %s  %s", b.String(),
		lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d/100", score)),
		lipgloss.NewStyle().Bold(true).Foreground(col).Render(lbl))
}

// gradient devuelve verde→amarillo→naranja→rojo para pos en [0,1]. El
// extremo "seguro" (pos≈0) usa el verde de marca de Endor Labs.
func gradient(pos float64) lipgloss.Color {
	stops := []lipgloss.Color{ColOK, "148", "220", "208", "196"}
	i := int(pos * float64(len(stops)))
	if i >= len(stops) {
		i = len(stops) - 1
	}
	return stops[i]
}

// Summary arma el panel completo de resumen (barra apilada + severidades + categorías + riesgo).
func Summary(fs []findings.Finding, width int) string {
	c := findings.Summarize(fs)
	if width < 30 {
		width = 30
	}
	barW := width - 18
	if barW > 40 {
		barW = 40
	}
	if barW < 8 {
		barW = 8
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d findings", c.Total())))
	b.WriteString("\n")
	b.WriteString(StackedBar(c, barW+12))
	b.WriteString("\n")
	b.WriteString(Legend())
	b.WriteString("\n\n")
	b.WriteString(SeverityBars(c, barW))
	b.WriteString("\n\n")
	b.WriteString(CategoryBars(fs, barW))
	b.WriteString("\n\n")
	b.WriteString("RISK " + RiskMeter(c.Risk(), barW))
	return b.String()
}

// Gauge dibuja una barra para una fracción 0..1 (p. ej. EPSS) con el color dado.
func Gauge(frac float64, width int, c lipgloss.Color) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	return renderBar(int(math.Round(frac*1000)), 1000, width, c)
}
