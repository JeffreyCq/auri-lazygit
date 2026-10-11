// Package history guarda un registro local (JSON) de cada escaneo para mostrar
// tendencias. Nunca guarda código ni secretos: solo conteos y la etiqueta del objetivo.
package history

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"time"

	"auri-lazygit/internal/findings"
)

const maxEntries = 300

// Entry es un escaneo registrado.
type Entry struct {
	Time   time.Time `json:"time"`
	Repo   string    `json:"repo"`   // nombre de la carpeta del repo
	Kind   string    `json:"kind"`   // file|local|commit|repo
	Target string    `json:"target"` // etiqueta legible
	Branch string    `json:"branch,omitempty"`
	Demo   bool      `json:"demo,omitempty"`

	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Risk     int `json:"risk"`
}

// Total devuelve el número total de hallazgos del escaneo.
func (e Entry) Total() int { return e.Critical + e.High + e.Medium + e.Low }

// NewEntry arma una entrada a partir de los hallazgos.
func NewEntry(repo, kind, target, branch string, demo bool, fs []findings.Finding) Entry {
	c := findings.Summarize(fs)
	return Entry{Time: time.Now().UTC(), Repo: repo, Kind: kind, Target: target, Branch: branch, Demo: demo,
		Critical: c.Critical, High: c.High, Medium: c.Medium, Low: c.Low, Risk: c.Risk()}
}

// Path devuelve la ruta del archivo de historial ($AURI_HISTORY o el directorio de configuración del usuario).
func Path() string {
	if p := os.Getenv("AURI_HISTORY"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil || dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "auri-lazygit", "history.json")
}

// Load lee el historial. Si no existe devuelve una lista vacía; si está corrupto
// devuelve lista vacía y el error (para que la UI pueda avisar sin romperse).
func Load(path string) ([]Entry, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var es []Entry
	if err := json.Unmarshal(b, &es); err != nil {
		return nil, err
	}
	sort.SliceStable(es, func(i, j int) bool { return es[i].Time.Before(es[j].Time) })
	return es, nil
}

// Append añade una entrada (conservando las últimas maxEntries) con escritura atómica.
// Si el archivo existente está corrupto se guarda una copia .bak y se empieza de cero.
func Append(path string, e Entry) error {
	es, err := Load(path)
	if err != nil {
		_ = os.Rename(path, path+".bak")
		es = nil
	}
	es = append(es, e)
	if len(es) > maxEntries {
		es = es[len(es)-maxEntries:]
	}
	b, err := json.MarshalIndent(es, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".history-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, 0o600); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// ForTarget filtra las entradas del mismo repo y objetivo, en orden cronológico.
// Los escaneos demo nunca se mezclan con los reales.
func ForTarget(es []Entry, repo, kind, target string, demo bool) []Entry {
	var out []Entry
	for _, e := range es {
		if e.Repo == repo && e.Kind == kind && e.Target == target && e.Demo == demo {
			out = append(out, e)
		}
	}
	return out
}

// Series extrae una serie numérica (últimas n) de las entradas.
func Series(es []Entry, n int, pick func(Entry) int) []int {
	if n > 0 && len(es) > n {
		es = es[len(es)-n:]
	}
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = pick(e)
	}
	return out
}
