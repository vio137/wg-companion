# Isolated GitHub-hosted Windows runner only. Actual install/removal if a real
# desktop exists; otherwise assert preflight refusal leaves no system writes.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$destination = Join-Path $env:ProgramFiles 'WireGuard Companion'
$exe = Join-Path $root 'dist/WireGuard-Companion-Setup-0.2.0-preview-x64.exe'
$manifest = 'HKLM:\SOFTWARE\Google\Chrome\NativeMessagingHosts\com.wgcompanion.tunnel'
$uninstallKey = 'HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion'
foreach ($p in @($destination,$manifest,'HKLM:\SOFTWARE\WGCompanion',$uninstallKey)) {
  if (Test-Path $p) { throw "Refuse to modify existing installation state: $p" }
}
if (Get-Service WGCompanion -ErrorAction SilentlyContinue) { throw 'Refuse to modify existing service' }
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class DesktopState {
 [DllImport("user32.dll")] public static extern IntPtr GetShellWindow();
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint p);
}
'@
$session = [System.Diagnostics.Process]::GetCurrentProcess().SessionId
$shell = [DesktopState]::GetShellWindow()
$hasDesktop = $session -ne 0 -and $shell -ne [IntPtr]::Zero
$p = Start-Process -FilePath $exe -ArgumentList '/S' -PassThru
if (-not $p.WaitForExit(45000)) { Stop-Process -Id $p.Id -Force; throw 'Installer hung' }
if (-not $hasDesktop) {
  if ($p.ExitCode -eq 0) { throw 'Headless install falsely reported success' }
  foreach ($path in @($destination,$manifest,'HKLM:\SOFTWARE\WGCompanion',$uninstallKey)) {
    if (Test-Path $path) { throw "Headless preflight changed $path" }
  }
  if (Get-Service WGCompanion -ErrorAction SilentlyContinue) { throw 'Headless preflight created service' }
  Write-Output 'PASS actual installer fails closed without desktop owner; no installation state created'
  exit 0
}
if ($p.ExitCode -ne 0) { throw "Actual installer failed: $($p.ExitCode)" }
try {
  $service = Get-CimInstance Win32_Service -Filter "Name='WGCompanion'"
  if (-not $service -or $service.State -ne 'Running' -or $service.StartName -ne 'NT AUTHORITY\LocalService') { throw 'Installed service identity/state mismatch' }
  if ($service.StartMode -ne 'Auto') { throw 'Service not configured for automatic startup' }
  $installedExe = Join-Path $destination 'wg-companion.exe'
  if ($service.PathName -notlike "*$installedExe*") { throw 'Service points to wrong binary' }
  [uint32]$shellPid = 0
  [void][DesktopState]::GetWindowThreadProcessId($shell,[ref]$shellPid)
  $shellProcess = Get-CimInstance Win32_Process -Filter "ProcessId=$shellPid"
  $shellOwner = Invoke-CimMethod -InputObject $shellProcess -MethodName GetOwnerSid
  if ($shellOwner.ReturnValue -ne 0) { throw 'Cannot resolve desktop owner for independent check' }
  $owner = (Get-ItemProperty 'HKLM:\SOFTWARE\WGCompanion').OwnerSID
  if ($owner -ne $shellOwner.Sid) { throw 'Installed pipe owner differs from actual desktop shell owner' }
  $registered = (Get-Item $manifest).GetValue('')
  $native = Get-Content -Raw $registered | ConvertFrom-Json
  if ($native.path -ne $installedExe -or $native.allowed_origins[0] -ne 'chrome-extension://dfcikohmdkdkkaampidiknlgkognlkah/') { throw 'Native manifest mismatch' }
  $st = & $installedExe service-status | ConvertFrom-Json
  if ($LASTEXITCODE -ne 0 -or $st.state -ne 'disconnected') { throw 'Fresh service incorrectly reports connected' }
  foreach ($path in @('wg-tray.exe','wgcf.exe','extension/manifest.json','licenses/INDEX.md','Uninstall.exe','README-WINDOWS.md')) {
    if (-not (Test-Path (Join-Path $destination $path))) { throw "Missing installed payload: $path" }
  }
  $tray = Start-Process -FilePath (Join-Path $destination 'wg-tray.exe') -PassThru
  Start-Sleep -Seconds 3
  $tray.Refresh()
  if ($tray.HasExited) { throw 'Installed tray exited unexpectedly' }
  Stop-Process -Id $tray.Id -Force
  # Re-running installer must refuse, preserving exact installed binaries.
  $before = (Get-FileHash $installedExe -Algorithm SHA256).Hash
  $second = Start-Process -FilePath $exe -ArgumentList '/S' -PassThru
  if (-not $second.WaitForExit(30000)) { Stop-Process -Id $second.Id -Force; throw 'Duplicate install preflight hung' }
  if ($second.ExitCode -eq 0 -or (Get-FileHash $installedExe -Algorithm SHA256).Hash -ne $before) { throw 'Existing-install preflight did not preserve installation' }
  Write-Output 'PASS actual installer: LocalService auto-start, desktop owner SID, manifest, payload, installed pipe status, tray process startup, existing-install refusal'
} finally {
  if (Test-Path (Join-Path $destination 'Uninstall.exe')) {
    $u = Start-Process -FilePath (Join-Path $destination 'Uninstall.exe') -ArgumentList '/S' -PassThru
    if (-not $u.WaitForExit(45000)) { Stop-Process -Id $u.Id -Force; throw 'Uninstaller hung' }
    if ($u.ExitCode -ne 0) { throw "Uninstaller failed: $($u.ExitCode)" }
  }
}
foreach ($path in @($destination,$manifest,'HKLM:\SOFTWARE\WGCompanion',$uninstallKey)) {
  if (Test-Path $path) { throw "Uninstaller retained installation state: $path" }
}
if (Get-Service WGCompanion -ErrorAction SilentlyContinue) { throw 'Uninstaller retained service' }
Write-Output 'PASS actual uninstaller removes program folder, service, native host, owner record and Apps registration'
