# AURI x Lazygit — code walkthrough

A plain-language map of what this codebase does, how its pieces call each
other, and the exact shapes of the data flowing between them. Written so you
can walk through it out loud without re-reading the source.

## 1. The one-sentence pitch

A Go terminal app (`auri-tui`), launched from Lazygit with `Ctrl+A`, that
scans a git repo's dependencies and secrets by talking to the **Endor Labs
MCP server** (`endorctl ai-tools mcp-server`) over JSON-RPC on stdio, renders
the findings as terminal charts, and — for dependency findings with a known
fixed version — can patch the manifest file for you after showing a diff.

## 2. Process boundaries (who talks to whom)

```
Lazygit  --Ctrl+A, output:terminal-->  auri-tui.exe
                                             |
                                             | spawns as a child process, talks
                                             | JSON-RPC 2.0 over stdin/stdout
                                             v
                                  endorctl ai-tools mcp-server
                                   (spawned via: npx -y endorctl ai-tools mcp-server)
                                             |
                                             | HTTPS, its own auth
                                             v
                                     api.endorlabs.com
```

`auri-tui` never calls Endor Labs' HTTP API directly. It only ever speaks
MCP to the local `endorctl` child process; `endorctl` is the thing that
holds credentials and talks to `api.endorlabs.com`. This matters for the
interview: the Go code has **zero knowledge of HTTP auth** — that's
entirely endorctl's problem.

## 3. Entry point

[`cmd/auri-tui/main.go`](../cmd/auri-tui/main.go)

```go
func main() { os.Exit(run()) }

func run() int {
    flag.Parse()                     // only flag: --version
    p := tea.NewProgram(ui.New(), tea.WithAltScreen())
    p.Run()
}
```

That's it — all real logic lives in `internal/ui`, built on
[Bubble Tea](https://github.com/charmbracelet/bubbletea) (the Elm-style
Model/Update/View TUI framework: one immutable `Model` struct, pure `Update`
functions that take a message and return a new model + next command, and a
`View` function that renders the model to a string every frame).

## 4. Package map

```
cmd/auri-tui        main() — starts the Bubble Tea program, nothing else
internal/ui          Model / Update / View — all screens, all state
internal/mcp          generic JSON-RPC 2.0 / MCP client over stdio
internal/mcpscan       turns MCP tool calls into normalized findings.Finding
internal/deps          discovers & parses dependency manifests (no network)
internal/findings       the normalized Finding struct + severity/sort logic
internal/gitx           shells out to `git` (root, worktree, commits, diff)
internal/fix             edits a manifest's version line, builds a diff, applies atomically
internal/report           block-character bar charts / risk meter
internal/demo              canned findings.json used by tests (and an unused demo path)
internal/endor              UNUSED — a dead CLI-wrapper code path, see §9
internal/history            local on-disk history of past scans (JSON file)
```

## 5. The MCP client — `internal/mcp`

This is a small, generic JSON-RPC 2.0 client; it knows nothing about Endor
Labs specifically, only about the wire protocol.

**`Client` struct** ([client.go](../internal/mcp/client.go)):

```go
type Client struct {
    cmd    *exec.Cmd
    stdin  io.WriteCloser
    stdout *bufio.Reader

    writeMu sync.Mutex             // serializes writes to stdin
    mu      sync.Mutex             // guards the fields below
    counter int                    // next request ID
    responses map[int]chan JSONRPCResponse  // id -> waiting caller
    initialized bool
}
```

**How a call actually works** (`sendRequest`):

1. Allocate the next integer ID (`nextID()`, mutex-protected counter).
2. Register a buffered `chan JSONRPCResponse` in the `responses` map under
   that ID.
