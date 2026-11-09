//go:build windows

package main

import (
	"bytes"
	"crypto/rand"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func TestWindowsDPAPI(t *testing.T) {
	p := filepath.Join(t.TempDir(), "protected")
	plain := make([]byte, 64)
	if _, err := rand.Read(plain); err != nil {
		t.Fatal(err)
	}
	if err := writeSecret(p, plain); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(stored, plain) || bytes.Contains(stored, plain) {
		t.Fatal("DPAPI wrote plaintext")
	}
	got, err := readSecret(p)
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("DPAPI round trip failed: %v", err)
	}
	stored[len(stored)/2] ^= 0xff
	os.WriteFile(p, stored, 0600)
	if _, err := readSecret(p); err == nil {
		t.Fatal("corrupt protected blob accepted")
	}
}
func TestServiceRejectsUnknownIPC(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(3 * time.Second))
	s := &wgService{tunnel: NewTunnel()}
	go s.handleConn(server)
	if err := writeMsg(client, &request{ID: 1, Cmd: "exec"}); err != nil {
		t.Fatal(err)
	}
	var r response
	if err := readMsg(client, &r); err != nil {
		t.Fatal(err)
	}
	if r.OK || !strings.Contains(r.Error, "does not handle") {
		t.Fatal("unknown IPC command accepted")
	}
}

// Runs only on an isolated Windows CI runner when explicitly enabled. Exercises
// the actual SCM + LocalService + named pipe, not installer/UAC desktop behavior.
func TestWindowsServiceRuntime(t *testing.T) {
	if os.Getenv("WG_WINDOWS_SERVICE_TEST") != "1" {
		t.Skip("isolated elevated Windows runner only")
	}
	m, err := mgr.Connect()
	if err != nil {
		t.Fatal(err)
	}
	defer m.Disconnect()
	if old, err := m.OpenService(serviceName); err == nil {
		old.Close()
		t.Fatal("refuse to touch an existing companion service")
	}
	exe := filepath.Join(t.TempDir(), "wg-companion.exe")
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	tok := windows.GetCurrentProcessToken()
	u, err := tok.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.OpenKey(registry.LOCAL_MACHINE, regPath, registry.QUERY_VALUE); err == nil {
		t.Fatal("refuse to overwrite existing owner registry")
	}
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, regPath, registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	if err = k.SetStringValue("OwnerSID", u.User.Sid.String()); err != nil {
		k.Close()
		t.Fatal(err)
	}
	k.Close()
	defer registry.DeleteKey(registry.LOCAL_MACHINE, regPath)
	s, err := m.CreateService(serviceName, exe, mgr.Config{DisplayName: "WG Companion isolated CI test", StartType: mgr.StartManual, ServiceStartName: `NT AUTHORITY\LocalService`}, "service")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer s.Delete()
	defer s.Control(svc.Stop)
	if err = s.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		st, e := s.Query()
		if e != nil {
			t.Fatal(e)
		}
		if st.State == svc.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("service did not enter Running: %+v", st)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// A sibling CLI of the exact server binary passes pipe-server image checks.
	cli := exec.Command(exe, "service-status")
	out, err := cli.CombinedOutput()
	if err != nil {
		t.Fatalf("status IPC: %v %s", err, out)
	}
	if !bytes.Contains(out, []byte(`"state":"disconnected"`)) {
		t.Fatalf("service running confused with tunnel connected: %s", out)
	}
	// This test executable is NOT the server image, so secret-bearing IPC must refuse it.
	ctl := newPipeController()
	if _, err = ctl.call(&request{Cmd: "ping"}); err == nil {
		t.Fatal("wrong client executable failed to reject server-image mismatch")
	}
	if _, err = s.Control(svc.Stop); err != nil {
		t.Fatal(err)
	}
	for {
		st, e := s.Query()
		if e != nil {
			t.Fatal(e)
		}
		if st.State == svc.Stopped {
			break
		}
		if time.Now().After(deadline.Add(10 * time.Second)) {
			t.Fatal("service failed to stop")
		}
		time.Sleep(200 * time.Millisecond)
	}
}
