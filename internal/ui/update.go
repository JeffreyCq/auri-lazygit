package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"auri-tui/internal/findings"
	"auri-tui/internal/fix"
	"auri-tui/internal/gitx"
	"auri-tui/internal/mcpscan"
	"auri-tui/internal/report"
)

// Update implementa tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case mcpDoneMsg:
		m.mcpConnecting = false
		if msg.err != nil {
			m.mcpErr = msg.err
			return m, nil
		}
		m.mcpClient = msg.client
		m.mcpTools = msg.tools
		return m, m.dispatchTarget(m.pendingTarget)

	case toolResultMsg:
		m.resultOutput = msg.output
		m.resultErr = msg.err
		m.screen = scRawToolResult
		return m, nil

	case tickMsg:
		if m.screen != scScanning {
			return m, nil
		}
		m.spinnerFrame++
		return m, tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })

	case scanProgressMsg:
		m.scanProgress = msg.p
		return m, waitForScan(m.scanProgressCh, m.scanDoneCh)

	case scanDoneMsg:
		m.scanCancel = nil
		m.scanResult = msg.result
		m.scanErr = msg.err
		m.resultCursor = 0
		m.screen = scScanResults
		return m, nil

	case fixBuiltMsg:
		m.fixPlan = msg.plan
		m.fixPlanErr = msg.err
		m.fixApplying = false
		return m, nil

	case fixAppliedMsg:
		if msg.err != nil {
			m.fixMsg = "Failed to apply: " + msg.err.Error()
		} else {
			p := m.fixPlan
			m.fixApplied++
			m.fixMsg = fmt.Sprintf("Applied: %s %s → %s (%s)", p.Package, p.From, p.To, p.Manifest)
		}
		m.fixApplying = false
		return m, m.advanceFix()

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			m.closeMCP()
			return m, tea.Quit
		}
		switch m.screen {
		case scMenu:
			return m.keysMenu(msg)
		case scCommitPick:
			return m.keysCommitPick(msg)
		case scFolderPick:
			return m.keysFolderPick(msg)
		case scScanning:
			return m.keysScanning(msg)
		case scScanResults:
			return m.keysScanResults(msg)
		case scFixReview:
			return m.keysFixReview(msg)
		case scMCPTools:
			return m.keysMCPTools(msg)
		case scToolInput:
			return m.keysToolInput(msg)
		case scEditParam:
			return m.keysEditParam(msg)
		case scRawToolResult:
			return m.keysRawToolResult(msg)
		}
	}
	return m, nil
}

// ---------- menú de objetivo ----------

func (m Model) keysMenu(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.closeMCP()
		return m, tea.Quit
	case "up", "k":
		m.cursor = clamp(m.cursor-1, 0, len(m.items)-1)
	case "down", "j":
		m.cursor = clamp(m.cursor+1, 0, len(m.items)-1)
	case "enter", " ":
		it := m.items[m.cursor]
		if it.kind == targetOpenFolder {
			m.status = ""
			m.startFolderPick()
			return m, nil
		}
		if (it.kind == targetLocalChanges || it.kind == targetCommit || it.kind == targetSecrets) && !m.gitOK {
			m.status = "This target requires a git repository."
			return m, nil
		}
		if it.kind == targetSecrets && !gitx.HasRemote(m.repoRoot) {
			m.status = "Secrets Scan requires a configured git remote (git remote add origin <url>); the dependency scans above don't need one."
			return m, nil
		}
		m.status = ""
		m.mcpErr = nil
		return m, m.connectMCP(it.kind)
	}
	return m, nil
}

