package gitx_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"auri-tui/internal/gitx"
)

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("uno\n"), 0o644)
	sh(t, dir, "add", "-A")
	sh(t, dir, "commit", "-q", "-m", "primero")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("dos\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("nuevo\n"), 0o644)
	sh(t, dir, "add", "-A")
	sh(t, dir, "commit", "-q", "-m", "segundo")
	return dir
}

func TestRootAndResolveCommit(t *testing.T) {
	dir := newRepo(t)
	sub := filepath.Join(dir, "x")
	os.MkdirAll(sub, 0o755)
	root, err := gitx.Root(sub)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	got, _ := filepath.EvalSymlinks(root)
	if got != want {
		t.Errorf("root = %s, quería %s", got, want)
	}
	c, err := gitx.ResolveCommit(dir, "HEAD~1")
	if err != nil || c.Subject != "primero" || len(c.Short) < 4 {
		t.Errorf("ResolveCommit: %+v %v", c, err)
	}
	if _, err := gitx.ResolveCommit(dir, "no-existe"); err == nil {
		t.Error("debería fallar con ref inexistente")
	}
	if _, err := gitx.ResolveCommit(dir, "--evil"); err == nil {
		t.Error("debería rechazar refs que empiezan con -")
	}
	if _, err := gitx.Root(t.TempDir()); err == nil {
		t.Error("fuera de un repo debería fallar")
	}
}

func TestWorktreeSnapshot(t *testing.T) {
	dir := newRepo(t)
	wt, cleanup, err := gitx.AddWorktree(dir, "HEAD~1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wt, "a.txt"))
	if err != nil || string(b) != "uno\n" {
		t.Fatalf("el worktree debe tener el contenido del commit viejo: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "b.txt")); !os.IsNotExist(err) {
		t.Error("b.txt no existía en HEAD~1")
	}
	// el árbol de trabajo real no cambia
	if cur, _ := os.ReadFile(filepath.Join(dir, "a.txt")); string(cur) != "dos\n" {
		t.Errorf("el árbol real fue modificado: %q", cur)
	}
	cleanup()
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("cleanup no eliminó el worktree")
	}
	if out := sh(t, dir, "worktree", "list"); strings.Count(out, "\n") != 0 {
		t.Errorf("quedó un worktree registrado: %s", out)
	}
}
