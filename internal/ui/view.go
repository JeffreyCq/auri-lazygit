package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"auri-tui/internal/fix"
	"auri-tui/internal/report"
)

// Estilos de la UI. sBrand, sAccent y sOK referencian la paleta de marca de
// Endor Labs definida en internal/report (verde), para que la pantalla de
// menú/estado y los paneles de gráficos se vean consistentes. sDim, sBold,
// sErr, sWarn y sSelBg son neutros/semánticos (error=rojo, warn=amarillo) y
// no deben "verdearse".
var (
	sDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	sBold   = lipgloss.NewStyle().Bold(true)
	sBrand  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(report.ColBrand).Padding(0, 1)
	sAccent = lipgloss.NewStyle().Foreground(report.ColAccent).Bold(true)
	sOK     = lipgloss.NewStyle().Foreground(report.ColOK).Bold(true)
	sErr    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	sWarn   = lipgloss.NewStyle().Foreground(lipgloss.Color("226"))
	sSelBg  = lipgloss.NewStyle().Background(lipgloss.Color("#10301F")) // dark muted green, on-brand selection highlight
)

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// View implementa tea.Model.
func (m Model) View() string {
	w, h := m.w, m.h
	if w < 40 || h < 12 {
		return "Terminal is too small (minimum 40×12). Make it larger to use auri-tui.\n"
	}
	bodyH := h - 5 // header + rule + repoStatus + rule + foot
	var body, foot string
	switch m.screen {
	case scMenu:
		body, foot = m.viewMenu(w, bodyH)
	case scCommitPick:
		body, foot = m.viewCommitPick(w, bodyH)
	case scFolderPick:
		body, foot = m.viewFolderPick(w, bodyH)
	case scScanning:
		body, foot = m.viewScanning(w, bodyH)
	case scScanResults:
		body, foot = m.viewScanResults(w, bodyH)
	case scFixReview:
		body, foot = m.viewFixReview(w, bodyH)
	case scMCPTools:
		body, foot = m.viewMCPTools(w, bodyH)
	case scToolInput:
		body, foot = m.viewToolInput(w, bodyH)
	case scEditParam:
		body, foot = m.viewEditParam(w, bodyH)
	case scRawToolResult:
		body, foot = m.viewRawToolResult(w, bodyH)
	}
	lines := strings.Split(body, "\n")
	for len(lines) < bodyH {
		lines = append(lines, "")
	}
	if len(lines) > bodyH {
		lines = lines[:bodyH]
	}
	rule := sDim.Render(strings.Repeat("─", w))
	top := m.header(w) + "\n" + rule + "\n" + m.repoStatus(w) + "\n" + rule
	return top + "\n" + strings.Join(lines, "\n") + "\n" + sDim.Render(trunc(foot, w))
}

func (m Model) header(w int) string {
	s := sBrand.Render("auri-tui") + " " + sDim.Render("AURI — Endor Labs MCP")
	return trunc(s, w)
}

// repoStatus muestra en qué carpeta está trabajando auri-tui ahora mismo y,
// si es un repositorio git, en qué rama — igual que el prompt de una shell,
// para que nunca haya duda de qué se va a escanear.
func (m Model) repoStatus(w int) string {
	var left string
	if m.gitOK {
		left = lipgloss.NewStyle().Foreground(report.ColAccent).Bold(true).Render("⎇ " + m.branch)
	} else {
		left = sWarn.Render("⚠ not a git repository")
	}
	rightW := w - lipgloss.Width(left) - 5
	right := sDim.Render("📂 " + trunc(m.cwd, rightW))
	return "  " + left + "   " + right
}