// dispatchTarget se ejecuta cuando la conexión MCP ya está lista (o ya lo estaba).
func (m *Model) dispatchTarget(target targetKind) tea.Cmd {
	switch target {
	case targetLocalChanges:
		files, err := gitx.ChangedFiles(m.repoRoot)
		if err != nil {
			m.status = "git status failed: " + err.Error()
			m.screen = scMenu
			return nil
		}
		if len(files) == 0 {
			m.status = "No uncommitted changes to scan."
			m.screen = scMenu
			return nil
		}
		m.scanLabel = fmt.Sprintf("Local changes (%d file%s)", len(files), plural(len(files)))
		m.scanKind = "deps"
		m.scanDir = m.repoRoot
		m.scanFiles = files
		return m.beginScan()

	case targetCommit:
		commits, err := gitx.RecentCommits(m.repoRoot, 25)
		if err != nil {
			m.status = "git log failed: " + err.Error()
			m.screen = scMenu
			return nil
		}
		m.commits = commits
		m.commitCursor = 0
		m.commitTyping = false
		m.commitInput = ""
		m.commitErr = nil
		m.screen = scCommitPick
		return nil

	case targetFullRepo:
		m.scanLabel = "Full repository"
		m.scanKind = "deps"
		m.scanDir = m.repoRoot
		m.scanFiles = nil
		return m.beginScan()

	case targetSecrets:
		m.scanLabel = "Secrets scan (full repository)"
		m.scanKind = "secrets"
		m.scanDir = m.repoRoot
		m.scanFiles = nil
		return m.beginScan()

	case targetMCPBrowse:
		m.screen = scMCPTools
		m.mcpToolSel = 0
		return nil
	}
	return nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------- selector de commit ----------

func (m Model) keysCommitPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.commitTyping {
		switch msg.String() {
		case "esc":
			m.commitTyping = false
			m.commitInput = ""
		case "enter":
			ref := m.commitInput
			m.commitTyping = false
			m.commitInput = ""
			if ref == "" {
				return m, nil
			}
			return m.selectCommit(ref)
		case "backspace":
			if len(m.commitInput) > 0 {
				m.commitInput = m.commitInput[:len(m.commitInput)-1]
			}
		default:
			if k := msg.String(); len(k) == 1 {
				m.commitInput += k
			}
		}
		return m, nil
	}

	n := len(m.commits) + 1 // +1 for the "type a ref manually" row
	switch msg.String() {
	case "q", "esc":
		m.screen = scMenu
	case "up", "k":
		m.commitCursor = clamp(m.commitCursor-1, 0, n-1)
	case "down", "j":
		m.commitCursor = clamp(m.commitCursor+1, 0, n-1)
	case "t":
		m.commitTyping = true
		m.commitInput = ""
	case "enter", " ":
		if m.commitCursor >= len(m.commits) {
			m.commitTyping = true
			m.commitInput = ""
			return m, nil
		}
		return m.selectCommit(m.commits[m.commitCursor].Hash)
	}
	return m, nil
}

// selectCommit resuelve ref, lo extrae a un worktree desechable e inicia el escaneo ahí.
func (m Model) selectCommit(ref string) (tea.Model, tea.Cmd) {
	commit, err := gitx.ResolveCommit(m.repoRoot, ref)
	if err != nil {
		m.commitErr = err
		return m, nil
	}
	path, cleanup, err := gitx.AddWorktree(m.repoRoot, commit.Hash)
	if err != nil {
		m.commitErr = err
		return m, nil
	}
	m.commitErr = nil
	m.scanCleanup = cleanup
	m.scanKind = "deps"
	m.scanDir = path
	m.scanFiles = nil
	m.scanLabel = fmt.Sprintf("Commit %s: %s", commit.Short, commit.Subject)
	return m, m.beginScan()
}

// ---------- selector de carpeta ----------

// startFolderPick abre el selector de carpeta en la carpeta actual.
func (m *Model) startFolderPick() {
	m.fpErr = nil
	m.loadFolderEntries(m.cwd)
	m.screen = scFolderPick
}

// loadFolderEntries lista las subcarpetas de dir (sin ocultas) y arma las
// filas del selector: usar esta carpeta, subir un nivel (si aplica), subcarpetas.
func (m *Model) loadFolderEntries(dir string) {
	m.fpDir = dir
	m.fpCursor = 0
	entries := []folderEntry{{kind: folderEntryUse}}
	if parent := filepath.Dir(dir); parent != dir {
		entries = append(entries, folderEntry{kind: folderEntryUp})
	}
	des, err := os.ReadDir(dir)
	if err != nil {
		m.fpErr = err
		m.fpEntries = entries
		return
	}
	var names []string
	for _, d := range des {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			continue
		}
		names = append(names, d.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		entries = append(entries, folderEntry{kind: folderEntrySub, name: n})
	}
	m.fpErr = nil
	m.fpEntries = entries
}

