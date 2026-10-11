package report

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"auri-lazygit/internal/demo"
	"auri-lazygit/internal/findings"
)

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansi.ReplaceAllString(s, "") }

func TestBarMinimumVisible(t *testing.T) {
	full, part, empty := bar(1, 1000, 20)
	if full != 0 || part == 0 || empty != 19 {
		t.Fatalf("full=%d part=%q empty=%d", full, part, empty)
	}
	full, part, empty = bar(0, 10, 10)
	if full != 0 || part != 0 || empty != 10 {
		t.Fatal("cero debe ser barra vacía")
	}
	full, _, empty = bar(10, 10, 10)
	if full != 10 || empty != 0 {
		t.Fatal("máximo debe llenar la barra")
	}
}

func TestStackedBarExactWidth(t *testing.T) {
	cases := []findings.Counts{
		{Critical: 4, High: 4, Medium: 3, Low: 2},
		{Critical: 1, Low: 1000},
		{Medium: 7},
		{},
	}
	for _, c := range cases {
		for _, w := range []int{4, 10, 33, 52} {
			got := []rune(plain(StackedBar(c, w)))
			if len(got) != w {
				t.Fatalf("%+v w=%d: ancho %d", c, w, len(got))
			}
		}
	}
}

func TestSeverityBarsAndSummary(t *testing.T) {
	fs, err := findings.ParseOutput(demo.FindingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	c := findings.Summarize(fs)
	out := plain(SeverityBars(c, 20))
	for _, want := range []string{"CRIT", "HIGH", "MED", "LOW", "31%"} {
		if !strings.Contains(out, want) {
			t.Fatalf("falta %q en:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "\n"); n != 3 {
		t.Fatalf("líneas = %d", n+1)
	}
	s := plain(Summary(fs, 70))
	for _, want := range []string{"13 findings", "SCA", "Secrets", "SAST", "Malware", "RISK", "/100"} {
		if !strings.Contains(s, want) {
			t.Fatalf("falta %q en resumen:\n%s", want, s)
		}
	}
	// ninguna línea del resumen debe desbordar 70 columnas
	for _, ln := range strings.Split(s, "\n") {
		if w := lipgloss.Width(ln); w > 70 {
			t.Fatalf("línea de %d cols: %q", w, ln)
		}
	}
}

func TestRiskMeter(t *testing.T) {
	if !strings.Contains(plain(RiskMeter(0, 20)), "CLEAN") {
		t.Fatal("0 debe ser CLEAN")
	}
	if !strings.Contains(plain(RiskMeter(100, 20)), "CRITICAL") {
		t.Fatal("100 debe ser CRITICAL")
	}
	if got := plain(RiskMeter(500, 20)); !strings.Contains(got, "100/100") {
		t.Fatalf("debe recortar a 100: %s", got)
	}
}

func TestSparklineAndDelta(t *testing.T) {
	if got := Sparkline([]int{0, 1, 4, 8}); got != "▁▂▅█" {
		t.Fatalf("sparkline = %q", got)
	}
	if Sparkline(nil) != "" {
		t.Fatal("vacío")
	}
	if !strings.Contains(plain(Delta(5, 3)), "2 fewer") || !strings.Contains(plain(Delta(3, 5)), "2 more") {
		t.Fatal("delta")
	}
}

func TestEmptyCategories(t *testing.T) {
	if !strings.Contains(plain(CategoryBars(nil, 10)), "no findings") {
		t.Fatal("categorías vacías")
	}
}
