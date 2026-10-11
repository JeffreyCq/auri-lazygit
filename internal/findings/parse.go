package findings

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoJSON indica que la salida no contiene ningún documento JSON.
var ErrNoJSON = errors.New("la salida no contiene JSON")

// ParseOutput interpreta la salida de `endorctl scan -o json`.
//
// El parser es deliberadamente tolerante con la FORMA del documento: recorre el
// JSON completo y recoge cualquier objeto con la forma del recurso Finding de
// Endor Labs (spec.level = FINDING_LEVEL_*). Así funciona tanto si la salida es
// un arreglo, un objeto con una lista, o la respuesta de `endorctl api list`.
// Si el documento es SARIF (clave "runs"), usa el parser SARIF.
func ParseOutput(raw []byte) ([]Finding, error) {
	doc, err := extractJSON(raw)
	if err != nil {
		return nil, err
	}
	if m, ok := doc.(map[string]any); ok {
		if _, isSarif := m["runs"]; isSarif {
			return parseSARIF(m), nil
		}
	}
	var out []Finding
	walk(doc, func(m map[string]any) {
		out = append(out, fromFinding(m))
	})
	Sort(out)
	return out, nil
}

// extractJSON localiza el primer documento JSON válido, ignorando líneas de log
// que algunas versiones de endorctl imprimen antes del JSON.
func extractJSON(raw []byte) (any, error) {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return nil, ErrNoJSON
	}
	try := func(data []byte) (any, bool) {
		var v any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, false
		}
		return v, true
	}
	if v, ok := try(b); ok {
		return v, nil
	}
	// buscar inicio de línea que empiece con { o [
	start := 0
	for start < len(b) {
		if b[start] == '{' || b[start] == '[' {
			if start == 0 || b[start-1] == '\n' {
				if v, ok := try(b[start:]); ok {
					return v, nil
				}
			}
		}
		next := bytes.IndexByte(b[start:], '\n')
		if next < 0 {
			break
		}
		start += next + 1
	}
	return nil, ErrNoJSON
}

func isFinding(m map[string]any) bool {
	spec, ok := m["spec"].(map[string]any)
	if !ok {
		return false
	}
	lvl, ok := spec["level"].(string)
	return ok && strings.HasPrefix(lvl, "FINDING_LEVEL_")
}

// walk recorre el documento y llama a fn por cada Finding; no desciende dentro de ellos.
func walk(v any, fn func(map[string]any)) {
	switch t := v.(type) {
	case []any:
		for _, e := range t {
			walk(e, fn)
		}
	case map[string]any:
		if isFinding(t) {
			fn(t)
			return
		}
		// orden estable de claves no es necesario: Sort() lo hará después.
		for _, e := range t {
			walk(e, fn)
		}
	}
}

// ---- helpers de acceso seguro a JSON dinámico ----

