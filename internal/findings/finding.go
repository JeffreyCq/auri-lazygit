// Package findings define el modelo normalizado de hallazgos de seguridad y
// el parser de la salida JSON/SARIF de `endorctl scan`.
package findings

import (
	"sort"
	"strings"
)

// Severity ordena de menor a mayor para poder comparar con >=.
type Severity int

const (
	SevLow Severity = iota
	SevMedium
	SevHigh
	SevCritical
)

func (s Severity) String() string {
	switch s {
	case SevCritical:
		return "CRITICAL"
	case SevHigh:
		return "HIGH"
	case SevMedium:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

// Short devuelve una etiqueta de 4 letras para tablas compactas.
func (s Severity) Short() string {
	switch s {
	case SevCritical:
		return "CRIT"
	case SevHigh:
		return "HIGH"
	case SevMedium:
		return "MED"
	default:
		return "LOW"
	}
}

// ParseSeverity acepta "critical", "high", "medium|med", "low" (sin distinguir mayúsculas).
func ParseSeverity(s string) (Severity, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "crit":
		return SevCritical, true
	case "high":
		return SevHigh, true
	case "medium", "med", "moderate":
		return SevMedium, true
	case "low":
		return SevLow, true
	}
	return SevLow, false
}

// Category agrupa los hallazgos por tipo de escáner.
type Category string

const (
	CatSCA     Category = "SCA"
	CatSecrets Category = "Secrets"
	CatSAST    Category = "SAST"
	CatMalware Category = "Malware"
	CatOther   Category = "Other"
)

// Categories es el orden de presentación.
var Categories = []Category{CatSCA, CatSecrets, CatSAST, CatMalware, CatOther}

// Upgrade describe la mejora de una dependencia DIRECTA que resuelve el hallazgo.
type Upgrade struct {
	Package string // nombre completo, p. ej. "npm://lodash@4.17.15"
	From    string
	To      string
	Risk    string
}

// Finding es un hallazgo normalizado, independiente del formato de origen.
type Finding struct {
	ID          string
	Severity    Severity
	Category    Category
	Title       string
	Summary     string
	Explanation string
	Remediation string

	Ecosystem  string // npm, go, pypi, maven, cargo, ...
	Package    string // nombre de la dependencia afectada
	Version    string // versión afectada
	Fixed      string // versión recomendada (proposed_version)
	Upgrade    *Upgrade
	Direct     bool
	Transitive bool
	Reachable  string // "función alcanzable", "dependencia alcanzable", ...
	Exploited  bool

	Aliases []string // CVE-..., GHSA-...
	CVSS    float64
	EPSS    float64 // probabilidad 0..1

	File      string
	Line      int
	Snippet   string
	Manifests []string // archivos de dependencias (relativos al repo)
}

// Location devuelve "archivo:línea" o "paquete@versión", lo que aplique.
func (f Finding) Location() string {
	if f.File != "" {
		if f.Line > 0 {
			return f.File + ":" + itoa(f.Line)
		}
		return f.File
	}
	if f.Package != "" {
		if f.Version != "" {
			return f.Package + "@" + f.Version
		}
		return f.Package
	}
	return ""
}

// Counts agrupa conteos por severidad.
type Counts struct {
	Critical, High, Medium, Low int
}

func (c Counts) Total() int { return c.Critical + c.High + c.Medium + c.Low }

// AtLeast cuenta los hallazgos con severidad >= s.
func (c Counts) AtLeast(s Severity) int {
	n := 0
	if s <= SevCritical {
		n += c.Critical
	}
	if s <= SevHigh {
		n += c.High
	}
	if s <= SevMedium {
		n += c.Medium
	}
	if s <= SevLow {
		n += c.Low
	}
	return n
}

// Risk calcula un índice local 0..100 (heurística propia, NO es un score de Endor Labs).
func (c Counts) Risk() int {
	raw := c.Critical*10 + c.High*5 + c.Medium*2 + c.Low
	if raw > 100 {
		return 100
	}
	return raw
}

// Summarize cuenta hallazgos por severidad.
func Summarize(fs []Finding) Counts {
	var c Counts
	for _, f := range fs {
		switch f.Severity {
		case SevCritical:
			c.Critical++
		case SevHigh:
			c.High++
		case SevMedium:
			c.Medium++
		default:
			c.Low++
		}
	}
	return c
}

// ByCategory cuenta hallazgos por categoría.
func ByCategory(fs []Finding) map[Category]int {
	m := map[Category]int{}
	for _, f := range fs {
		m[f.Category]++
	}
	return m
}

// Sort ordena por severidad (desc), categoría, ubicación y título. Estable y determinista.
func Sort(fs []Finding) {
	order := map[Category]int{}
	for i, c := range Categories {
		order[c] = i
	}
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Severity != b.Severity {
			return a.Severity > b.Severity
		}
		if a.Category != b.Category {
			return order[a.Category] < order[b.Category]
		}
		if la, lb := a.Location(), b.Location(); la != lb {
			return la < lb
		}
		return a.Title < b.Title
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// SplitRef separa una referencia de paquete tipo "npm://@scope/pkg@1.2.3" en
// (ecosistema, nombre, versión). Si no tiene formato de URL devuelve ("", s, "").
func SplitRef(ref string) (eco, name, version string) {
	i := strings.Index(ref, "://")
	if i < 0 {
		return "", ref, ""
	}
	eco = NormalizeEcosystem(ref[:i])
	rest := ref[i+3:]
	if at := strings.LastIndex(rest, "@"); at > 0 {
		return eco, rest[:at], rest[at+1:]
	}
	return eco, rest, ""
}

// NormalizeEcosystem unifica los nombres que usa Endor Labs en referencias y enums.
func NormalizeEcosystem(s string) string {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "ECOSYSTEM_"))
	s = strings.TrimPrefix(s, "ecosystem_")
	switch s {
	case "mvn", "maven":
		return "maven"
	case "pip", "pypi", "python":
		return "pypi"
	case "golang", "go":
		return "go"
	case "crates", "cargo", "rust":
		return "cargo"
	case "gem", "rubygems", "ruby":
		return "gem"
	case "javascript", "npm":
		return "npm"
	case "packagist", "composer":
		return "packagist"
	}
	return s
}
