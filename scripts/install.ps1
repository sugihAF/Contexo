# install.ps1 — install the Contexo CLI on Windows and add it to PATH.
#
# Run from PowerShell:
#   iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
#
# Requires: Go 1.25+. Refuses (without installing anything) if Go is missing.
# Idempotent: safe to re-run; PATH is updated at most once.

$ErrorActionPreference = 'Stop'

function Write-Bold($msg) { Write-Host $msg -ForegroundColor White -BackgroundColor DarkGray }
function Write-Warn($msg) { Write-Host $msg -ForegroundColor Yellow }
function Die($msg)        { Write-Host $msg -ForegroundColor Red; exit 1 }

Write-Bold 'Contexo CLI installer'

# 1. Go check ----------------------------------------------------------------
$go = Get-Command go -ErrorAction SilentlyContinue
if (-not $go) {
    Die 'Go is not installed. Install Go 1.25+ from https://go.dev/dl, then re-run this script.'
}
Write-Host ('  Found Go: ' + (& go version))

# 2. Install -----------------------------------------------------------------
Write-Bold 'Installing ctx (go install github.com/sugihAF/Contexo/cmd/ctx@latest)'
& go install github.com/sugihAF/Contexo/cmd/ctx@latest
if ($LASTEXITCODE -ne 0) { Die "go install failed (exit $LASTEXITCODE)" }

# 3. Resolve install directory ----------------------------------------------
$installDir = (& go env GOBIN)
if ([string]::IsNullOrWhiteSpace($installDir)) {
    $installDir = Join-Path (& go env GOPATH) 'bin'
}
$ctxBin = Join-Path $installDir 'ctx.exe'
if (-not (Test-Path $ctxBin)) {
    Die "ctx was not written to $installDir (unexpected — check 'go install' output above)."
}
Write-Host ('  Installed to: ' + $ctxBin)

# 4. Already on user PATH? ---------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ([string]::IsNullOrEmpty($userPath)) { $userPath = '' }
$onUserPath = ($userPath -split ';') -contains $installDir
$onProcessPath = ($env:Path -split ';') -contains $installDir

if ($onUserPath -and $onProcessPath) {
    Write-Bold 'ctx is already on your PATH. Try: ctx --help'
    exit 0
}

# 5. Append to User Path (persistent) ---------------------------------------
if (-not $onUserPath) {
    if ($userPath -and -not $userPath.EndsWith(';')) { $userPath = $userPath + ';' }
    $newPath = $userPath + $installDir
    [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
    Write-Host ('  Added ' + $installDir + ' to your user Path (persistent)')
}

# 6. Refresh current session's Path so the user doesn't need to restart -----
if (-not $onProcessPath) {
    $env:Path = $env:Path + ';' + $installDir
    Write-Host '  Refreshed current session PATH'
}

# 7. Done --------------------------------------------------------------------
Write-Bold 'Done.'
Write-Host 'Try: ctx --help'
Write-Host '(Other terminals already open will need a restart to see the new PATH.)'
