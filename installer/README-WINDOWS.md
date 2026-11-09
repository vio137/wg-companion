# Windows service preview 0.2.0

UNVERIFIED WINDOWS RUNTIME. Build checks are not installation or security tests.
Do not use this preview for sensitive activity. Unsigned installer and binaries;
do not turn off SmartScreen, antivirus or company policy to install.

## Install

1. Remove the older per-user preview first with its Uninstall.cmd. Turn off and
   remove its Chrome extension. This installer does not migrate old program files.
   Encrypted profiles are kept and can be read by the same Windows user.
2. Run WireGuard-Companion-Setup-0.2.0-preview-x64.exe from the intended user's
   interactive desktop. Approve administrator access. Alternate-admin UAC is
   designed to retain the desktop user's control SID, not that admin's SID.
   Headless/session-0 installs fail closed. This behavior still needs Windows tests.
3. Open WireGuard Companion from Start as your normal user. The tray starts on
   that user's next sign-in. The service starts with Windows as LocalService.
4. In chrome://extensions enable Developer mode, Load unpacked, and choose
   C:\Program Files\WireGuard Companion\extension (fixed protected install path).
   The extension ID is dfcikohmdkdkkaampidiknlgkognlkah.
5. Add profile in Chrome. Import .conf, or explicitly agree to Cloudflare's terms
   and opt into unofficial wgcf WARP registration. No registration occurs at install.
6. Connect in Chrome to enable its proxy and the tunnel. Connected requires a
   real peer handshake. A service running in the background is not a connection.

The tray dashboard opens locally in a browser app window, not an embedded WebView.
Its token is in a fragment only, then removed. It is not a network-accessible server.
Dashboard Connect starts the tunnel, NOT Chrome's proxy. Enable routing in Chrome.
Dashboard Disconnect leaves a Chrome proxy rule blocked until turned off in Chrome.
Quitting the tray does not stop the service or active tunnel. There is no automatic
reconnect after reboot and no stored private key under the service identity.

## Privacy and limits

Profiles and WARP backup stay DPAPI encrypted for the Windows user in
%APPDATA%\wg-companion\configs. Imported originals remain where you put them.
LocalService holds the selected config in memory only. Named-pipe controls are
restricted to the desktop owner, LocalService, System and administrators. Admins
can always control a local machine. Pipe server identity is checked by the host.
The installer and pipe ACL still require runtime validation on Windows.

TCP SOCKS proxy only. Not a system VPN or system kill switch. Arbitrary UDP,
Incognito and full WebRTC/QUIC/IPv6 leak coverage are not verified. Endpoint
hostname bootstrap uses the normal network. Other local apps can use the loopback
SOCKS proxy while active. Disabling/removing Chrome extension clears its settings.

## Remove

Turn off in Chrome, remove the extension, quit tray, then uninstall in Windows Apps.
The uninstaller stops/removes the service, manifest, startup entry and program files.
Saved profiles are retained. Delete the profile directory yourself only if wanted.
Close Chrome and uninstall the current service preview before installing another.
