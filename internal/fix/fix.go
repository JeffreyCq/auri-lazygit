// Package fix genera y aplica remediaciones de dependencias: sube la versión de
// una dependencia directa en su manifiesto (package.json, go.mod, requirements.txt,
// pyproject.toml, pom.xml, Cargo.toml) tras mostrar un diff y pedir confirmación.
//
// Solo toca la línea de la versión; no regenera lockfiles (te sugiere el comando).
package fix

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"auri-lazygit/internal/findings"
)

// ErrNoAutoFix indica que el hallazgo no se puede remediar automáticamente; el
// mensaje explica por qué.
var ErrNoAutoFix = errors.New("no automatic fix available")

// ErrAlreadyFixed indica que el manifiesto ya tiene la versión objetivo.
var ErrAlreadyFixed = errors.New("manifest already has the target version")

// DiffLine es una línea del diff: Kind ' ' contexto, '-' quitada, '+' añadida, '@' separador.
type DiffLine struct {
	Kind byte
	No   int
	Text string
}

// Plan es una remediación lista para mostrar y aplicar.
type Plan struct {
	Ecosystem string
	Package   string
	From, To  string
	Manifest  string // relativo a la raíz del repo, con "/"
	Abs       string
	Before    string
	After     string
	Changes   int
	Diff      []DiffLine
	Mode      os.FileMode
}

func noAutoFix(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrNoAutoFix, fmt.Sprintf(format, a...))
}

// target decide qué dependencia directa hay que subir y a qué versión.
func target(f findings.Finding) (eco, name, from, to string, err error) {
	switch f.Category {
	case findings.CatSecrets:
		return "", "", "", "", noAutoFix("exposed secret: revoke/rotate it and remove it from the code (editing a manifest isn't enough)")
	case findings.CatSAST:
		return "", "", "", "", noAutoFix("code-level finding: follow the remediation guidance in the detail panel")
	case findings.CatMalware:
		return "", "", "", "", noAutoFix("malicious package: remove it, rotate any exposed credentials, and install the legitimate package")
	}
	if f.Upgrade != nil && f.Upgrade.To != "" {
		// El sufijo de versión del propio ref (si lo tiene) es la versión
		// recomendada/fija, no la instalada actualmente — "from" siempre se
		// toma de Upgrade.From en vez de parsearlo del ref.
		e, n, _ := findings.SplitRef(f.Upgrade.Package)
		if n != "" {
			if e == "" {
				e = f.Ecosystem
			}
			from := f.Upgrade.From
			if from == "" {
				from = f.Version
			}
			return e, n, from, f.Upgrade.To, nil
		}
	}
	if f.Fixed != "" && f.Package != "" && f.Direct {
		return f.Ecosystem, f.Package, f.Version, f.Fixed, nil
	}
	switch {
	case f.Fixed == "" && f.Upgrade == nil:
		return "", "", "", "", noAutoFix("AURI did not report a fixed version for %s", f.Package)
	case f.Transitive:
		return "", "", "", "", noAutoFix("%s is transitive and no direct upgrade was reported; bump the direct dependency that pulls it in", f.Package)
	}
	return "", "", "", "", noAutoFix("could not determine which direct dependency to upgrade")
}

// Build prepara el plan de remediación de f sobre el repositorio en repoDir.
func Build(f findings.Finding, repoDir string) (*Plan, error) {
	eco, name, from, to, err := target(f)
	if err != nil {
		return nil, err
	}
	edit, ok := editors[eco]
	if !ok {
		return nil, noAutoFix("ecosystem %q not yet supported for automatic editing (update %s to %s by hand)", eco, name, to)
	}
	cands := candidates(repoDir, eco, f.Manifests)
	if len(cands) == 0 {
		return nil, noAutoFix("could not find a %s manifest in the repository", eco)
	}
	alreadyAt := ""
	var tried []string
	for _, rel := range cands {
		abs := filepath.Join(repoDir, filepath.FromSlash(rel))
		b, rerr := os.ReadFile(abs)
		if rerr != nil {
			continue
		}
		tried = append(tried, rel)
		after, matched, changed := edit(string(b), rel, name, to)
		if matched == 0 {
			continue
		}
		if changed == 0 {
			alreadyAt = rel
			continue
		}
		mode := os.FileMode(0o644)
		if st, serr := os.Stat(abs); serr == nil {
			mode = st.Mode().Perm()
		}
		return &Plan{
			Ecosystem: eco, Package: name, From: from, To: to,
			Manifest: rel, Abs: abs, Before: string(b), After: after,
			Changes: changed, Diff: MakeDiff(string(b), after, 2), Mode: mode,
		}, nil
	}
	if alreadyAt != "" {
		return nil, fmt.Errorf("%w (%s already declares %s %s)", ErrAlreadyFixed, alreadyAt, name, to)
	}
	return nil, noAutoFix("could not find %q declared with an editable version in: %s (composite constraint, or managed by another tool?)", name, strings.Join(tried, ", "))
}