// connectingPanel dibuja el panel de "conectando al servidor MCP": el
// spinner con el mensaje, el sobre viajando hacia Endor Labs, y una barra de
// carga indeterminada debajo — se usa tanto en el menú como en el navegador
// de herramientas MCP mientras no hay progreso medible que mostrar todavía.
func connectingPanel(frame, w int) string {
	var b strings.Builder
	spin := spinnerFrames[frame%len(spinnerFrames)]
	b.WriteString("  " + sAccent.Render(string(spin)+" Connecting to the Endor Labs MCP server...") + "\n\n")

	animW := w - 30
	if animW > 40 {
		animW = 40
	}
	if animW < 10 {
		animW = 10
	}
	b.WriteString("  " + sDim.Render("you") + "  " + mailAnimation(frame, animW) + "  " + sDim.Render("Endor Labs") + "\n\n")

	barW := w - 6
	if barW > 40 {
		barW = 40
	}
	b.WriteString("  " + indeterminateBar(frame, barW) + "\n")
	return b.String()
}

// ---------- menú de objetivo ----------

func (m Model) viewMenu(w, h int) (string, string) {
	var b strings.Builder

	// El banner grande + panel con borde solo se muestran si hay espacio de
	// sobra; en terminales pequeñas (hasta el mínimo soportado de 40×12) se
	// cae a una lista plana sin decoración, para que los ítems del menú
	// —lo único realmente funcional de esta pantalla— nunca queden
	// recortados fuera de vista por el adorno.
	full := h >= 24 && w >= 60

	if full {
		b.WriteString("\n" + renderBanner(w) + "\n\n")
	} else {
		b.WriteString("\n")
	}
	b.WriteString("  " + sAccent.Render("What do you want to scan?") + "\n\n")

	// boxW es el ancho interior del panel cuando full=true (w menos el borde
	// y el padding horizontal); en modo compacto se usa para el truncado del
	// texto secundario nada más, sin reservar espacio de borde.
	boxW := w - 6
	if !full {
		boxW = w - 2
	}
	if boxW < 24 {
		boxW = 24
	}
	var items strings.Builder
	for i, it := range m.items {
		cur, title := "  ", sBold.Render(it.title)
		if i == m.cursor {
			cur = sAccent.Render("▸ ")
			title = sAccent.Render(it.title)
		}
		subW := boxW - 30
		if subW < 8 {
			subW = 8
		}
		line := fmt.Sprintf("%s %s%s", it.icon, cur, pad(title, 20)) + sDim.Render(trunc(it.sub, subW))
		if !full {
			line = "  " + line
		}
		// Se rellena a boxW (o w en modo compacto) nosotros mismos — pad()
		// ya se usa en todo el resto de la UI y mide el ancho visible
		// correctamente — en vez de pasarle Width() al estilo del borde,
		// que reenvuelve cada línea con su propia lógica de word-wrap y
		// puede desalinearse por un par de columnas frente a nuestro propio
		// truncado, partiendo la línea en dos.
		if full {
			line = pad(line, boxW)
		} else {
			line = pad(line, w)
		}
		if i == m.cursor {
			line = sSelBg.Render(line)
		}
		items.WriteString(line)
		if i < len(m.items)-1 {
			items.WriteByte('\n')
		}
	}
	if full {
		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(report.ColLow).
			Padding(0, 2).
			Render(items.String())
		b.WriteString(box + "\n")
	} else {
		b.WriteString(items.String() + "\n")
	}

	if !m.gitOK {
		b.WriteString("\n  " + sWarn.Render("No git repository detected in this folder — pick Open Folder to choose one, or run Full Repository / Browse MCP Tools anyway.") + "\n")
	}
	if m.status != "" {
		b.WriteString("\n  " + sWarn.Render(m.status) + "\n")
	}
	if m.mcpErr != nil {
		b.WriteString("\n  " + sErr.Render("MCP connection error: "+m.mcpErr.Error()) + "\n")
		b.WriteString("  " + sDim.Render("Make sure npx and endorctl are installed and you're logged in (endorctl init).") + "\n")
	}
	if m.mcpConnecting {
		b.WriteString("\n" + connectingPanel(m.spinnerFrame, w) + "\n")
	}

	b.WriteString("\n" + sDim.Render("  This is a manual pre-push check: scans run only when you ask, never automatically.") + "\n")

	foot := " ↑↓ move · enter select · q quit"
	return b.String(), foot
}

