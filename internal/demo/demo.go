// Package demo contiene datos de muestra (en el formato de Endor Labs) usados
// como fixture en los tests de otros paquetes: hallazgos de ejemplo
// (FindingsJSON) y el contenido de un repositorio ficticio (Files) sobre el
// que esos hallazgos apuntan.
package demo

import (
	_ "embed"
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
