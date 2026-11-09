//go:build windows

// wg-tray: user-session tray icon + control surface for the WG Companion
// Windows service. Shows the real tunnel state as an animated tray icon,
// serves the glass dashboard on 127.0.0.1 (random port + per-run token), and
// runs config actions through wg-companion.exe (DPAPI configs stay in the
// user's own profile).
package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unsafe"
	"wireguard-companion/internal/serviceipc"
	"wireguard-companion/tray/dash"

	"github.com/getlantern/systray"
)

const (
	pipePath     = `\\.\pipe\WGCompanion`
	pollFast     = time.Second      // while connecting or animating a transition
	pollSteady   = 3 * time.Second  // connected/disconnected steady states
	pollSvcDown  = 15 * time.Second // service not reachable
	dashTokenLen = 16               // bytes
)

// ---------- minimal pipe protocol (same framing as the companion host) ----------

type request struct {
	ID   any    `json:"id"`
	Cmd  string `json:"cmd"`
	Name string `json:"name,omitempty"`
}

type response struct {
	ID    any    `json:"id"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Data  any    `json:"data,omitempty"`
}

type stats struct {
	State     string `json:"state"`
	Config    string `json:"config,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"`
	RxBytes   int64  `json:"rx_bytes,omitempty"`
	TxBytes   int64  `json:"tx_bytes,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

func writeMsg(c net.Conn, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var hdr [4]byte
	hdr[0] = byte(len(b))
	hdr[1] = byte(len(b) >> 8)
	hdr[2] = byte(len(b) >> 16)
	hdr[3] = byte(len(b) >> 24)
	if _, err := c.Write(hdr[:]); err != nil {
		return err
	}
	_, err = c.Write(b)
	return err
}

func readMsg(c net.Conn, v any) error {
	var hdr [4]byte
	if _, err := io.ReadFull(c, hdr[:]); err != nil {
		return err
	}
	n := int(hdr[0]) | int(hdr[1])<<8 | int(hdr[2])<<16 | int(hdr[3])<<24
	if n == 0 || n > 1<<20 {
		return fmt.Errorf("bad message length %d", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		return err
	}
	return json.Unmarshal(buf, v)
}

func pipeStats() (stats, error) {
	var st stats
	c, err := serviceipc.Dial(pipePath, companionExe())
	if err != nil {
		return st, err
	}
	defer c.Close()
	if err = writeMsg(c, &request{ID: 1, Cmd: "stats"}); err != nil {
		return st, err
	}
	var r response
	if err = readMsg(c, &r); err != nil {
		return st, err
	}
	if !r.OK {
		return st, fmt.Errorf("%s", r.Error)
	}
	b, err := json.Marshal(r.Data)
	if err != nil {
		return st, err
	}
	if err = json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	return st, nil
}

// Real ICO assets are generated from the same glyphs as the extension.
// No per-poll PNG encoding or image allocation.
//
//go:embed icons/*.ico
var iconAssets embed.FS
var iconCache = map[string][]byte{}

func iconData(name string) []byte {
	if b := iconCache[name]; b != nil {
		return b
	}
	b, _ := iconAssets.ReadFile("icons/" + name + ".ico")
	iconCache[name] = b
	return b
}

// ---------- tray state ----------

type trayState struct {
	mu           sync.Mutex
	st           stats
	svcDown      bool
	reduceMotion bool
}

var state trayState

func stateFile() string {
	dir, _ := os.UserConfigDir()
	return filepath.Join(dir, "WGCompanion", "tray-state.json")
}

func loadPrefs() {
	// Respect Windows' animation setting until a tray preference is saved.
	var animate uint32
	r, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("SystemParametersInfoW").Call(0x1042, 0, uintptr(unsafe.Pointer(&animate)), 0)
	state.reduceMotion = r == 0 || animate == 0
	b, err := os.ReadFile(stateFile())
	if err == nil {
		var p struct {
			ReduceMotion bool `json:"reduce_motion"`
		}
		if json.Unmarshal(b, &p) == nil {
			state.reduceMotion = p.ReduceMotion
		}
	}
}

func savePrefs() {
	b, _ := json.Marshal(map[string]bool{"reduce_motion": state.reduceMotion})
	_ = os.MkdirAll(filepath.Dir(stateFile()), 0700)
	_ = os.WriteFile(stateFile(), b, 0600)
}

func companionExe() string {
	exe, _ := os.Executable()
	return filepath.Join(filepath.Dir(exe), "wg-companion.exe")
}

func runCompanion(args ...string) (string, error) {
	cmd := exec.Command(companionExe(), args...)
	cmd.SysProcAttr = sysProcHideWindow
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// ---------- polling + icon animation ----------

func pollLoop() {
	frame := 0
	for {
		st, err := pipeStats()
		state.mu.Lock()
		state.svcDown = err != nil
		state.st = st
		rm := state.reduceMotion
		state.mu.Unlock()

		var tip, label string
		var icon []byte
		var wait time.Duration
		switch {
		case err != nil:
			tip = "WG Companion: service not running"
			label = "Service not running"
			icon = iconData("blocked")
			wait = pollSvcDown
		case st.State == "connecting":
			tip = "WG Companion: connecting to " + st.Endpoint
			label = "Connecting..."
			if rm {
				icon = iconData("busy0")
				wait = pollFast
			} else {
				icon = iconData(fmt.Sprintf("busy%d", frame%4))
				wait = pollFast
			}
			frame++
		case st.State == "ready":
			tip = "WG Companion: waiting for a peer handshake"
			label = "Waiting for peer"
			icon = iconData("ready")
			wait = pollSteady
		case st.State == "connected":
			tip = fmt.Sprintf("WG Companion: connected to %s", st.Endpoint)
			label = "Connected"
			icon = iconData("on")
			wait = pollSteady
		case st.State == "error":
			tip = "WG Companion: error - " + st.LastError
			label = "Error"
			icon = iconData("error")
			wait = pollSteady
		default:
			tip = "WG Companion: disconnected"
			label = "Disconnected"
			icon = iconData("off")
			wait = pollSteady
		}
		systray.SetIcon(icon)
		systray.SetTooltip(tip)
		mStatus.SetTitle("Status: " + label)
		time.Sleep(wait)
	}
}

var mStatus *systray.MenuItem

// openDashboard prefers a Chrome app window, falls back to the default browser.
func openDashboard(url string) {
	chrome := filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe")
	if _, err := os.Stat(chrome); err == nil {
		_ = exec.Command(chrome, "--app="+url).Start()
		return
	}
	_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
}

// ---------- entry ----------

func main() {
	loadPrefs()
	systray.Run(onReady, func() {})
}

func onReady() {
	b := make([]byte, dashTokenLen)
	if _, err := rand.Read(b); err != nil {
		return
	}
	token := hex.EncodeToString(b)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		systray.SetTooltip("WG Companion: dashboard failed to start")
		return
	}
	origin := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	api := &dash.API{
		Token:  token,
		Origin: origin,
		Status: func() any {
			state.mu.Lock()
			defer state.mu.Unlock()
			return map[string]any{"svc_down": state.svcDown, "stats": state.st}
		},
		Exec: runCompanion,
	}
	go func() { _ = http.Serve(ln, api) }()
	dashURL := origin + "/#t=" + token

	mStatus = systray.AddMenuItem("Status: ...", "Current tunnel state")
	mStatus.Disable()
	systray.AddSeparator()
	mOpen := systray.AddMenuItem("Open control panel", "Open the WG Companion dashboard")
	mConn := systray.AddMenuItem("Connect", "Connect using your saved config")
	mDisc := systray.AddMenuItem("Disconnect", "Disconnect the tunnel")
	systray.AddSeparator()
	mMotion := systray.AddMenuItem("Reduce motion", "Pause tray icon animation")
	if state.reduceMotion {
		mMotion.Check()
	}
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Close the tray app (the service keeps running)")

	go pollLoop()

	go func() {
		for {
			select {
			case <-mOpen.ClickedCh:
				openDashboard(dashURL)
			case <-mConn.ClickedCh:
				go runCompanion("service-connect")
			case <-mDisc.ClickedCh:
				go runCompanion("service-disconnect")
			case <-mMotion.ClickedCh:
				state.mu.Lock()
				state.reduceMotion = !state.reduceMotion
				rm := state.reduceMotion
				state.mu.Unlock()
				if rm {
					mMotion.Check()
				} else {
					mMotion.Uncheck()
				}
				savePrefs()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}
