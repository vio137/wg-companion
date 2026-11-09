//go:build windows

package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/net/proxy"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// LocalService cannot execute from the runner user's private Temp tree.
	// Use a fresh ProgramData fixture directory with explicit read/execute only.
	dir, err := os.MkdirTemp(os.Getenv("ProgramData"), "WGCompanion-CI-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	exe := filepath.Join(dir, "wg-companion.exe")
	build := exec.Command("go", "build", "-o", exe, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	if out, err := exec.Command("icacls", dir, "/grant", "*S-1-5-19:(OI)(CI)RX", "/T").CombinedOutput(); err != nil {
		t.Fatalf("LocalService read/execute fixture ACL: %v %s", err, out)
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
	// Anonymous impersonation is not the installing user or an allowed system
	// principal. The real service ACL must reject opening the control pipe.
	runtime.LockOSThread()
	proc := windows.NewLazySystemDLL("advapi32.dll").NewProc("ImpersonateAnonymousToken")
	ok, _, e := proc.Call(uintptr(windows.CurrentThread()))
	if ok == 0 {
		runtime.UnlockOSThread()
		t.Fatalf("anonymous impersonation setup: %v", e)
	}
	path, _ := windows.UTF16PtrFromString(pipePath)
	h, denied := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING, windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
	revert := windows.RevertToSelf()
	runtime.UnlockOSThread()
	if revert != nil {
		t.Fatalf("revert impersonation: %v", revert)
	}
	if denied == nil {
		windows.CloseHandle(h)
		t.Fatal("anonymous principal opened owner-only service pipe")
	}
	if !errors.Is(denied, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("expected ACL access denial, not unrelated pipe failure: %v", denied)
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

	// Actual LocalService tunnel data path against a local userspace peer.
	// Random per-test keys are never logged or uploaded as artifacts.
	t.Setenv("APPDATA", t.TempDir())
	peerKey, _ := GeneratePrivateKey()
	clientKey, _ := GeneratePrivateKey()
	port := freeUDP(t)
	stopPeer := startPeer(t, peerKey, pub(clientKey), port)
	defer stopPeer()
	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.9.0.2/32\nDNS = 10.9.0.1\n[Peer]\nPublicKey = %s\nEndpoint = 127.0.0.1:%d\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 5\n", clientKey, pub(peerKey), port)
	if _, err := SaveConfig("runtime", conf); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(exe, "service-connect", "-name", "runtime").CombinedOutput(); err != nil {
		t.Fatalf("service connect: %v %s", err, out)
	}
	dialer, err := proxy.SOCKS5("tcp", socksListenAddr, nil, proxy.Direct)
	if err != nil {
		t.Fatal(err)
	}
	hc := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Dial: dialer.Dial, DisableKeepAlives: true}}
	for _, target := range []string{"http://10.9.0.1:8080/", "http://peer.test:8080/"} {
		res, err := hc.Get(target)
		if err != nil {
			t.Fatalf("LocalService tunnel HTTP/DNS: %v", err)
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		if err != nil || !bytes.Contains(body, []byte("hello-from-peer remote=10.9.0.2")) {
			t.Fatal("unexpected tunnel response")
		}
	}
	// Closing a native host must not shut down the independently owned tunnel.
	var input bytes.Buffer
	_ = writeMsg(&input, &request{ID: 8, Cmd: "ping"})
	native := exec.Command(exe, "host")
	native.Stdin = &input
	if out, err := native.CombinedOutput(); err != nil {
		t.Fatalf("native EOF: %v %s", err, out)
	}
	stOut, err := exec.Command(exe, "service-status").Output()
	if err != nil {
		t.Fatal(err)
	}
	var st TunnelStats
	if err = json.Unmarshal(stOut, &st); err != nil || st.State != "connected" || st.LastHandshakeUnix == 0 {
		t.Fatalf("native EOF incorrectly stopped tunnel: %v state=%s", err, st.State)
	}
	if out, err := exec.Command(exe, "service-disconnect").CombinedOutput(); err != nil {
		t.Fatalf("disconnect: %v %s", err, out)
	}
	if res, err := hc.Get("http://peer.test:8080/"); err == nil {
		res.Body.Close()
		t.Fatal("proxy still serving after explicit disconnect")
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
