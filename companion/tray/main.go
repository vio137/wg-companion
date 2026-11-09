//go:build windows

// wg-tray: user-session tray icon + control surface for the WG Companion
// Windows service. Shows the real tunnel state as an animated tray icon,
// serves the glass dashboard on 127.0.0.1 (random port + per-run token), and
// runs config actions through wg-companion.exe (DPAPI configs stay in the
// user's own profile).
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unsafe"
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
	out, err := runCompanion("service-status")
	if err != nil {
		return st, err
	}
	if err = json.Unmarshal([]byte(out), &st); err != nil {
		return st, err
	}
	if st.State == "error" {
		return st, fmt.Errorf("%s", st.LastError)
	}
	return st, nil
}

// ---------- tray icon rendering ----------

var (
	colOff  = color.NRGBA{139, 147, 167, 255}
	colOn   = color.NRGBA{52, 211, 153, 255}
	colBusy = color.NRGBA{90, 162, 255, 255}
	colErr  = color.NRGBA{248, 113, 113, 255}
	colRing = color.NRGBA{226, 232, 255, 220}
)

// iconFrame renders the status mark: a glass ring with a state core. Busy
// frames orbit a highlight around the ring.
func iconFrame(core color.NRGBA, busyStep int, ringOnly bool) []byte {
	const sz = 64
	img := image.NewNRGBA(image.Rect(0, 0, sz, sz))
	cx, cy := sz/2, sz/2
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			dx, dy := float64(x-cx), float64(y-cy)
			r := math.Hypot(dx, dy)
			switch {
			case r > 27 && r < 31:
				a := uint8(colRing.A)
				if busyStep >= 0 {
					ang := math.Atan2(dy, dx)
					highlight := math.Cos(ang - float64(busyStep)*math.Pi/3)
					if highlight > 0.3 {
						a = uint8(math.Min(255, float64(a)+highlight*120))
					}
				}
				img.SetNRGBA(x, y, color.NRGBA{colRing.R, colRing.G, colRing.B, a})
			case r <= 17 && !ringOnly:
				img.SetNRGBA(x, y, core)
			}
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	// Windows tray API requires ICO, not raw PNG. ICO supports PNG payloads.
	data := buf.Bytes()
	header := make([]byte, 22)
	header[2] = 1
	header[4] = 1
	header[6] = 64
	header[7] = 64
	header[10] = 1
	header[12] = 32
	n := len(data)
	for i := 0; i < 4; i++ {
		header[14+i] = byte(n >> (8 * i))
	}
	header[18] = 22
	return append(header, data...)
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
			icon = iconFrame(colOff, -1, true)
			wait = pollSvcDown
		case st.State == "connecting" || st.State == "ready":
			tip = "WG Companion: connecting to " + st.Endpoint
			label = "Connecting..."
			if rm {
				icon = iconFrame(colBusy, -1, false)
				wait = pollFast
			} else {
				icon = iconFrame(colBusy, frame, false)
				wait = pollFast
			}
			frame++
		case st.State == "connected":
			tip = fmt.Sprintf("WG Companion: connected to %s", st.Endpoint)
			label = "Connected"
			icon = iconFrame(colOn, -1, false)
			wait = pollSteady
		case st.State == "error":
			tip = "WG Companion: error - " + st.LastError
			label = "Error"
			icon = iconFrame(colErr, -1, false)
			wait = pollSteady
		default:
			tip = "WG Companion: disconnected"
			label = "Disconnected"
			icon = iconFrame(colOff, -1, false)
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
