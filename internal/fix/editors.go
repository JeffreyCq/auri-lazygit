package fix

import (
	"regexp"
	"strings"
)

// editor modifica el contenido de un manifiesto. Devuelve el texto nuevo, cuántas
// declaraciones de la dependencia encontró (matched) y cuántas modificó (changed).
type editor func(src, rel, name, to string) (after string, matched, changed int)

var editors = map[string]editor{
	"npm":   editNPM,
	"go":    editGo,
	"pypi":  editPyPI,
	"maven": editMaven,
	"cargo": editCargo,
}

// replaceGroups reemplaza, por cada coincidencia de re, el grupo `verGroup` (1-based)
// por to, conservando el resto. Cuenta coincidencias y cambios reales.
func replaceGroups(re *regexp.Regexp, src string, verGroup int, to func(old string) string) (string, int, int) {
	var b strings.Builder
	last, matched, changed := 0, 0, 0
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		s, e := m[2*verGroup], m[2*verGroup+1]
		if s < 0 {
			continue
		}
		matched++
		old := src[s:e]
		neu := to(old)
		b.WriteString(src[last:s])
		b.WriteString(neu)
		last = e
		if neu != old {
			changed++
		}
	}
	b.WriteString(src[last:])
	return b.String(), matched, changed
}

// ---- npm: package.json ----

func editNPM(src, _, name, to string) (string, int, int) {
	to = strings.TrimPrefix(to, "v")
	// "name": "<prefijo><versión>"  (conserva ^, ~, >=; ignora workspace:, file:, git+…, latest)
	re := regexp.MustCompile(`(?m)^\s*"` + regexp.QuoteMeta(name) + `"\s*:\s*"[\^~<>=\s]*(\d[^"]*)"`)
	return replaceGroups(re, src, 1, func(string) string { return to })
}

// ---- go: go.mod ----

func editGo(src, _, name, to string) (string, int, int) {
	if !strings.HasPrefix(to, "v") {
		to = "v" + to
	}
	re := regexp.MustCompile(`^(\s*(?:require\s+)?` + regexp.QuoteMeta(name) + `\s+)(v\S+)(.*)$`)
	lines := strings.Split(src, "\n")
	matched, changed := 0, 0
	for i, ln := range lines {
		if strings.Contains(ln, "=>") {
			continue
		}
		m := re.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		matched++
		if m[2] != to {
			lines[i] = m[1] + to + m[3]
			changed++
		}
	}
	return strings.Join(lines, "\n"), matched, changed
}

// ---- pypi: requirements*.txt y pyproject.toml ----

// pyNamePattern hace equivalentes -, _ y . como en PEP 503, sin distinguir mayúsculas.
func pyNamePattern(name string) string {
	parts := regexp.MustCompile(`[-_.]+`).Split(name, -1)
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return strings.Join(parts, `[-_.]+`)
}

func editPyPI(src, rel, name, to string) (string, int, int) {
	to = strings.TrimPrefix(to, "v")
	np := pyNamePattern(name)
	versionTo := func(string) string { return to }
	if strings.HasSuffix(rel, "pyproject.toml") {
		// 1) especificaciones PEP 508 entre comillas: "requests>=2.0"
		re1 := regexp.MustCompile(`(?i)["']` + np + `(?:\[[^\]]*\])?\s*(?:===|==|~=|>=)\s*(\d[A-Za-z0-9._+!*-]*)`)
		out, m1, c1 := replaceGroups(re1, src, 1, versionTo)
		// 2) Poetry: name = "^1.2.3"
		re2 := regexp.MustCompile(`(?im)^\s*` + np + `\s*=\s*"[\^~<>=\s]*(\d[^"]*)"`)
		out, m2, c2 := replaceGroups(re2, out, 1, versionTo)
		return out, m1 + m2, c1 + c2
	}
	// requirements.txt: name[extras]==1.2.3   (se ignoran rangos compuestos como ">=1,<2")
	re := regexp.MustCompile(`(?im)^\s*` + np + `(?:\[[^\]]*\])?\s*(?:===|==|~=|>=)\s*(\d[A-Za-z0-9._+!*-]*)([^\n]*)$`)
	matches := re.FindAllStringSubmatchIndex(src, -1)
	var b strings.Builder
	last, matched, changed := 0, 0, 0
	for _, m := range matches {
		rest := src[m[4]:m[5]]
		if strings.HasPrefix(strings.TrimSpace(rest), ",") {
			continue // rango compuesto: no se edita automáticamente
		}
		matched++
		s, e := m[2], m[3]
		b.WriteString(src[last:s])
		b.WriteString(to)
		last = e
		if src[s:e] != to {
			changed++
		}
	}
	b.WriteString(src[last:])
	return b.String(), matched, changed
}

// ---- maven: pom.xml ----

func editMaven(src, _, name, to string) (string, int, int) {
	group, artifact := "", name
	if i := strings.Index(name, ":"); i >= 0 {
		group, artifact = name[:i], name[i+1:]
	}
	to = strings.TrimPrefix(to, "v")
	var re *regexp.Regexp
	if group != "" {
		re = regexp.MustCompile(`(?s)<dependency>\s*<groupId>\s*` + regexp.QuoteMeta(group) + `\s*</groupId>\s*<artifactId>\s*` +
			regexp.QuoteMeta(artifact) + `\s*</artifactId>\s*<version>\s*([^<\s]+)\s*</version>`)
	} else {
		re = regexp.MustCompile(`(?s)<dependency>\s*<groupId>[^<]*</groupId>\s*<artifactId>\s*` +
			regexp.QuoteMeta(artifact) + `\s*</artifactId>\s*<version>\s*([^<\s]+)\s*</version>`)
	}
	propRe := regexp.MustCompile(`^\$\{([^}]+)\}$`)
	// Si la versión es una propiedad ${x.version}, se edita la propiedad.
	var prop string
	out, matched, changed := replaceGroups(re, src, 1, func(old string) string {
		if m := propRe.FindStringSubmatch(old); m != nil {
			prop = m[1]
			return old
		}
		return to
	})
	if prop != "" {
		pre := regexp.MustCompile(`<` + regexp.QuoteMeta(prop) + `>\s*([^<\s]+)\s*</` + regexp.QuoteMeta(prop) + `>`)
		o2, m2, c2 := replaceGroups(pre, out, 1, func(string) string { return to })
		return o2, matched + m2 - 1, changed + c2
	}
	return out, matched, changed
}

// ---- cargo: Cargo.toml ----

func editCargo(src, _, name, to string) (string, int, int) {
	to = strings.TrimPrefix(to, "v")
	np := strings.NewReplacer(`\-`, `[-_]`, `\_`, `[-_]`, `-`, `[-_]`, `_`, `[-_]`).Replace(regexp.QuoteMeta(name))
	versionTo := func(string) string { return to }
	re1 := regexp.MustCompile(`(?m)^\s*` + np + `\s*=\s*"[\^~=<>\s]*(\d[^"]*)"`)
	out, m1, c1 := replaceGroups(re1, src, 1, versionTo)
	re2 := regexp.MustCompile(`(?m)^\s*` + np + `\s*=\s*\{[^}\n]*?\bversion\s*=\s*"[\^~=<>\s]*(\d[^"]*)"`)
	out, m2, c2 := replaceGroups(re2, out, 1, versionTo)
	return out, m1 + m2, c1 + c2
}
