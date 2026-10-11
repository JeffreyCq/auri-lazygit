package mcpscan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"auri-lazygit/internal/findings"
	"auri-lazygit/internal/mcp"
)

// ErrNoGitRemote is returned when the MCP `scan` tool rejects the target
// because its repository has no configured git origin remote. This is a
// real precondition of Endor's secrets/SAST scanner (unrelated to license
// tier) that the dependency engine in scan.go does not share.
var ErrNoGitRemote = errors.New("the Endor Labs secrets scanner requires the repository to have a git remote configured (e.g. `git remote add origin <url>`); the dependency scan doesn't need one")

// RunSecrets calls the MCP server's `scan` tool with scan_types: ["secrets"]
// against dir and normalizes the result into the same Result shape used by
// Run/RunFiles (Result.Kind is set to "secrets"), so the UI can render it
// with the existing results and fix-review screens unchanged.
//
// Unlike Run/RunFiles, this is a single, coarse-grained MCP call: there's no
// per-dependency progress to report, it can legitimately take minutes on a
// non-trivial repo, and client must be initialized with a context that has
// no artificial deadline (the caller is expected to let the user cancel via
// its own ctx instead).
func RunSecrets(ctx context.Context, client *mcp.Client, dir string, progress func(Progress)) (*Result, error) {
	if progress != nil {
		progress(Progress{Phase: "secrets"})
	}

	result, err := client.CallTool(ctx, "scan", map[string]interface{}{
		"path":       dir,
		"scan_types": []string{"secrets"},
		"scan_options": map[string]interface{}{
			"quick_scan": false, // a quick scan only samples files; we want the full pass
			"summary":    false, // summary mode truncates to the top 5 findings
		},
	})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, fmt.Errorf("%s", result.Text())
	}

	var sr scanToolResult
	if err := result.DecodeStructured(&sr); err != nil {
		return nil, fmt.Errorf("decoding scan tool response: %w", err)
	}
	if sr.Error != "" {
		if strings.Contains(sr.Error, "INITIALIZATION_ERROR") || strings.Contains(sr.Results, "no git origin remote found") {
			return nil, ErrNoGitRemote
		}
		return nil, fmt.Errorf("%s", sr.Error)
	}

	fs, err := parseSecretsResults(sr.Results)
	if err != nil {
		return nil, err
	}
	findings.Sort(fs)
	return &Result{Kind: "secrets", Findings: fs}, nil
}

// scanToolResult is the top-level shape of the `scan` tool's response.
// Results is free-form prose followed by an embedded JSON object when there
// are findings ("The following findings were identified. ...\n{...}"), or
// just prose ("...\nNo problems found") when there are none.
type scanToolResult struct {
	Results string `json:"results"`
	Error   string `json:"error"`
}

// scanFindingsDoc is the embedded JSON object inside scanToolResult.Results.
type scanFindingsDoc struct {
	Objects []scanFindingObject `json:"objects"`
}

// scanFindingObject mirrors the Endor Finding resource shape returned by the
// `scan` tool (camelCase JSON, distinct from the snake_case shape emitted by
// `endorctl scan -o json` that internal/findings/parse.go handles).
type scanFindingObject struct {
	UUID string `json:"uuid"`
	Meta struct {
		Description string `json:"description"`
	} `json:"meta"`
	Spec struct {
		Level               string   `json:"level"`
		Summary             string   `json:"summary"`
		Explanation         string   `json:"explanation"`
		Remediation         string   `json:"remediation"`
		FindingCategories   []string `json:"findingCategories"`
		DependencyFilePaths []string `json:"dependencyFilePaths"`
		Custom              struct {
			SecretRuleID   string `json:"secret_rule_id"`
			SecretRuleName string `json:"secret_rule_name"`
			Validation     string `json:"validation"`
		} `json:"custom"`
		FindingMetadata struct {
			SourcePolicyInfo struct {
				FindingName string `json:"findingName"`
				Results     []struct {
					Fields map[string]string `json:"fields"`
				} `json:"results"`
			} `json:"sourcePolicyInfo"`
		} `json:"findingMetadata"`
	} `json:"spec"`
}

