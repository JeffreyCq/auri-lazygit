package report

import (
	"fmt"
	"strings"
	"time"

	"auri-tui/internal/findings"
)

// MarkdownMeta son los datos de contexto de un escaneo para el reporte exportable.
type MarkdownMeta struct {
	Repo         string
	Branch       string
	Target       string // scanLabel: "Full repository", "Local changes (3 files)", ...
	Kind         string // "deps" (SCA/malware) o "secrets"
	Dependencies int
	Vulnerable   int
	GeneratedAt  time.Time
}

// Markdown arma un reporte Markdown autocontenido con los hallazgos de un
// escaneo, listo para guardar en disco o pegar en un issue/PR.
func Markdown(meta MarkdownMeta, fs []findings.Finding) string {
	var b strings.Builder
	b.WriteString("# AURI Scan Report\n\n")
	if meta.Repo != "" {
		fmt.Fprintf(&b, "**Repository:** %s  \n", meta.Repo)
	}
	if meta.Branch != "" {
		fmt.Fprintf(&b, "**Branch:** %s  \n", meta.Branch)
	}
	if meta.Target != "" {
		fmt.Fprintf(&b, "**Target:** %s  \n", meta.Target)
	}
	fmt.Fprintf(&b, "**Generated:** %s\n\n", meta.GeneratedAt.UTC().Format("2006-01-02 15:04:05 UTC"))

	c := findings.Summarize(fs)
	b.WriteString("## Summary\n\n")
	b.WriteString("| Severity | Count |\n|---|---|\n")
	fmt.Fprintf(&b, "| Critical | %d |\n", c.Critical)
	fmt.Fprintf(&b, "| High | %d |\n", c.High)
	fmt.Fprintf(&b, "| Medium | %d |\n", c.Medium)
	fmt.Fprintf(&b, "| Low | %d |\n\n", c.Low)
	label, _ := RiskLevel(c.Risk())
	fmt.Fprintf(&b, "**Risk score:** %d/100 (%s) — a local heuristic, not an Endor Labs score.\n\n", c.Risk(), label)
	if meta.Kind != "secrets" {
		fmt.Fprintf(&b, "%d dependencies checked, %d vulnerable.\n\n", meta.Dependencies, meta.Vulnerable)
	}

	if len(fs) == 0 {
		b.WriteString("No findings. ✓\n")
		return b.String()
	}

	b.WriteString("## Findings\n\n")
	lastSev := findings.Severity(-1)
	for _, f := range fs {
		if f.Severity != lastSev {
			if lastSev != -1 {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "### %s\n\n", f.Severity.String())
			lastSev = f.Severity
		}
		title := f.Title
		if title == "" {
			title = f.ID
		}
		fmt.Fprintf(&b, "- **%s**", title)
		if loc := f.Location(); loc != "" {
			fmt.Fprintf(&b, " — `%s`", loc)
		}
		b.WriteString("\n")
		if f.Fixed != "" {
			fmt.Fprintf(&b, "  - Fixed version: `%s`\n", f.Fixed)
		}
		if len(f.Aliases) > 0 {
			fmt.Fprintf(&b, "  - %s\n", strings.Join(f.Aliases, ", "))
		}
		if f.Remediation != "" {
			fmt.Fprintf(&b, "  - Remediation: %s\n", f.Remediation)
		}
	}
	return b.String()
}
