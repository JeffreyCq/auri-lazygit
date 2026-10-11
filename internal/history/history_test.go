package history

import (
	"os"
	"path/filepath"
	"testing"

	"auri-lazygit/internal/demo"
	"auri-lazygit/internal/findings"
)

func TestAppendLoadAndFilter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "h.json")
	fs, _ := findings.ParseOutput(demo.FindingsJSON())

	if es, err := Load(p); err != nil || es != nil {
		t.Fatalf("inexistente: %v %v", es, err)
	}
	for i := 0; i < 3; i++ {
		if err := Append(p, NewEntry("repo", "repo", "repositorio completo", "main", false, fs[:13-i*4])); err != nil {
			t.Fatal(err)
		}
	}
	_ = Append(p, NewEntry("repo", "repo", "repositorio completo", "main", true, fs))
	_ = Append(p, NewEntry("otro", "repo", "repositorio completo", "main", false, fs))

	es, err := Load(p)
	if err != nil || len(es) != 5 {
		t.Fatalf("len=%d err=%v", len(es), err)
	}
	f := ForTarget(es, "repo", "repo", "repositorio completo", false)
	if len(f) != 3 {
		t.Fatalf("filtrado = %d", len(f))
	}
	got := Series(f, 2, func(e Entry) int { return e.Total() })
	if len(got) != 2 || got[0] != 9 || got[1] != 5 {
		t.Fatalf("serie = %v", got)
	}
	st, _ := os.Stat(p)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("permisos %v", st.Mode().Perm())
	}
}

func TestCorruptFileIsBackedUp(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.json")
	_ = os.WriteFile(p, []byte("{no es json"), 0o600)
	if _, err := Load(p); err == nil {
		t.Fatal("debe fallar con JSON corrupto")
	}
	if err := Append(p, Entry{Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p + ".bak"); err != nil {
		t.Fatal("falta copia .bak")
	}
	if es, err := Load(p); err != nil || len(es) != 1 {
		t.Fatalf("%v %v", es, err)
	}
}

func TestCap(t *testing.T) {
	p := filepath.Join(t.TempDir(), "h.json")
	for i := 0; i < maxEntries+5; i++ {
		_ = Append(p, Entry{Repo: "r", Critical: i})
	}
	es, _ := Load(p)
	if len(es) != maxEntries {
		t.Fatalf("len=%d", len(es))
	}
}
