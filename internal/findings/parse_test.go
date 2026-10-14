package findings_test

import (
	"strings"
	"testing"

	"auri-tui/internal/demo"
	"auri-tui/internal/findings"
)

func TestParseDemoFindings(t *testing.T) {
	fs, err := findings.ParseOutput(demo.FindingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 13 {
		t.Fatalf("esperaba 13 hallazgos, hay %d", len(fs))
	}
	c := findings.Summarize(fs)
	if c.Critical != 4 || c.High != 4 || c.Medium != 3 || c.Low != 2 {
		t.Fatalf("conteos inesperados: %+v", c)
	}
	// orden: lo más grave primero
	if fs[0].Severity != findings.SevCritical || fs[len(fs)-1].Severity != findings.SevLow {
		t.Fatalf("orden incorrecto: primero %v, último %v", fs[0].Severity, fs[len(fs)-1].Severity)
	}
	byTitle := map[string]findings.Finding{}
	for _, f := range fs {
		byTitle[f.ID] = f
	}

	mini := byTitle["GHSA-xvch-5gv4-984h"]
	if mini.Category != findings.CatSCA || mini.Ecosystem != "npm" || mini.Package != "minimist" || mini.Version != "1.2.0" {
		t.Errorf("minimist mal parseado: %+v", mini)
	}
	if mini.Upgrade == nil || mini.Upgrade.To != "1.2.6" || mini.Fixed != "1.2.6" {
		t.Errorf("upgrade de minimist: %+v", mini.Upgrade)
	}
	if !mini.Direct || mini.Reachable != "reachable function" || mini.CVSS != 9.8 {
		t.Errorf("flags de minimist: %+v", mini)
	}
	if len(mini.Aliases) != 2 || mini.Aliases[0] != "GHSA-xvch-5gv4-984h" {
		t.Errorf("aliases: %v", mini.Aliases)
	}

	sec := byTitle["aws-access-key"]
	if sec.Category != findings.CatSecrets || sec.File != "config/settings.py" || sec.Line != 6 || sec.Severity != findings.SevCritical {
		t.Errorf("secreto mal parseado: %+v", sec)
	}
	sast := byTitle["sql-injection"]
	if sast.Category != findings.CatSAST || sast.File != "src/routes/users.js" || sast.Line != 9 || !strings.Contains(sast.Snippet, "SELECT") {
		t.Errorf("SAST mal parseado: %+v", sast)
	}
	mal := byTitle["malware-reqeusts"]
	if mal.Category != findings.CatMalware {
		t.Errorf("malware: %+v", mal)
	}
	if deb := byTitle["GHSA-9vvw-cc9w-f27h"]; deb.Upgrade != nil || !deb.Transitive {
		t.Errorf("debug debería ser transitivo sin upgrade: %+v", deb)
	}
}

// Las líneas citadas por los hallazgos de muestra deben coincidir con los archivos del repo demo.
func TestDemoLinesMatchFiles(t *testing.T) {
	fs, err := findings.ParseOutput(demo.FindingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.File == "" {
			continue
		}
		content, ok := demo.Files[f.File]
		if !ok {
			t.Errorf("%s: archivo %q no existe en demo.Files", f.ID, f.File)
			continue
		}
		lines := strings.Split(content, "\n")
		if f.Line < 1 || f.Line > len(lines) {
			t.Errorf("%s: línea %d fuera de rango", f.ID, f.Line)
			continue
		}
		if got := strings.TrimSpace(lines[f.Line-1]); got != strings.TrimSpace(f.Snippet) {
			t.Errorf("%s: línea %d es %q, el snippet dice %q", f.ID, f.Line, got, strings.TrimSpace(f.Snippet))
		}
	}
}

func TestParseShapes(t *testing.T) {
	one := `{"meta":{"name":"X-1","description":"Algo"},"spec":{"level":"FINDING_LEVEL_HIGH","summary":"s","finding_categories":["FINDING_CATEGORY_VULNERABILITY"]}}`
	cases := map[string]string{
		"arreglo":       "[" + one + "]",
		"objeto":        `{"findings":[` + one + `]}`,
		"api list":      `{"list":{"objects":[` + one + `]}}`,
		"suelto":        one,
		"log previo":    "INFO scanning...\nWARN algo\n[" + one + "]\n",
		"anidado mixto": `{"result":{"a":[{"x":1},{"deep":{"items":[` + one + `]}}]}}`,
	}
	for name, doc := range cases {
		fs, err := findings.ParseOutput([]byte(doc))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if len(fs) != 1 || fs[0].Severity != findings.SevHigh || fs[0].Title != "Algo" {
			t.Errorf("%s: resultado inesperado %+v", name, fs)
		}
	}
}

func TestParseEmptyAndGarbage(t *testing.T) {
	if fs, err := findings.ParseOutput([]byte(`[]`)); err != nil || len(fs) != 0 {
		t.Errorf("[] debe dar 0 hallazgos sin error: %v %v", fs, err)
	}
	if _, err := findings.ParseOutput([]byte("")); err != findings.ErrNoJSON {
		t.Errorf("vacío debe dar ErrNoJSON, dio %v", err)
	}
	if _, err := findings.ParseOutput([]byte("esto no es json\nni esto")); err != findings.ErrNoJSON {
		t.Errorf("basura debe dar ErrNoJSON, dio %v", err)
	}
}

func TestParseSARIF(t *testing.T) {
	doc := `{"version":"2.1.0","runs":[{"tool":{"driver":{"rules":[
	  {"id":"CVE-2021-23337","shortDescription":{"text":"lodash command injection"},"properties":{"security-severity":"7.2","tags":["dependency","CVE-2021-23337"]}},
	  {"id":"generic.secrets.aws","shortDescription":{"text":"AWS key"}},
	  {"id":"javascript.sqli","shortDescription":{"text":"SQL injection"}}]}},
	 "results":[
	  {"ruleId":"CVE-2021-23337","message":{"text":"lodash 4.17.15"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"package.json"},"region":{"startLine":7}}}]},
	  {"ruleId":"generic.secrets.aws","level":"error","message":{"text":"key"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"file://a.py"},"region":{"startLine":3,"snippet":{"text":"K=1"}}}}]},
	  {"ruleId":"javascript.sqli","level":"warning","message":{"text":"concat"}}]}]}`
	fs, err := findings.ParseOutput([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) != 3 {
		t.Fatalf("esperaba 3, hay %d", len(fs))
	}
	got := map[string]findings.Finding{}
	for _, f := range fs {
		got[f.ID] = f
	}
	if f := got["CVE-2021-23337"]; f.Severity != findings.SevHigh || f.Category != findings.CatSCA || f.Line != 7 || len(f.Aliases) != 1 {
		t.Errorf("SCA SARIF: %+v", f)
	}
	if f := got["generic.secrets.aws"]; f.Category != findings.CatSecrets || f.File != "a.py" || f.Severity != findings.SevHigh || f.Snippet != "K=1" {
		t.Errorf("secreto SARIF: %+v", f)
	}
	if f := got["javascript.sqli"]; f.Category != findings.CatSAST || f.Severity != findings.SevMedium {
		t.Errorf("SAST SARIF: %+v", f)
	}
}

func TestSplitRef(t *testing.T) {
	cases := []struct{ in, eco, name, ver string }{
		{"npm://lodash@4.17.15", "npm", "lodash", "4.17.15"},
		{"npm://@scope/pkg@1.0.0", "npm", "@scope/pkg", "1.0.0"},
		{"mvn://org.foo:bar@2.0", "maven", "org.foo:bar", "2.0"},
		{"go://golang.org/x/text@v0.3.6", "go", "golang.org/x/text", "v0.3.6"},
		{"pypi://PyYAML@5.1", "pypi", "PyYAML", "5.1"},
		{"solo-nombre", "", "solo-nombre", ""},
	}
	for _, c := range cases {
		eco, name, ver := findings.SplitRef(c.in)
		if eco != c.eco || name != c.name || ver != c.ver {
			t.Errorf("SplitRef(%q) = %q,%q,%q", c.in, eco, name, ver)
		}
	}
}

func TestCountsAndSeverity(t *testing.T) {
	c := findings.Counts{Critical: 1, High: 2, Medium: 3, Low: 4}
	if c.Total() != 10 || c.AtLeast(findings.SevHigh) != 3 || c.AtLeast(findings.SevLow) != 10 || c.AtLeast(findings.SevCritical) != 1 {
		t.Errorf("conteos: %+v", c)
	}
	if c.Risk() != 10+10+6+4 {
		t.Errorf("riesgo: %d", c.Risk())
	}
	if (findings.Counts{Critical: 50}).Risk() != 100 {
		t.Error("el riesgo debe topar en 100")
	}
	if s, ok := findings.ParseSeverity("HIGH"); !ok || s != findings.SevHigh {
		t.Error("ParseSeverity HIGH")
	}
	if _, ok := findings.ParseSeverity("nada"); ok {
		t.Error("ParseSeverity inválido debería fallar")
	}
}
