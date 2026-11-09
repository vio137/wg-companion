package main

// Local SOCKS5 proxy served on 127.0.0.1. Every connection accepted here is
// dialed through the WireGuard tunnel's netstack, and DNS names are resolved
// tunnel-side, so nothing escapes to the host network.

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"

	socks5 "github.com/armon/go-socks5"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

type ProxyServer struct {
	mu        sync.Mutex
	ln        net.Listener
	port      int
	server    *socks5.Server
	closed    chan struct{}
	closeOnce sync.Once
}

type tunnelResolver struct{ tnet *netstack.Net }

func (r tunnelResolver) Resolve(ctx context.Context, name string) (context.Context, net.IP, error) {
	addrs, err := r.tnet.LookupContextHost(ctx, name)
	if err != nil {
		return ctx, nil, err
	}
	if len(addrs) == 0 {
		return ctx, nil, fmt.Errorf("no addresses for %s", name)
	}
	ip := net.ParseIP(addrs[0])
	if ip == nil {
		return ctx, nil, fmt.Errorf("bad address %q for %s", addrs[0], name)
	}
	return ctx, ip, nil
}

func NewProxyServer(tnet *netstack.Net) *ProxyServer {
	conf := &socks5.Config{
		Logger: log.New(io.Discard, "", 0),
		Dial: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// Force everything through the tunnel netstack.
			return tnet.DialContext(ctx, network, addr)
		},
		Resolver: tunnelResolver{tnet: tnet},
	}
	srv, _ := socks5.New(conf) // only errors on nil config fields we always set
	return &ProxyServer{server: srv, closed: make(chan struct{})}
}

func (p *ProxyServer) Listen(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.ln = ln
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		p.port = a.Port
	}
	p.mu.Unlock()
	go func() {
		_ = p.server.Serve(ln) // returns on listener close
	}()
	return nil
}

func (p *ProxyServer) Port() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.port
}

func (p *ProxyServer) Close() {
	p.closeOnce.Do(func() {
		p.mu.Lock()
		if p.ln != nil {
			p.ln.Close()
		}
		p.mu.Unlock()
		close(p.closed)
	})
}