// Apply escribe el manifiesto modificado. Falla si el archivo cambió desde que se creó el plan.
func (p *Plan) Apply() error {
	cur, err := os.ReadFile(p.Abs)
	if err != nil {
		return err
	}
	if string(cur) != p.Before {
		return errors.New("the manifest changed since the plan was built; reopen the fix")
	}
	tmp, err := os.CreateTemp(filepath.Dir(p.Abs), ".auri-fix-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.WriteString(p.After); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, p.Mode); err != nil {
		return err
	}
	return os.Rename(tmpName, p.Abs)
}

// PostCommand sugiere el comando para regenerar el lockfile tras editar el manifiesto.
func (p *Plan) PostCommand() string {
	switch p.Ecosystem {
	case "npm":
		return "npm install   # (or pnpm install / yarn) to refresh the lockfile"
	case "go":
		return "go mod tidy"
	case "pypi":
		return "pip install -r requirements.txt   # and recompile your lock if using pip-tools/poetry"
	case "maven":
		return "mvn -q dependency:resolve"
	case "cargo":
		return "cargo update -p " + p.Package
	}
	return ""
}

// ---- búsqueda de manifiestos ----

var manifestNames = map[string][]string{
	"npm":       {"package.json"},
	"go":        {"go.mod"},
	"pypi":      {"requirements.txt", "pyproject.toml"},
	"maven":     {"pom.xml"},
	"cargo":     {"Cargo.toml"},
	"gem":       {"Gemfile"},
	"nuget":     {"packages.config"},
	"packagist": {"composer.json"},
}

func isManifest(eco, base string) bool {
	for _, n := range manifestNames[eco] {
		if base == n {
			return true
		}
	}
	if eco == "pypi" && strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return true
	}
	return false
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true, "dist": true,
	"build": true, ".venv": true, "venv": true, "__pycache__": true, ".tox": true,
}

// candidates lista manifiestos posibles: primero los que reporta el hallazgo, luego los del árbol.
func candidates(repoDir, eco string, reported []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(rel string) {
		rel = filepath.ToSlash(filepath.Clean(rel))
		if rel == "." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) || seen[rel] {
			return
		}
		if !isManifest(eco, filepath.Base(rel)) {
			return
		}
		if st, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(rel))); err != nil || st.IsDir() {
			return
		}
		seen[rel] = true
		out = append(out, rel)
	}
	for _, r := range reported {
		add(r)
	}
	var found []string
	_ = filepath.WalkDir(repoDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(repoDir, path)
		depth := strings.Count(rel, string(filepath.Separator))
		if d.IsDir() {
			if skipDirs[d.Name()] || depth > 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if isManifest(eco, d.Name()) {
			found = append(found, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.SliceStable(found, func(i, j int) bool {
		di, dj := strings.Count(found[i], "/"), strings.Count(found[j], "/")
		if di != dj {
			return di < dj
		}
		return found[i] < found[j]
	})
	for _, r := range found {
		add(r)
	}
	return out
}

// ---- diff ----

// MakeDiff produce un diff por líneas con ctx líneas de contexto. Pensado para
// ediciones que no cambian el número de líneas (que es lo que hacen los editores).
func MakeDiff(before, after string, ctx int) []DiffLine {
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var changed []int
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			changed = append(changed, i)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	var out []DiffLine
	lastEnd := -1
	i := 0
	for i < len(changed) {
		start := changed[i] - ctx
		if start < 0 {
			start = 0
		}
		end := changed[i] + ctx
		j := i
		for j+1 < len(changed) && changed[j+1]-ctx <= end+1 {
			j++
			end = changed[j] + ctx
		}
		if end >= n {
			end = n - 1
		}
		if lastEnd >= 0 && start > lastEnd+1 {
			out = append(out, DiffLine{Kind: '@', Text: "⋯"})
		}
		isChanged := map[int]bool{}
		for _, c := range changed[i : j+1] {
			isChanged[c] = true
		}
		for k := start; k <= end; k++ {
			if isChanged[k] {
				out = append(out, DiffLine{Kind: '-', No: k + 1, Text: a[k]})
				out = append(out, DiffLine{Kind: '+', No: k + 1, Text: b[k]})
			} else {
				out = append(out, DiffLine{Kind: ' ', No: k + 1, Text: a[k]})
			}
		}
		lastEnd = end
		i = j + 1
	}
	return out
}