// ---------- selector de commit ----------

func (m Model) viewCommitPick(w, h int) (string, string) {
	var b strings.Builder
	b.WriteString("\n" + sAccent.Render("  Pick a commit to scan:") + "\n\n")

	if m.commitErr != nil {
		b.WriteString("  " + sErr.Render("Error: "+m.commitErr.Error()) + "\n\n")
	}

	if m.commitTyping {
		b.WriteString("  Ref: " + sBold.Render(m.commitInput) + sDim.Render("_") + "\n\n")
		b.WriteString("  " + sDim.Render("Type a commit hash, branch, or tag, then press enter.") + "\n")
		return b.String(), " enter confirm · esc cancel"
	}

	if len(m.commits) == 0 {
		b.WriteString("  " + sDim.Render("(no commits found)") + "\n\n")
	}
	for i, c := range m.commits {
		cur := "  "
		if i == m.commitCursor {
			cur = sAccent.Render("▸ ")
		}
		line := cur + sBold.Render(c.Short) + "  " + trunc(c.Subject, w-42) + "  " + sDim.Render(c.Author)
		if i == m.commitCursor {
			line = sSelBg.Render(pad(line, w))
		}
		b.WriteString(line + "\n")
	}
	cur := "  "
	if m.commitCursor == len(m.commits) {
		cur = sAccent.Render("▸ ")
	}
	typeLine := cur + sDim.Render("Type a ref manually (hash, branch, tag)…")
	if m.commitCursor == len(m.commits) {
		typeLine = sSelBg.Render(pad(typeLine, w))
	}
	b.WriteString(typeLine + "\n")

	foot := " ↑↓ move · enter select · t type ref · esc back"
	return b.String(), foot
}

// ---------- selector de carpeta ----------

func (m Model) viewFolderPick(w, h int) (string, string) {
	var b strings.Builder
	b.WriteString("\n" + sAccent.Render("  Choose a folder to work in:") + "\n")
	b.WriteString("  " + sDim.Render(trunc(m.fpDir, w-2)) + "\n\n")

	if m.fpErr != nil {
		b.WriteString("  " + sErr.Render("Error: "+m.fpErr.Error()) + "\n\n")
	}

	for i, e := range m.fpEntries {
		cur := "  "
		if i == m.fpCursor {
			cur = sAccent.Render("▸ ")
		}
		var label string
		switch e.kind {
		case folderEntryUse:
			label = sOK.Render("✓ Use this folder")
		case folderEntryUp:
			label = sDim.Render("📁 ..")
		case folderEntrySub:
			label = "📁 " + e.name
		}
		line := cur + label
		if i == m.fpCursor {
			line = sSelBg.Render(pad(line, w))
		}
		b.WriteString(line + "\n")
	}

	foot := " ↑↓ move · enter open/select · esc cancel"
	return b.String(), foot
}

// ---------- escaneo ----------

func (m Model) viewScanning(w, h int) (string, string) {
	var b strings.Builder
	frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
	b.WriteString("\n  " + sAccent.Render(string(frame)+" Scanning: "+m.scanLabel) + "\n\n")

	p := m.scanProgress
	switch p.Phase {
	case "":
		b.WriteString("  " + sDim.Render("Discovering dependency manifests...") + "\n")
	case "deps":
		b.WriteString("  " + sBold.Render("Checking dependencies for known risks") + "\n")
	case "advisories":
		b.WriteString("  " + sBold.Render("Fetching vulnerability details") + "\n")
	case "secrets":
		b.WriteString("  " + sBold.Render(fmt.Sprintf("Running Endor Labs secrets scan... (%s elapsed)", fmtElapsed(m.spinnerFrame))) + "\n")
	}
	animW := w - 30
	if animW > 40 {
		animW = 40
	}
	if animW >= 10 {
		b.WriteString("\n  " + sDim.Render("you") + "  " + mailAnimation(m.spinnerFrame, animW) + "  " + sDim.Render("Endor Labs") + "\n")
	}
	if p.Total > 0 {
		frac := float64(p.Done) / float64(p.Total)
		barW := w - 24
		if barW > 50 {
			barW = 50
		}
		if barW < 10 {
			barW = 10
		}
		b.WriteString("  " + report.Gauge(frac, barW, report.ColLow) + fmt.Sprintf("  %d/%d\n", p.Done, p.Total))
		if p.Current != "" {
			b.WriteString("  " + sDim.Render(trunc(p.Current, w-4)) + "\n")
		}
	}
	footNote := "This queries the Endor Labs MCP server once per dependency — larger repos take longer."
	if p.Phase == "secrets" {
		footNote = "This runs Endor Labs' secrets scanner over the whole repository in one server-side pass — can take a minute or two, especially on the first scan."
	}
	b.WriteString("\n  " + sDim.Render(footNote) + "\n")

	foot := " esc cancel"
	return b.String(), foot
}

