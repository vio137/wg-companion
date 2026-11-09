package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func sampleConf() string {
	k, _ := GeneratePrivateKey()
	return "[Interface]\nPrivateKey = " + k + "\nAddress = 10.0.0.2/32\nDNS = 1.1.1.1\n[Peer]\nPublicKey = " + pub(k) + "\nEndpoint = 127.0.0.1:1234\nAllowedIPs = 0.0.0.0/0, ::/0\n"
}
func TestConfigValidation(t *testing.T) {
	s := sampleConf()
	if _, err := ParseWGConfig("ok", s); err != nil {
		t.Fatal(err)
	}
	cases := []string{s + "[Peer]\nPublicKey = bad", strings.Replace(s, "Endpoint = 127.0.0.1:1234", "Endpoint = 127.0.0.1:0", 1), strings.Replace(s, "DNS = 1.1.1.1\n", "", 1), strings.Replace(s, "DNS = 1.1.1.1", "DNS = 1.1.1.1\nPostUp = curl evil", 1), s + "PersistentKeepalive = -1"}
	for _, c := range cases {
		if _, err := ParseWGConfig("bad", c); err == nil {
			t.Fatal("accepted invalid profile")
		}
	}
	for _, n := range []string{"../secret", ".hidden", "a/b", "", strings.Repeat("x", 65)} {
		if _, err := sanitizeName(n); err == nil {
			t.Fatalf("bad name %q", n)
		}
	}
}
func TestNativeFraming(t *testing.T) {
	var b bytes.Buffer
	v := map[string]any{"id": 1, "cmd": "ping"}
	if err := writeMsg(&b, v); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := readMsg(&b, &got); err != nil {
		t.Fatal(err)
	}
	if got["cmd"] != "ping" {
		t.Fatal("roundtrip")
	}
	for _, n := range []uint32{0, 2 << 20} {
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], n)
		if readMsg(bytes.NewReader(hdr[:]), &got) == nil {
			t.Fatal("bad length accepted")
		}
	}
	if _, err := json.Marshal(TunnelStats{}); err != nil {
		t.Fatal(err)
	}
}
func TestStorage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	if _, err := SaveConfig("stored", sampleConf()); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig("stored"); err != nil {
		t.Fatal(err)
	}
	l, _ := ListConfigs()
	if len(l) != 1 || l[0].Name != "stored" {
		t.Fatal(l)
	}
	if err := RemoveConfig("stored"); err != nil {
		t.Fatal(err)
	}
}
func TestHostnameEndpoint(t *testing.T) {
	c, err := ParseWGConfig("hostname", strings.Replace(sampleConf(), "127.0.0.1:1234", "localhost:1234", 1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buildUAPI(c); err != nil {
		t.Fatal(err)
	}
}
