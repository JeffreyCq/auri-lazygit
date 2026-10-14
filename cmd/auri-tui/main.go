// auri-tui: integración MCP para las herramientas de Endor Labs.
//
// Una interfaz de terminal mínima para interactuar con el servidor MCP de
// Endor. Uso:
//
//	auri-tui                 # abre el menú de herramientas MCP
//	auri-tui --version       # muestra la versión
package main

import (
	"flag"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"auri-tui/internal/ui"
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
