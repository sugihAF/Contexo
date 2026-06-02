# install.ps1 — install the Contexo CLI (prebuilt binary) on Windows and add it to PATH.
#
# Run from PowerShell:
#   iwr -useb https://raw.githubusercontent.com/sugihAF/Contexo/main/scripts/install.ps1 | iex
#
# No Go toolchain required — this downloads a prebuilt, checksum-verified binary
# from GitHub Releases. Idempotent: safe to re-run. To update later, just run
# `ctx update`.

$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

$Repo = 'sugihAF/Contexo'
$InstallDir = if ($env:CONTEXO_INSTALL_DIR) { $env:CONTEXO_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\contexo' }

function Write-Bold($msg) { Write-Host $msg -ForegroundColor White -BackgroundColor DarkGray }
function Write-Warn($msg) { Write-Host $msg -ForegroundColor Yellow }
function Die($msg)        { Write-Host $msg -ForegroundColor Red; exit 1 }

Write-Bold 'Contexo CLI installer'

# 1. Detect arch ------------------------------------------------------------
switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { $arch = 'amd64' }
    'ARM64' { $arch = 'arm64' }
    default { Die "Unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (prebuilt binaries cover amd64 and arm64)." }
}
Write-Host "  Platform: windows/$arch"

# 2. Resolve the latest release tag -----------------------------------------
$headers = @{ 'User-Agent' = 'contexo-installer'; 'Accept' = 'application/vnd.github+json' }
try {
    $rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
} catch {
    Die "Could not contact GitHub to find the latest release: $($_.Exception.Message)"
}
$tag = $rel.tag_name
if (-not $tag) { Die 'Could not determine the latest release (no tag_name). Has a release been published yet?' }
$version = $tag.TrimStart('v')
Write-Host "  Latest release: $tag"

# 3. Download archive + checksums -------------------------------------------
$asset = "ctx_${version}_windows_${arch}.zip"
$base  = "https://github.com/$Repo/releases/download/$tag"
$tmp   = Join-Path ([System.IO.Path]::GetTempPath()) ("contexo-" + [System.IO.Path]::GetRandomFileName())
New-Item -ItemType Directory -Path $tmp -Force | Out-Null
try {
    Write-Bold "Downloading $asset"
    Invoke-WebRequest -Uri "$base/$asset" -OutFile (Join-Path $tmp $asset) -UseBasicParsing
    Invoke-WebRequest -Uri "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt') -UseBasicParsing

    # 4. Verify checksum ----------------------------------------------------
    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { $_ -match ([regex]::Escape($asset) + '$') } | Select-Object -First 1
    if (-not $line) { Die "No checksum listed for $asset; refusing to install." }
    $expected = ($line -split '\s+')[0]
    $actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmp $asset)).Hash
    if ($actual -ne $expected) { Die "Checksum mismatch for $asset (got $actual, want $expected)." }
    Write-Host '  Verified checksum.'

    # 5. Extract + install --------------------------------------------------
    Expand-Archive -Path (Join-Path $tmp $asset) -DestinationPath $tmp -Force
    $extracted = Join-Path $tmp 'ctx.exe'
    if (-not (Test-Path $extracted)) { Die 'Archive did not contain ctx.exe.' }
    New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
    Move-Item -Path $extracted -Destination (Join-Path $InstallDir 'ctx.exe') -Force
    Write-Host "  Installed to: $(Join-Path $InstallDir 'ctx.exe')"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

# 5a. Warn if a different ctx already shadows this one ----------------------
$existing = (Get-Command ctx -ErrorAction SilentlyContinue).Source
if ($existing -and $existing -ne (Join-Path $InstallDir 'ctx.exe')) {
    Write-Warn "Note: another ctx is already on your PATH at $existing."
    Write-Warn 'Which one runs depends on PATH order — remove the old one if it is stale.'
}

# 6. Already on user PATH? --------------------------------------------------
$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ([string]::IsNullOrEmpty($userPath)) { $userPath = '' }
$onUserPath = ($userPath -split ';') -contains $InstallDir
$onProcessPath = ($env:Path -split ';') -contains $InstallDir

if ($onUserPath -and $onProcessPath) {
    Write-Bold 'ctx is on your PATH. Try: ctx --help'
    exit 0
}

# 7. Append to User Path (persistent) ---------------------------------------
if (-not $onUserPath) {
    if ($userPath -and -not $userPath.EndsWith(';')) { $userPath = $userPath + ';' }
    [Environment]::SetEnvironmentVariable('Path', $userPath + $InstallDir, 'User')
    Write-Host "  Added $InstallDir to your user Path (persistent)"
}

# 8. Refresh current session's Path so no restart is needed ------------------
if (-not $onProcessPath) {
    $env:Path = $env:Path + ';' + $InstallDir
    Write-Host '  Refreshed current session PATH'
}

# 9. Done -------------------------------------------------------------------
Write-Bold 'Done.'
Write-Host 'Try: ctx --help'
Write-Host '(Other terminals already open will need a restart to see the new PATH.)'
