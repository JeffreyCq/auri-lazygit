// Package mcpscan convierte un repositorio en disco en registros
// findings.Finding normalizados, vía el servidor MCP de Endor Labs. Tiene
// dos motores, ambos confirmados como funcionales en el nivel gratuito
// Developer Edition:
//
//   - Escaneo de dependencias (SCA/malware) en este archivo: descubre los
//     manifiestos localmente (internal/deps) y consulta
//     check_dependency_for_risks + get_endor_vulnerability una vez por cada
//     dependencia única. Es dependencia por dependencia en vez de una sola
//     llamada para todo el repo porque así "Local Changes" puede acotar el
//     escaneo solo a los manifiestos cambiados.
//   - Escaneo de secretos en secrets.go: una sola llamada a la herramienta
//     MCP `scan` (scan_types: ["secrets"]), que corre el escáner de
//     secretos real de Endor sobre todo el árbol de trabajo en el
//     servidor. No tiene granularidad por archivo, puede tardar minutos, y
//     exige que el repo tenga un remoto git origin configurado — nada de
//     esto aplica al motor SCA.
package mcpscan

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"auri-lazygit/internal/deps"
	"auri-lazygit/internal/findings"
	"auri-lazygit/internal/mcp"
)

// Progress informa el avance de una llamada a Run. Phase es "deps" mientras
// se revisa el riesgo de cada dependencia, y luego "advisories" mientras se
// resuelven los detalles completos de cada vulnerabilidad distinta encontrada.
type Progress struct {
	Phase       string
	Done, Total int
	Current     string
}

// Result es el resultado normalizado de un escaneo, de cualquiera de los dos motores.
type Result struct {
	Kind string // "deps" (SCA/malware, el motor por defecto) o "secrets"

	Findings     []findings.Finding
	Dependencies int // dependencias únicas revisadas (solo motor deps)
	Vulnerable   int // dependencias con al menos un problema conocido (solo motor deps)
	Manifests    []deps.Manifest
	Unsupported  []deps.Manifest // manifiestos encontrados pero no consultables (cargo, gem, ...)
	Errors       []string        // errores no fatales de consulta por dependencia/aviso
}

// ecosystemToMCP mapea deps.Dependency.Ecosystem a los valores enum que
// acepta check_dependency_for_risks.
var ecosystemToMCP = map[string]string{
	"npm": "npm", "go": "go", "pypi": "python", "maven": "maven",
}

const workers = 4

// Run descubre manifiestos de dependencias bajo dir y consulta al servidor
// MCP por cada dependencia única, luego resuelve los detalles completos de
// cada vulnerabilidad referenciada. client ya debe estar inicializado.
// progress puede ser nil; cuando se define, se llama desde las goroutines
// worker del escaneo y no debe bloquear.
func Run(ctx context.Context, client *mcp.Client, dir string, progress func(Progress)) (*Result, error) {
	return run(ctx, client, func() (*deps.Result, error) { return deps.Discover(dir) }, progress)
}

// RunFiles es como Run pero acota el descubrimiento de manifiestos a las
// rutas de archivo dadas (relativas a dir) en vez de recorrer todo el
// árbol. Se usa para escanear solo los archivos con cambios sin commitear.
func RunFiles(ctx context.Context, client *mcp.Client, dir string, files []string, progress func(Progress)) (*Result, error) {
	return run(ctx, client, func() (*deps.Result, error) { return deps.DiscoverFiles(dir, files) }, progress)
}

