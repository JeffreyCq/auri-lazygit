package endor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"auri-lazygit/internal/demo"
)

func fakeBin(t *testing.T, body string) []string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "endorctl")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return []string{p}
}

func TestBuildArgsFile(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "app.js"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644)

	args, notes, err := BuildArgs(Target{Kind: KindFile, RepoDir: dir, Path: "app.js"}, dir, DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	j := strings.Join(args, " ")
	if strings.Contains(j, "--dependencies") || !strings.Contains(j, "--include-path=app.js") || !strings.Contains(j, "--dry-run") {
		t.Fatalf("args inesperados: %s", j)
	}
	if len(notes) == 0 {
		t.Fatal("se esperaba una nota de SCA omitido")
	}

	args, _, _ = BuildArgs(Target{Kind: KindFile, RepoDir: dir, Path: "package.json"}, dir, DefaultOptions())
	if !strings.Contains(strings.Join(args, " "), "--dependencies") {
		t.Fatal("package.json debe activar SCA")
	}
}

func TestBuildArgsLocalAndNoScanners(t *testing.T) {
	dir := t.TempDir()
	args, _, err := BuildArgs(Target{Kind: KindLocal, RepoDir: dir}, dir, DefaultOptions())
	if err != nil || !strings.Contains(strings.Join(args, " "), "--diff-scope=local") {
		t.Fatalf("local: %v %v", args, err)
	}
	if _, _, err := BuildArgs(Target{Kind: KindRepo, RepoDir: dir}, dir, Options{}); err == nil {
		t.Fatal("sin escáneres debe fallar")
	}
}

func TestScanSuccessWithFakeBinary(t *testing.T) {
	dir := t.TempDir()
	jf := filepath.Join(t.TempDir(), "out.json")
	_ = os.WriteFile(jf, demo.FindingsJSON(), 0o644)
	o := DefaultOptions()
	o.Bin = fakeBin(t, "echo 'INFO starting'; cat "+jf)
	res, err := Scan(context.Background(), Target{Kind: KindRepo, RepoDir: dir}, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Findings) != 13 {
		t.Fatalf("hallazgos = %d", len(res.Findings))
	}
}

func TestScanErrorsClassified(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		script string
		kind   ErrKind
	}{
		{"echo 'Error: unauthenticated, run endorctl init' >&2; exit 1", ErrAuth},
		{"echo 'dial tcp: lookup api.endorlabs.com: no such host' >&2; exit 1", ErrNetwork},
		{"echo 'boom' >&2; exit 2", ErrOther},
	}
	for _, c := range cases {
		o := DefaultOptions()
		o.Bin = fakeBin(t, c.script)
		_, err := Scan(context.Background(), Target{Kind: KindRepo, RepoDir: dir}, o)
		var se *ScanError
		if !errors.As(err, &se) || se.Kind != c.kind {
			t.Fatalf("%q: err=%v", c.script, err)
		}
	}
}

func TestScanEmptyOutputIsNoFindings(t *testing.T) {
	o := DefaultOptions()
	o.Bin = fakeBin(t, "exit 0")
	res, err := Scan(context.Background(), Target{Kind: KindRepo, RepoDir: t.TempDir()}, o)
	if err != nil || len(res.Findings) != 0 {
		t.Fatalf("res=%v err=%v", res, err)
	}
}

func TestScanDemo(t *testing.T) {
	r, err := ScanDemo()
	if err != nil || !r.Demo || len(r.Findings) != 13 {
		t.Fatalf("%v %v", r, err)
	}
}
