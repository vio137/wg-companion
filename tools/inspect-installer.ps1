# Capture real Windows installer pages without installing. A successful image
# proves layout only. Installation/UAC and tray remain separate runtime checks.
$ErrorActionPreference = 'Stop'
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public static class InstallerPixels {
 [StructLayout(LayoutKind.Sequential)] public struct RECT { public int L,T,R,B; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out RECT r);
 [DllImport("user32.dll")] public static extern bool PrintWindow(IntPtr h, IntPtr dc, uint flags);
 [DllImport("user32.dll")] public static extern IntPtr SendMessage(IntPtr h, uint m, IntPtr w, IntPtr l);
}
'@
$root = Split-Path $PSScriptRoot -Parent
$exe = Join-Path $root 'dist/WireGuard-Companion-Setup-0.2.0-preview-x64.exe'
$p = Start-Process -FilePath $exe -PassThru
try {
  $deadline = (Get-Date).AddSeconds(15)
  do {
    $p.Refresh()
    $h = $p.MainWindowHandle
    if ($h -ne [IntPtr]::Zero) { break }
    if ($p.HasExited) { throw 'installer exited without a window' }
    Start-Sleep -Milliseconds 200
  } while ((Get-Date) -lt $deadline)
  if ($h -eq [IntPtr]::Zero) { throw 'installer UI not available in this runner session' }
  function Save-Page($name) {
    $r = New-Object InstallerPixels+RECT
    if (-not [InstallerPixels]::GetWindowRect($h,[ref]$r)) { throw 'GetWindowRect failed' }
    $b = New-Object System.Drawing.Bitmap(($r.R-$r.L),($r.B-$r.T))
    $g = [System.Drawing.Graphics]::FromImage($b)
    $dc = $g.GetHdc()
    try { if (-not [InstallerPixels]::PrintWindow($h,$dc,0)) { throw 'PrintWindow failed' } }
    finally { $g.ReleaseHdc($dc) }
    try { $b.Save((Join-Path $root "build/windows/$name.png"),[System.Drawing.Imaging.ImageFormat]::Png) }
    finally { $g.Dispose();$b.Dispose() }
  }
  Save-Page 'installer-welcome'
  # Advance welcome -> license ONLY, not license -> install.
  [void][InstallerPixels]::SendMessage($h,0x111,[IntPtr]1,[IntPtr]::Zero)
  Save-Page 'installer-license'
} finally {
  if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force }
}
