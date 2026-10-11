// auri-tui: MCP integration for Endor Labs tools
//
// A minimal terminal interface to interact with the Endor MCP server.
// Usage:
//
//	auri-tui                 # Open the MCP tools menu
//	auri-tui --version       # Show version
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"auri-lazygit/internal/ui"
)

var version = "0.2.0"

func main() { os.Exit(run()) }

func run() int {
	var (
		showVer = flag.Bool("version", false, "show version")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "auri-tui %s — Endor Labs MCP tools interface\n\nOptions:\n", version)
		flag.PrintDefaults()
	}
	flag.Parse()
	if *showVer {
		fmt.Println("auri-tui", version)
		return 0
	}

	p := tea.NewProgram(ui.New(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
