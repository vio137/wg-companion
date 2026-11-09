package main

// Userspace WireGuard tunnel: wireguard-go device over a gvisor netstack TUN.
// No kernel interface, no admin rights needed. Traffic reaches the tunnel only
// through the local SOCKS5 proxy the companion serves.

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type TunnelStats struct {
	State             string `json:"state"` // "disconnected", "connecting", "connected", "error"
	Config            string `json:"config,omitempty"`
	Endpoint          string `json:"endpoint,omitempty"`
	SOCKSPort         int    `json:"socks_port,omitempty"`
	LastHandshakeUnix int64  `json:"last_handshake_unix,omitempty"`
	RxBytes           int64  `json:"rx_bytes,omitempty"`
	TxBytes           int64  `json:"tx_bytes,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	StartedUnix       int64  `json:"started_unix,omitempty"`
}

type Tunnel struct {
	opMu   sync.Mutex // serialize connect/disconnect across independent control clients
	mu     sync.Mutex
	dev    *device.Device
	tnet   *netstack.Net
	proxy  *ProxyServer
	stats  TunnelStats
	onDown func(reason string) // called when the device dies unexpectedly
}

func NewTunnel() *Tunnel {
	return &Tunnel{stats: TunnelStats{State: "disconnected"}}
}

func (t *Tunnel) Stats() TunnelStats {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.stats
	if t.dev != nil {
		hs, rx, tx := readPeerStats(t.dev)
		s.LastHandshakeUnix = hs
		s.RxBytes = rx
		s.TxBytes = tx
		if hs > 0 {
			s.State = "connected"
		} else {
			s.State = "ready"
		}
	}
	return s
}

func (t *Tunnel) setState(st TunnelStats) {
	t.mu.Lock()
	t.stats = st
	t.mu.Unlock()
}

func readPeerStats(dev *device.Device) (handshakeUnix, rx, tx int64) {
	uapi, err := dev.IpcGet()
	if err != nil {
		return 0, 0, 0
	}
	for _, line := range strings.Split(uapi, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "last_handshake_time_sec":
			handshakeUnix, _ = strconv.ParseInt(v, 10, 64)
		case "rx_bytes":
			rx, _ = strconv.ParseInt(v, 10, 64)
		case "tx_bytes":
			tx, _ = strconv.ParseInt(v, 10, 64)
		}
	}
	return
}

func (t *Tunnel) Connect(cfg *WGConfig, socksAddr string) error {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	t.mu.Lock()
	if t.dev != nil {
		t.mu.Unlock()
		return fmt.Errorf("already connected; disconnect first")
	}
	t.mu.Unlock()

	t.setState(TunnelStats{State: "connecting", Config: cfg.Name, Endpoint: cfg.Endpoint})

	tunDev, tnet, err := netstack.CreateNetTUN(cfg.Addresses, cfg.DNS, cfg.MTU)
	if err != nil {
		t.setState(TunnelStats{State: "error", LastError: err.Error()})
		return fmt.Errorf("create netstack: %w", err)
	}

	logger := device.NewLogger(device.LogLevelSilent, "wg: ")
	dev := device.NewDevice(tunDev, conn.NewDefaultBind(), logger)

	uapi, err := buildUAPI(cfg)
	if err != nil {
		dev.Close()
		t.setState(TunnelStats{State: "error", LastError: err.Error()})
		return err
	}
	if err := dev.IpcSet(uapi); err != nil {
		dev.Close()
		t.setState(TunnelStats{State: "error", LastError: err.Error()})
		return fmt.Errorf("configure device: %w", err)
	}
	if err := dev.Up(); err != nil {
		dev.Close()
		t.setState(TunnelStats{State: "error", LastError: err.Error()})
		return fmt.Errorf("device up: %w", err)
	}

	proxy := NewProxyServer(tnet)
	if err := proxy.Listen(socksAddr); err != nil {
		dev.Close()
		t.setState(TunnelStats{State: "error", LastError: err.Error()})
		return fmt.Errorf("proxy listen: %w", err)
	}

	t.mu.Lock()
	t.dev = dev
	t.tnet = tnet
	t.proxy = proxy
	t.mu.Unlock()

	t.setState(TunnelStats{
		State:       "ready",
		Config:      cfg.Name,
		Endpoint:    cfg.Endpoint,
		SOCKSPort:   proxy.Port(),
		StartedUnix: time.Now().Unix(),
	})
	return nil
}

func (t *Tunnel) Disconnect(reason string) {
	t.opMu.Lock()
	defer t.opMu.Unlock()
	t.mu.Lock()
	dev, proxy := t.dev, t.proxy
	t.dev, t.proxy, t.tnet = nil, nil, nil
	t.mu.Unlock()
	if proxy != nil {
		proxy.Close()
	}
	if dev != nil {
		dev.Close()
	}
	t.setState(TunnelStats{State: "disconnected"})
	_ = reason
}

// DisconnectOnEOF is used when the controlling extension channel closes.
func (t *Tunnel) WatchDevice() {
	// wireguard-go logs handshake failures but does not exit on its own;
	// liveness is reported via stats (handshake age), so nothing to do here.
}

func buildUAPI(cfg *WGConfig) (string, error) {
	priv, err := b64ToHex(cfg.PrivateKey)
	if err != nil {
		return "", fmt.Errorf("private key: %w", err)
	}
	pub, err := b64ToHex(cfg.PeerPublicKey)
	if err != nil {
		return "", fmt.Errorf("peer public key: %w", err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "private_key=%s\n", priv)
	b.WriteString("listen_port=0\n")
	fmt.Fprintf(&b, "public_key=%s\n", pub)
	if cfg.PresharedKey != "" {
		psk, err := b64ToHex(cfg.PresharedKey)
		if err != nil {
			return "", fmt.Errorf("preshared key: %w", err)
		}
		fmt.Fprintf(&b, "preshared_key=%s\n", psk)
	}
	endpoint := cfg.Endpoint
	if _, err := netip.ParseAddrPort(endpoint); err != nil {
		host, port, _ := net.SplitHostPort(endpoint)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(ips) == 0 {
			return "", fmt.Errorf("could not resolve WireGuard endpoint")
		}
		chosen := ips[0]
		for _, ip := range ips {
			if ip.Is4() {
				chosen = ip
				break
			}
		}
		endpoint = net.JoinHostPort(chosen.String(), port)
	}
	fmt.Fprintf(&b, "endpoint=%s\n", endpoint)
	b.WriteString("replace_allowed_ips=true\n")
	if len(cfg.AllowedIPs) == 0 {
		b.WriteString("allowed_ip=0.0.0.0/0\nallowed_ip=::/0\n")
	}
	for _, p := range cfg.AllowedIPs {
		fmt.Fprintf(&b, "allowed_ip=%s\n", p.String())
	}
	if cfg.PersistentKeepalive > 0 {
		fmt.Fprintf(&b, "persistent_keepalive_interval=%d\n", cfg.PersistentKeepalive)
	}
	return b.String(), nil
}

// ResolveOnTunnel exposes tunnel-side DNS (used by the proxy and tests).
func (t *Tunnel) ResolveOnTunnel(ctx context.Context, host string) ([]string, error) {
	t.mu.Lock()
	tnet := t.tnet
	t.mu.Unlock()
	if tnet == nil {
		return nil, fmt.Errorf("not connected")
	}
	return tnet.LookupContextHost(ctx, host)
}