func (m Model) keysFolderPick(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.screen = scMenu
	case "up", "k":
		m.fpCursor = clamp(m.fpCursor-1, 0, len(m.fpEntries)-1)
	case "down", "j":
		m.fpCursor = clamp(m.fpCursor+1, 0, len(m.fpEntries)-1)
	case "enter", " ":
		if m.fpCursor >= len(m.fpEntries) {
			return m, nil
		}
		switch e := m.fpEntries[m.fpCursor]; e.kind {
		case folderEntryUse:
			m.setWorkingDir(m.fpDir)
			m.buildMenu()
			m.screen = scMenu
			m.status = "Now working in " + m.cwd
		case folderEntryUp:
			m.loadFolderEntries(filepath.Dir(m.fpDir))
		case folderEntrySub:
			m.loadFolderEntries(filepath.Join(m.fpDir, e.name))
		}
	}
	return m, nil
}

// ---------- escaneo ----------

func (m *Model) beginScan() tea.Cmd {
	m.screen = scScanning
	m.scanProgress = mcpscan.Progress{}
	m.scanResult = nil
	m.scanErr = nil
	m.resultCursor = 0
	m.spinnerFrame = 0
	m.exportMsg = ""
	m.exportErr = nil
	return tea.Batch(m.startScan(), tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} }))
}

func (m *Model) startScan() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	m.scanCancel = cancel
	progressCh := make(chan mcpscan.Progress, 32)
	doneCh := make(chan scanDoneMsg, 1)
	m.scanProgressCh = progressCh
	m.scanDoneCh = doneCh

	client, dir, files, kind := m.mcpClient, m.scanDir, m.scanFiles, m.scanKind
	go func() {
		report := func(p mcpscan.Progress) {
			select {
			case progressCh <- p:
			default:
			}
		}
		var res *mcpscan.Result
		var err error
		switch {
		case kind == "secrets":
			res, err = mcpscan.RunSecrets(ctx, client, dir, report)
		case files != nil:
			res, err = mcpscan.RunFiles(ctx, client, dir, files, report)
		default:
			res, err = mcpscan.Run(ctx, client, dir, report)
		}
		doneCh <- scanDoneMsg{result: res, err: err}
	}()
	return waitForScan(progressCh, doneCh)
}

func waitForScan(progressCh chan mcpscan.Progress, doneCh chan scanDoneMsg) tea.Cmd {
	return func() tea.Msg {
		select {
		case p := <-progressCh:
			return scanProgressMsg{p: p}
		case d := <-doneCh:
			return d
		}
	}
}

func (m Model) keysScanning(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.cleanupScan()
		m.screen = scMenu
		m.status = "Scan cancelled."
	}
	return m, nil
}

// ---------- resultados del escaneo ----------

func (m Model) keysScanResults(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	n := 0
	if m.scanResult != nil {
		n = len(m.scanResult.Findings)
	}
	switch msg.String() {
	case "q", "esc":
		m.cleanupScan()
		m.screen = scMenu
	case "up", "k":
		if n > 0 {
			m.resultCursor = clamp(m.resultCursor-1, 0, n-1)
		}
	case "down", "j":
		if n > 0 {
			m.resultCursor = clamp(m.resultCursor+1, 0, n-1)
		}
	case "f":
		if n > 0 {
			return m, m.startFixReview()
		}
	case "e":
		if m.scanResult != nil {
			m.exportReport()
		}
	}
	return m, nil
}

// exportReport escribe los resultados del escaneo actual como un reporte
// Markdown junto a la raíz del repositorio.
func (m *Model) exportReport() {
	res := m.scanResult
	meta := report.MarkdownMeta{
		Repo:         filepath.Base(m.repoRoot),
		Branch:       m.branch,
		Target:       m.scanLabel,
		Kind:         res.Kind,
		Dependencies: res.Dependencies,
		Vulnerable:   res.Vulnerable,
		GeneratedAt:  time.Now(),
	}
	md := report.Markdown(meta, res.Findings)
	name := fmt.Sprintf("auri-report-%s.md", time.Now().Format("20060102-150405"))
	path := filepath.Join(m.repoRoot, name)
	if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
		m.exportErr = err
		m.exportMsg = ""
		return
	}
	m.exportErr = nil
	m.exportMsg = "Report exported to " + path
}

// ---------- revisión de arreglos ----------

