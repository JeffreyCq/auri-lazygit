// Package endor construye y ejecuta `endorctl scan` y traduce su salida a
// hallazgos normalizados.
//
// Los flags usados están documentados en
// https://docs.endorlabs.com/developers-api/cli/commands/scan/ :
//
//	--path, --dry-run, --output-type=json, --dependencies, --secrets, --sast,
//	--quick-scan, --include-path, --diff-scope=local
package endor

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"auri-lazygit/internal/gitx"
)

// Kind es el tipo de objetivo a escanear.
type Kind string

const (
	KindFile   Kind = "file"   // un archivo o carpeta
	KindLocal  Kind = "local"  // cambios locales (staged + unstaged + sin seguimiento)
	KindCommit Kind = "commit" // el árbol de un commit o rama
	KindRepo   Kind = "repo"   // el repositorio completo
)

// Target describe qué se escanea.
type Target struct {
	Kind    Kind
	RepoDir string // raíz del repositorio REAL (donde se aplican remediaciones)
	Path    string // KindFile: ruta relativa a RepoDir, con "/"
	Ref     string // KindCommit: hash o referencia
	Display string // etiqueta opcional ya calculada
}

// Label devuelve una descripción corta para la interfaz.
func (t Target) Label() string {
	if t.Display != "" {
		return t.Display
	}
	switch t.Kind {
	case KindFile:
		return "archivo " + t.Path
	case KindLocal:
		return "cambios locales"
	case KindCommit:
		return "commit " + t.Ref
	default:
		return "repositorio completo"
	}
}

// NewFileTarget valida y normaliza una ruta (absoluta o relativa al cwd) dentro del repo.
func NewFileTarget(repoDir, path string) (Target, error) {
	p := path
	if !filepath.IsAbs(p) {
		// las rutas relativas se interpretan desde el cwd del proceso (lazygit ejecuta en la raíz del repo)
		if abs, err := filepath.Abs(p); err == nil {
			p = abs
		}
	}
	rel, err := gitx.RelPath(repoDir, p)
	if err != nil {
		return Target{}, err
	}
	if _, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(rel))); err != nil {
		return Target{}, fmt.Errorf("no existe %s", rel)
	}
	return Target{Kind: KindFile, RepoDir: repoDir, Path: rel}, nil
}

// NewCommitTarget resuelve ref y arma una etiqueta con hash corto y asunto.
func NewCommitTarget(repoDir, ref string) (Target, error) {
	c, err := gitx.ResolveCommit(repoDir, ref)
	if err != nil {
		return Target{}, err
	}
	subj := c.Subject
	if r := []rune(subj); len(r) > 48 {
		subj = string(r[:47]) + "…"
	}
	disp := "commit " + c.Short
	if subj != "" {
		disp += " · " + subj
	}
	return Target{Kind: KindCommit, RepoDir: repoDir, Ref: c.Hash, Display: disp}, nil
}

// Options controla qué escáneres se ejecutan y cómo.
type Options struct {
	Deps, Secrets, SAST bool
	Quick               bool // --quick-scan: sin call graph (más rápido, sin alcanzabilidad)
	Bin                 []string
	ExtraArgs           []string
	Timeout             time.Duration
	SaveRaw             string // si no está vacío, guarda stdout crudo en esa ruta
}

// DefaultOptions: todos los escáneres, modo rápido, 10 minutos de límite.
func DefaultOptions() Options {
	return Options{Deps: true, Secrets: true, SAST: true, Quick: true, Timeout: 10 * time.Minute}
}

// ScannersLabel resume los escáneres activos, p. ej. "SCA + Secretos + SAST".
func (o Options) ScannersLabel() string {
	var p []string
	if o.Deps {
		p = append(p, "SCA")
	}
	if o.Secrets {
		p = append(p, "Secretos")
	}
	if o.SAST {
		p = append(p, "SAST")
	}
	if len(p) == 0 {
		return "(ninguno)"
	}
	s := strings.Join(p, " + ")
	if o.Quick {
		s += " · rápido"
	}
	return s
}