// ---------- resultados del escaneo ----------

func (m Model) viewScanResults(w, h int) (string, string) {
	var b strings.Builder
	if m.scanErr != nil {
		b.WriteString("\n  " + sErr.Render("Scan error:") + "\n\n  " + m.scanErr.Error() + "\n")
		return b.String(), " esc back to menu"
	}
	res := m.scanResult
	if res == nil {
		b.WriteString("\n  " + sWarn.Render("No scan result.") + "\n")
		return b.String(), " esc back to menu"
	}

	b.WriteString("\n" + sAccent.Render("  "+m.scanLabel) + "\n\n")

	if res.Kind != "secrets" && res.Dependencies == 0 {
		b.WriteString("  " + sDim.Render("No dependency manifests were found for this target.") + "\n")
		return b.String(), " esc back to menu"
	}

	summaryW := w - 4
	if summaryW > 70 {
		summaryW = 70
	}
	for _, line := range strings.Split(report.Summary(res.Findings, summaryW), "\n") {
		b.WriteString("  " + line + "\n")
	}
	if res.Kind != "secrets" {
		b.WriteString("\n  " + sDim.Render(fmt.Sprintf("%d dependencies checked, %d vulnerable", res.Dependencies, res.Vulnerable)) + "\n")
		if len(res.Unsupported) > 0 {
			b.WriteString("  " + sDim.Render(fmt.Sprintf("%d manifest(s) skipped (unsupported ecosystem)", len(res.Unsupported))) + "\n")
		}
	}
	if len(res.Errors) > 0 {
		b.WriteString("  " + sWarn.Render(fmt.Sprintf("%d lookup error(s) during scan", len(res.Errors))) + "\n")
	}

	if len(res.Findings) == 0 {
		msg := "✓ No known vulnerabilities found."
		if res.Kind == "secrets" {
			msg = "✓ No exposed secrets found."
		}
		b.WriteString("\n  " + sOK.Render(msg) + "\n")
		return b.String(), " esc back to menu"
	}

	used := strings.Count(b.String(), "\n")
	remaining := h - used - 2
	if remaining < 3 {
		remaining = 3
	}
	fs := res.Findings
	start := m.resultCursor - remaining/2
	if start > len(fs)-remaining {
		start = len(fs) - remaining
	}
	if start < 0 {
		start = 0
	}
	end := start + remaining
	if end > len(fs) {
		end = len(fs)
	}

	b.WriteString("\n" + sBold.Render("  Findings:") + "\n\n")
	for i := start; i < end; i++ {
		f := fs[i]
		title := f.Title
		if title == "" {
			title = f.ID
		}
		text := fmt.Sprintf("%s  %s", title, sDim.Render(f.Location()))
		cur := "  "
		line := cur + report.SevBadge(f.Severity) + " " + trunc(text, w-16)
		if i == m.resultCursor {
			line = sSelBg.Render(pad(sAccent.Render("▸ ")+report.SevBadge(f.Severity)+" "+trunc(text, w-16), w))
		}
		b.WriteString(line + "\n")
	}

	if m.exportMsg != "" {
		b.WriteString("\n  " + sOK.Render("✓ "+m.exportMsg) + "\n")
	} else if m.exportErr != nil {
		b.WriteString("\n  " + sErr.Render("Export failed: "+m.exportErr.Error()) + "\n")
	}

	foot := " ↑↓ move · f review & apply fixes · e export report · esc back to menu"
	return b.String(), foot
}

