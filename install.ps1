#Requires -Version 5.1
<#
  AURI x Lazygit installer (Windows / PowerShell).

  Usage (from PowerShell):
    irm https://raw.githubusercontent.com/JeffreyCq/auri-lazygit/main/install.ps1 | iex

  This builds auri-tui.exe from source with your local Go toolchain instead
  of downloading a prebuilt binary, so it always matches your machine and
  needs no code-signing to run.
#>

# Not 'Stop': PowerShell treats any native-command stderr write (e.g. git's
# routine progress text) as an error, which 'Stop' would promote to
# terminating and halt the script on perfectly successful commands. We check
# $LASTEXITCODE ourselves after the native calls that matter instead.
$ErrorActionPreference = 'Continue'
$PSNativeCommandUseErrorActionPreference = $false  # same guard on PS 7.3+; no-op elsewhere

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

# ---- requirements check --------------------------------------------------
# Check everything up front and report the full picture in one pass, instead
# of failing on the first missing tool, having the user fix it, rerun, and
# hit the next one.

Info "Checking requirements..."

$hardFailures = @()
$minGo = [version]'1.24'
$minNode = 18

function Show-Check($ok, $label, $detail) {
    $mark = if ($ok) { '[ok]' } else { '[--]' }
    $color = if ($ok) { 'Green' } else { 'Red' }
    Write-Host ("      {0,-5} {1,-10} {2}" -f $mark, $label, $detail) -ForegroundColor $color
}

# git
if (Have 'git') {
    $gitVer = (git --version) 2>$null
    Show-Check $true 'git' $gitVer
} else {
    Show-Check $false 'git' 'not found on PATH'
    $hardFailures += "git is required to fetch the source.`n        Install it with:  winget install Git.Git`n        or see https://git-scm.com/"
}

# go (version-gated)
if (Have 'go') {
    $goRaw = (go version) 2>$null  # e.g. "go version go1.27.0 windows/amd64"
    $goOk = $false
    if ($goRaw -match 'go(\d+\.\d+(\.\d+)?)') {
        try {
            $goVer = [version]$Matches[1]
            $goOk = $goVer -ge $minGo
        } catch { $goOk = $false }
    }
    Show-Check $goOk 'Go' "$goRaw $(if (-not $goOk) { "(need >= $minGo)" })"
    if (-not $goOk) {
        $hardFailures += "Go $minGo or newer is required to build auri-tui (found: $goRaw).`n        Install it with:  winget install GoLang.Go`n        or see https://go.dev/dl/"
    }
} else {
    Show-Check $false 'Go' 'not found on PATH'
    $hardFailures += "Go $minGo or newer is required to build auri-tui, but wasn't found on your PATH.`n        Install it with:  winget install GoLang.Go`n        or see https://go.dev/dl/"
}

# node / npx (version-gated, soft requirement  -  needed to *run* auri-tui,
# not to build it, so this warns instead of blocking the install)
$nodeWarning = $null
if (Have 'node') {
    $nodeRaw = (node --version) 2>$null  # e.g. "v24.14.1"
    $nodeOk = $false
    if ($nodeRaw -match 'v(\d+)') {
        $nodeOk = [int]$Matches[1] -ge $minNode
    }
    Show-Check $nodeOk 'Node.js' "$nodeRaw $(if (-not $nodeOk) { "(need >= v$minNode, for npx)" })"
    if (-not $nodeOk) {
        $nodeWarning = "Node.js v$minNode or newer is recommended (found: $nodeRaw)  -  auri-tui launches the Endor Labs MCP server via npx."
    }
} else {
    Show-Check $false 'Node.js' 'not found on PATH (needed to run auri-tui, not to build it)'
    $nodeWarning = "Node.js wasn't found. auri-tui needs it (for npx) to launch the Endor Labs MCP server.`n        Install it with:  winget install OpenJS.NodeJS.LTS`n        or see https://nodejs.org/"
}

# lazygit (fully optional  -  only used to wire up the Ctrl+A keybinding)
if (Have 'lazygit') {
    $lgVer = (lazygit --version) 2>$null
    Show-Check $true 'lazygit' "$lgVer (optional)"
} else {
    Show-Check $false 'lazygit' 'not found on PATH (optional  -  install to get the Ctrl+A keybinding wired up automatically)'
}

Write-Host ""

if ($hardFailures.Count -gt 0) {
    Err "Missing requirements  -  install these first, then rerun this script:"
    Write-Host ""
    foreach ($f in $hardFailures) {
        Err $f
        Write-Host ""
    }
    exit 1
}

if ($nodeWarning) { Warn $nodeWarning; Write-Host "" }

Info "All required tools are present. Continuing..."
Write-Host ""

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

$userPath = [Environment]::GetEnvironmentVariable('PATH', 'User')
if (-not $userPath) { $userPath = '' }
if (-not ($userPath -split ';' | Where-Object { $_ -eq $BinDir })) {
    $newPath = if ($userPath) { "$userPath;$BinDir" } else { $BinDir }
    [Environment]::SetEnvironmentVariable('PATH', $newPath, 'User')
    Warn "Added $BinDir to your User PATH  -  open a new terminal for it to take effect."
} else {
    Info "$BinDir is already on your PATH."
}

# ---- wire up Lazygit -----------------------------------------------------

$AuriMarker = 'AURI: open Endor Labs scan menu'
$CmdPath = (Join-Path $BinDir $BinName) -replace '\\', '/'
$EntryLines = @(
    '  - key: "<c-a>"'
    '    context: "global"'
    "    description: ""$AuriMarker"""
    "    command: ""$CmdPath"""
    '    output: terminal'
)
$EntryBlock = $EntryLines -join "`n"

if (Have 'lazygit') {
    $cfgDir = (& lazygit --print-config-dir 2>$null | Select-Object -First 1)
    if ($cfgDir) {
        $cfgDir = $cfgDir.Trim()
        New-Item -ItemType Directory -Force -Path $cfgDir | Out-Null
        $cfg = Join-Path $cfgDir 'config.yml'
        if (-not (Test-Path $cfg)) {
            ("customCommands:`n" + $EntryBlock + "`n") | Set-Content -Path $cfg -Encoding utf8
            Info "Created $cfg with the AURI keybinding (Ctrl+A)."
        } elseif (Select-String -Path $cfg -SimpleMatch $AuriMarker -Quiet) {
            Info "Lazygit is already wired up ($cfg already has the AURI entry)."
        } elseif (-not (Select-String -Path $cfg -Pattern '^customCommands:' -Quiet)) {
            ("`ncustomCommands:`n" + $EntryBlock + "`n") | Add-Content -Path $cfg -Encoding utf8
            Info "Added the AURI keybinding to $cfg."
        } else {
            Warn "$cfg already has a customCommands: section with other entries."
            Warn "Add this under it by hand (same indentation as the existing list):"
            Write-Host ""
            Write-Host $EntryBlock
            Write-Host ""
        }
    } else {
        Warn "Found lazygit but couldn't read its config dir  -  add the entry from lazygit/config.yml by hand."
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
