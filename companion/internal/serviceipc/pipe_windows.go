//go:build windows

// Package serviceipc authenticates a local pipe server before any request
// crosses it. Both the native host and tray use this same verified path.
//
package serviceipc

import (
	"context"
	"fmt"
	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"net"
	"path/filepath"
	"strings"
	"time"
)

func VerifyServer(h windows.Handle, expected string) error {
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(h, &pid); err != nil {
		return err
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(p)
	var buf [32768]uint16
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(p, 0, &buf[0], &n); err != nil {
		return err
	}
	server := windows.UTF16ToString(buf[:n])
	if !strings.EqualFold(filepath.Clean(server), filepath.Clean(expected)) {
		return fmt.Errorf("pipe server is not the installed companion binary")
	}
	return nil
}
func Dial(path, expected string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// go-winio opens overlapped handles with anonymous SQOS, preventing an
	// untrusted pipe server from impersonating the client. Real deadlines work.
	c, err := winio.DialPipeContext(ctx, path)
	if err != nil {
		return nil, err
	}
	fd, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, fmt.Errorf("pipe handle unavailable for server verification")
	}
	if err := VerifyServer(windows.Handle(fd.Fd()), expected); err != nil {
		c.Close()
		return nil, fmt.Errorf("refusing untrusted pipe server: %w", err)
	}
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	return c, nil
}
