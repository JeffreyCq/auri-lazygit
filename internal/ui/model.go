// Package ui implements the terminal interface (Bubble Tea) for AURI × Lazygit.
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

// targetKind identifies what the user chose to scan.
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

// ---- messages ----

type mcpDoneMsg struct {
	client *mcp.Client
	tools  []mcp.Tool
	err    error
}

type toolResultMsg struct {
	output string
	err    error
}

// scanProgressMsg carries one progress update from the background scan.
type scanProgressMsg struct{ p mcpscan.Progress }

// scanDoneMsg carries the final scan result.
type scanDoneMsg struct {
	result *mcpscan.Result
	err    error
}

// tickMsg drives the scanning-screen spinner animation.
type tickMsg struct{}

// fixBuiltMsg carries the outcome of building a fix.Plan for the current finding.
type fixBuiltMsg struct {
	plan *fix.Plan
	err  error
}

// fixAppliedMsg carries the outcome of applying the current fix.Plan.
type fixAppliedMsg struct{ err error }

// Model is the Bubble Tea model.
type Model struct {
	w, h int

	screen screen

	// repo detection
	cwd      string
	repoRoot string
	gitOK    bool

	// target menu
	items  []menuItem
	cursor int
	status string

	// MCP connection (shared by every target)
	mcpClient     *mcp.Client
	mcpTools      []mcp.Tool
	mcpErr        error
	mcpConnecting bool
	pendingTarget targetKind // what to do once the connection is ready

	// MCP tool browser (advanced fallback, targetMCPBrowse)
	mcpToolSel    int
	toolInput     map[string]interface{}
	toolInputList []string
	inputCursor   int
	editingParam  string
	editingValue  string
	resultOutput  string
	resultErr     error

	// commit picker (targetCommit)
	commits      []gitx.Commit
	commitCursor int
	commitInput  string
	commitTyping bool
	commitErr    error

	// scan execution
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

	// scan results navigation
	resultCursor int

	// fix review
	fixQueue    []findings.Finding
	fixIndex    int
	fixPlan     *fix.Plan
	fixPlanErr  error
	fixApplying bool
	fixApplied  int
	fixSkipped  int
	fixMsg      string
}

// New creates the initial model, detecting the git repository root from cwd.
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

// Init implements tea.Model.
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

// connectMCP starts (or reuses) the MCP connection, then proceeds to target.
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
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := client.Initialize(ctx); err != nil {
			client.Close()
			return mcpDoneMsg{err: err}
		}
		tools, err := client.ListTools(ctx)
		if err != nil {
			client.Close()
			return mcpDoneMsg{err: err}
		}
		return mcpDoneMsg{client: client, tools: tools, err: nil}
	}
}

// closeMCP closes the connection to the MCP server and any pending worktree.
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
