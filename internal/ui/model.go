// Package ui implementa la interfaz de terminal (Bubble Tea) de AURI × Lazygit.
package ui

import (
	"context"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"auri-lazygit/internal/findings"
	"auri-lazygit/internal/fix"
	"auri-lazygit/internal/gitx"
	"auri-lazygit/internal/mcp"
	"auri-lazygit/internal/mcpscan"
)

type screen int

const (
	scMenu        screen = iota
	scCommitPick         // choose a commit to scan
	scScanning           // scan in progress, streaming progress
	scScanResults        // chart summary + navigable finding list
	scFixReview          // diff + apply/skip for one finding at a time
	scMCPTools           // raw MCP tool browser (advanced/manual fallback)
	scToolInput
	scEditParam
	scRawToolResult
)

// targetKind identifica qué eligió escanear el usuario.
type targetKind int

const (
	targetLocalChanges targetKind = iota
	targetCommit
	targetFullRepo
	targetSecrets
	targetMCPBrowse
)

type menuItem struct {
	title string
	sub   string
	icon  string
	kind  targetKind
}

// ---- mensajes ----

type mcpDoneMsg struct {
	client *mcp.Client
	tools  []mcp.Tool
	err    error
}

type toolResultMsg struct {
	output string
	err    error
}

// scanProgressMsg transporta una actualización de progreso del escaneo en segundo plano.
type scanProgressMsg struct{ p mcpscan.Progress }

// scanDoneMsg transporta el resultado final del escaneo.
type scanDoneMsg struct {
	result *mcpscan.Result
	err    error
}

// tickMsg impulsa la animación del spinner en la pantalla de escaneo.
type tickMsg struct{}

// fixBuiltMsg transporta el resultado de construir un fix.Plan para el hallazgo actual.
type fixBuiltMsg struct {
	plan *fix.Plan
	err  error
}

// fixAppliedMsg transporta el resultado de aplicar el fix.Plan actual.
type fixAppliedMsg struct{ err error }

// Model es el modelo de Bubble Tea.
type Model struct {
	w, h int

	screen screen

	// detección del repo
	cwd      string
	repoRoot string
	gitOK    bool

	// menú de objetivo
	items  []menuItem
	cursor int
	status string

	// conexión MCP (compartida por todos los objetivos)
	mcpClient     *mcp.Client
	mcpTools      []mcp.Tool
	mcpErr        error
	mcpConnecting bool
	pendingTarget targetKind // qué hacer cuando la conexión esté lista

	// navegador de herramientas MCP (alternativa avanzada, targetMCPBrowse)
	mcpToolSel    int
	toolInput     map[string]interface{}
	toolInputList []string
	inputCursor   int
	editingParam  string
	editingValue  string
	resultOutput  string
	resultErr     error

	// selector de commit (targetCommit)
	commits      []gitx.Commit
	commitCursor int
	commitInput  string
	commitTyping bool
	commitErr    error

	// ejecución del escaneo
	scanLabel      string // human-readable description of what's being scanned
	scanKind       string // "deps" (SCA/malware) or "secrets" — picks the mcpscan engine
	scanDir        string
	scanFiles      []string // nil = full-repo discovery
	scanCleanup    func()   // removes a temp worktree, if one was created
	scanCancel     context.CancelFunc
	scanProgressCh chan mcpscan.Progress
	scanDoneCh     chan scanDoneMsg
	scanProgress   mcpscan.Progress
	scanResult     *mcpscan.Result
	scanErr        error
	spinnerFrame   int

	// navegación de resultados del escaneo
	resultCursor int

	// revisión de arreglos
	fixQueue    []findings.Finding
	fixIndex    int
	fixPlan     *fix.Plan
	fixPlanErr  error
	fixApplying bool
	fixApplied  int
	fixSkipped  int
	fixMsg      string
}

// New crea el modelo inicial, detectando la raíz del repositorio git desde cwd.
func New() Model {
	m := Model{w: 100, h: 30}
	m.cwd, _ = os.Getwd()
	if m.cwd == "" {
		m.cwd = "."
	}
	if root, err := gitx.Root(m.cwd); err == nil && root != "" {
		m.repoRoot = root
		m.gitOK = true
	} else {
		m.repoRoot = m.cwd
		m.gitOK = false
	}
	m.buildMenu()
	return m
}

// Init implementa tea.Model.
func (m Model) Init() tea.Cmd { return nil }

func (m *Model) buildMenu() {
	m.items = nil
	changedNote := "scan only files with uncommitted changes"
	if m.gitOK {
		if files, err := gitx.ChangedFiles(m.repoRoot); err == nil && len(files) == 0 {
			changedNote = "no uncommitted changes right now"
		}
	} else {
		changedNote = "not a git repository"
	}
	m.items = append(m.items,
		menuItem{kind: targetLocalChanges, icon: "📝", title: "Local Changes", sub: changedNote},
		menuItem{kind: targetCommit, icon: "📌", title: "Pick a Commit", sub: "scan the dependency manifests as of a specific commit"},
		menuItem{kind: targetFullRepo, icon: "📁", title: "Full Repository", sub: "scan every manifest in the working tree"},
		menuItem{kind: targetSecrets, icon: "🔑", title: "Secrets Scan", sub: "scan the full repo for exposed credentials (needs a git remote; can take a minute or two)"},
		menuItem{kind: targetMCPBrowse, icon: "🛠 ", title: "Browse MCP Tools", sub: "advanced: call any Endor Labs MCP tool manually"},
	)
	m.cursor = 0
}

// connectMCP inicia (o reutiliza) la conexión MCP, y luego continúa hacia target.
func (m *Model) connectMCP(target targetKind) tea.Cmd {
	m.pendingTarget = target
	if m.mcpClient != nil {
		return m.dispatchTarget(target)
	}
	m.mcpConnecting = true
	m.mcpErr = nil
	return func() tea.Msg {
		client, err := mcp.NewClient("npx", "-y", "endorctl", "ai-tools", "mcp-server")
		if err != nil {
			return mcpDoneMsg{err: err}
		}
		// Al arrancar, endorctl hace una llamada bloqueante de sincronización de
		// políticas antes de poder atender MCP; en una sesión nueva/anónima esa
		// llamada puede fallar y reintentar, empujando el tiempo hasta la
		// primera respuesta más allá de 20s. Por eso cada fase tiene su propio
		// presupuesto en vez de compartir un único plazo ajustado.
		initCtx, initCancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer initCancel()
		if err := client.Initialize(initCtx); err != nil {
			client.Close()
			return mcpDoneMsg{err: err}
		}
		listCtx, listCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer listCancel()
		tools, err := client.ListTools(listCtx)
		if err != nil {
			client.Close()
			return mcpDoneMsg{err: err}
		}
		return mcpDoneMsg{client: client, tools: tools, err: nil}
	}
}

// closeMCP cierra la conexión al servidor MCP y cualquier worktree pendiente.
func (m *Model) closeMCP() {
	if m.mcpClient != nil {
		m.mcpClient.Close()
		m.mcpClient = nil
	}
	m.cleanupScan()
}

func (m *Model) cleanupScan() {
	if m.scanCleanup != nil {
		m.scanCleanup()
		m.scanCleanup = nil
	}
	if m.scanCancel != nil {
		m.scanCancel()
		m.scanCancel = nil
	}
}
