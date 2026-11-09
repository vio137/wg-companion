//go:build windows

package main

// Client side of the service pipe: the native messaging host (per-user) uses
// this to drive the tunnel owned by the Windows service. Dials lazily and
// redials once per command if the service restarted, so a stopped service
// surfaces as a clean error to the extension, not a dead host.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
	"wireguard-companion/internal/serviceipc"
)

type pipeController struct {
	mu   sync.Mutex
	conn net.Conn
	id   int
}

func newPipeController() *pipeController { return &pipeController{} }

func (p *pipeController) call(req *request) (*response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	resp, err := p.roundTrip(req)
	if err == nil {
		return resp, nil
	}
	// broken pipe (service restarted?): redial once, then give up
	_ = p.closeLocked()
	if _, derr := p.dialLocked(); derr != nil {
		return nil, fmt.Errorf("WG Companion service is not reachable (is it installed and running?): %w", derr)
	}
	return p.roundTrip(req)
}

// dialLocked opens the pipe with a raw handle so the server process can be
// verified on this very connection before any bytes (or configs) cross it.
func (p *pipeController) dialLocked() (net.Conn, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	c, err := serviceipc.Dial(pipePath, self)
	if err != nil {
		return nil, err
	}
	p.conn = c
	return c, nil
}

func (p *pipeController) closeLocked() error {
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn = nil
	return err
}

func (p *pipeController) roundTrip(req *request) (*response, error) {
	if p.conn == nil {
		if _, err := p.dialLocked(); err != nil {
			return nil, fmt.Errorf("WG Companion service is not reachable (is it installed and running?): %w", err)
		}
	}
	_ = p.conn.SetDeadline(time.Now().Add(5 * time.Second))
	p.id++
	req.ID = p.id
	if err := writeMsg(p.conn, req); err != nil {
		return nil, err
	}
	var resp response
	if err := readMsg(p.conn, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func decodeStats(v any) TunnelStats {
	var st TunnelStats
	b, _ := json.Marshal(v)
	_ = json.Unmarshal(b, &st)
	return st
}

func (p *pipeController) Stats() TunnelStats {
	resp, err := p.call(&request{Cmd: "stats"})
	if err != nil {
		return TunnelStats{State: "error", LastError: err.Error()}
	}
	return decodeStats(resp.Data)
}

func (p *pipeController) Connect(cfg *WGConfig, _ string) error {
	resp, err := p.call(&request{Cmd: "connect", Config: cfg})
	if err != nil {
		return err
	}
	if !resp.OK {
		return fmt.Errorf("%s", resp.Error)
	}
	return nil
}

func (p *pipeController) Disconnect(_ string) {
	_, _ = p.call(&request{Cmd: "disconnect"})
}

// hostController returns the tunnel owner for the native messaging host on
// Windows: the background service over its ACL'd pipe.
func hostController() tunnelController { return newPipeController() }

func (p *pipeController) CloseControl() { p.mu.Lock(); defer p.mu.Unlock(); _ = p.closeLocked() }
