# Isolated GitHub-hosted Windows runner only. Verifies the actual signed-out /
# session-0 installer fails closed and does not pretend installation succeeded.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$destination = Join-Path $env:ProgramFiles 'WireGuard Companion'
$exe = Join-Path $root 'dist/WireGuard-Companion-Setup-0.2.0-preview-x64.exe'
if (Test-Path $destination) { throw 'Refuse to modify existing installed files' }
if (Get-Service WGCompanion -ErrorAction SilentlyContinue) { throw 'Refuse to modify existing service' }
$manifest = 'HKLM:\SOFTWARE\Google\Chrome\NativeMessagingHosts\com.wgcompanion.tunnel'
if (Test-Path $manifest) { throw 'Refuse to modify existing native host registration' }
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class DesktopState {
 [DllImport("user32.dll")] public static extern IntPtr GetShellWindow();
}
'@
$session = [System.Diagnostics.Process]::GetCurrentProcess().SessionId
$shell = [DesktopState]::GetShellWindow()
if ($session -ne 0 -and $shell -ne [IntPtr]::Zero) {
  throw 'This test requires no interactive desktop; it does not authorize an actual install'
}
$p = Start-Process -FilePath $exe -ArgumentList '/S' -PassThru
if (-not $p.WaitForExit(30000)) { Stop-Process -Id $p.Id -Force; throw 'Installer headless preflight hung' }
if ($p.ExitCode -eq 0) { throw 'Headless installation falsely reported success' }
if (Test-Path $destination) { throw 'Headless preflight wrote Program Files despite refusing install' }
if (Get-Service WGCompanion -ErrorAction SilentlyContinue) { throw 'Headless preflight created a service' }
if (Test-Path $manifest) { throw 'Headless preflight changed native host registration' }
if (Test-Path 'HKLM:\SOFTWARE\WGCompanion') { throw 'Headless preflight wrote owner SID' }
Write-Output 'PASS actual silent installer fails closed without desktop owner; no files, service or registration created'
