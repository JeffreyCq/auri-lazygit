package endor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"auri-lazygit/internal/demo"
	"auri-lazygit/internal/findings"
	"auri-lazygit/internal/gitx"
)

// Result es el resultado de un escaneo.
type Result struct {
	Findings []findings.Finding
	Command  string // línea de comandos ejecutada (informativa)
	Notes    []string
	Duration time.Duration
	Demo     bool
	Raw      []byte
}

// ErrKind clasifica los fallos para mostrar ayuda específica.
type ErrKind int

const (
	ErrOther ErrKind = iota
	ErrMissingBinary
	ErrAuth
	ErrNetwork
	ErrLicense
	ErrEmptyRepo
	ErrTimeout
	ErrCanceled
)

// ScanError es un error con causa clasificada y una sugerencia accionable.
type ScanError struct {
	Kind   ErrKind
	Msg    string
	Hint   string
	Detail string // cola de stderr, para diagnóstico
}

func (e *ScanError) Error() string { return e.Msg }

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// classify traduce la salida de error de endorctl a un ScanError.
func classify(stderr string, runErr error) *ScanError {
	l := strings.ToLower(stderr)
	d := tail(stderr, 12)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(l, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("unauthenticated", "not authenticated", "authentication failed", "endorctl init", "no valid", "invalid token", "token expired", "failed to authenticate", "unauthorized", "401"):
		return &ScanError{ErrAuth, "no hay sesión válida de Endor Labs",
			"Ejecuta `endorctl init` (Developer Edition gratis) o `endorctl init --auth-mode=google` y vuelve a intentar.", d}
	case has("no such host", "dial tcp", "connection refused", "network is unreachable", "i/o timeout", "tls handshake", "temporary failure in name resolution"):
		return &ScanError{ErrNetwork, "no se pudo conectar con Endor Labs",
			"Revisa tu conexión/VPN/proxy. endorctl necesita red para consultar la base de vulnerabilidades.", d}
	case has("license", "not entitled", "permission denied", "403", "forbidden"):
		return &ScanError{ErrLicense, "tu cuenta no tiene permiso para esta función",
			"Desactiva SAST (tecla 3 en la pantalla de objetivo) o revisa la licencia/namespace con `endorctl api list -r Namespace`.", d}
	}
	return &ScanError{ErrOther, "endorctl terminó con error: " + runErr.Error(),
		"Pulsa `d` para ver la salida de diagnóstico, o ejecuta el mismo comando a mano.", d}
}

// Scan ejecuta endorctl según el objetivo. Para commits crea un worktree temporal
// y lo elimina al terminar, sin tocar tu árbol de trabajo.
func Scan(ctx context.Context, t Target, o Options) (*Result, error) {
	start := time.Now()
	if t.RepoDir == "" {
		return nil, &ScanError{Kind: ErrOther, Msg: "no se indicó la raíz del repositorio"}
	}
	scanDir := t.RepoDir
	if t.Kind == KindCommit {
		if t.Ref == "" {
			return nil, &ScanError{Kind: ErrOther, Msg: "falta la referencia del commit"}
		}
		wt, cleanup, err := gitx.AddWorktree(t.RepoDir, t.Ref)
		if err != nil {
			return nil, &ScanError{Kind: ErrOther, Msg: "no se pudo extraer el commit a un directorio temporal", Hint: "Comprueba que el commit existe y que git está disponible.", Detail: err.Error()}
		}
		defer cleanup()
		scanDir = wt
	}

	args, notes, err := BuildArgs(t, scanDir, o)
	if err != nil {
		return nil, &ScanError{Kind: ErrOther, Msg: err.Error()}
	}

	bin := o.Bin
	if len(bin) == 0 {
		bin, err = ResolveBin()
		if err != nil {
			return nil, &ScanError{Kind: ErrMissingBinary, Msg: err.Error(),
				Hint: "Instala endorctl (https://docs.endorlabs.com) o Node.js (npx) y reintenta. También puedes probar con --demo."}
		}
	}
	argv := append(append([]string{}, bin...), args...)

	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, argv[0], argv[1:]...)
	cmd.Dir = scanDir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	runErr := cmd.Run()

	res := &Result{Command: ShellQuote(argv), Notes: notes, Duration: time.Since(start), Raw: stdout.Bytes()}
	if o.SaveRaw != "" {
		_ = os.WriteFile(o.SaveRaw, stdout.Bytes(), 0o600)
	}

	if cctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return res, &ScanError{Kind: ErrCanceled, Msg: "escaneo cancelado"}
		}
		return res, &ScanError{Kind: ErrTimeout, Msg: fmt.Sprintf("el escaneo superó el límite de %s", timeout),
			Hint: "Prueba con un archivo concreto o con el modo rápido activado."}
	}

	fs, perr := findings.ParseOutput(stdout.Bytes())
	switch {
	case perr == nil:
		// hay JSON válido: aunque endorctl devuelva código ≠ 0 (p. ej. políticas), usamos los hallazgos
		res.Findings = fs
		findings.Sort(res.Findings)
		return res, nil
	case runErr != nil:
		return res, classify(stderr.String()+"\n"+stdout.String(), runErr)
	case errors.Is(perr, findings.ErrNoJSON):
		// éxito sin JSON: escaneo sin resultados
		res.Notes = append(res.Notes, "endorctl no devolvió hallazgos")
		return res, nil
	default:
		return res, &ScanError{Kind: ErrOther, Msg: "no se pudo interpretar la salida de endorctl: " + perr.Error(),
			Hint: "Guarda la salida cruda con AURI_SAVE_RAW=ruta y compárala con el esquema de Finding.", Detail: tail(stdout.String(), 8)}
	}
}

// ScanDemo devuelve hallazgos de ejemplo sin tocar la red ni endorctl.
func ScanDemo() (*Result, error) {
	raw := demo.FindingsJSON()
	fs, err := findings.ParseOutput(raw)
	if err != nil {
		return nil, err
	}
	findings.Sort(fs)
	return &Result{Findings: fs, Command: "(modo demo: datos de ejemplo, sin red)", Demo: true, Raw: raw,
		Notes: []string{"Datos de demostración: no provienen de tu código"}}, nil
}

// ScanDemoFor devuelve los hallazgos de ejemplo recortados según el objetivo, para que
// la demo se comporte de forma creíble: un archivo muestra solo lo suyo y el primer
// commit del repo de ejemplo todavía no tiene config/settings.py.
func ScanDemoFor(t Target) (*Result, error) {
	r, err := ScanDemo()
	if err != nil {
		return nil, err
	}
	keep := r.Findings[:0:0]
	for _, f := range r.Findings {
		ok := true
		switch t.Kind {
		case KindFile:
			ok = f.File == t.Path || strings.HasPrefix(f.File, strings.TrimSuffix(t.Path, "/")+"/")
			for _, m := range f.Manifests {
				if m == t.Path || strings.HasPrefix(m, strings.TrimSuffix(t.Path, "/")+"/") {
					ok = true
				}
			}
		case KindCommit:
			if gitx.CommitCount(t.RepoDir, t.Ref) == 1 && strings.HasPrefix(f.File, "config/") {
				ok = false
			}
		}
		if ok {
			keep = append(keep, f)
		}
	}
	r.Findings = keep
	return r, nil
}