// parseSecretsResults extracts the embedded findings document from the
// `scan` tool's prose+JSON response, or returns (nil, nil) when the prose
// indicates a clean scan (e.g. "No problems found").
func parseSecretsResults(results string) ([]findings.Finding, error) {
	marker := `{"objects"`
	idx := strings.Index(results, marker)
	if idx < 0 {
		return nil, nil
	}
	var doc scanFindingsDoc
	if err := json.Unmarshal([]byte(results[idx:]), &doc); err != nil {
		return nil, fmt.Errorf("parsing secrets scan findings: %w", err)
	}
	fs := make([]findings.Finding, 0, len(doc.Objects))
	for _, o := range doc.Objects {
		fs = append(fs, secretToFinding(o))
	}
	return fs, nil
}

func secretToFinding(o scanFindingObject) findings.Finding {
	title := firstNonEmptyStr(o.Meta.Description, o.Spec.FindingMetadata.SourcePolicyInfo.FindingName, o.Spec.Custom.SecretRuleName, "exposed secret")

	var file, secretID string
	line := 0
	secretType := o.Spec.Custom.SecretRuleName
	if len(o.Spec.FindingMetadata.SourcePolicyInfo.Results) > 0 {
		fields := o.Spec.FindingMetadata.SourcePolicyInfo.Results[0].Fields
		if loc := fields["Secret Location"]; loc != "" {
			file, line = splitSecretLocation(loc)
		}
		if t := fields["Secret Type"]; t != "" {
			secretType = t
		}
		secretID = fields["Secret ID"]
	}
	if file == "" && len(o.Spec.DependencyFilePaths) > 0 {
		file = o.Spec.DependencyFilePaths[0]
	}

	sev, ok := findings.ParseSeverity(strings.TrimPrefix(o.Spec.Level, "FINDING_LEVEL_"))
	if !ok {
		sev = findings.SevMedium
	}

	explanation := firstNonEmptyStr(o.Spec.Summary, o.Spec.Explanation)
	if note := validationNote(o.Spec.Custom.Validation); note != "" {
		explanation = strings.TrimSpace(explanation + "\n" + note)
	}

	id := o.UUID
	if id == "" {
		id = "secret:" + o.Spec.Custom.SecretRuleID + ":" + file + ":" + strconv.Itoa(line)
	}

	return findings.Finding{
		ID:          id,
		Severity:    sev,
		Category:    findings.CatSecrets,
		Title:       title,
		Summary:     secretType,
		Explanation: explanation,
		Remediation: o.Spec.Remediation,
		File:        file,
		Line:        line,
		Aliases:     nonEmptyStrs(secretID),
	}
}

// validationNote turns Endor's live-validation status into a short,
// human-readable note appended to the finding's explanation. Endor's
// secrets scanner doesn't just pattern-match — it actively checks whether a
// found secret is still a live credential.
func validationNote(status string) string {
	switch {
	case strings.Contains(status, "IS_INVALID"):
		return "Endor Labs checked this secret against the live service and found it inactive/rotated — still worth a quick audit, but it's not an active credential."
	case strings.Contains(status, "IS_VALID"):
		return "⚠ Endor Labs checked this secret against the live service and confirmed it is ACTIVE. Rotate it immediately."
	default:
		return ""
	}
}

// splitSecretLocation parses Endor's "path/to/file.ext#L5" location format.
func splitSecretLocation(loc string) (file string, line int) {
	if i := strings.LastIndex(loc, "#L"); i >= 0 {
		if n, err := strconv.Atoi(loc[i+2:]); err == nil {
			return loc[:i], n
		}
	}
	return loc, 0
}

func firstNonEmptyStr(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

func nonEmptyStrs(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
