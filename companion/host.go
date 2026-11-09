package main

// Chrome native messaging host: 4-byte little-endian length-prefixed JSON on
// stdio. Chrome spawns this process when the extension calls
// chrome.runtime.connectNative; when the extension/browser goes away stdin
// hits EOF and we tear the tunnel down (nothing outlives Chrome's intent).

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
)

const (
	maxMessageSize = 1 << 20 // generous; Chrome's own cap is 1MB to the app
	hostName       = "com.wgcompanion.tunnel"
)

type request struct {
	ID          any       `json:"id"`
	Cmd         string    `json:"cmd"`
	Name        string    `json:"name,omitempty"`
	Contents    string    `json:"contents,omitempty"`
	AcceptTerms bool      `json:"accept_terms,omitempty"`
	Config      *WGConfig `json:"config,omitempty"` // service IPC only: full config for connect
}

// tunnelController is what the host's command loop needs from whoever owns
// the tunnel: the in-process Tunnel on unix, the Windows service over a pipe.
type tunnelController interface {
	Stats() TunnelStats
	Connect(cfg *WGConfig, socksAddr string) error
	Disconnect(reason string)
}

type response struct {
	ID    any    `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

func writeMsg(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.LittleEndian.PutUint32(hdr[:], uint32(len(b)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readMsg(r io.Reader, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(hdr[:])
	if n == 0 || n > maxMessageSize {
		return fmt.Errorf("bad message length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}

// runHost serves native messaging until stdin EOF or a write failure.
func runHost(t tunnelController, socksAddr string) error {
	in, out := os.Stdin, os.Stdout
	var wmu sync.Mutex
	send := func(v any) error {
		wmu.Lock()
		defer wmu.Unlock()
		return writeMsg(out, v)
	}

	for {
		var req request
		if err := readMsg(in, &req); err != nil {
			// EOF or protocol error: extension/browser went away.
			t.Disconnect("host channel closed")
			return nil
		}
		resp := response{ID: req.ID, OK: true}
		switch req.Cmd {
		case "ping":
			resp.Data = map[string]any{"version": version, "state": t.Stats().State}
		case "status":
			st := t.Stats()
			resp.Data = st
		case "list_configs":
			list, err := ListConfigs()
			if err != nil {
				resp.OK, resp.Error = false, err.Error()
			} else {
				if list == nil {
					list = []ConfigInfo{}
				}
				resp.Data = map[string]any{"configs": list}
			}
		case "import_config":
			cfg, err := SaveConfig(req.Name, req.Contents)
			if err != nil {
				resp.OK, resp.Error = false, err.Error()
			} else {
				resp.Data = map[string]any{"name": cfg.Name, "endpoint": cfg.Endpoint}
			}
		case "register_warp":
			if !req.AcceptTerms {
				resp.OK, resp.Error = false, "accept Cloudflare terms first"
				break
			}
			if err := registerWARP(); err != nil {
				resp.OK, resp.Error = false, err.Error()
			} else {
				resp.Data = map[string]string{"name": "WARP"}
			}
		case "remove_config":
			if st := t.Stats(); (st.State == "connected" || st.State == "ready") && st.Config == req.Name {
				resp.OK, resp.Error = false, "config is in use; disconnect first"
				break
			}
			if err := RemoveConfig(req.Name); err != nil {
				resp.OK, resp.Error = false, err.Error()
			}
		case "connect":
			name := req.Name
			if name == "" {
				list, _ := ListConfigs()
				if len(list) == 1 {
					name = list[0].Name
				} else if len(list) == 0 {
					resp.OK, resp.Error = false, "no config imported yet"
					break
				} else {
					resp.OK, resp.Error = false, "multiple configs; specify name"
					break
				}
			}
			cfg, err := LoadConfig(name)
			if err != nil {
				resp.OK, resp.Error = false, err.Error()
				break
			}
			if err := t.Connect(cfg, socksAddr); err != nil {
				resp.OK, resp.Error = false, err.Error()
			} else {
				resp.Data = t.Stats()
			}
		case "disconnect":
			t.Disconnect("requested")
			resp.Data = t.Stats()
		case "stats":
			resp.Data = t.Stats()
		default:
			resp.OK, resp.Error = false, "unknown cmd "+req.Cmd
		}
		if err := send(resp); err != nil {
			t.Disconnect("host channel write failed")
			return nil
		}
	}
}
