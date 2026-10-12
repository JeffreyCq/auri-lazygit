// Package deps descubre manifiestos de dependencias en un árbol de
// directorios (package.json, go.mod, requirements.txt/pyproject.toml,
// pom.xml) y extrae de ellos las tripletas (ecosistema, nombre, versión)
// declaradas. Deliberadamente solo resuelve dependencias fijadas a una
// versión concreta: un rango o alias no se puede consultar contra una base
// de datos de vulnerabilidades sin antes elegir una versión, así que esos
// casos se omiten en vez de adivinarlos.
package deps

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Dependency es una tripleta (ecosistema, nombre, versión) resuelta,
// encontrada en uno o más manifiestos.
type Dependency struct {
	Ecosystem string // npm, go, pypi, maven
	Name      string
	Version   string
	Direct    bool     // false solo para requires de go.mod con "// indirect"
	Manifests []string // rutas relativas (con "/") donde se declaró
}

// Manifest es un archivo de dependencias encontrado en disco.
type Manifest struct {
	Path      string // relativa al directorio escaneado, con "/"
	Ecosystem string
}

// Result es el resultado de una llamada a Discover.
type Result struct {
	Dependencies []Dependency
	Manifests    []Manifest // manifiestos encontrados y parseados
	Unsupported  []Manifest // manifiestos encontrados pero no consultables (cargo, gem, ...)
}

var manifestNames = map[string]string{ // nombre de archivo -> ecosistema
	"package.json":   "npm",
	"go.mod":         "go",
	"pyproject.toml": "pypi",
	"pom.xml":        "maven",
}

var unsupportedNames = map[string]string{
	"Cargo.toml":      "cargo",
	"Gemfile":         "gem",
	"packages.config": "nuget",
	"composer.json":   "packagist",
}

// EcosystemFor clasifica un manifiesto por su nombre de archivo. supported es
// false para manifiestos reconocidos pero no consultables (Cargo.toml, Gemfile, ...).
func EcosystemFor(base string) (eco string, supported bool) {
	if eco, ok := manifestNames[base]; ok {
		return eco, true
	}
	if strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") {
		return "pypi", true
	}
	if eco, ok := unsupportedNames[base]; ok {
		return eco, false
	}
	return "", false
}

var skipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "target": true, "dist": true,
	"build": true, ".venv": true, "venv": true, "__pycache__": true, ".tox": true,
}

type foundManifest struct {
	rel string
	eco string
}

