// Package gitx envuelve los pocos comandos de git que necesita la herramienta.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// run ejecuta git en dir y devuelve stdout recortado. El error incluye stderr.
func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	return strings.TrimSpace(out.String()), nil
}

// Root devuelve la raíz del repositorio que contiene dir.
func Root(dir string) (string, error) {
	return run(dir, "rev-parse", "--show-toplevel")
}

// Branch devuelve la rama actual ("HEAD" si está desacoplado).
func Branch(dir string) string {
	b, err := run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	return b
}

// Commit describe un commit.
type Commit struct {
	Hash, Short, Subject, Author string
}

// ResolveCommit valida una referencia (hash, rama, tag, HEAD~1…) y devuelve sus datos.
func ResolveCommit(dir, ref string) (Commit, error) {
	if strings.HasPrefix(ref, "-") {
		return Commit{}, fmt.Errorf("referencia inválida: %q", ref)
	}
	out, err := run(dir, "show", "-s", "--format=%H%n%h%n%s%n%an", ref+"^{commit}")
	if err != nil {
		return Commit{}, fmt.Errorf("no existe el commit %q: %w", ref, err)
	}
	p := strings.SplitN(out, "\n", 4)
	for len(p) < 4 {
		p = append(p, "")
	}
	return Commit{Hash: p[0], Short: p[1], Subject: p[2], Author: p[3]}, nil
}

// AddWorktree extrae un commit a un directorio temporal usando `git worktree`,
// de modo que el escáner ve un repositorio git real sin tocar tu árbol de trabajo.
// cleanup elimina el worktree y el directorio.
func AddWorktree(repoDir, ref string) (path string, cleanup func(), err error) {
	if strings.HasPrefix(ref, "-") {
		return "", nil, fmt.Errorf("referencia inválida: %q", ref)
	}
	tmp, err := os.MkdirTemp("", "auri-wt-")
	if err != nil {
		return "", nil, err
	}
	// git worktree add exige que el destino no exista o esté vacío.
	if _, err := run(repoDir, "worktree", "add", "--detach", "--force", tmp, ref); err != nil {
		_ = os.RemoveAll(tmp)
		return "", nil, fmt.Errorf("git worktree add: %w", err)
	}
	cleanup = func() {
		_, _ = run(repoDir, "worktree", "remove", "--force", tmp)
		_ = os.RemoveAll(tmp)
		_, _ = run(repoDir, "worktree", "prune")
	}
	return tmp, cleanup, nil
}

// HasRemote indica si el repositorio tiene al menos un remoto configurado
// (p. ej. "origin"). El MCP tool `scan` (usado para el escaneo de secretos)
// exige un remoto configurado y falla con un error de inicialización si no
// hay ninguno; comprobarlo antes de llamar al MCP evita una espera inútil.
func HasRemote(dir string) bool {
	out, err := run(dir, "remote")
	return err == nil && out != ""
}

// RecentCommits devuelve los últimos n commits de HEAD, el más nuevo primero.
func RecentCommits(dir string, n int) ([]Commit, error) {
	out, err := run(dir, "log", "-n", strconv.Itoa(n), "--format=%H%x1f%h%x1f%s%x1f%an")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\x1f")
		for len(parts) < 4 {
			parts = append(parts, "")
		}
		commits = append(commits, Commit{Hash: parts[0], Short: parts[1], Subject: parts[2], Author: parts[3]})
	}
	return commits, nil
}

// ChangedFiles devuelve las rutas (relativas a dir) con cambios sin commitear
// (staged, unstaged o sin seguimiento), según `git status --porcelain`.
func ChangedFiles(dir string) ([]string, error) {
	out, err := run(dir, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		rest := line[3:]
		if i := strings.Index(rest, " -> "); i >= 0 {
			rest = rest[i+4:]
		}
		rest = strings.Trim(rest, "\"")
		files = append(files, rest)
	}
	return files, nil
}
