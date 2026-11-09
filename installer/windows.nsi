Unicode true
!include "MUI2.nsh"
!include "x64.nsh"
!define PRODUCT "WireGuard Companion"
!define VERSION "0.2.0"
Name "${PRODUCT} ${VERSION} Preview"
OutFile "../dist/WireGuard-Companion-Setup-0.2.0-preview-x64.exe"
InstallDir "$PROGRAMFILES64\WireGuard Companion"
RequestExecutionLevel admin
SetCompressor /SOLID lzma
BrandingText "Local WireGuard tunnel · Windows preview"
!define MUI_ICON "../extension/app.ico"
!define MUI_UNICON "../extension/app.ico"
!define MUI_WELCOMEPAGE_TEXT "Install the local WireGuard background service and Chrome extension files.$\r$\n$\r$\nPREVIEW: Windows runtime verification is pending. TCP proxy only, not a system VPN. No code-signing certificate. Do not disable Windows security to install.$\r$\n$\r$\nAdministrator approval is needed for the service. Start setup from the desktop of the person who will use it. No cloud account is created. Saved profiles remain private to that Windows user."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "../LICENSE"
!insertmacro MUI_PAGE_INSTFILES
!define MUI_FINISHPAGE_TEXT "The background service is installed. A running service does not mean a connected tunnel.$\r$\n$\r$\nAfter closing setup, open WireGuard Companion from Start in your normal desktop session. It starts automatically next sign-in.$\r$\n$\r$\nChrome: Developer mode > Load unpacked > select the extension folder in your install directory. Import a profile in Chrome. Read README-WINDOWS.md for limits."
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
Function .onInit
 ${IfNot} ${RunningX64}
  MessageBox MB_ICONSTOP "This package needs 64-bit Windows."
  Abort
 ${EndIf}
 SetRegView 64
FunctionEnd
Section "Companion + service" SecMain
 SetShellVarContext all
 ; Fixed protected path. A LocalService binary must never be user-writable.
 StrCpy $INSTDIR "$PROGRAMFILES64\WireGuard Companion"
 InitPluginsDir
 SetOutPath "$PLUGINSDIR"
 File /oname=preflight.exe "../build/windows/wg-companion.exe"
 ExecWait '"$PLUGINSDIR\preflight.exe" preflight-service' $0
 ${If} $0 != 0
  MessageBox MB_ICONSTOP "Setup preflight failed (code $0). Nothing was copied to Program Files. Use the intended user's desktop and uninstall an existing service first."
  SetErrorLevel 1
  Abort
 ${EndIf}
 SetOutPath "$INSTDIR"
 File "../build/windows/wg-companion.exe"
 File "../build/windows/wg-tray.exe"
 File "../build/windows/wgcf.exe"
 File "../LICENSE"
 File "../installer/README-WINDOWS.md"
 File "../THIRD-PARTY.md"
 SetOutPath "$INSTDIR\extension"
 File /r "../extension/*"
 SetOutPath "$INSTDIR"
 ExecWait '"$INSTDIR\wg-companion.exe" install-service' $0
 ${If} $0 != 0
  MessageBox MB_ICONSTOP "Service setup failed (code $0). No successful installation is claimed. Start setup from the intended user's interactive desktop; an existing install must be removed before reinstalling. See README-WINDOWS.md."
  SetErrorLevel 1
  Abort
 ${EndIf}
 WriteUninstaller "$INSTDIR\Uninstall.exe"
 CreateDirectory "$SMPROGRAMS\WireGuard Companion"
 CreateShortcut "$SMPROGRAMS\WireGuard Companion\WireGuard Companion.lnk" "$INSTDIR\wg-tray.exe"
 CreateShortcut "$SMPROGRAMS\WireGuard Companion\Extension folder.lnk" "$INSTDIR\extension"
 CreateShortcut "$SMPROGRAMS\WireGuard Companion\Read me.lnk" "$INSTDIR\README-WINDOWS.md"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion" "DisplayName" "${PRODUCT} (Preview)"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion" "DisplayVersion" "${VERSION}"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion" "Publisher" "WG Companion project (unsigned)"
 WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
 WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion" "NoModify" 1
 WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion" "NoRepair" 1
SectionEnd
Section "Uninstall"
 SetRegView 64
 SetShellVarContext all
 MessageBox MB_OKCANCEL "Turn off and remove the Chrome extension first. Quit the tray app. Saved encrypted profiles will be kept. Continue?" IDOK +2
 Abort
 ExecWait '"$INSTDIR\wg-companion.exe" uninstall-service' $0
 ${If} $0 != 0
  MessageBox MB_ICONSTOP "Service removal failed (code $0). Program files have been kept so you can retry."
  Abort
 ${EndIf}
 DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\WGCompanion"
 RMDir /r "$SMPROGRAMS\WireGuard Companion"
 Delete /REBOOTOK "$INSTDIR\wg-companion.exe"
 Delete /REBOOTOK "$INSTDIR\wg-tray.exe"
 Delete /REBOOTOK "$INSTDIR\wgcf.exe"
 Delete "$INSTDIR\LICENSE"
 Delete "$INSTDIR\README-WINDOWS.md"
 Delete "$INSTDIR\THIRD-PARTY.md"
 RMDir /r "$INSTDIR\extension"
 Delete /REBOOTOK "$INSTDIR\Uninstall.exe"
 RMDir "$INSTDIR"
SectionEnd