// Discover recorre dir (con límite de profundidad, omitiendo directorios de
// build/VCS) y parsea cada manifiesto de dependencias que reconoce.
func Discover(dir string) (*Result, error) {
	res := &Result{}
	var supported []foundManifest

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() {
			if rel != "." && (skipDirs[d.Name()] || strings.Count(rel, string(filepath.Separator)) > 5) {
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		relSlash := filepath.ToSlash(rel)
		if eco, ok := EcosystemFor(base); ok {
			supported = append(supported, foundManifest{relSlash, eco})
			res.Manifests = append(res.Manifests, Manifest{Path: relSlash, Ecosystem: eco})
		} else if eco, ok := unsupportedNames[base]; ok {
			res.Unsupported = append(res.Unsupported, Manifest{Path: relSlash, Ecosystem: eco})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(supported, func(i, j int) bool { return supported[i].rel < supported[j].rel })
	return parseManifests(dir, res, supported)
}

// DiscoverFiles clasifica y parsea solo las rutas de archivo dadas
// (relativas a dir, con "/" o separadores del SO), sin recorrer directorios.
// Se usa para acotar un escaneo a un conjunto específico de archivos cambiados.
func DiscoverFiles(dir string, files []string) (*Result, error) {
	res := &Result{}
	var supported []foundManifest
	for _, f := range files {
		relSlash := filepath.ToSlash(f)
		base := filepath.Base(relSlash)
		if eco, ok := EcosystemFor(base); ok {
			supported = append(supported, foundManifest{relSlash, eco})
			res.Manifests = append(res.Manifests, Manifest{Path: relSlash, Ecosystem: eco})
		} else if eco, ok := unsupportedNames[base]; ok {
			res.Unsupported = append(res.Unsupported, Manifest{Path: relSlash, Ecosystem: eco})
		}
	}
	sort.Slice(supported, func(i, j int) bool { return supported[i].rel < supported[j].rel })
	return parseManifests(dir, res, supported)
}

func parseManifests(dir string, res *Result, supported []foundManifest) (*Result, error) {
	type key struct{ eco, name, version string }
	byKey := map[key]*Dependency{}
	add := func(eco, name, version string, direct bool, manifest string) {
		name, version = strings.TrimSpace(name), strings.TrimSpace(version)
		if name == "" || version == "" {
			return
		}
		k := key{eco, name, version}
		if d, ok := byKey[k]; ok {
			if !containsStr(d.Manifests, manifest) {
				d.Manifests = append(d.Manifests, manifest)
			}
			d.Direct = d.Direct || direct
			return
		}
		byKey[k] = &Dependency{Ecosystem: eco, Name: name, Version: version, Direct: direct, Manifests: []string{manifest}}
	}

	for _, f := range supported {
		abs := filepath.Join(dir, filepath.FromSlash(f.rel))
		b, rerr := os.ReadFile(abs)
		if rerr != nil {
			continue
		}
		src := string(b)
		switch f.eco {
		case "npm":
			for _, e := range parsePackageJSON(src) {
				add("npm", e.name, e.version, true, f.rel)
			}
		case "go":
			for _, e := range parseGoMod(src) {
				add("go", e.name, e.version, e.direct, f.rel)
			}
		case "pypi":
			if strings.HasSuffix(f.rel, ".toml") {
				for _, e := range parsePyprojectToml(src) {
					add("pypi", e.name, e.version, true, f.rel)
				}
			} else {
				for _, e := range parseRequirementsTxt(src) {
					add("pypi", e.name, e.version, true, f.rel)
				}
			}
		case "maven":
			for _, e := range parsePomXml(src) {
				add("maven", e.name, e.version, true, f.rel)
			}
		}
	}

	for _, d := range byKey {
		res.Dependencies = append(res.Dependencies, *d)
	}
	sort.Slice(res.Dependencies, func(i, j int) bool {
		a, b := res.Dependencies[i], res.Dependencies[j]
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Version < b.Version
	})

	return res, nil
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// ---- npm: package.json ----

type rawEntry struct {
	name, version string
	direct        bool
}

func parsePackageJSON(src string) []rawEntry {
	var pkg struct {
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if err := json.Unmarshal([]byte(src), &pkg); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []rawEntry
	addMap := func(m map[string]string) {
		names := make([]string, 0, len(m))
		for n := range m {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if seen[n] {
				continue
			}
			v := cleanNPMVersion(m[n])
			if v == "" {
				continue
			}
			seen[n] = true
			out = append(out, rawEntry{name: n, version: v, direct: true})
		}
	}
	addMap(pkg.Dependencies)
	addMap(pkg.DevDependencies)
	return out
}

// cleanNPMVersion quita los prefijos de rango (^, ~, >=, etc.) y rechaza
// alias que no son una versión concreta (workspace:, file:, git+, latest, *, npm:).
func cleanNPMVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimLeft(v, "^~>=<| ")
	if v == "" || v[0] < '0' || v[0] > '9' {
		return ""
	}
	return v
}

// ---- go: go.mod ----

var goModLineRe = regexp.MustCompile(`^(\S+)\s+(v\S+)(.*)$`)
var goRequireOneLineRe = regexp.MustCompile(`^require\s+(\S+)\s+(v\S+)(.*)$`)

func parseGoMod(src string) []rawEntry {
	var out []rawEntry
	inBlock := false
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case trim == "require (":
			inBlock = true
			continue
		case inBlock && trim == ")":
			inBlock = false
			continue
		}
		if strings.Contains(trim, "=>") {
			continue
		}
		var m []string
		if inBlock {
			m = goModLineRe.FindStringSubmatch(trim)
		} else if strings.HasPrefix(trim, "require ") {
			m = goRequireOneLineRe.FindStringSubmatch(trim)
		}
		if m == nil {
			continue
		}
		name, version, rest := m[1], m[2], m[3]
		direct := !strings.Contains(rest, "// indirect")
		out = append(out, rawEntry{name: name, version: version, direct: direct})
	}
	return out
}

// ---- pypi: requirements*.txt ----

var reqPinRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)(?:\[[^\]]*\])?\s*==\s*([^\s,;]+)`)

func parseRequirementsTxt(src string) []rawEntry {
	var out []rawEntry
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") || strings.HasPrefix(trim, "-") {
			continue
		}
		m := reqPinRe.FindStringSubmatch(trim)
		if m == nil {
			continue
		}
		out = append(out, rawEntry{name: m[1], version: m[2], direct: true})
	}
	return out
}

// ---- pypi: pyproject.toml (PEP 621 + Poetry) ----

var pep621DepRe = regexp.MustCompile(`"([A-Za-z0-9][A-Za-z0-9._-]*)(?:\[[^\]]*\])?\s*==\s*([^"\s,;]+)[^"]*"`)
var poetryDepRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*=\s*"([^"]+)"`)