func run(ctx context.Context, client *mcp.Client, discover func() (*deps.Result, error), progress func(Progress)) (*Result, error) {
	disc, err := discover()
	if err != nil {
		return nil, err
	}
	res := &Result{Kind: "deps", Manifests: disc.Manifests, Unsupported: disc.Unsupported, Dependencies: len(disc.Dependencies)}
	if len(disc.Dependencies) == 0 {
		return res, nil
	}
	report := func(p Progress) {
		if progress != nil {
			progress(p)
		}
	}

	type depRisk struct {
		dep  deps.Dependency
		risk *riskResponse
		err  error
	}
	riskResults := make([]depRisk, len(disc.Dependencies))

	var mu sync.Mutex
	doneCount := 0
	bump := func(d deps.Dependency) {
		mu.Lock()
		doneCount++
		report(Progress{Phase: "deps", Done: doneCount, Total: len(disc.Dependencies), Current: d.Name + "@" + d.Version})
		mu.Unlock()
	}

	runPool(len(disc.Dependencies), func(i int) {
		d := disc.Dependencies[i]
		mcpEco, ok := ecosystemToMCP[d.Ecosystem]
		if !ok {
			riskResults[i] = depRisk{dep: d, err: fmt.Errorf("ecosystem %q not supported by check_dependency_for_risks", d.Ecosystem)}
			bump(d)
			return
		}
		rr, rerr := checkDependency(ctx, client, mcpEco, d.Name, d.Version)
		riskResults[i] = depRisk{dep: d, risk: rr, err: rerr}
		bump(d)
	})

	vulnIDSet := map[string]bool{}
	for _, r := range riskResults {
		if r.err != nil || r.risk == nil {
			continue
		}
		for _, id := range r.risk.VulnerabilityIDs {
			vulnIDSet[id] = true
		}
	}
	ids := make([]string, 0, len(vulnIDSet))
	for id := range vulnIDSet {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	vulnDetails := make(map[string]*vulnInfo, len(ids))
	var vmu sync.Mutex
	vdone := 0
	runPool(len(ids), func(i int) {
		id := ids[i]
		info, verr := fetchVulnerability(ctx, client, id)
		vmu.Lock()
		if verr == nil {
			vulnDetails[id] = info
		} else {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", id, verr))
		}
		vdone++
		report(Progress{Phase: "advisories", Done: vdone, Total: len(ids), Current: id})
		vmu.Unlock()
	})

	var fs []findings.Finding
	for _, r := range riskResults {
		if r.err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s@%s: %v", r.dep.Name, r.dep.Version, r.err))
			continue
		}
		rr := r.risk
		if rr.Error != "" {
			res.Errors = append(res.Errors, fmt.Sprintf("%s@%s: %s", r.dep.Name, r.dep.Version, rr.Error))
			continue
		}
		if !rr.IsVulnerable && !rr.IsMalware {
			continue
		}
		res.Vulnerable++

		var upgrade *findings.Upgrade
		fixed := ""
		for ref, to := range rr.VersionRecommendations {
			fixed, upgrade = to, &findings.Upgrade{Package: ref, From: r.dep.Version, To: to}
			break
		}

		if rr.IsMalware {
			fs = append(fs, findings.Finding{
				ID:        "malware:" + r.dep.Name + "@" + r.dep.Version,
				Severity:  findings.SevCritical,
				Category:  findings.CatMalware,
				Title:     fmt.Sprintf("%s@%s is flagged as malicious", r.dep.Name, r.dep.Version),
				Summary:   strings.Join(rr.MalwareIDs, ", "),
				Ecosystem: r.dep.Ecosystem, Package: r.dep.Name, Version: r.dep.Version,
				Direct: r.dep.Direct, Manifests: r.dep.Manifests,
				Aliases: rr.MalwareIDs,
			})
		}

		for _, vid := range rr.VulnerabilityIDs {
			f := findings.Finding{
				ID:        vid + ":" + r.dep.Name + "@" + r.dep.Version,
				Category:  findings.CatSCA,
				Ecosystem: r.dep.Ecosystem, Package: r.dep.Name, Version: r.dep.Version,
				Fixed: fixed, Upgrade: upgrade,
				Direct: r.dep.Direct, Manifests: r.dep.Manifests,
				Aliases: []string{vid},
			}
			if info := vulnDetails[vid]; info != nil {
				f.Severity = info.Severity
				f.Title = info.Description
				f.CVSS = info.CVSS
				f.EPSS = info.EPSS
				if len(info.Aliases) > 0 {
					f.Aliases = dedupStrings(append(f.Aliases, info.Aliases...))
				}
			} else {
				f.Severity = findings.SevMedium
				f.Title = vid + " (advisory details unavailable)"
			}
			fs = append(fs, f)
		}
	}
	findings.Sort(fs)
	res.Findings = fs
	return res, nil
}

