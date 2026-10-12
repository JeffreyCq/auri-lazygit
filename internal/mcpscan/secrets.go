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

// ErrNoGitRemote se devuelve cuando la herramienta MCP `scan` rechaza el
// objetivo porque su repositorio no tiene un remoto git origin configurado.
// Es una precondición real del escáner de secretos/SAST de Endor (no
// relacionada con el nivel de licencia) que el motor de dependencias en
// scan.go no comparte.
var ErrNoGitRemote = errors.New("the Endor Labs secrets scanner requires the repository to have a git remote configured (e.g. `git remote add origin <url>`); the dependency scan doesn't need one")

// RunSecrets llama a la herramienta `scan` del servidor MCP con
// scan_types: ["secrets"] contra dir y normaliza el resultado a la misma
// forma Result que usan Run/RunFiles (Result.Kind queda en "secrets"), para
// que la UI lo renderice con las mismas pantallas de resultados y revisión
// de arreglos sin cambios.
//
// A diferencia de Run/RunFiles, esta es una sola llamada MCP de grano
// grueso: no hay progreso por dependencia que reportar, puede tardar
// legítimamente varios minutos en un repo no trivial, y client debe
// inicializarse con un contexto sin plazo artificial (se espera que quien
// llama deje cancelar al usuario a través de su propio ctx).
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

// scanToolResult es la forma de más alto nivel de la respuesta de la
// herramienta `scan`. Results es prosa libre seguida de un objeto JSON
// embebido cuando hay hallazgos ("The following findings were
// identified. ...\n{...}"), o solo prosa ("...\nNo problems found") cuando
// no los hay.
type scanToolResult struct {
	Results string `json:"results"`
	Error   string `json:"error"`
}

// scanFindingsDoc es el objeto JSON embebido dentro de scanToolResult.Results.
type scanFindingsDoc struct {
	Objects []scanFindingObject `json:"objects"`
}

// scanFindingObject refleja la forma del recurso Finding de Endor que
// devuelve la herramienta `scan` (JSON en camelCase, distinto de la forma en
// snake_case que emite `endorctl scan -o json`, que maneja
// internal/findings/parse.go).
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

// parseSecretsResults extrae el documento de hallazgos embebido de la
// respuesta prosa+JSON de la herramienta `scan`, o devuelve (nil, nil)
// cuando la prosa indica un escaneo limpio (p. ej. "No problems found").
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

// validationNote convierte el estado de validación en vivo de Endor en una
// nota corta y legible que se agrega a la explicación del hallazgo. El
// escáner de secretos de Endor no solo hace coincidir patrones: verifica
// activamente si un secreto encontrado sigue siendo una credencial viva.
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

// splitSecretLocation parsea el formato de ubicación de Endor "path/to/file.ext#L5".
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