// ---------- revisión de arreglos ----------

func (m Model) viewFixReview(w, h int) (string, string) {
	var b strings.Builder
	total := len(m.fixQueue)
	if total == 0 {
		b.WriteString("\n  " + sDim.Render("No findings to review.") + "\n")
		return b.String(), " esc back to results"
	}

	if m.fixIndex >= total {
		b.WriteString("\n" + sAccent.Render("  Fix review complete") + "\n\n")
		b.WriteString(fmt.Sprintf("  %s applied, %s skipped, out of %d finding(s).\n",
			sOK.Render(fmt.Sprintf("%d", m.fixApplied)), sWarn.Render(fmt.Sprintf("%d", m.fixSkipped)), total))
		if m.fixApplied > 0 {
			b.WriteString("\n  " + sDim.Render("Review the changes with git diff and commit when you're ready.") + "\n")
		}
		return b.String(), " esc back to results"
	}

	f := m.fixQueue[m.fixIndex]
	b.WriteString("\n" + sAccent.Render(fmt.Sprintf("  Finding %d/%d", m.fixIndex+1, total)) + "  " + report.SevBadge(f.Severity) + "\n\n")
	title := f.Title
	if title == "" {
		title = f.ID
	}
	b.WriteString("  " + sBold.Render(trunc(title, w-4)) + "\n")
	if loc := f.Location(); loc != "" {
		b.WriteString("  " + sDim.Render(loc) + "\n")
	}
	b.WriteString("\n")

	switch {
	case m.fixApplying && m.fixPlan == nil && m.fixPlanErr == nil:
		b.WriteString("  " + sDim.Render("Preparing fix...") + "\n")
		return b.String(), " esc stop reviewing"

	case m.fixPlanErr != nil:
		b.WriteString("  " + sWarn.Render("Cannot auto-fix:") + " " + m.fixPlanErr.Error() + "\n")
		return b.String(), " s skip · esc stop reviewing"

	case m.fixPlan != nil:
		p := m.fixPlan
		b.WriteString("  " + sBold.Render(fmt.Sprintf("Upgrade %s: %s → %s", p.Package, p.From, p.To)) + sDim.Render("  ("+p.Manifest+")") + "\n\n")
		b.WriteString(renderDiff(p.Diff, w-4) + "\n")
		if cmd := p.PostCommand(); cmd != "" {
			b.WriteString("\n  " + sDim.Render("After committing: "+cmd) + "\n")
		}
	}

	if m.fixMsg != "" {
		b.WriteString("\n  " + m.fixMsg + "\n")
	}

	foot := " a apply · s skip · esc stop reviewing"
	return b.String(), foot
}