// runPool ejecuta fn(i) para i en [0,n) repartido en un pool fijo pequeño de workers.
func runPool(n int, fn func(i int)) {
	if n == 0 {
		return
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				fn(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

// ---- check_dependency_for_risks ----

type riskResponse struct {
	VersionRequested       string            `json:"version_requested"`
	LatestVersion          string            `json:"latest_version"`
	IsVulnerable           bool              `json:"is_vulnerable"`
	IsValidVersion         bool              `json:"is_valid_version"`
	VulnerabilityCount     int               `json:"vulnerability_count"`
	VulnerabilityIDs       []string          `json:"vulnerability_ids"`
	IsMalware              bool              `json:"is_malware"`
	MalwareCount           int               `json:"malware_count"`
	MalwareIDs             []string          `json:"malware_ids"`
	VersionRecommendations map[string]string `json:"version_recommendations"`
	Error                  string            `json:"error"`
}

func checkDependency(ctx context.Context, client *mcp.Client, ecosystem, name, version string) (*riskResponse, error) {
	result, err := client.CallTool(ctx, "check_dependency_for_risks", map[string]interface{}{
		"ecosystem":       ecosystem,
		"dependency_name": name,
		"version":         version,
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("%s", result.Text())
	}
	var rr riskResponse
	if err := result.DecodeStructured(&rr); err != nil {
		return nil, err
	}
	return &rr, nil
}

// ---- get_endor_vulnerability ----

type vulnInfo struct {
	Description string
	Severity    findings.Severity
	CVSS        float64
	EPSS        float64
	Aliases     []string
}

type vulnResponse struct {
	Vulnerability struct {
		Meta struct {
			Description string `json:"description"`
		} `json:"meta"`
		Spec struct {
			Summary        string `json:"summary"`
			CvssV3Severity struct {
				Level string  `json:"level"`
				Score float64 `json:"score"`
			} `json:"cvssV3Severity"`
			EpssScore struct {
				ProbabilityScore float64 `json:"probabilityScore"`
			} `json:"epssScore"`
			DatabaseSpecific struct {
				Severity string `json:"severity"`
			} `json:"databaseSpecific"`
			Aliases []string `json:"aliases"`
		} `json:"spec"`
	} `json:"vulnerability"`
	Error string `json:"error"`
}

func fetchVulnerability(ctx context.Context, client *mcp.Client, vulnID string) (*vulnInfo, error) {
	result, err := client.CallTool(ctx, "get_endor_vulnerability", map[string]interface{}{"vuln_id": vulnID})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("%s", result.Text())
	}
	var vr vulnResponse
	if err := result.DecodeStructured(&vr); err != nil {
		return nil, err
	}
	if vr.Error != "" {
		return nil, fmt.Errorf("%s", vr.Error)
	}
	sev, ok := findings.ParseSeverity(vr.Vulnerability.Spec.DatabaseSpecific.Severity)
	if !ok {
		sev, ok = findings.ParseSeverity(strings.TrimPrefix(vr.Vulnerability.Spec.CvssV3Severity.Level, "LEVEL_"))
	}
	if !ok {
		sev = severityFromCVSS(vr.Vulnerability.Spec.CvssV3Severity.Score)
	}
	title := vr.Vulnerability.Meta.Description
	if title == "" {
		title = vr.Vulnerability.Spec.Summary
	}
	if title == "" {
		title = vulnID
	}
	return &vulnInfo{
		Description: title,
		Severity:    sev,
		CVSS:        vr.Vulnerability.Spec.CvssV3Severity.Score,
		EPSS:        vr.Vulnerability.Spec.EpssScore.ProbabilityScore,
		Aliases:     vr.Vulnerability.Spec.Aliases,
	}, nil
}

func severityFromCVSS(score float64) findings.Severity {
	switch {
	case score >= 9:
		return findings.SevCritical
	case score >= 7:
		return findings.SevHigh
	case score >= 4:
		return findings.SevMedium
	default:
		return findings.SevLow
	}
}

func dedupStrings(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