3. Marshal the request to JSON, write it + `\n` to the child's stdin
   (`writeMu` serializes this so concurrent calls don't interleave bytes).
4. Block on `select { case resp := <-respChan: ...; case <-ctx.Done(): ... }`.

A **separate goroutine**, `readResponses()`, runs for the client's whole
lifetime: it does `bufio.Reader.ReadBytes('\n')` in a loop, unmarshals each
line as a `JSONRPCResponse`, reads the response's `id`, looks up the
matching channel in `responses`, and pushes the response into it. This is
the standard pattern for multiplexing a request/response protocol over a
single bidirectional stream: **one reader goroutine demuxing by ID**,
arbitrarily many caller goroutines each blocked on their own channel.

**Why it needs this and can't just do request-then-read-one-line:** the
caller passes a `context.Context`, and `check_dependency_for_risks` calls
happen concurrently from a worker pool (§6) — several requests can be
in flight on the wire at once, so responses can come back out of order
relative to when they were sent. The ID map is what makes that safe.

**Three methods built on `sendRequest`:**
- `Initialize(ctx)` — sends `method: "initialize"` with a fixed ID of `1`,
  `protocolVersion: "2024-11-05"`, and `clientInfo: {name: "auri-lazygit",
  version: "0.1.0"}`. Must succeed before anything else.
- `ListTools(ctx)` — `method: "tools/list"`, no params; returns the
  server's tool catalog (name + JSON Schema per tool).
- `CallTool(ctx, name, args)` — `method: "tools/call"`, `params: {name,
  arguments}`; returns a `ToolResult`.

**`ToolResult` shape** ([types.go](../internal/mcp/types.go)):

```go
type ToolResult struct {
    Content           []ContentBlock   // free-text blocks (always present)
    StructuredContent json.RawMessage  // typed JSON, when the tool defines an output schema
    IsError           bool
}
```

`DecodeStructured(dst)` is the one helper every call site uses: it prefers
`StructuredContent` if present, otherwise falls back to parsing the first
text block as JSON. This exists because not every MCP tool/server version
populates `structuredContent`.

**Windows quirk worth mentioning**: `newCommand()` wraps the spawned command
in `cmd /C` on Windows, because `npx` resolves to a `.cmd` shim there and
`exec.Command("npx", ...)` can't execute a `.cmd` directly (`CreateProcess`
needs an actual `.exe`). Routing everything through `cmd /C` sidesteps
having to detect which one `npx` resolves to.

## 6. Dependency scanning — `internal/mcpscan/scan.go`

This is the "Local Changes / Pick a Commit / Full Repository" path.
Entry points: `Run`, `RunFiles` (both call the shared `run()`).

**Step 1 — manifest discovery, local, no network** (`internal/deps`):
walks the directory tree (skipping `.git`, `node_modules`, `vendor`, etc.,
max depth considerations aside), recognizes `package.json` → npm, `go.mod`
→ go, `pyproject.toml`/`requirements*.txt` → pypi, `pom.xml` → maven, and
extracts `(ecosystem, name, version)` triples. Only **pinned, concrete**
versions are kept — a semver range can't be looked up against a
vulnerability DB without first resolving it to one version, so ranges are
skipped rather than guessed at. `go.mod`'s `// indirect` comment is how a
dependency gets marked `Direct: false`.

**Step 2 — risk check, one MCP call per unique dependency, worker pool of
4** (`runPool`, a simple fixed-size goroutine pool reading off a jobs
channel):

```go
client.CallTool(ctx, "check_dependency_for_risks", map[string]interface{}{
    "ecosystem":       mcpEco,   // "npm" | "go" | "python" | "maven"
    "dependency_name": d.Name,
    "version":         d.Version,
})
```

