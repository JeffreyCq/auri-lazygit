package fix

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"auri-lazygit/internal/demo"
	"auri-lazygit/internal/findings"
)

func demoRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range demo.Files {
		p := filepath.Join(dir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func demoFinding(t *testing.T, id string) findings.Finding {
	t.Helper()
	fs, err := findings.ParseOutput(demo.FindingsJSON())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.ID == id {
			return f
		}
	}
	t.Fatalf("no existe el hallazgo %s", id)
	return findings.Finding{}
}

func TestEditNPMPreservesPrefixAndSkipsOthers(t *testing.T) {
	src := `{
  "dependencies": {
    "lodash": "^4.17.15",
    "lodash-es": "4.17.15",
    "local": "file:../lodash",
    "minimist": "~1.2.0"
  }
}`
	out, matched, changed := editNPM(src, "package.json", "lodash", "4.17.21")
	if matched != 1 || changed != 1 {
		t.Fatalf("matched=%d changed=%d", matched, changed)
	}
	if !strings.Contains(out, `"lodash": "^4.17.21"`) {
		t.Errorf("no preservó el prefijo ^:\n%s", out)
	}
	if !strings.Contains(out, `"lodash-es": "4.17.15"`) {
		t.Errorf("tocó lodash-es:\n%s", out)
	}
	if _, m, _ := editNPM(src, "package.json", "local", "2.0.0"); m != 0 {
		t.Error("no debe editar versiones que no son semver (file:)")
	}
	if _, m, c := editNPM(out, "package.json", "lodash", "4.17.21"); m != 1 || c != 0 {
		t.Errorf("idempotencia: matched=%d changed=%d", m, c)
	}
}

func TestEditGo(t *testing.T) {
	src := "module x\n\nrequire (\n\tgolang.org/x/text v0.3.6\n\tgolang.org/x/net v0.1.0 // indirect\n)\n\nreplace golang.org/x/net v0.1.0 => ./net\n"
	out, matched, changed := editGo(src, "go.mod", "golang.org/x/text", "0.3.7")
	if matched != 1 || changed != 1 || !strings.Contains(out, "golang.org/x/text v0.3.7\n") {
		t.Errorf("go: %d %d\n%s", matched, changed, out)
	}
	out, _, _ = editGo(src, "go.mod", "golang.org/x/net", "v0.2.0")
	if !strings.Contains(out, "v0.2.0 // indirect") {
		t.Errorf("perdió el comentario indirect:\n%s", out)
	}
	if !strings.Contains(out, "replace golang.org/x/net v0.1.0 => ./net") {
		t.Errorf("modificó la línea replace:\n%s", out)
	}
	one := "require golang.org/x/text v0.3.6\n"
	if o, _, _ := editGo(one, "go.mod", "golang.org/x/text", "v0.3.7"); o != "require golang.org/x/text v0.3.7\n" {
		t.Errorf("require en una línea: %q", o)
	}
}

func TestEditPyPI(t *testing.T) {
	src := "# deps\nrequests==2.19.0\nPyYAML>=5.1 ; python_version>'3'\nflask>=2.0,<3\nDjango[argon2] == 3.0.0  # viejo\npy_yaml_extra==1.0\n"
	cases := []struct {
		name, to, want string
	}{
		{"requests", "2.20.0", "requests==2.20.0\n"},
		{"pyyaml", "5.4", "PyYAML>=5.4 ; python_version>'3'\n"},
		{"django", "3.2.1", "Django[argon2] == 3.2.1  # viejo\n"},
	}
	for _, c := range cases {
		out, m, ch := editPyPI(src, "requirements.txt", c.name, c.to)
		if m != 1 || ch != 1 || !strings.Contains(out, c.want) {
			t.Errorf("%s: matched=%d changed=%d\n%s", c.name, m, ch, out)
		}
	}
	if _, m, _ := editPyPI(src, "requirements.txt", "flask", "2.1"); m != 0 {
		t.Error("rango compuesto >=2.0,<3 no debe editarse automáticamente")
	}
	// equivalencia - _ .
	if out, m, _ := editPyPI(src, "requirements.txt", "py-yaml.extra", "2.0"); m != 1 || !strings.Contains(out, "py_yaml_extra==2.0") {
		t.Errorf("normalización PEP 503: %d\n%s", m, out)
	}
	toml := "[project]\ndependencies = [\n  \"requests>=2.19.0\",\n  \"flask\",\n]\n\n[tool.poetry.dependencies]\nrequests = \"^2.19.0\"\npython = \"^3.10\"\n"
	out, m, ch := editPyPI(toml, "pyproject.toml", "requests", "2.20.0")
	if m != 2 || ch != 2 || !strings.Contains(out, `"requests>=2.20.0"`) || !strings.Contains(out, `requests = "^2.20.0"`) {
		t.Errorf("pyproject: matched=%d changed=%d\n%s", m, ch, out)
	}
}

func TestEditMaven(t *testing.T) {
	src := `<project>
  <properties>
    <jackson.version>2.9.0</jackson.version>
  </properties>
  <dependencies>
    <dependency>
      <groupId>org.apache.logging.log4j</groupId>
      <artifactId>log4j-core</artifactId>
      <version>2.14.1</version>
    </dependency>
    <dependency>
      <groupId>com.fasterxml.jackson.core</groupId>
      <artifactId>jackson-databind</artifactId>
      <version>${jackson.version}</version>
    </dependency>
  </dependencies>
</project>
`
	out, m, c := editMaven(src, "pom.xml", "org.apache.logging.log4j:log4j-core", "2.17.1")
	if m != 1 || c != 1 || !strings.Contains(out, "<version>2.17.1</version>") {
		t.Errorf("literal: %d %d\n%s", m, c, out)
	}
	out, m, c = editMaven(src, "pom.xml", "com.fasterxml.jackson.core:jackson-databind", "2.13.4")
	if m != 1 || c != 1 || !strings.Contains(out, "<jackson.version>2.13.4</jackson.version>") || !strings.Contains(out, "${jackson.version}") {
		t.Errorf("propiedad: %d %d\n%s", m, c, out)
	}
	if _, m, _ := editMaven(src, "pom.xml", "org.otro:nada", "1"); m != 0 {
		t.Error("no debe encontrar dependencias inexistentes")
	}
}

func TestEditCargo(t *testing.T) {
	src := "[dependencies]\nserde = \"1.0.100\"\ntokio-util = { version = \"0.7.0\", features = [\"io\"] }\nreqwest = { git = \"https://x\" }\n"
	out, m, c := editCargo(src, "Cargo.toml", "serde", "1.0.200")
	if m != 1 || c != 1 || !strings.Contains(out, `serde = "1.0.200"`) {
		t.Errorf("serde: %d %d\n%s", m, c, out)
	}
	out, m, c = editCargo(src, "Cargo.toml", "tokio_util", "0.7.10")
	if m != 1 || c != 1 || !strings.Contains(out, `version = "0.7.10", features`) {
		t.Errorf("tabla inline: %d %d\n%s", m, c, out)
	}
}

func TestMakeDiffHunksAndContext(t *testing.T) {
	before := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj\n"
	after := "a\nB\nc\nd\ne\nf\ng\nh\nI\nj\n"
	d := MakeDiff(before, after, 1)
	var kinds strings.Builder
	for _, l := range d {
		kinds.WriteByte(l.Kind)
	}
	// ctx(a) -b +B ctx(c) ... ctx(h) -i +I ctx(j)
	if kinds.String() != " -+ @ -+ " {
		t.Errorf("forma del diff: %q (%+v)", kinds.String(), d)
	}
	if d[1].No != 2 || d[2].Text != "B" {
		t.Errorf("números/texto: %+v", d[:3])
	}
	if MakeDiff(before, before, 2) != nil {
		t.Error("sin cambios debe devolver nil")
	}
}

func TestBuildAndApplyDemoFindings(t *testing.T) {
	repo := demoRepo(t)

	// directo con upgrade: lodash en package.json, conserva el formato del resto
	p, err := Build(demoFinding(t, "GHSA-35jh-r3h4-6jhm"), repo)
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest != "package.json" || p.Package != "lodash" || p.From != "4.17.15" || p.To != "4.17.21" || p.Changes != 1 {
		t.Fatalf("plan: %+v", p)
	}
	if !strings.Contains(p.After, `"lodash": "4.17.21"`) || !strings.Contains(p.After, `"express": "^4.17.1"`) {
		t.Errorf("After inesperado:\n%s", p.After)
	}
	if err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	disk, _ := os.ReadFile(filepath.Join(repo, "package.json"))
	if string(disk) != p.After {
		t.Error("el archivo en disco no coincide con el plan")
	}
	if st, _ := os.Stat(filepath.Join(repo, "package.json")); st.Mode().Perm() != 0o644 {
		t.Errorf("permisos alterados: %v", st.Mode())
	}
	if err := p.Apply(); err == nil {
		t.Error("aplicar dos veces debe fallar: el archivo cambió desde el plan")
	}
	if left, _ := filepath.Glob(filepath.Join(repo, ".auri-fix-*")); len(left) != 0 {
		t.Errorf("quedaron temporales: %v", left)
	}
	// ya aplicado → ErrAlreadyFixed
	if _, err := Build(demoFinding(t, "GHSA-35jh-r3h4-6jhm"), repo); !errors.Is(err, ErrAlreadyFixed) {
		t.Errorf("esperaba ErrAlreadyFixed, dio %v", err)
	}

	// transitivo con upgrade de la directa: qs -> sube express
	p, err = Build(demoFinding(t, "GHSA-hrpp-h998-j3pp"), repo)
	if err != nil {
		t.Fatal(err)
	}
	if p.Package != "express" || p.To != "4.17.3" || !strings.Contains(p.After, `"express": "^4.17.3"`) {
		t.Errorf("plan transitivo: %+v", p)
	}

	// pypi, go
	p, err = Build(demoFinding(t, "GHSA-w596-4wvx-j9j6"), repo)
	if err != nil || p.Manifest != "requirements.txt" || !strings.Contains(p.After, "PyYAML==5.4") {
		t.Errorf("pypi: %+v %v", p, err)
	}
	p, err = Build(demoFinding(t, "GO-2021-0113"), repo)
	if err != nil || p.Manifest != "go.mod" || !strings.Contains(p.After, "golang.org/x/text v0.3.7") {
		t.Errorf("go: %+v %v", p, err)
	}
	if p != nil && p.PostCommand() != "go mod tidy" {
		t.Errorf("PostCommand go: %q", p.PostCommand())
	}
}

func TestBuildRefusals(t *testing.T) {
	repo := demoRepo(t)
	for _, id := range []string{"aws-access-key", "sql-injection", "malware-reqeusts", "GHSA-9vvw-cc9w-f27h"} {
		_, err := Build(demoFinding(t, id), repo)
		if !errors.Is(err, ErrNoAutoFix) {
			t.Errorf("%s: esperaba ErrNoAutoFix, dio %v", id, err)
		}
	}
	// ecosistema sin editor
	f := findings.Finding{Category: findings.CatSCA, Ecosystem: "nuget", Package: "X", Version: "1", Fixed: "2", Direct: true}
	if _, err := Build(f, repo); !errors.Is(err, ErrNoAutoFix) {
		t.Errorf("nuget: %v", err)
	}
	// manifiesto inexistente
	empty := t.TempDir()
	f = findings.Finding{Category: findings.CatSCA, Ecosystem: "npm", Package: "x", Version: "1.0.0", Fixed: "1.0.1", Direct: true}
	if _, err := Build(f, empty); !errors.Is(err, ErrNoAutoFix) {
		t.Errorf("sin manifiesto: %v", err)
	}
	// ruta del hallazgo con escape de directorio: se ignora
	f.Manifests = []string{"../../etc/passwd", "/etc/passwd"}
	if _, err := Build(f, repo); !errors.Is(err, ErrNoAutoFix) {
		t.Errorf("rutas fuera del repo deben ignorarse: %v", err)
	}
}

func TestBuildFindsNestedManifestsAndSkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules", "lodash"), 0o755)
	os.MkdirAll(filepath.Join(dir, "services", "api"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules", "lodash", "package.json"), []byte(`{"dependencies":{"lodash":"1.0.0"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "services", "api", "package.json"), []byte("{\n  \"dependencies\": {\n    \"lodash\": \"^4.17.15\"\n  }\n}\n"), 0o600)
	f := findings.Finding{Category: findings.CatSCA, Ecosystem: "npm", Package: "lodash", Version: "4.17.15", Fixed: "4.17.21", Direct: true}
	p, err := Build(f, dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Manifest != "services/api/package.json" {
		t.Errorf("manifiesto = %s", p.Manifest)
	}
	if err := p.Apply(); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p.Abs); st.Mode().Perm() != 0o600 {
		t.Errorf("no preservó permisos 0600: %v", st.Mode().Perm())
	}
}

func TestBranchAndCommitMessage(t *testing.T) {
	p := &Plan{Package: "@scope/Pkg_Name", From: "1.0.0", To: "2.0.0+build"}
	b := p.BranchName()
	t.Logf("rama=%q", b)
	if !strings.HasPrefix(b, "auri/fix-") || strings.ContainsAny(b, " @/:~^?*[\\") && strings.Contains(strings.TrimPrefix(b, "auri/"), "/") {
		t.Errorf("rama insegura: %q", b)
	}
	if strings.Contains(b, "..") || strings.HasSuffix(b, ".") || strings.HasSuffix(b, "-") {
		t.Errorf("rama inválida: %q", b)
	}
	msg := p.CommitMessage([]string{"CVE-1", "GHSA-2"})
	if !strings.HasPrefix(msg, "fix(deps): bump @scope/Pkg_Name from 1.0.0 to 2.0.0+build") || !strings.Contains(msg, "CVE-1, GHSA-2") {
		t.Errorf("mensaje: %q", msg)
	}
}
