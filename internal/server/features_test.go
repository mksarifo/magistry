package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"magistry/internal/auth"
	"magistry/internal/storage"
)

func TestFirstRunLoginLogoutAndCSRF(t *testing.T) {
	a, _ := auth.New(filepath.Join(t.TempDir(), "account.json"), "", "")
	s, _ := storage.New(t.TempDir(), "", "", "")
	h := handler(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"repositories":[]}`) }, Config{Auth: a, Storage: s})
	do := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://example.com"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := do("GET", "/api/repositories", "", "", nil); w.Code != 401 {
		t.Fatal("unconfigured registry was open")
	}
	w := do("POST", "/api/auth/setup", `{"username":"admin","password":"a-long-secret-password"}`, "http://example.com", nil)
	if w.Code != 200 {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	cookie := w.Result().Cookies()[0]
	if w := do("GET", "/api/repositories", "", "", cookie); w.Code != 200 {
		t.Fatal("session not authorized")
	}
	if w := do("GET", "/v2/", "", "", cookie); w.Code != 401 {
		t.Fatal("browser cookie authorized Docker API")
	}
	if w := do("POST", "/api/auth/setup", `{"username":"other","password":"some-other-password"}`, "", nil); w.Code != 409 {
		t.Fatal("setup reopened")
	}
	if w := do("PUT", "/api/storage/limit", `{"limitBytes":1000}`, "http://attacker.example", cookie); w.Code != 403 {
		t.Fatal("cross-origin write accepted")
	}
	if w := do("PUT", "/api/storage/limit", `{"limitBytes":1000}`, "http://example.com", cookie); w.Code != 204 {
		t.Fatalf("limit update: %d %s", w.Code, w.Body.String())
	}
	if w := do("POST", "/api/auth/logout", "", "http://example.com", cookie); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if w := do("GET", "/api/repositories", "", "", cookie); w.Code != 401 {
		t.Fatal("logged-out cookie accepted")
	}
	w = do("POST", "/api/auth/login", `{"username":"admin","password":"a-long-secret-password"}`, "", nil)
	if w.Code != 200 {
		t.Fatal("login failed")
	}
}

func TestMaintenanceBlocksRegistryAndDeletion(t *testing.T) {
	s, _ := storage.New(t.TempDir(), "", "", "")
	calls := 0
	h := handler(t, func(w http.ResponseWriter, _ *http.Request) { calls++; w.WriteHeader(200) }, Config{Storage: s})
	s.Gate.Lock()
	for _, method := range []string{"GET", "POST"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/v2/", nil))
		if w.Code != 503 {
			t.Fatalf("maintenance allowed %s: %d", method, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("DELETE", "/api/manifests?repository=app&digest=sha256:"+strings.Repeat("a", 64), nil))
	if w.Code != 503 {
		t.Fatal("deletion allowed during maintenance")
	}
	s.Gate.Unlock()
	if calls != 0 {
		t.Fatal("maintenance reached upstream")
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := handler(t, func(w http.ResponseWriter, _ *http.Request) {}, Config{Username: "admin", Password: "secret"})
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("attempt %d status %d want %d", i, w.Code, want)
		}
	}
}
