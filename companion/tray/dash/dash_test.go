package dash

import (
	"errors"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testAPI() *API {
	return &API{
		Token:  "secret-token-for-unit-tests",
		Origin: "http://127.0.0.1:9999",
		Status: func() any { return map[string]string{"state": "disconnected"} },
		Exec: func(args ...string) (string, error) {
			if args[0] == "configs" {
				return `[]`, nil
			}
			return "{}", errors.New("boom")
		},
	}
}

func req(t *testing.T, a *API, method, path, token, origin string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Host = "127.0.0.1:9999"
	r.Header.Set("Content-Type", "application/json")
	if token != "" {
		r.Header.Set("X-WG-Token", token)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}

func TestIndexAndStaticOpen(t *testing.T) {
	a := testAPI()
	for _, p := range []string{"/", "/static/app.css", "/static/app.js"} {
		if w := req(t, a, "GET", p, "", ""); w.Code != 200 {
			t.Fatalf("%s: got %d, want 200 (static must be open)", p, w.Code)
		}
	}
}

func TestAPIRequiresToken(t *testing.T) {
	a := testAPI()
	for _, p := range []string{"/api/status", "/api/configs", "/api/disconnect"} {
		if w := req(t, a, "POST", p, "", ""); w.Code != 403 {
			t.Fatalf("%s without token: got %d, want 403", p, w.Code)
		}
		if w := req(t, a, "POST", p, "wrong", ""); w.Code != 403 {
			t.Fatalf("%s with wrong token: got %d, want 403", p, w.Code)
		}
		if w := req(t, a, "GET", p, "secret-token-for-unit-tests", ""); w.Code == 403 {
			t.Fatalf("%s with right token: got 403", p)
		}
	}
}

func TestAPIRejectsForeignOrigin(t *testing.T) {
	a := testAPI()
	if w := req(t, a, "GET", "/api/status", "secret-token-for-unit-tests", "https://evil.example"); w.Code != 403 {
		t.Fatalf("foreign Origin: got %d, want 403", w.Code)
	}
	if w := req(t, a, "GET", "/api/status", "secret-token-for-unit-tests", "http://127.0.0.1:9999"); w.Code != 200 {
		t.Fatalf("own Origin: got %d, want 200", w.Code)
	}
}

func TestConnectValidatesName(t *testing.T) {
	a := testAPI()
	for _, bad := range []string{"", "../etc", "a b", "x;rm -rf", string(make([]byte, 65))} {
		w := req(t, a, "POST", "/api/connect?name="+url.QueryEscape(bad), "secret-token-for-unit-tests", "http://127.0.0.1:9999")
		if w.Code != 400 {
			t.Fatalf("name %q: got %d, want 400", bad, w.Code)
		}
	}
	if w := req(t, a, "POST", "/api/connect?name=my-tunnel_1.conf", "secret-token-for-unit-tests", "http://127.0.0.1:9999"); w.Code == 400 {
		t.Fatalf("valid name rejected: %d", w.Code)
	}
}

func TestTokenNotEchoedInIndex(t *testing.T) {
	a := testAPI()
	w := req(t, a, "GET", "/", "", "")
	if strings.Contains(w.Body.String(), "secret-token-for-unit-tests") {
		t.Fatal("token leaked into served index")
	}
}

func TestControlRestrictions(t *testing.T) {
	for _, tt := range []struct {
		method, host, origin, content, token string
		want                                 int
	}{
		{"GET", "127.0.0.1:9999", "http://127.0.0.1:9999", "application/json", "secret-token-for-unit-tests", 405},
		{"POST", "evil.test:9999", "http://127.0.0.1:9999", "application/json", "secret-token-for-unit-tests", 403},
		{"POST", "127.0.0.1:9999", "", "application/json", "secret-token-for-unit-tests", 403},
		{"POST", "127.0.0.1:9999", "null", "application/json", "secret-token-for-unit-tests", 403},
		{"POST", "127.0.0.1:9999", "http://127.0.0.1:9999", "text/plain", "secret-token-for-unit-tests", 415},
		{"OPTIONS", "127.0.0.1:9999", "https://evil.test", "application/json", "", 403},
	} {
		a := testAPI()
		called := false
		a.Exec = func(args ...string) (string, error) { called = true; return "{}", nil }
		r := httptest.NewRequest(tt.method, "/api/disconnect", nil)
		r.Host = tt.host
		r.Header.Set("Origin", tt.origin)
		r.Header.Set("Content-Type", tt.content)
		r.Header.Set("X-WG-Token", tt.token)
		w := httptest.NewRecorder()
		a.ServeHTTP(w, r)
		if w.Code != tt.want || called {
			t.Fatalf("%+v: code=%d exec=%v", tt, w.Code, called)
		}
		if w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("CORS enabled")
		}
	}
}
func TestEmptyTokenFailsClosed(t *testing.T) {
	a := testAPI()
	a.Token = ""
	if w := req(t, a, "GET", "/api/status", "", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
}