func renderDiff(lines []fix.DiffLine, w int) string {
	if len(lines) == 0 {
		return "  " + sDim.Render("(no textual diff available)")
	}
	var b strings.Builder
	for i, l := range lines {
		var rendered string
		switch l.Kind {
		case '-':
			rendered = lipgloss.NewStyle().Foreground(report.ColCritical).Render(trunc("- "+l.Text, w))
		case '+':
			rendered = lipgloss.NewStyle().Foreground(report.ColOK).Render(trunc("+ "+l.Text, w))
		default:
			rendered = sDim.Render(trunc("  "+l.Text, w))
		}
		b.WriteString("  " + rendered)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ---------- navegador de herramientas MCP (alternativa avanzada) ----------

func (m Model) viewMCPTools(w, h int) (string, string) {
	var b strings.Builder

	if m.mcpConnecting {
		b.WriteString("\n" + connectingPanel(m.spinnerFrame, w) + "\n")
		return b.String(), " esc cancel"
	}

	if m.mcpErr != nil {
		b.WriteString("\n  " + sErr.Render("Error connecting to MCP server:") + "\n\n")
		b.WriteString("  " + m.mcpErr.Error() + "\n\n")
		b.WriteString("  Make sure npx and endorctl are installed.\n")
		return b.String(), " esc back"
	}

	if len(m.mcpTools) == 0 {
		b.WriteString("\n  " + sWarn.Render("No tools available from MCP server") + "\n")
		return b.String(), " esc back"
	}

	b.WriteString("\n" + sAccent.Render("  Available MCP Tools:") + "\n\n")
	for i, tool := range m.mcpTools {
		cur := "  "
		if i == m.mcpToolSel {
			cur = sAccent.Render("▸ ")
		}
		line := cur + sBold.Render(tool.Name)
		if tool.Description != "" {
			line += sDim.Render("  " + trunc(tool.Description, w-30))
		}
		if i == m.mcpToolSel {
			line = sSelBg.Render(pad(line, w))
		}
		b.WriteString(line + "\n")
	}

	foot := " ↑↓ move · enter invoke · esc back"
	return b.String(), foot
}

// ---------- entrada de la herramienta ----------

func (m Model) viewToolInput(w, h int) (string, string) {
	var b strings.Builder

	if m.mcpToolSel >= len(m.mcpTools) {
		b.WriteString("\n  " + sWarn.Render("Tool not found") + "\n")
		return b.String(), " esc back"
	}

	tool := m.mcpTools[m.mcpToolSel]
	b.WriteString("\n" + sAccent.Render("  Tool: "+tool.Name) + "\n")
	if tool.Description != "" {
		b.WriteString(sDim.Render("  "+wrap(tool.Description, w-4)) + "\n")
	}

	b.WriteString("\n" + sBold.Render("  Required Parameters:") + "\n\n")

	if len(m.toolInputList) == 0 {
		b.WriteString("  " + sOK.Render("✓") + " No parameters required\n\n")
		b.WriteString("  " + sDim.Render("Press enter to invoke tool") + "\n")
	} else {
		for i, paramName := range m.toolInputList {
			cur := "  "
			if i == m.inputCursor {
				cur = sAccent.Render("▸ ")
			}
			val, ok := m.toolInput[paramName]
			valStr := ""
			if ok {
				valStr = fmt.Sprintf("%v", val)
			} else {
				valStr = sDim.Render("(required)")
			}
			line := cur + sBold.Render(paramName) + ": " + valStr
			if i == m.inputCursor {
				line = sSelBg.Render(pad(line, w))
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n  " + sDim.Render("Note: Edit parameters by pressing 'e' on a field") + "\n")
	}

	foot := " ↑↓ move · enter invoke · e edit · esc back"
	return b.String(), foot
}

// ---------- editar parámetro ----------

func (m Model) viewEditParam(w, h int) (string, string) {
	var b strings.Builder

	b.WriteString("\n" + sAccent.Render("  Edit Parameter: "+m.editingParam) + "\n\n")
	b.WriteString("  Value: " + sBold.Render(m.editingValue) + sDim.Render("_") + "\n\n")
	b.WriteString("  " + sDim.Render("Type the value and press enter to confirm") + "\n")

	foot := " enter confirm · esc cancel"
	return b.String(), foot
}

// ---------- resultado crudo de la herramienta ----------

func (m Model) viewRawToolResult(w, h int) (string, string) {
	var b strings.Builder

	if m.resultErr != nil {
		b.WriteString("\n  " + sErr.Render("Error:") + "\n\n")
		b.WriteString("  " + m.resultErr.Error() + "\n")
		return b.String(), " esc back · q quit"
	}

	if m.resultOutput == "" {
		b.WriteString("\n  " + sDim.Render("(no output)") + "\n")
		return b.String(), " esc back · q quit"
	}

	b.WriteString("\n" + sAccent.Render("  Tool Output:") + "\n\n")
	for _, line := range strings.Split(m.resultOutput, "\n") {
		b.WriteString("  " + trunc(line, w-4) + "\n")
	}

	foot := " esc back · q quit"
	return b.String(), foot
}