func get(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func num(v any) float64 {
	switch t := v.(type) {
	case json.Number:
		f, _ := t.Float64()
		return f
	case float64:
		return t
	case string:
		f, _ := strconv.ParseFloat(t, 64)
		return f
	}
	return 0
}

func strs(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s := str(e); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func has(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func levelToSeverity(lvl string) Severity {
	switch strings.TrimPrefix(lvl, "FINDING_LEVEL_") {
	case "CRITICAL":
		return SevCritical
	case "HIGH":
		return SevHigh
	case "MEDIUM":
		return SevMedium
	}
	return SevLow
}

// fromFinding convierte un recurso Finding de Endor Labs en un Finding normalizado.
func fromFinding(m map[string]any) Finding {
	f := Finding{}
	f.ID = str(get(m, "meta", "name"))
	if u := str(m["uuid"]); u != "" && f.ID == "" {
		f.ID = u
	}
	f.Severity = levelToSeverity(str(get(m, "spec", "level")))
	f.Summary = str(get(m, "spec", "summary"))
	f.Explanation = str(get(m, "spec", "explanation"))
	f.Remediation = str(get(m, "spec", "remediation"))

	cats := strs(get(m, "spec", "finding_categories"))
	tags := strs(get(m, "spec", "finding_tags"))
	ai := asMap(get(m, "spec", "finding_metadata", "ai_sast_data"))
	switch {
	case has(cats, "FINDING_CATEGORY_SECRETS"):
		f.Category = CatSecrets
	case has(cats, "FINDING_CATEGORY_MALWARE") || has(tags, "FINDING_TAGS_MALWARE"):
		f.Category = CatMalware
	case has(cats, "FINDING_CATEGORY_SAST") || ai != nil:
		f.Category = CatSAST
	case has(cats, "FINDING_CATEGORY_VULNERABILITY") || has(cats, "FINDING_CATEGORY_SCA") ||
		has(cats, "FINDING_CATEGORY_SUPPLY_CHAIN") || has(cats, "FINDING_CATEGORY_LICENSE_RISK"):
		f.Category = CatSCA
	default:
		f.Category = CatOther
	}

	// dependencia afectada
	f.Package = str(get(m, "spec", "target_dependency_name"))
	f.Version = str(get(m, "spec", "target_dependency_version"))
	fq := str(get(m, "spec", "target_dependency_package_name"))
	eco, name, ver := SplitRef(fq)
	if f.Package == "" {
		f.Package = name
	}
	if f.Version == "" {
		f.Version = ver
	}
	f.Ecosystem = eco
	if f.Ecosystem == "" {
		f.Ecosystem = NormalizeEcosystem(str(get(m, "spec", "ecosystem")))
		if f.Ecosystem == "unspecified" {
			f.Ecosystem = ""
		}
	}
	f.Fixed = str(get(m, "spec", "proposed_version"))
	f.Manifests = strs(get(m, "spec", "dependency_file_paths"))

	if list, ok := get(m, "spec", "fixing_upgrades", "upgrade_list").([]any); ok {
		for _, e := range list {
			um, ok := e.(map[string]any)
			if !ok {
				continue
			}
			to := str(um["to_version"])
			if to == "" {
				continue
			}
			f.Upgrade = &Upgrade{
				Package: str(um["direct_dependency_name"]),
				From:    str(um["from_version"]),
				To:      to,
				Risk:    str(um["upgrade_risk"]),
			}
			break
		}
	}

	f.Direct = has(tags, "FINDING_TAGS_DIRECT")
	f.Transitive = has(tags, "FINDING_TAGS_TRANSITIVE")
	f.Exploited = has(tags, "FINDING_TAGS_EXPLOITED")
	switch {
	case has(tags, "FINDING_TAGS_REACHABLE_FUNCTION"):
		f.Reachable = "reachable function"
	case has(tags, "FINDING_TAGS_REACHABLE_DEPENDENCY"):
		f.Reachable = "reachable dependency"
	case has(tags, "FINDING_TAGS_POTENTIALLY_REACHABLE_FUNCTION"), has(tags, "FINDING_TAGS_POTENTIALLY_REACHABLE_DEPENDENCY"):
		f.Reachable = "potentially reachable"
	case has(tags, "FINDING_TAGS_UNREACHABLE_FUNCTION"), has(tags, "FINDING_TAGS_UNREACHABLE_DEPENDENCY"):
		f.Reachable = "unreachable"
	}

	// vulnerabilidad
	if vs := asMap(get(m, "spec", "finding_metadata", "vulnerability", "spec")); vs != nil {
		f.Aliases = append(f.Aliases, strs(vs["aliases"])...)
		f.CVSS = num(get(vs, "cvss_v3_severity", "score"))
		f.EPSS = num(get(vs, "epss_score", "probability_score"))
		if f.Summary == "" {
			f.Summary = str(vs["summary"])
		}
	}
	if isAdvisoryID(f.ID) && !has(f.Aliases, f.ID) {
		f.Aliases = append([]string{f.ID}, f.Aliases...)
	}
	f.Aliases = uniq(f.Aliases)

	// SAST (AI SAST trae ubicación y snippet)
	if ai != nil {
		if loc := asMap(ai["location"]); loc != nil {
			f.File = str(loc["relative_path"])
			f.Line = int(num(loc["start_line"]))
			f.Snippet = str(loc["snippet"])
		}
		if s := str(ai["summary"]); s != "" && f.Summary == "" {
			f.Summary = s
		}
		if r := str(ai["remediation"]); r != "" && f.Remediation == "" {
			f.Remediation = r
		}
	}
	// metadatos libres (secretos, reglas SAST clásicas)
	if custom := asMap(get(m, "spec", "finding_metadata", "custom")); custom != nil && f.File == "" {
		f.File = firstNonEmpty(str(custom["file"]), str(custom["path"]), str(custom["relative_path"]))
		f.Line = int(num(firstAny(custom["line"], custom["start_line"])))
		if f.Snippet == "" {
			f.Snippet = str(custom["snippet"])
		}
	}
	if f.File == "" {
		ann := asMap(get(m, "meta", "annotations"))
		if ann != nil {
			f.File = firstNonEmpty(str(ann["file"]), str(ann["path"]))
			f.Line = int(num(firstAny(ann["line"], ann["start_line"])))
		}
	}

	f.Title = pickTitle(m, f)
	return f
}

func pickTitle(m map[string]any, f Finding) string {
	if d := strings.TrimSpace(str(get(m, "meta", "description"))); d != "" {
		return firstLine(d, 110)
	}
	if f.Category == CatSCA && len(f.Aliases) > 0 && f.Package != "" {
		return f.Aliases[0] + " in " + f.Package
	}
	if n := strings.TrimSpace(f.ID); n != "" && !looksLikeUUID(n) {
		return firstLine(n, 110)
	}
	if f.Summary != "" {
		return firstLine(f.Summary, 110)
	}
	return "(untitled)"
}

func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	r := []rune(s)
	if len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}

func looksLikeUUID(s string) bool {
	return len(s) == 36 && strings.Count(s, "-") == 4
}

func isAdvisoryID(s string) bool {
	u := strings.ToUpper(s)
	return strings.HasPrefix(u, "CVE-") || strings.HasPrefix(u, "GHSA-")
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func firstAny(vs ...any) any {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// ---- SARIF (respaldo: endorctl scan --sarif-file) ----

func parseSARIF(doc map[string]any) []Finding {
	var out []Finding
	runs, _ := doc["runs"].([]any)
	for _, r := range runs {
		run, ok := r.(map[string]any)
		if !ok {
			continue
		}
		rules := map[string]map[string]any{}
		if rs, ok := get(run, "tool", "driver", "rules").([]any); ok {
			for _, x := range rs {
				if rm, ok := x.(map[string]any); ok {
					rules[str(rm["id"])] = rm
				}
			}
		}
		results, _ := run["results"].([]any)
		for i, x := range results {
			res, ok := x.(map[string]any)
			if !ok {
				continue
			}
			id := str(res["ruleId"])
			rule := rules[id]
			f := Finding{ID: id}
			if f.ID == "" {
				f.ID = "sarif-" + strconv.Itoa(i)
			}
			f.Summary = str(get(res, "message", "text"))
			f.Severity = sarifSeverity(res, rule)
			if loc, ok := firstMap(res["locations"]); ok {
				f.File = strings.TrimPrefix(str(get(loc, "physicalLocation", "artifactLocation", "uri")), "file://")
				f.Line = int(num(get(loc, "physicalLocation", "region", "startLine")))
				f.Snippet = str(get(loc, "physicalLocation", "region", "snippet", "text"))
			}
			tags := strs(get(rule, "properties", "tags"))
			for _, t := range append([]string{id}, tags...) {
				if isAdvisoryID(t) {
					f.Aliases = append(f.Aliases, t)
				}
			}
			f.Aliases = uniq(f.Aliases)
			low := strings.ToLower(id + " " + strings.Join(tags, " "))
			switch {
			case strings.Contains(low, "secret"):
				f.Category = CatSecrets
			case len(f.Aliases) > 0 || strings.Contains(low, "dependency") || strings.Contains(low, "vulnerab"):
				f.Category = CatSCA
			default:
				f.Category = CatSAST
			}
			title := firstNonEmpty(str(get(rule, "shortDescription", "text")), str(get(rule, "name")), id)
			f.Title = firstLine(title, 110)
			if f.Title == "" {
				f.Title = fmt.Sprintf("Hallazgo %d", i+1)
			}
			out = append(out, f)
		}
	}
	Sort(out)
	return out
}

func firstMap(v any) (map[string]any, bool) {
	arr, ok := v.([]any)
	if !ok || len(arr) == 0 {
		return nil, false
	}
	m, ok := arr[0].(map[string]any)
	return m, ok
}

func sarifSeverity(res, rule map[string]any) Severity {
	// security-severity (CVSS 0-10) tiene prioridad si existe
	for _, src := range []map[string]any{res, rule} {
		if src == nil {
			continue
		}
		if v := get(src, "properties", "security-severity"); v != nil {
			s := num(v)
			switch {
			case s >= 9:
				return SevCritical
			case s >= 7:
				return SevHigh
			case s >= 4:
				return SevMedium
			default:
				return SevLow
			}
		}
	}
	switch strings.ToLower(str(res["level"])) {
	case "error":
		return SevHigh
	case "warning":
		return SevMedium
	}
	return SevLow
}
