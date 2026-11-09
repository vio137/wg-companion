//go:build windows

package main

// Windows service: owns the tunnel + SOCKS5 proxy and takes control commands.
// Identity: LocalService (not LocalSystem) - a userspace tunnel needs only
// outbound UDP and loopback/pipe access, and LocalService has no admin
// rights and a minimal profile, so a tunnel bug grants an attacker far less.
// It uses an ACL-restricted named pipe, has no config store and
// no network identity of its own - the per-user native messaging host reads
// the user's DPAPI-protected configs and passes a config over the pipe when
// the user asks to connect. The config lives in service memory only, never
// on disk under the service identity.

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
)

const (
	serviceName = "WGCompanion"
	pipePath    = `\\.\pipe\` + serviceName
)

// pipeSDDL restricts pipe access to LocalService (the service identity),
// LocalSystem, Administrators, and the single installing user recorded at
// install time - not every interactive user. Missing owner record fails
// closed to system accounts only.
func pipeSDDL() string {
	owner, err := ownerSID()
	if err != nil {
		svcErrorf("owner SID unavailable, locking pipe to system accounts: %v", err)
		return "D:P(A;;GA;;;LS)(A;;GA;;;SY)(A;;GA;;;BA)"
	}
	return "D:P(A;;GA;;;LS)(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + owner.String() + ")"
}

var elog *eventlog.Log

func runService() error {
	inSvc, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !inSvc {
		return fmt.Errorf("the service command must be started by the Windows Service Control Manager")
	}
	l, err := eventlog.Open(serviceName)
	if err == nil {
		elog = l
		defer elog.Close()
	}
	return svc.Run(serviceName, &wgService{tunnel: NewTunnel()})
}

func svcInfof(format string, a ...any) {
	if elog != nil {
		_ = elog.Info(1, fmt.Sprintf(format, a...))
	}
}

func svcErrorf(format string, a ...any) {
	if elog != nil {
		_ = elog.Error(2, fmt.Sprintf(format, a...))
	}
}

type wgService struct {
	tunnel *Tunnel
}

func (m *wgService) Execute(_ []string, reqs <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	ln, err := winio.ListenPipe(pipePath, &winio.PipeConfig{SecurityDescriptor: pipeSDDL()})
	if err != nil {
		svcErrorf("listen pipe: %v", err)
		return false, 1
	}
	var wg sync.WaitGroup
	done := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		m.servePipe(ln, done)
	}()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	svcInfof("service started, pipe %s", pipePath)

	for r := range reqs {
		switch r.Cmd {
		case svc.Interrogate:
			status <- r.CurrentStatus
		case svc.Stop, svc.Shutdown:
			status <- svc.Status{State: svc.StopPending}
			m.tunnel.Disconnect("service stopping")
			close(done)
			ln.Close()
			wg.Wait()
			svcInfof("service stopped")
			return false, 0
		}
	}
	return false, 0
}

// servePipe bounds concurrent control clients so Chrome's persistent host
// cannot starve the tray, and an idle client cannot block service shutdown.
func (m *wgService) servePipe(ln net.Listener, done <-chan struct{}) {
	slots := make(chan struct{}, 8)
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		c, err := ln.Accept()
		if err != nil {
			select {
			case <-done:
				return
			default:
				svcErrorf("pipe accept: %v", err)
				time.Sleep(time.Second)
				continue
			}
		}
		select {
		case slots <- struct{}{}:
		default:
			c.Close()
			continue
		}
		clients.Add(1)
		go func() {
			defer clients.Done()
			defer func() { <-slots }()
			finished := make(chan struct{})
			go func() {
				select {
				case <-done:
					c.Close()
				case <-finished:
				}
			}()
			m.handleConn(c)
			close(finished)
		}()
	}
}

// handleConn serves the same length-prefixed JSON protocol Chrome uses, but
// only tunnel commands. Configs never pass through here except as the
// in-memory payload of a connect request.
func (m *wgService) handleConn(c net.Conn) {
	defer c.Close()
	for {
		_ = c.SetDeadline(time.Now().Add(15 * time.Second))
		var req request
		if err := readMsg(c, &req); err != nil {
			if err != io.EOF {
				svcErrorf("pipe read: %v", err)
			}
			return
		}
		resp := response{ID: req.ID, OK: true}
		switch req.Cmd {
		case "ping":
			resp.Data = map[string]any{"version": version, "state": m.tunnel.Stats().State}
		case "status", "stats":
			resp.Data = m.tunnel.Stats()
		case "connect":
			if req.Config == nil {
				resp.OK, resp.Error = false, "connect needs a config"
				break
			}
			if err := m.tunnel.Connect(req.Config, socksListenAddr); err != nil {
				resp.OK, resp.Error = false, err.Error()
			} else {
				resp.Data = m.tunnel.Stats()
			}
		case "disconnect":
			m.tunnel.Disconnect("requested")
			resp.Data = m.tunnel.Stats()
		default:
			resp.OK, resp.Error = false, "service does not handle cmd "+req.Cmd
		}
		if err := writeMsg(c, resp); err != nil {
			return
		}
	}
}
