# Rebuild the unsigned Windows installer. Run from a clean checkout with Go 1.26,
# Git and NSIS 3.x in PATH. No cloud account or WARP registration is performed.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Push-Location $root
try {
  New-Item -ItemType Directory -Force build/windows, dist, .cache | Out-Null
  Push-Location companion
  try {
    go build -trimpath -ldflags='-s -w' -o ../build/windows/wg-companion.exe .
    if ($LASTEXITCODE -ne 0) { throw 'companion build failed' }
    go build -trimpath -ldflags='-s -w -H windowsgui' -o ../build/windows/wg-tray.exe ./tray
    if ($LASTEXITCODE -ne 0) { throw 'tray build failed' }
  } finally { Pop-Location }
  $revision = 'de6bba7147dc965a1b8e4de113a709750697ccea'
  if (-not (Test-Path .cache/wgcf/.git)) {
    git clone https://github.com/ViRb3/wgcf .cache/wgcf
    if ($LASTEXITCODE -ne 0) { throw 'wgcf source clone failed' }
  }
  Push-Location .cache/wgcf
  try {
    git checkout --detach $revision
    if ($LASTEXITCODE -ne 0) { throw 'wgcf revision unavailable' }
    if ((git rev-parse HEAD).Trim() -ne $revision) { throw 'wgcf revision mismatch' }
    if ((git status --porcelain).Length -gt 0) { throw 'wgcf source is not clean' }
    go build -trimpath -ldflags='-s -w' -o ../../build/windows/wgcf.exe .
    if ($LASTEXITCODE -ne 0) { throw 'wgcf build failed' }
  } finally { Pop-Location }
  Push-Location installer
  try {
    makensis /V2 windows.nsi
    if ($LASTEXITCODE -ne 0) { throw 'NSIS build failed' }
  } finally { Pop-Location }
  Get-FileHash dist/WireGuard-Companion-Setup-0.2.0-preview-x64.exe -Algorithm SHA256
} finally { Pop-Location }