func parsePyprojectToml(src string) []rawEntry {
	var out []rawEntry
	seen := map[string]bool{}
	for _, m := range pep621DepRe.FindAllStringSubmatch(src, -1) {
		if seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, rawEntry{name: m[1], version: m[2], direct: true})
	}
	// Poetry section: [tool.poetry.dependencies] / [tool.poetry.group.*.dependencies]
	inPoetrySection := false
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "[") {
			inPoetrySection = strings.Contains(trim, "poetry") && strings.Contains(trim, "dependencies")
			continue
		}
		if !inPoetrySection {
			continue
		}
		m := poetryDepRe.FindStringSubmatch(line)
		if m == nil || strings.EqualFold(m[1], "python") {
			continue
		}
		v := cleanNPMVersion(m[2]) // misma regla de recorte de prefijos (^, ~, >=...)
		if v == "" || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		out = append(out, rawEntry{name: m[1], version: v, direct: true})
	}
	return out
}

// ---- maven: pom.xml ----

var propertyRe = regexp.MustCompile(`(?s)<properties>(.*?)</properties>`)
var propertyEntryRe = regexp.MustCompile(`<([A-Za-z0-9._-]+)>\s*([^<\s][^<]*)\s*</[A-Za-z0-9._-]+>`)
var dependencyBlockRe = regexp.MustCompile(`(?s)<dependency>\s*(.*?)\s*</dependency>`)
var groupIDRe = regexp.MustCompile(`<groupId>\s*([^<\s]+)\s*</groupId>`)
var artifactIDRe = regexp.MustCompile(`<artifactId>\s*([^<\s]+)\s*</artifactId>`)
var versionRe = regexp.MustCompile(`<version>\s*([^<\s]+)\s*</version>`)
var propPlaceholderRe = regexp.MustCompile(`^\$\{([^}]+)\}$`)

func parsePomXml(src string) []rawEntry {
	props := map[string]string{}
	if pm := propertyRe.FindStringSubmatch(src); pm != nil {
		for _, em := range propertyEntryRe.FindAllStringSubmatch(pm[1], -1) {
			props[em[1]] = strings.TrimSpace(em[2])
		}
	}
	var out []rawEntry
	for _, dm := range dependencyBlockRe.FindAllStringSubmatch(src, -1) {
		block := dm[1]
		gm, am, vm := groupIDRe.FindStringSubmatch(block), artifactIDRe.FindStringSubmatch(block), versionRe.FindStringSubmatch(block)
		if gm == nil || am == nil || vm == nil {
			continue
		}
		version := vm[1]
		if pp := propPlaceholderRe.FindStringSubmatch(version); pp != nil {
			resolved, ok := props[pp[1]]
			if !ok {
				continue
			}
			version = resolved
		}
		if propPlaceholderRe.MatchString(version) || version == "" {
			continue
		}
		out = append(out, rawEntry{name: gm[1] + ":" + am[1], version: version, direct: true})
	}
	return out
}
