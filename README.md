# WireGuard Companion for Chrome

A Chrome extension that routes browser traffic through a **real WireGuard tunnel** via a small local companion program. The tunnel runs entirely in userspace on your own machine (wireguard-go + gVisor netstack) and is exposed to the browser as a local SOCKS5 proxy over Chrome's native messaging host model. No kernel driver, no separate WireGuard app, no remote relay service.

> **Status: PREVIEW.** The companion and extension have passed local tests on Linux/Chromium. The Windows binaries in this release are cross-compiled and PE-validated, but **have not yet been run on a real Windows machine**. Treat this as an early preview, not production software.

## Architecture

```
Chrome extension  <--native messaging-->  companion (Go)  <--WireGuard UDP-->  your peer
      |                                        |
   PAC/SOCKS5                           userspace netstack
   proxy settings                       (no TUN device needed)
```

- **companion/** - Go program: config storage (DPAPI-protected on Windows, keyring-file elsewhere), userspace WireGuard device over gVisor netstack, SOCKS5 listener on 127.0.0.1 with tunnel-side DNS, Chrome native-messaging host, per-OS install/uninstall.
- **extension/** - Manifest V3 Chrome extension: connect/disconnect UI, per-profile proxy control, WebRTC leak settings, state survives popup close.
- **tests/** - Chromium integration suite (real WireGuard peer, DNS, fail-closed on crash, disconnect/reconnect, popup-close persistence).

## Development verification

The 0.2.0 development tree has passed scoped real Windows service, DPAPI and
local tunnel tests. See [verification scope](docs/VALIDATION.md) for actual CI
evidence and remaining interactive installer/UAC/Chrome gaps. This does not
retroactively verify the older released v0.1.0 ZIP.

## What is verified

- All Go unit tests pass, plus `go vet` and the race detector.
- Full Chromium integration suite passes against a real WireGuard peer: traffic flows through the tunnel, DNS resolves tunnel-side, killing the companion fails closed (no silent leak), reconnect works, popup state persists.
- Windows binaries compile and are valid PE executables.

## What is NOT yet verified

- **Interactive Windows installation/UAC and desktop behavior**, plus non-admin Windows Chrome end-to-end. Scoped service/DPAPI/tunnel tests for development0.2 have passed; see the verification scope above.
- WebRTC settings are applied but not packet-verified.

## Known limitations

- TCP only: UDP over SOCKS (including QUIC/HTTP/3) is not supported; Chrome falls back or fails closed for UDP.
- Incognito mode is unsupported (the extension does not request incognito access).
- You must supply your own WireGuard peer (server). The companion does not provide VPN service.

## Cloudflare WARP (optional, opt-in)

The companion can register a Cloudflare WARP peer for you with `wg-companion warp register`, but only when **you** run it: it prints Cloudflare's terms and requires an explicit accept flag. Registration is never performed automatically.

## Install (released v0.1.0 Windows preview)

The released ZIP has its own README and Install.cmd. Extract the entire ZIP,
run Install.cmd as your normal Windows user, then load
`%LOCALAPPDATA%\WireGuardCompanion\extension` through Chrome's Developer mode
and Load unpacked. Open Add profile, import a WireGuard `.conf` file or explicitly
opt into WARP and its linked terms, select the profile and Connect.

The packaged manifest pins extension ID `dfcikohmdkdkkaampidiknlgkognlkah`.
Install.cmd registers that ID automatically. Do not load an unrelated unpacked
copy from another folder. The ZIP's full README governs installation, removal,
and preview limits. Windows runtime is still unverified.

For a custom source build only, register the ID displayed by Chrome using
`wg-companion.exe install --extension-id <id>`.

The service/installer revision under development is separate from v0.1.0.
Do not apply its future instructions to the older preview ZIP.

## Build from source

Requires Go 1.26+ and Node (tests only).

```sh
cd companion
go build -o wg-companion .
go test ./...
```

Cross-compile for Windows:

```sh
cd companion
GOOS=windows GOARCH=amd64 go build -o wg-companion.exe .
```

Integration tests (Linux with Chromium installed):

```sh
npm ci
npx playwright install chromium
node tests/integration.cjs
```

## Build the Windows installer

On Windows with Go 1.26, Git and NSIS 3.x installed, run
`./tools/build-windows.ps1`. It builds the companion/tray, checks out a pinned
wgcf revision, then creates an unsigned NSIS installer. It does not register WARP.

## Security notes

- The extension ID is pinned in the companion's native-host manifest; the companion only accepts connections from that extension.
- Private keys never leave the local config store and are never logged.
- The SOCKS5 listener binds to 127.0.0.1 only.

## License

This project is MIT licensed (see LICENSE).

Built on third-party open source, whose licenses remain with their authors:

- [wireguard-go](https://git.zx2c4.com/wireguard-go) (MIT)
- [gVisor netstack](https://gvisor.dev) (Apache 2.0)
- [wintun](https://git.zx2c4.com/wintun) (MIT)
- [go-socks5](https://github.com/armon/go-socks5) (MIT-style)
- golang.org/x/* (BSD 3-Clause)
