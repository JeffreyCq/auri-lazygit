# AURI × Lazygit

A Go TUI launched from [Lazygit](https://github.com/jesseduffield/lazygit) that scans your
repository for vulnerable dependencies and exposed secrets using the **Endor Labs AURI MCP
server**, shows the findings as terminal charts, and lets you review and apply fixes with a
diff and confirmation.

It talks to AURI exclusively over MCP (`endorctl ai-tools mcp-server`), which works on the
free Endor Labs Developer Edition tier — no `endorctl scan`/Enterprise CLI license required.

**Contents:** [Install](#install) · [How it works](#how-it-works) ·
[Authenticating](#authenticating-with-endor-labs) · [Keys](#keys) · [Fix review](#fix-review) ·
[Security](#security) · [Known limits](#known-limits) · [Structure](#structure) ·
[Development](#development)

```
 auri-tui  AURI × Lazygit — Endor Labs MCP
────────────────────────────────────────────────────────────────────────────────────────────────────

    █   █   █ ████  █████
   █ █  █   █ █   █   █
  █████ █   █ ████    █
  █   █ █   █ █  █    █
  █   █  ███  █   █ █████
  Endor Labs security scanner, right inside Lazygit

  What do you want to scan?

╭──────────────────────────────────────────────────────────────────────────────────────────────────╮
│  📝 ▸ Local Changes       not a git repository                                                    │
│  📌   Pick a Commit       scan the dependency manifests as of a specific commit                   │
│  📁   Full Repository     scan every manifest in the working tree                                 │
│  🔑   Secrets Scan        scan the full repo for exposed credentials (needs a git remote; …)       │
│  🛠    Browse MCP Tools    advanced: call any Endor Labs MCP tool manually                          │
╰──────────────────────────────────────────────────────────────────────────────────────────────────╯

  This is a manual pre-push check: scans run only when you ask, never automatically.
```

## Install

### macOS / Linux

```bash
curl -fsSL https://raw.githubusercontent.com/JeffreyCq/auri-lazygit/main/install.sh | bash
```

### Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/JeffreyCq/auri-lazygit/main/install.ps1 | iex
```

Both scripts build `auri-tui` from source with your local Go toolchain (no prebuilt binaries
to trust, no macOS Gatekeeper prompt), install it to a per-user directory
(`~/.local/bin` on macOS/Linux, `%LOCALAPPDATA%\auri-tui\bin` on Windows), and — if they find
`lazygit` on your `PATH` — wire up the `Ctrl+A` keybinding in your Lazygit config automatically.
If you already have a `customCommands:` section in your Lazygit config with other entries, the
installer leaves it alone and prints the one entry to add by hand instead of risking a broken
YAML merge.

**Requirements:** [Go](https://go.dev/dl/) 1.24+, [git](https://git-scm.com/), and
[Node.js](https://nodejs.org/) 18+ (for `npx`, which launches the Endor Labs MCP server).
The installer checks every requirement up front — printing a pass/fail checklist for all of
them before doing any cloning or building — rather than failing partway through on the first
one it hits.

### From source (manual)

```bash
git clone https://github.com/JeffreyCq/auri-lazygit.git
cd auri-lazygit
make install     # builds and copies the binary to ~/.local/bin/auri-tui
# make sure ~/.local/bin is on your PATH
```

Then copy the `customCommands` entry from [`lazygit/config.yml`](lazygit/config.yml) into your
own Lazygit config (`lazygit --print-config-dir` shows where).

## How it works

Run `auri-tui` (no flags) from anywhere inside a git repository, or launch it from Lazygit
with a single keybinding. It then:

1. Detects the current repository root from the working directory.
2. Shows a menu to pick **what to scan**: local changes only, a specific commit (typed ref,
   hash, or branch), the full repository, a repo-wide secrets scan, or a manual MCP tool
   browser for advanced use.
3. For dependency targets, connects to the Endor Labs MCP server and discovers dependency
   manifests in the selected scope (`package.json`, `go.mod`, `requirements*.txt`/
   `pyproject.toml`, `pom.xml`, `Cargo.toml`), then queries `check_dependency_for_risks` for
   every dependency found and `get_endor_vulnerability` for every vulnerability referenced,
   streaming progress as it goes. For the secrets target, it makes a single `scan` call that
   runs Endor's secrets detector over the whole working tree server-side (this one needs a
   git `origin` remote configured; the dependency targets don't).
4. Renders the results as block-character charts: severity bars, a category breakdown, and a
   0–100 risk meter (a local heuristic — not an Endor Labs score).
5. Lets you step through findings one at a time and, where a direct-dependency upgrade is
   available, shows a diff and asks whether to apply it or skip — nothing is written to disk
   without confirmation. Secrets findings are never auto-fixable (rotating a credential isn't
   something a diff can do), so review always explains what to do by hand instead.

This is a **manual, user-invoked check** — it never runs automatically (e.g. on commit or push).

## Authenticating with Endor Labs

```bash
npx -y endorctl init     # free Developer Edition — opens a browser to log in / sign up
```

`auri-tui` launches the MCP server itself via `npx -y endorctl ai-tools mcp-server`, so no
separate server process is needed — just a valid `endorctl` session.

## Keys

| Screen | Keys |
|---|---|
| Scan menu | ↑↓ move · enter select (Local Changes, Pick a Commit, Full Repository, Secrets Scan, Browse MCP Tools) · q/esc quit |
| Commit picker | ↑↓ move · enter select · t type a ref (hash/branch/tag) · esc back |
| Scan results | ↑↓ move · f review & apply fixes · esc back to menu |
| Fix review | a apply · s/n skip · esc stop reviewing |
| MCP tool browser | advanced: call any Endor Labs MCP tool manually with custom parameters |

## Fix review

- Bumps the **direct** dependency's version in its manifest (`package.json`, `go.mod`,
  `requirements.txt`, `pyproject.toml`, `pom.xml`, `Cargo.toml`); it does not regenerate
  lockfiles, but suggests the follow-up command (`npm install`, `go mod tidy`, …).
- Shows a diff before writing anything; the write is atomic and fails if the manifest changed
  on disk since the plan was built.
- Findings for a historical commit are scanned in a temporary `git worktree`, but fixes are
  always applied to your real working tree (the commit itself can't be edited).
- Does not attempt to auto-fix exposed secrets, SAST findings, malware, or transitive
  dependencies with no direct upgrade path — it explains why and what to do instead.
- Nothing is ever applied without you pressing `a` on that specific finding — there's no
  bulk-apply, no `--yes` flag, and no way to trigger it from the Lazygit config.

## Security

This is a security-scanning tool, so it holds itself to a few explicit rules:

- **Manual only.** Nothing in `auri-tui` runs on a git hook, a timer, or any other trigger —
  only when you launch it and press a key. It never calls `git commit`, `git push`, or any
  other side-effecting git command on your behalf.
- **No shell injection surface.** Every subprocess call (`git`, `npx`/`endorctl`) is built as
  an argument vector (`exec.Command("git", arg1, arg2, ...)`), never as a shell string — the
  arguments can't be reinterpreted by a shell no matter what they contain. References that
  come from outside your explicit input (like a resolved commit hash) are additionally
  rejected if they start with `-`, so they can never be mistaken for a flag.
- **Your Endor Labs session never passes through this code.** `auri-tui` talks to `endorctl`
  over MCP; it has no HTTP client, holds no API keys, and never sees your credentials —
  `endorctl` owns authentication entirely.
- **Atomic, confirmed writes.** A fix is never applied without you pressing `a` on that exact
  finding. The manifest write happens via a temp file + atomic rename, and aborts if the file
  changed on disk since the diff was shown — no partial writes, no stale edits.
- **A historical commit is scanned in an isolated `git worktree`**, never your real working
  tree, so picking an old commit can't disturb uncommitted changes.

Found something that doesn't hold up? Please open an issue.

## Known limits

- The 0–100 risk index is a local heuristic (critical×10 + high×5 + medium×2 + low, capped
  at 100) — not an Endor Labs score.
- Only MCP tools available on the free Developer Edition tier are used: `check_dependency_for_risks`
  and `get_endor_vulnerability` for dependency/malware findings, and `scan` (scoped to
  `scan_types: ["secrets"]`) for the secrets engine. SAST and malware-dedicated scanning
  are not wired in yet.
- Larger dependency scans take longer: it's one MCP round trip per unique dependency, plus
  one per unique vulnerability ID. The secrets scan is a single call but runs Endor's full
  scanner server-side, so it can take a minute or two, especially on the first run.
- The secrets scan requires the repository to have a git `origin` remote configured
  (`git remote add origin <url>`) — the dependency scans don't need one.
- Windows support: fully exercised and working, including the Lazygit `Ctrl+A` keybinding and
  the installer. The one environment-specific gap is that file permission *bits* (e.g. `0600`
  on a sensitive file) aren't meaningful on Windows/NTFS the way they are on macOS/Linux, so
  that part of the fix-apply behavior is best-effort there. If something's off, please open an
  issue.

## Structure

```
cmd/auri-tui        main() — just starts the Bubble Tea program
internal/mcp        MCP (JSON-RPC 2.0 over stdio) client
internal/mcpscan    orchestrates MCP calls into a scan Result: per-dependency SCA/malware
                    lookups (worker pools, progress streaming) and the single-call secrets
                    engine (secrets.go), both normalized to the same Result/Finding shape
internal/deps       discovers and parses dependency manifests
internal/findings   normalized Finding model
internal/gitx       git helpers: repo root, changed files, recent commits, temp worktrees
internal/fix        manifest editors, diff, atomic apply
internal/report     block-character charts: bars, risk meter, summaries
internal/ui         Bubble Tea: scan menu, commit picker, scanning, results, fix review
install.sh          macOS/Linux installer: builds from source, wires up Lazygit
install.ps1         Windows installer: same, in PowerShell
lazygit/config.yml  customCommands entry to copy into your Lazygit config by hand
```

## Development

```bash
git clone https://github.com/JeffreyCq/auri-lazygit.git
cd auri-lazygit
go build ./...      # or: make build
go vet ./...
go test ./...        # or: make test
```

No linter or formatter config beyond `gofmt`/`go vet` — run those before sending a change.
Tests use real `git` and a real `go build`/`go test`, no network calls or external services.

## License

[MIT](LICENSE)
