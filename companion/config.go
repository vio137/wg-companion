package main

// wg-quick style INI config parsing and on-disk config storage.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type WGConfig struct {
	Name                string         `json:"name"`
	PrivateKey          string         `json:"private_key"` // base64, 32-byte curve25519
	Addresses           []netip.Addr   `json:"addresses"`
	DNS                 []netip.Addr   `json:"dns,omitempty"`
	MTU                 int            `json:"mtu"`
	PeerPublicKey       string         `json:"peer_public_key"`
	PresharedKey        string         `json:"preshared_key,omitempty"`
	Endpoint            string         `json:"endpoint"` // host:port or ip:port
	AllowedIPs          []netip.Prefix `json:"allowed_ips"`
	PersistentKeepalive int            `json:"persistent_keepalive,omitempty"`
}

// ParseWGConfig parses a wg-quick / wgcf generated .conf (INI) file.
func ParseWGConfig(name, contents string) (*WGConfig, error) {
	c := &WGConfig{Name: name, MTU: 1280, PersistentKeepalive: 25}
	peerCount := 0
	section := ""
	for ln, raw := range strings.Split(contents, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if section == "peer" {
				peerCount++
				if peerCount > 1 {
					return nil, fmt.Errorf("only single-peer profiles are supported")
				}
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key=value", ln+1)
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(strings.SplitN(raw, "=", 2)[1])
		switch section {
		case "interface":
			switch k {
			case "privatekey":
				c.PrivateKey = v
			case "address":
				for _, part := range strings.Split(v, ",") {
					addr, err := parseAddrOrCIDR(strings.TrimSpace(part))
					if err != nil {
						return nil, fmt.Errorf("interface address %q: %w", part, err)
					}
					c.Addresses = append(c.Addresses, addr)
				}
			case "dns":
				for _, part := range strings.Split(v, ",") {
					part = strings.TrimSpace(part)
					a, err := netip.ParseAddr(part)
					if err != nil {
						// hostname DNS servers are not supported by netstack
						return nil, fmt.Errorf("dns server %q: %w", part, err)
					}
					c.DNS = append(c.DNS, a)
				}
			case "preup", "postup", "predown", "postdown":
				return nil, fmt.Errorf("wg-quick shell hooks are not supported")
			case "mtu":
				m, err := strconv.Atoi(v)
				if err != nil || m < 576 || m > 9000 {
					return nil, fmt.Errorf("invalid mtu %q", v)
				}
				c.MTU = m
			}
		case "peer":
			switch k {
			case "publickey":
				c.PeerPublicKey = v
			case "presharedkey":
				c.PresharedKey = v
			case "endpoint":
				c.Endpoint = v
			case "persistentkeepalive":
				n, err := strconv.Atoi(v)
				if err != nil || n < 0 || n > 65535 {
					return nil, fmt.Errorf("invalid keepalive")
				}
				c.PersistentKeepalive = n
			case "allowedips":
				for _, part := range strings.Split(v, ",") {
					p, err := netip.ParsePrefix(strings.TrimSpace(part))
					if err != nil {
						return nil, fmt.Errorf("allowedips entry %q: %w", part, err)
					}
					c.AllowedIPs = append(c.AllowedIPs, p)
				}
			}
		default:
			return nil, fmt.Errorf("line %d: key %q outside [Interface]/[Peer] section", ln+1, k)
		}
	}
	if err := validB64Key(c.PrivateKey); err != nil {
		return nil, fmt.Errorf("interface PrivateKey: %w", err)
	}
	if err := validB64Key(c.PeerPublicKey); err != nil {
		return nil, fmt.Errorf("peer PublicKey: %w", err)
	}
	if c.PresharedKey != "" {
		if err := validB64Key(c.PresharedKey); err != nil {
			return nil, fmt.Errorf("peer PresharedKey: %w", err)
		}
	}
	if len(c.Addresses) == 0 {
		return nil, fmt.Errorf("interface Address is required")
	}
	if c.Endpoint == "" {
		return nil, fmt.Errorf("peer Endpoint is required")
	}
	if len(c.DNS) == 0 {
		return nil, fmt.Errorf("at least one numeric DNS server is required")
	}
	if _, port, err := net.SplitHostPort(c.Endpoint); err != nil {
		return nil, fmt.Errorf("endpoint must be host:port")
	} else if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nil, fmt.Errorf("invalid endpoint port")
	}
	return c, nil
}

func parseAddrOrCIDR(s string) (netip.Addr, error) {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr(), nil
	}
	return netip.ParseAddr(s)
}

func validB64Key(k string) error {
	b, err := base64.StdEncoding.DecodeString(k)
	if err != nil || len(b) != 32 {
		return fmt.Errorf("not a valid 32-byte base64 key")
	}
	return nil
}

func b64ToHex(b64 string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- storage ---

func configDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "wg-companion", "configs")
	if err := os.MkdirAll(d, 0700); err != nil {
		return "", err
	}
	return d, nil
}

func sanitizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 {
		return "", fmt.Errorf("config name must be 1-64 chars")
	}
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.'
		if !ok {
			return "", fmt.Errorf("config name %q has invalid character %q", name, r)
		}
	}
	if strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("config name must not start with a dot")
	}
	return name, nil
}

func SaveConfig(name, contents string) (*WGConfig, error) {
	name, err := sanitizeName(name)
	if err != nil {
		return nil, err
	}
	cfg, err := ParseWGConfig(name, contents)
	if err != nil {
		return nil, err
	}
	d, err := configDir()
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(d, name+".conf")); err == nil {
		return nil, fmt.Errorf("profile name already exists; choose another name or remove the old profile")
	}
	if err := writeSecret(filepath.Join(d, name+".conf"), []byte(contents)); err != nil {
		return nil, err
	}
	return cfg, nil
}

func LoadConfig(name string) (*WGConfig, error) {
	name, err := sanitizeName(name)
	if err != nil {
		return nil, err
	}
	d, err := configDir()
	if err != nil {
		return nil, err
	}
	b, err := readSecret(filepath.Join(d, name+".conf"))
	if err != nil {
		return nil, fmt.Errorf("config %q not found", name)
	}
	return ParseWGConfig(name, string(b))
}

type ConfigInfo struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

func ListConfigs() ([]ConfigInfo, error) {
	d, err := configDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, err
	}
	var out []ConfigInfo
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".conf")
		cfg, err := LoadConfig(name)
		if err != nil {
			continue
		}
		out = append(out, ConfigInfo{Name: name, Endpoint: cfg.Endpoint})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func RemoveConfig(name string) error {
	name, err := sanitizeName(name)
	if err != nil {
		return err
	}
	d, err := configDir()
	if err != nil {
		return err
	}
	return os.Remove(filepath.Join(d, name+".conf"))
}

// GeneratePrivateKey returns a new base64 curve25519 private key.
func GeneratePrivateKey() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// curve25519 clamping
	b[0] &= 248
	b[31] &= 127
	b[31] |= 64
	return base64.StdEncoding.EncodeToString(b[:]), nil
}
