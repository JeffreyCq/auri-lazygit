#Requires -Version 5.1
<#
  AURI x Lazygit installer (Windows / PowerShell).

  Usage (from PowerShell):
    irm https://raw.githubusercontent.com/JeffreyCq/auri-lazygit/main/install.ps1 | iex

  This builds auri-tui.exe from source with your local Go toolchain instead
  of downloading a prebuilt binary, so it always matches your machine and
  needs no code-signing to run.
#>

$ErrorActionPreference = 'Stop'

$RepoUrl = 'https://github.com/JeffreyCq/auri-lazygit.git'
$SrcDir  = Join-Path $env:LOCALAPPDATA 'auri-tui\src'   # private cache clone, owned by this script
$BinDir  = Join-Path $env:LOCALAPPDATA 'auri-tui\bin'
$BinName = 'auri-tui.exe'

function Info($msg) { Write-Host "  ==> $msg" -ForegroundColor Green }
function Warn($msg) { Write-Host "  !!  $msg" -ForegroundColor Yellow }
function Err($msg)  { Write-Host "  xx  $msg" -ForegroundColor Red }
function Have($cmd) { [bool](Get-Command $cmd -ErrorAction SilentlyContinue) }

Write-Host ""
Write-Host "  AURI x Lazygit installer"
Write-Host "  ------------------------"
Write-Host ""

# ---- prerequisites -----------------------------------------------------

if (-not (Have 'go')) {
    Err "Go is required to build auri-tui, but wasn't found on your PATH."
    Err "Install it with:  winget install GoLang.Go"
    Err "or see https://go.dev/dl/"
    exit 1
}

if (-not (Have 'git')) {
    Err "git is required to fetch the source, but wasn't found on your PATH."
    Err "Install it with:  winget install Git.Git"
    exit 1
}

# ---- fetch source -------------------------------------------------------

Info "Fetching source into $SrcDir"
if (Test-Path $SrcDir) { Remove-Item -Recurse -Force $SrcDir }
New-Item -ItemType Directory -Force -Path (Split-Path $SrcDir) | Out-Null
git clone --depth 1 $RepoUrl $SrcDir *> $null
if ($LASTEXITCODE -ne 0) {
    # A failed native command (unlike a cmdlet) doesn't throw, so this has
    # to be checked explicitly instead of relying on try/catch.
    Err "Failed to clone $RepoUrl"
    exit 1
}

# ---- build + install -----------------------------------------------------

Info "Building $BinName (this downloads Go module deps on first run)..."
New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
Push-Location $SrcDir
try {
    & go build -ldflags "-s -w" -o (Join-Path $BinDir $BinName) ./cmd/auri-tui
    if ($LASTEXITCODE -ne 0) { throw "go build exited with code $LASTEXITCODE" }
} finally {
    Pop-Location
}
Info "Installed $(Join-Path $BinDir $BinName)"

if (-not (Have 'npx')) {
    Warn "Node.js (npx) wasn't found. auri-tui needs it to launch the Endor Labs MCP server."
    Warn "Install it with:  winget install OpenJS.NodeJS.LTS"
    Warn "or see https://nodejs.org/"
}

$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if (-not $userPath) { $userPath = '' }
if (-not ($userPath -split ';' | Where-Object { $_ -eq $BinDir })) {
    $newPath = if ($userPath) { "$userPath;$BinDir" } else { $BinDir }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    Warn "Added $BinDir to your User PATH — open a new terminal for it to take effect."
} else {
    Info "$BinDir is already on your PATH."
}

# ---- wire up Lazygit -----------------------------------------------------

$AuriMarker = 'AURI: open Endor Labs scan menu'
$CmdPath = (Join-Path $BinDir $BinName) -replace '\\', '/'
$EntryBlock = @"
  - key: "<c-a>"
    context: "global"
    description: "$AuriMarker"
    command: "$CmdPath"
    output: terminal
"@

if (Have 'lazygit') {
    $cfgDir = (& lazygit --print-config-dir 2>$null | Select-Object -First 1)
    if ($cfgDir) {
        $cfgDir = $cfgDir.Trim()
        New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
        $cfg = Join-Path $cfgDir 'config.yml'
        if (-not (Test-Path $cfg)) {
            "customCommands:`n$EntryBlock`n" | Set-Content -Path $cfg -Encoding utf8
            Info "Created $cfg with the AURI keybinding (Ctrl+A)."
        } elseif (Select-String -Path $cfg -SimpleMatch $AuriMarker -Quiet) {
            Info "Lazygit is already wired up ($cfg already has the AURI entry)."
        } elseif (-not (Select-String -Path $cfg -Pattern '^customCommands:' -Quiet)) {
            "`ncustomCommands:`n$EntryBlock`n" | Add-Content -Path $cfg -Encoding utf8
            Info "Added the AURI keybinding to $cfg."
        } else {
            Warn "$cfg already has a customCommands: section with other entries."
            Warn "Add this under it by hand (same indentation as the existing list):"
            Write-Host ""
            Write-Host $EntryBlock
            Write-Host ""
        }
    } else {
        Warn "Found lazygit but couldn't read its config dir — add the entry from lazygit/config.yml by hand."
    }
} else {
    Warn "lazygit wasn't found. Install it (https://github.com/jesseduffield/lazygit), then add:"
    Write-Host ""
    Write-Host "customCommands:"
    Write-Host $EntryBlock
    Write-Host ""
    Warn "to its config.yml (lazygit --print-config-dir shows where)."
}

Write-Host ""
Info "Done. Next steps:"
Write-Host "    1. npx -y endorctl init     # free Endor Labs Developer Edition login"
Write-Host "    2. Open lazygit in a git repo and press Ctrl+A"
Write-Host ""
