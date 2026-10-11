// Package demo crea un repositorio de ejemplo y hallazgos de muestra (en el
// formato de Endor Labs) para probar la TUI sin cuenta ni red: `auri-tui --demo`.
package demo

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed findings.json
var findingsJSON []byte

// FindingsJSON devuelve los hallazgos de muestra tal como los emitiría `endorctl scan -o json`.
func FindingsJSON() []byte { return findingsJSON }

// Files es el contenido del repositorio de ejemplo. Las líneas de los
// hallazgos de findings.json apuntan a estas posiciones (hay una prueba que lo verifica).
var Files = map[string]string{
	"package.json": `{
  "name": "demo-shop",
  "version": "1.0.0",
  "dependencies": {
    "express": "^4.17.1",
    "lodash": "4.17.15",
    "minimist": "~1.2.0"
  },
  "devDependencies": {
    "jest": "^29.0.0"
  }
}
`,
	"requirements.txt": `# dependencias de la API
requests==2.19.0
PyYAML==5.1
flask>=2.0
`,
	"go.mod": `module example.com/demo-shop

go 1.22

require (
	golang.org/x/text v0.3.6
	github.com/google/uuid v1.6.0 // indirect
)
`,
	"src/routes/users.js": `const express = require('express');
const db = require('../db');
const router = express.Router();

// GET /users?name=alice
router.get('/users', async (req, res) => {
  const name = req.query.name;
  // TODO: validate input
  const rows = await db.query("SELECT * FROM users WHERE name = '" + name + "'");
  res.json(rows);
});

module.exports = router;
`,
	"src/auth.js": `const crypto = require('crypto');

function hashPassword(password) {
  // legacy hashing
  return crypto.createHash('md5').update(password).digest('hex');
}

module.exports = { hashPassword };
`,
	"src/db.js": `const { Pool } = require('pg');

const pool = new Pool({
  host: 'db.internal',
  user: 'app',
  password: 'hunter2-demo',
});

module.exports = pool;
`,
	"config/settings.py": `import os

DEBUG = True

# --- cloud credentials (demo) ---
AWS_ACCESS_KEY_ID = "AKIAIOSFODNN7EXAMPLE"
AWS_SECRET_ACCESS_KEY = os.environ.get("AWS_SECRET_ACCESS_KEY")
REGION = "us-east-1"
`,
}

// Setup crea un repositorio git temporal con dos commits y devuelve su ruta.
// El segundo commit añade config/settings.py, de modo que `HEAD~1` es un commit escaneable.
func Setup() (dir string, cleanup func(), err error) {
	dir, err = os.MkdirTemp("", "auri-demo-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	fail := func(e error) (string, func(), error) {
		cleanup()
		return "", nil, e
	}
	write := func(rel string) error {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		return os.WriteFile(p, []byte(Files[rel]), 0o644)
	}
	git := func(args ...string) error {
		cmd := exec.Command("git", append([]string{"-c", "user.name=AURI Demo", "-c", "user.email=demo@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		if out, e := cmd.CombinedOutput(); e != nil {
			return fmt.Errorf("git %v: %v: %s", args, e, out)
		}
		return nil
	}
	if err := git("init", "-q", "-b", "main"); err != nil {
		return fail(err)
	}
	for rel := range Files {
		if rel == "config/settings.py" {
			continue
		}
		if err := write(rel); err != nil {
			return fail(err)
		}
	}
	if err := git("add", "-A"); err != nil {
		return fail(err)
	}
	if err := git("commit", "-q", "-m", "feat: API base de la tienda"); err != nil {
		return fail(err)
	}
	if err := write("config/settings.py"); err != nil {
		return fail(err)
	}
	if err := git("add", "-A"); err != nil {
		return fail(err)
	}
	if err := git("commit", "-q", "-m", "chore: añade configuración de despliegue"); err != nil {
		return fail(err)
	}
	return dir, cleanup, nil
}