func (m *Model) startFixReview() tea.Cmd {
	if m.scanResult == nil {
		return nil
	}
	m.fixQueue = append([]findings.Finding{}, m.scanResult.Findings...)
	m.fixIndex = 0
	m.fixApplied = 0
	m.fixSkipped = 0
	m.fixMsg = ""
	m.fixPlan = nil
	m.fixPlanErr = nil
	m.screen = scFixReview
	return m.loadFixPlan()
}

func (m *Model) loadFixPlan() tea.Cmd {
	m.fixPlan = nil
	m.fixPlanErr = nil
	m.fixMsg = ""
	if m.fixIndex >= len(m.fixQueue) {
		return nil
	}
	f := m.fixQueue[m.fixIndex]
	repoRoot := m.repoRoot
	m.fixApplying = true
	return func() tea.Msg {
		p, err := fix.Build(f, repoRoot)
		return fixBuiltMsg{plan: p, err: err}
	}
}

func (m *Model) advanceFix() tea.Cmd {
	m.fixIndex++
	return m.loadFixPlan()
}

func (m Model) keysFixReview(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "q":
		m.screen = scScanResults
	case "a":
		if m.fixPlan != nil && m.fixPlanErr == nil && !m.fixApplying {
			plan := m.fixPlan
			m.fixApplying = true
			return m, func() tea.Msg { return fixAppliedMsg{err: plan.Apply()} }
		}
	case "s", "n":
		if m.fixIndex < len(m.fixQueue) {
			m.fixSkipped++
			return m, m.advanceFix()
		}
	}
	return m, nil
}

// ---------- navegador de herramientas MCP (alternativa avanzada) ----------

func (m Model) keysMCPTools(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.mcpErr = nil
		m.screen = scMenu
	case "up", "k":
		if m.mcpToolSel > 0 {
			m.mcpToolSel--
		}
	case "down", "j":
		if m.mcpToolSel < len(m.mcpTools)-1 {
			m.mcpToolSel++
		}
	case "enter", " ":
		if m.mcpToolSel < len(m.mcpTools) {
			m.screen = scToolInput
			tool := m.mcpTools[m.mcpToolSel]
			m.toolInput = make(map[string]interface{})
			m.toolInputList = tool.InputSchema.Required
			m.inputCursor = 0
		}
	}
	return m, nil
}

func (m Model) keysToolInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.screen = scMCPTools
	case "up", "k":
		if m.inputCursor > 0 {
			m.inputCursor--
		}
	case "down", "j":
		if m.inputCursor < len(m.toolInputList)-1 {
			m.inputCursor++
		}
	case "e":
		if m.inputCursor < len(m.toolInputList) {
			paramName := m.toolInputList[m.inputCursor]
			m.editingParam = paramName
			val := m.toolInput[paramName]
			m.editingValue = fmt.Sprintf("%v", val)
			m.screen = scEditParam
		}
	case "enter":
		if m.mcpToolSel < len(m.mcpTools) {
			tool := m.mcpTools[m.mcpToolSel]
			return m, m.invokeTool(tool.Name, m.toolInput)
		}
	}
	return m, nil
}

func (m Model) keysEditParam(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k := msg.String(); k {
	case "esc":
		m.screen = scToolInput
		m.editingParam = ""
		m.editingValue = ""
	case "enter":
		if m.editingParam != "" {
			m.toolInput[m.editingParam] = m.editingValue
		}
		m.screen = scToolInput
		m.editingParam = ""
		m.editingValue = ""
	case "backspace":
		if len(m.editingValue) > 0 {
			m.editingValue = m.editingValue[:len(m.editingValue)-1]
		}
	default:
		if len(k) == 1 && k[0] >= 32 && k[0] <= 126 {
			m.editingValue += k
		}
	}
	return m, nil
}

func (m Model) keysRawToolResult(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc":
		m.screen = scMCPTools
	}
	return m, nil
}

// invokeTool llama a una herramienta MCP y devuelve el resultado.
func (m *Model) invokeTool(toolName string, args map[string]interface{}) tea.Cmd {
	client := m.mcpClient
	return func() tea.Msg {
		if client == nil {
			return toolResultMsg{err: fmt.Errorf("MCP client not connected")}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		result, err := client.CallTool(ctx, toolName, args)
		if err != nil {
			return toolResultMsg{err: err}
		}
		output := result.Text()
		if result.IsError {
			return toolResultMsg{err: fmt.Errorf("%s", output)}
		}
		return toolResultMsg{output: output, err: nil}
	}
}
