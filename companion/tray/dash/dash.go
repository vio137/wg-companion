// Package dash serves the WG Companion control dashboard on a loopback
// listener. Cross-platform so its auth surface is unit-testable anywhere.
//
// Security model: the tray generates a random per-run token and hands it to
// the browser in the URL fragment (#t=...), which is never sent to the
// server, never appears in Referer, and is stripped from history by the
// page's own replaceState. API calls carry it in an X-WG-Token header, which
// a cross-site page cannot set without CORS (we send no CORS headers), so
// the header doubles as CSRF protection. Static files are unauthenticated
// but carry nothing sensitive.
package dash

import (
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

//go:embed static/index.html
var indexHTML string

//go:embed static/app.css
var appCSS string

//go:embed static/app.js
var appJS string

var configName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

type API struct {
	Token  string
	Origin string // e.g. http://127.0.0.1:51234; empty disables Origin check
	Status func() any
	Exec   func(args ...string) (string, error) // runs wg-companion CLI
}

func (a *API) authOK(r *http.Request) bool {
	if len(a.Token) < 16 || subtle.ConstantTimeCompare([]byte(r.Header.Get("X-WG-Token")), []byte(a.Token)) != 1 {
		return false
	}
	if a.Origin != "" {
		if o := r.Header.Get("Origin"); o != "" && o != a.Origin {
			return false
		}
	}
	return true
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	expected, err := url.Parse(a.Origin)
	if err != nil || expected.Scheme != "http" || expected.Hostname() != "127.0.0.1" || r.Host != expected.Host {
		http.Error(w, "invalid host", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == "/":
		if r.Method != "GET" {
			http.Error(w, "method not allowed", 405)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	case r.URL.Path == "/static/app.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		_, _ = w.Write([]byte(appCSS))
	case r.URL.Path == "/static/app.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		_, _ = w.Write([]byte(appJS))
	case strings.HasPrefix(r.URL.Path, "/api/"):
		if !a.authOK(r) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		mutating := r.URL.Path == "/api/connect" || r.URL.Path == "/api/disconnect"
		if mutating {
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				http.Error(w, "method not allowed", 405)
				return
			}
			if r.Header.Get("Origin") != a.Origin {
				http.Error(w, "origin required", 403)
				return
			}
			mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mt != "application/json" {
				http.Error(w, "JSON required", 415)
				return
			}
		} else if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", 405)
			return
		}
		a.api(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *API) api(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	write := func(out string, err error) {
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			if out == "" {
				out = `{"error":` + strconv(err.Error()) + `}`
			}
		}
		_, _ = w.Write([]byte(out))
	}
	switch r.URL.Path {
	case "/api/status":
		b, _ := json.Marshal(a.Status())
		_, _ = w.Write(b)
	case "/api/configs":
		write(a.Exec("configs"))
	case "/api/connect":
		name := r.URL.Query().Get("name")
		if !configName.MatchString(name) {
			http.Error(w, "bad config name", http.StatusBadRequest)
			return
		}
		write(a.Exec("service-connect", "-name", name))
	case "/api/disconnect":
		write(a.Exec("service-disconnect"))
	default:
		http.NotFound(w, r)
	}
}

func strconv(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