// ErrNoEndorctl: no hay endorctl ni npx en el PATH.
var ErrNoEndorctl = errors.New("no se encontró `endorctl` ni `npx` en el PATH")

// ResolveBin decide cómo invocar endorctl:
//  1. $AURI_ENDORCTL (p. ej. "endorctl" o "npx -y endorctl"),
//  2. `endorctl` en el PATH,
//  3. `npx -y endorctl` (el mismo paquete que usa el servidor MCP de AURI).
func ResolveBin() ([]string, error) {
	if v := strings.TrimSpace(os.Getenv("AURI_ENDORCTL")); v != "" {
		return strings.Fields(v), nil
	}
	if p, err := exec.LookPath("endorctl"); err == nil {
		return []string{p}, nil
	}
	if p, err := exec.LookPath("npx"); err == nil {
		return []string{p, "-y", "endorctl"}, nil
	}
	return nil, ErrNoEndorctl
}

// depFiles son los nombres de archivo para los que tiene sentido el escaneo SCA.
var depFiles = map[string]bool{
	"package.json": true, "package-lock.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"go.mod": true, "go.sum": true, "pyproject.toml": true, "poetry.lock": true, "pipfile": true,
	"pipfile.lock": true, "pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
	"cargo.toml": true, "cargo.lock": true, "gemfile": true, "gemfile.lock": true,
	"composer.json": true, "composer.lock": true, "packages.config": true,
}

func isDepFile(base string) bool {
	b := strings.ToLower(base)
	return depFiles[b] || (strings.HasPrefix(b, "requirements") && strings.HasSuffix(b, ".txt")) || strings.HasSuffix(b, ".csproj")
}

// BuildArgs devuelve los argumentos de `endorctl` (sin el binario) para escanear
// scanDir. scanDir es la raíz del repo, o un worktree temporal para commits.
func BuildArgs(t Target, scanDir string, o Options) ([]string, []string, error) {
	deps, secrets, sast := o.Deps, o.Secrets, o.SAST
	var notes []string

	var extra []string
	switch t.Kind {
	case KindFile:
		abs := filepath.Join(scanDir, filepath.FromSlash(t.Path))
		st, err := os.Stat(abs)
		if err != nil {
			return nil, nil, fmt.Errorf("no existe %s", t.Path)
		}
		if st.IsDir() {
			extra = append(extra, "--include-path="+strings.TrimSuffix(t.Path, "/")+"/**")
		} else {
			extra = append(extra, "--include-path="+t.Path)
			if deps && !isDepFile(filepath.Base(t.Path)) {
				deps = false
				notes = append(notes, "SCA omitido: "+filepath.Base(t.Path)+" no es un archivo de dependencias")
			}
		}
	case KindLocal:
		if secrets || sast {
			extra = append(extra, "--diff-scope=local")
		}
	case KindCommit, KindRepo:
		// sin flags adicionales: scanDir ya es el árbol correcto
	default:
		return nil, nil, fmt.Errorf("tipo de objetivo desconocido: %q", t.Kind)
	}
	if !deps && !secrets && !sast {
		return nil, notes, errors.New("no hay escáneres aplicables: activa al menos uno (SCA, Secretos o SAST)")
	}

	args := []string{"scan", "--path", scanDir, "--dry-run", "--output-type=json"}
	if deps {
		args = append(args, "--dependencies")
	}
	if secrets {
		args = append(args, "--secrets")
	}
	if sast {
		args = append(args, "--sast")
	}
	if o.Quick {
		args = append(args, "--quick-scan")
	}
	args = append(args, extra...)
	args = append(args, o.ExtraArgs...)
	return args, notes, nil
}

// ExtraArgsFromEnv lee $AURI_ENDOR_ARGS (separados por espacios) para ajustes finos.
func ExtraArgsFromEnv() []string { return strings.Fields(os.Getenv("AURI_ENDOR_ARGS")) }

// ShellQuote muestra un argv como línea de comandos legible (solo para mostrar, no para ejecutar).
func ShellQuote(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\"'$`\\*?[]{}()<>|&;!#~") {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
