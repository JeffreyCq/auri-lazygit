// Package mcpscan turns a repository on disk into normalized
// findings.Finding records via the Endor Labs MCP server. It has two
// engines, both confirmed working on the free Developer Edition tier:
//
//   - Dependency (SCA/malware) scanning in this file: discovers manifests
//     locally (internal/deps) and queries check_dependency_for_risks +
//     get_endor_vulnerability once per unique dependency. This is
//     dependency-by-dependency rather than a single repo-wide call because
//     it lets "Local Changes" scope the scan to just the changed manifests.
//   - Secrets scanning in secrets.go: a single call to the MCP `scan` tool
//     (scan_types: ["secrets"]), which runs Endor's real secrets scanner
//     over the whole working tree server-side. It has no per-file
//     granularity, can take minutes, and requires the repo to have a git
//     origin remote configured — none of which apply to the SCA engine.
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

// Progress reports advancement of a Run call. Phase is "deps" while checking
// each dependency for risk, then "advisories" while resolving full details
// for every distinct vulnerability found.
type Progress struct {
	Phase       string
	Done, Total int
	Current     string
}

// Result is the normalized outcome of a scan, from either engine.
type Result struct {
	Kind string // "deps" (SCA/malware, the default) or "secrets"

	Findings     []findings.Finding
	Dependencies int // unique dependencies checked (deps engine only)
	Vulnerable   int // dependencies with at least one known issue (deps engine only)
	Manifests    []deps.Manifest
	Unsupported  []deps.Manifest // manifests found but not queryable (cargo, gem, ...)
	Errors       []string        // non-fatal per-dependency / per-advisory lookup errors
}

// ecosystemToMCP maps deps.Dependency.Ecosystem to the enum values
// check_dependency_for_risks accepts.
var ecosystemToMCP = map[string]string{
	"npm": "npm", "go": "go", "pypi": "python", "maven": "maven",
}

const workers = 4

// Run discovers dependency manifests under dir and queries the MCP server
// for every unique dependency, then resolves full advisory details for every
// vulnerability referenced. client must already be initialized. progress may
// be nil; when set, it is called from scan worker goroutines and must not
// block.
func Run(ctx context.Context, client *mcp.Client, dir string, progress func(Progress)) (*Result, error) {
	return run(ctx, client, func() (*deps.Result, error) { return deps.Discover(dir) }, progress)
}

// RunFiles is like Run but scopes manifest discovery to the given file paths
// (relative to dir) instead of walking the whole tree. Used to scan only the
// files with uncommitted changes.
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

// runPool runs fn(i) for i in [0,n) across a small fixed worker pool.
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