Response (`riskResponse`) tells you, per dependency: `is_vulnerable`,
`is_malware`, `vulnerability_ids []string`, `malware_ids []string`, and
`version_recommendations map[string]string` (a package-ref → recommended
version map — this is where a fix's target version comes from).

**Step 3 — resolve every distinct vulnerability ID**, again via a worker
pool of 4, one call per unique ID (dependencies often share a CVE, so IDs
are deduped into a `map[string]bool` first):

```go
client.CallTool(ctx, "get_endor_vulnerability", map[string]interface{}{
    "vuln_id": id,
})
```

Response gives severity (several possible fields are tried in order:
`databaseSpecific.severity`, then `cvssV3Severity.level`, then falls back to
bucketing the raw CVSS score), CVSS score, EPSS probability (likelihood of
real-world exploitation), and alias IDs (CVE ↔ GHSA cross-references).

**Step 4 — flatten into `[]findings.Finding`** and sort (severity desc,
then category, then location). A dependency that's both vulnerable *and*
malware produces two separate findings (one `CatMalware`, one `CatSCA` per
vuln ID) — malware is never silently merged into a CVE finding.

**Progress reporting**: a non-blocking `func(Progress)` callback is threaded
through; the UI side wraps it so progress goes over a buffered channel (see
§8) instead of calling back into Bubble Tea's model directly from a
goroutine, which would be a data race.

## 7. Secrets scanning — `internal/mcpscan/secrets.go`

Completely different shape from §6: **one single MCP call**, not
per-dependency fan-out, because Endor's secrets detector runs server-side
over the whole working tree:

```go
client.CallTool(ctx, "scan", map[string]interface{}{
    "path":       dir,
    "scan_types": []string{"secrets"},
    "scan_options": map[string]interface{}{
        "quick_scan": false,  // false = full pass, not a sample
        "summary":    false,  // false = all findings, not just top 5
    },
})
```

This can legitimately take minutes, and — unlike the dependency path — it
**requires the repo to have a git `origin` remote configured**; if it
doesn't, the tool call fails with an error containing
`"no git origin remote found"`, which the code specifically detects and
turns into the sentinel `ErrNoGitRemote` so the UI can show a clear message
instead of a raw server error.

**Response parsing is the interesting part.** The `scan` tool doesn't
return clean JSON — it returns **prose with an embedded JSON object**:
either `"...\nNo problems found"` (clean) or `"The following findings were
identified...\n{"objects": [...]}"`. `parseSecretsResults` finds the
`{"objects"` substring and JSON-decodes from there to the end. Each object
in `objects` is an Endor "Finding" resource (camelCase, a different shape
from the snake_case `endorctl scan -o json` CLI output that
`internal/findings/parse.go` separately handles — two different wire
formats for conceptually the same thing, because one comes from the MCP
tool's text response and the other from the CLI's native JSON output).

Each secret also carries a **live-validation status**
(`spec.custom.validation`): Endor doesn't just pattern-match a string that
looks like a key, it actually checks the credential against the live
service. `IS_VALID` → the finding's explanation gets a `⚠ ... confirmed
ACTIVE. Rotate it immediately.` note appended; `IS_INVALID` → a reassuring
"inactive/rotated" note. That distinction is genuinely useful signal and is
worth calling out as a deliberate design choice in the parsing code.

## 8. The UI layer — `internal/ui` (Bubble Tea)

**State machine**: a `screen` enum drives which view renders —
`scMenu → scCommitPick (optional) → scScanning → scScanResults →
scFixReview`, plus a parallel `scMCPTools / scToolInput / scEditParam /
scRawToolResult` path for the manual "Browse MCP Tools" fallback.

**Connecting** (`connectMCP` in [model.go](../internal/ui/model.go)):
spawns the client (`mcp.NewClient("npx", "-y", "endorctl", "ai-tools",
"mcp-server")`), calls `Initialize` then `ListTools`, each under its own
`context.WithTimeout` (this used to be one shared 20s budget for both calls
combined — see §10, that was a real bug), and returns an `mcpDoneMsg`. The
client is cached on the model (`m.mcpClient`) and reused for every
subsequent target in the session — you only pay the connect cost once.

**Running a scan** (`startScan` in [update.go](../internal/ui/update.go)):
launches a goroutine that calls `mcpscan.Run` / `RunFiles` / `RunSecrets`
depending on `scanKind`, with a `progress func(Progress)` that does a
**non-blocking send** (`select { case progressCh <- p: default: }`) onto a
buffered channel — never blocks the scan goroutine even if the UI is slow
to drain it, at the cost of possibly dropping an intermediate progress
tick (final result always arrives via `doneCh`, which is never dropped).
`waitForScan` returns a `tea.Cmd` that blocks on
`select { progressCh | doneCh }` and turns whichever fires into a Bubble
Tea message — this is the bridge between "a goroutine with channels" and
"Bubble Tea's message-passing event loop".

**Targets and where their data comes from**:
| Target | Scan dir | Notes |
|---|---|---|
| Local Changes | repo root | `gitx.ChangedFiles` scopes it to `RunFiles` |
| Pick a Commit | temp `git worktree` | `gitx.AddWorktree`; cleaned up after |
| Full Repository | repo root | `mcpscan.Run`, walks everything |
| Secrets Scan | repo root | needs `gitx.HasRemote` to be true first |

A commit scan never touches your real working tree — `gitx.AddWorktree`
checks the commit out into a disposable temp directory via `git worktree
add --detach --force`, and `cleanup()` runs `git worktree remove --force`
+ `prune` afterward.

## 9. Fix review and apply — `internal/fix`

`Build(finding, repoDir) → *Plan`:
1. `target()` decides *what* to bump: secrets/SAST/malware are rejected up
   front with `ErrNoAutoFix` (rotating a secret isn't a manifest edit); for
   SCA findings it prefers `finding.Upgrade` (from
   `version_recommendations`) and falls back to `finding.Fixed` only for a
   **direct** dependency (a transitive finding with no direct upgrade path
   is rejected — bumping a transitive dep in the manifest wouldn't do
   anything, since it's not declared there).
2. `candidates()` finds the right manifest file: reported manifests first,
   then a bounded filesystem walk (depth ≤ 4, skipping `node_modules` /
   `vendor` / etc.) as a fallback.
3. Each ecosystem has its own regex-based line editor
   (`editors[eco]` in [editors.go](../internal/fix/editors.go)) that finds
   and rewrites *only* the version string — it never touches surrounding
   structure or regenerates lockfiles (the UI suggests the follow-up
   command: `npm install`, `go mod tidy`, etc. via `PostCommand()`).
4. `MakeDiff(before, after, ctx)` builds a line-level diff (this edit never
   changes line count, so it's a direct index-by-index comparison, not a
   general LCS diff algorithm) for the UI to render before asking for
   confirmation.

`Plan.Apply()`: re-reads the file and **aborts if its content has changed
since the plan was built** (optimistic-concurrency check), then writes via
`os.CreateTemp` in the same directory + `os.Rename` — rename is atomic on
the same filesystem, so a crash mid-write can never leave a half-written
manifest on disk.

## 10. A real bug this project had — good interview material

`internal/ui/model.go`'s `connectMCP` originally gave **one 20-second
`context.WithTimeout`** to *both* `client.Initialize(ctx)` and the
following `client.ListTools(ctx)` call, sharing the same deadline:

```go
ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
if err := client.Initialize(ctx); err != nil { ... }
tools, err := client.ListTools(ctx)   // reuses the same, now-nearly-expired ctx
```

In practice, `endorctl ai-tools mcp-server` does a blocking background call
to a default namespace's policy list on startup before it's ready to serve
JSON-RPC at all; on a fresh/anonymous session that call reliably fails
(403) after ~5s and the server doesn't become ready for another ~5-10s
after that — so time-to-first-response measured out around **21
seconds**, past the 20s budget. The connection attempt lost the race almost
every run, `Initialize` returned `context.DeadlineExceeded`, and the UI sat
on "Connecting..." before failing — and even on a lucky run, `ListTools`
would get started with essentially no time left on the now-shared,
nearly-expired context. Fix: separate `context.WithTimeout` per call (60s
for `Initialize`, 30s for `ListTools`), each with its own full budget. This
is a nice concrete example of **shared-deadline-context-reuse** as a bug
class: it's not that the timeout value was wrong in isolation, it's that
two sequential operations were forced to split one budget.

## 11. Dead code worth knowing about

`internal/endor` (`run.go`, `endor.go`) implements an entirely separate
code path: shelling out to `endorctl scan ...` directly as a CLI subprocess
and parsing its JSON/SARIF output, with its own error classification
(`ScanError`, `ErrKind` for auth/network/license/timeout). **Nothing in
`cmd/` or `internal/ui` imports it** — the real app went all-in on the MCP
path (`internal/mcpscan`) instead. It's only reachable from its own test
file. Likely a leftover from an earlier design before the project
committed to MCP-only. Good to know it's there so you don't accidentally
describe it as part of the live code path in an interview.

## 12. Supporting pieces, briefly

- **`internal/findings`** — the `Finding` struct (severity, category,
  package/version, file/line, CVSS/EPSS, aliases) that both scan engines
  normalize into; `Sort()` orders severity desc → category → location →
  title; `Counts.Risk()` is a **local heuristic**, explicitly not an Endor
  score: `critical*10 + high*5 + medium*2 + low`, capped at 100.
- **`internal/gitx`** — thin wrapper over shelling out to `git` (never a
  shell string, always an argv slice, so there's no shell-injection
  surface). `CreateBranchAndCommit` only ever `git add`s the files you pass
  it explicitly — never `git add -A` — so an in-progress fix commit can't
  accidentally sweep up unrelated changes.
- **`internal/report`** — pure-text bar charts (severity bars, a stacked
  category bar, a 0-100 risk meter, sparklines) built on Lip Gloss for
  ANSI styling; no external charting dependency.
- **`internal/history`** — appends each scan's summary to a local JSON file
  so results can be compared over time (used for the sparkline trend).

## 13. The installer scripts

`install.ps1` / `install.sh` both: check requirements up front (git, Go
≥1.24, Node ≥18 for npx, lazygit optionally) and print a full pass/fail
checklist before doing any work, clone the repo into a private cache dir,
`go build` the binary into a per-user bin dir, add that dir to PATH if
missing, then write (or merge into) Lazygit's `config.yml` a
`customCommands` entry binding `Ctrl+A` to the installed binary with
`output: terminal` (so Lazygit suspends and hands the whole screen to
`auri-tui` while it runs, same as any other Lazygit custom command).
