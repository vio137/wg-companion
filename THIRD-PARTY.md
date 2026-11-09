# Third-party components

wgcf source revision: de6bba7147dc965a1b8e4de113a709750697ccea

- wgcf: https://github.com/ViRb3/wgcf (MIT). Bundled executable is built from the pinned upstream revision. The build script verifies the checkout before compiling.
- wireguard-go: https://github.com/WireGuard/wireguard-go (MIT), revision pinned in source/companion/go.mod.
- gVisor netstack: https://gvisor.dev (Apache 2.0), module pinned in go.mod.
- go-socks5: https://github.com/armon/go-socks5 (MIT), module pinned in go.mod.
- Go x/crypto, x/net, x/sys, x/time and google/btree: license texts included in licenses.
- Additional tray and wgcf dependency notices are listed in licenses/INDEX.md. Go and NSIS notices are included too.
- UI design guidance: unslop by Damir Sokolovsky, https://github.com/heydamir/unslop (CC BY 4.0). Guidance applied to a newly written interface, not a copied example screen.
- Ponytail guidance: https://github.com/dietrichgebert/ponytail (MIT).

This project is not affiliated with Cloudflare or the WireGuard project. The original companion and extension code is provided under MIT; third-party terms remain applicable to their components.
