package main

import (
	"encoding/base64"
	"fmt"
	"golang.org/x/net/dns/dnsmessage"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/net/proxy"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func pub(priv string) string {
	b, _ := base64.StdEncoding.DecodeString(priv)
	p, _ := curve25519.X25519(b, curve25519.Basepoint)
	return base64.StdEncoding.EncodeToString(p)
}

func freeUDP(t *testing.T) int {
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).Port
}

// startPeer runs a WireGuard "server" (netstack) at 10.9.0.1 serving HTTP.
func startPeer(t *testing.T, peerPriv, clientPub string, port int) func() {
	tun, tnet, err := netstack.CreateNetTUN([]netip.Addr{netip.MustParseAddr("10.9.0.1")}, []netip.Addr{netip.MustParseAddr("10.9.0.1")}, 1420)
	if err != nil {
		t.Fatal(err)
	}
	dev := device.NewDevice(tun, conn.NewDefaultBind(), device.NewLogger(device.LogLevelSilent, ""))
	ph, _ := b64ToHex(peerPriv)
	ch, _ := b64ToHex(clientPub)
	if err := dev.IpcSet(fmt.Sprintf("private_key=%s\nlisten_port=%d\npublic_key=%s\nallowed_ip=10.9.0.2/32\n", ph, port, ch)); err != nil {
		t.Fatal(err)
	}
	dev.Up()
	ln, err := tnet.ListenTCP(&net.TCPAddr{Port: 8080})
	if err != nil {
		t.Fatal(err)
	}
	dnsConn, err := tnet.ListenUDP(&net.UDPAddr{Port: 53})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		buf := make([]byte, 1500)
		for {
			n, addr, err := dnsConn.ReadFrom(buf)
			if err != nil {
				return
			}
			var msg dnsmessage.Message
			if msg.Unpack(buf[:n]) != nil {
				continue
			}
			msg.Header.Response = true
			msg.Header.RecursionAvailable = true
			for _, q := range msg.Questions {
				if q.Type == dnsmessage.TypeA {
					msg.Answers = append(msg.Answers, dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: q.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 60}, Body: &dnsmessage.AResource{A: [4]byte{10, 9, 0, 1}}})
				}
			}
			out, _ := msg.Pack()
			dnsConn.WriteTo(out, addr)
		}
	}()
	go http.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "hello-from-peer remote=%s", r.RemoteAddr)
	}))
	return func() { dnsConn.Close(); ln.Close(); dev.Close() }
}

func TestEndToEnd(t *testing.T) {
	cpriv, _ := GeneratePrivateKey()
	ppriv, _ := GeneratePrivateKey()
	port := freeUDP(t)
	stop := startPeer(t, ppriv, pub(cpriv), port)
	defer stop()

	// a "host-network" server the tunnel must NOT reach (leak canary)
	canary, _ := net.Listen("tcp", "127.0.0.1:0")
	canaryHit := make(chan struct{}, 1)
	go func() {
		for {
			c, err := canary.Accept()
			if err != nil {
				return
			}
			canaryHit <- struct{}{}
			c.Close()
		}
	}()
	defer canary.Close()

	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.9.0.2/32\nDNS = 10.9.0.1\n[Peer]\nPublicKey = %s\nEndpoint = 127.0.0.1:%d\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 5\n", cpriv, pub(ppriv), port)
	cfg, err := ParseWGConfig("t", conf)
	if err != nil {
		t.Fatal(err)
	}
	tn := NewTunnel()
	socks := "127.0.0.1:0"
	if err := tn.Connect(cfg, socks); err != nil {
		t.Fatal(err)
	}
	sp := tn.Stats().SOCKSPort
	dialer, _ := proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", sp), nil, proxy.Direct)
	hc := &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Dial: dialer.Dial}}

	// 1. traffic goes through tunnel
	resp, err := hc.Get("http://10.9.0.1:8080/")
	if err != nil {
		t.Fatalf("fetch via tunnel: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "hello-from-peer remote=10.9.0.2") {
		t.Fatalf("unexpected body %q", body)
	}
	t.Logf("tunnel fetch OK: %s", body)

	resp, err = hc.Get("http://peer.test:8080/")
	if err != nil {
		t.Fatalf("DNS through tunnel: %v", err)
	}
	resp.Body.Close()
	t.Log("tunnel-side DNS OK")
	// 2. handshake + counters
	st := tn.Stats()
	if st.LastHandshakeUnix == 0 || st.RxBytes == 0 || st.TxBytes == 0 {
		t.Fatalf("stats not populated: %+v", st)
	}
	t.Logf("stats: hs=%d rx=%d tx=%d", st.LastHandshakeUnix, st.RxBytes, st.TxBytes)

	// 3. leak canary: a loopback host service must be unreachable through the tunnel
	_, err = hc.Get("http://" + canary.Addr().String() + "/")
	if err == nil {
		t.Fatal("LEAK: host loopback service reachable through tunnel proxy")
	}
	select {
	case <-canaryHit:
		t.Fatal("LEAK: canary was contacted on host network")
	case <-time.After(300 * time.Millisecond):
		t.Logf("leak canary not contacted: %v", err)
	}

	// 4. disconnect closes the proxy port
	tn.Disconnect("test")
	time.Sleep(200 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", sp), time.Second); err == nil {
		c.Close()
		t.Fatal("proxy port still open after disconnect")
	}
	if tn.Stats().State != "disconnected" {
		t.Fatal("state not disconnected")
	}
	t.Log("disconnect closed proxy port")

	// 5. reconnect works
	if err := tn.Connect(cfg, socks); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	sp = tn.Stats().SOCKSPort
	dialer, _ = proxy.SOCKS5("tcp", fmt.Sprintf("127.0.0.1:%d", sp), nil, proxy.Direct)
	hc = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{Dial: dialer.Dial}}
	if _, err := hc.Get("http://10.9.0.1:8080/"); err != nil {
		t.Fatalf("fetch after reconnect: %v", err)
	}
	tn.Disconnect("done")
}

// TestChromeFixture is enabled only by the local Chrome integration harness.
func TestChromeFixture(t *testing.T) {
	if os.Getenv("WG_CHROME_FIXTURE") != "1" {
		t.Skip("fixture only")
	}
	cpriv, _ := GeneratePrivateKey()
	ppriv, _ := GeneratePrivateKey()
	port := freeUDP(t)
	stop := startPeer(t, ppriv, pub(cpriv), port)
	defer stop()
	conf := fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = 10.9.0.2/32\nDNS = 10.9.0.1\n[Peer]\nPublicKey = %s\nEndpoint = 127.0.0.1:%d\nAllowedIPs = 0.0.0.0/0\nPersistentKeepalive = 5\n", cpriv, pub(ppriv), port)
	if err := os.WriteFile("/tmp/chrome-fixture.conf", []byte(conf), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove("/tmp/chrome-fixture.conf")
	os.WriteFile("/tmp/chrome-fixture-ready", []byte("ready"), 0600)
	defer os.Remove("/tmp/chrome-fixture-ready")
	for i := 0; i < 120; i++ {
		if _, err := os.Stat("/tmp/chrome-fixture-stop"); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
}
