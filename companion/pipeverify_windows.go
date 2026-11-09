//go:build windows

package main

// Pipe server authentication: before any config crosses the pipe, the client
// verifies the pipe's server process is the installed companion binary. A
// random local process squatting on the pipe name could otherwise capture the
// WireGuard private key sent on connect.

import (
	"fmt"
	"os"
	"unsafe"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

var (
	kernel32                = windows.NewLazySystemDLL("kernel32.dll")
	procPipeServerProcessId = kernel32.NewProc("GetNamedPipeServerProcessId")
)

// verifyPipeServer checks that the process serving handle h is the same
// binary as the running executable (the installed companion in Program
// Files, which only administrators can replace).
func verifyPipeServer(h windows.Handle) error {
	var pid uint32
	r, _, err := procPipeServerProcessId.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
	if r == 0 {
		return fmt.Errorf("query pipe server pid: %w", err)
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("open pipe server process: %w", err)
	}
	defer windows.CloseHandle(p)
	var buf [windows.MAX_PATH]uint16
	n := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(p, 0, &buf[0], &n); err != nil {
		return fmt.Errorf("query pipe server image: %w", err)
	}
	serverPath := windows.UTF16ToString(buf[:n])
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Clean(serverPath), filepath.Clean(self)) {
		return fmt.Errorf("pipe server is %s, not the companion binary", serverPath)
	}
	return nil
}
