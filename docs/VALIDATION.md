# Verification scope

## Development 0.2.0

Real Windows runner evidence:
https://github.com/vio137/wg-companion/actions/runs/42662921489

Passed there: Go tests/vet; DPAPI protected storage round trip and corruption
rejection; local userspace WireGuard HTTP and tunnel-side DNS; host loopback
canary; actual LocalService SCM start/status/stop; anonymous principal denied
opening the control pipe; wrong server-image identity rejected; real HTTP/DNS
through the service-owned tunnel; native host EOF leaves that tunnel alive;
explicit disconnect closes the proxy; per-user native host registration/removal;
companion and tray build.

The installing-user SID is resolved from the desktop shell in the installer's
own session, not the UAC elevation account. This behavior, full interactive
installer/uninstaller operation, alternate-admin UAC, non-admin Chrome routing
on Windows, desktop tray rendering and broader leak behavior remain unverified.

Local Linux checks also pass: race detector, vet, Chromium real-peer HTTP/DNS,
crash fail-closed for tested proxied HTTP, explicit turn-off and reconnect.
Screenshots of Connected in that integration test use a real local peer, not
Cloudflare/public internet. No speed or leak-proof guarantees are implied.

## Released 0.1.0 preview

This older ZIP remains unchanged. Its Windows executables were cross-compiled,
not runtime-verified as that exact package. Use the README inside its ZIP.
Current development tests do not retroactively certify that old artifact.

## Limits across versions

TCP SOCKS proxy, not a system VPN or system kill switch. Arbitrary UDP, Incognito,
full IPv6, WebRTC/QUIC leak behavior and public internet egress are not verified.
WARP is optional and has not been live-tested; registration is never automatic.
