# Endor Labs MCP client

## What it is

`internal/mcp` is a client for the Model Context Protocol (MCP): it runs an MCP server as a
subprocess and talks to it with JSON-RPC 2.0 over stdio. `auri-tui` uses it to talk to the
Endor Labs AURI MCP server (`endorctl ai-tools mcp-server`), which is the only Endor Labs
integration path that works on the free Developer Edition tier — `endorctl scan` and the rest
of the Enterprise CLI require a paid license.

## Free-tier tools actually used

The MCP server exposes more tools than this, but only three are confirmed usable without a
paid Enterprise CLI license:

- **`check_dependency_for_risks`** — given `{ecosystem, dependency_name, version}` (ecosystem
  is one of `python`/`java`/`go`/`npm`/`maven`), returns whether the dependency is vulnerable
  or flagged as malware, the vulnerability IDs involved, and `version_recommendations`: a map
  whose *keys* are refs like `mvn://pkg@<fixed-version>` and whose values are the recommended
  version. The version embedded in that key is the **fixed** version, not the one installed —
  `internal/fix`'s plan builder is careful to always take "current version" from the dependency
  it looked up, never from parsing that ref.
- **`get_endor_vulnerability`** — given a vulnerability ID (e.g. a GHSA or CVE alias), returns
  full advisory details: `vulnerability.meta.description`, `vulnerability.spec.summary`,
  `.cvssV3Severity.{level,score}`, `.epssScore.probabilityScore`, `.databaseSpecific.severity`,
  `.aliases`.
- **`scan`** — a generic repo-wide scanner (`{path, scan_types, scan_options}`). Only
  `scan_types: ["secrets"]` is wired up here (`internal/mcpscan/secrets.go`); it runs Endor's
  real secrets detector over the whole working tree server-side and requires the target repo
  to have a git `origin` remote configured. Its response isn't clean JSON — the `results` field
  is prose text with an embedded `{"objects": [...]}` blob appended, or literal text ending in
  "No problems found" when clean. SAST and malware-dedicated `scan_types` were not tested and
  are not wired in.

`internal/mcpscan` orchestrates all of it: for dependency targets, it discovers every
dependency in scope (`internal/deps`), calls `check_dependency_for_risks` for each (worker
pool, 4 concurrent), collects the union of vulnerability IDs referenced, calls
`get_endor_vulnerability` for each of those, and assembles the result into normalized
`findings.Finding` values. For the secrets target, a single `scan` call replaces all of that.

## How `auri-tui` uses it

There's no manual tool-invocation flag — the MCP client is wired into the main scan flow
(`internal/ui`). Running `auri-tui` opens a menu to pick a scan target (local changes, a
commit, the full repo, or a repo-wide secrets scan); selecting one connects to the MCP server,
runs the scan with live progress, and renders chart-based results. The **Browse MCP Tools**
menu item is a manual fallback that lists every tool the server advertises (via `tools/list`)
and lets you call any of them with hand-typed parameters — useful for exploring tools beyond
the three above, though most of them require a paid license to actually return data.

## Client API (`internal/mcp`)

- **types.go** — JSON-RPC 2.0 types: `JSONRPCRequest`, `JSONRPCResponse`, `Tool`, `ToolResult`
  (`{Content []ContentBlock, StructuredContent, IsError}` — matches the real server's response
  shape, which wraps a JSON string in a text content block *and* mirrors it as structured data).
- **client.go**:
  - `NewClient(cmd, args...)` — starts the given command as a subprocess.
  - `Initialize(ctx)` — performs the MCP handshake.
  - `ListTools(ctx)` — lists the tools the server advertises.
  - `CallTool(ctx, name, args)` — invokes a tool.
  - `Close()` — terminates the subprocess.
- **client_test.go** — tests against a fake in-process server (`FAKE_SERVER=1` re-exec trick).

```go
client, err := mcp.NewClient("npx", "-y", "endorctl", "ai-tools", "mcp-server")
if err != nil {
    log.Fatal(err)
}
defer client.Close()

ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
defer cancel()
if err := client.Initialize(ctx); err != nil {
    log.Fatal(err)
}

result, err := client.CallTool(ctx, "check_dependency_for_risks", map[string]interface{}{
    "ecosystem":       "npm",
    "dependency_name": "lodash",
    "version":         "4.17.15",
})
```

## Requirements

- `endorctl` available (installed, or reachable via `npx -y endorctl`).
- A valid Endor Labs session: `npx -y endorctl init` (free Developer Edition).

## Tests

```bash
go test ./internal/mcp -v        # client, against a fake server
```
