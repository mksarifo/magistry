package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"magistry/internal/registry"
)

func handler(t *testing.T, upstream http.HandlerFunc, config Config) http.Handler {
	t.Helper()
	s := httptest.NewServer(upstream)
	t.Cleanup(s.Close)
	c, err := registry.New(s.URL)
	if err != nil {
		t.Fatal(err)
	}
	autoAuth := config.Username == "" && config.Auth == nil
	if autoAuth {
		config.Username = "admin"
		config.Password = "secret"
	}
	h := New(c, config)
	if autoAuth {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { r.SetBasicAuth("admin", "secret"); h.ServeHTTP(w, r) })
	}
	return h
}

func TestAuthenticationCoversUIAPIAndRegistry(t *testing.T) {
	h := handler(t, func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "{}") }, Config{Username: "admin", Password: "secret"})
	for _, path := range []string{"/api/status", "/v2/", "/v2/app/blobs/uploads/"} {
		for _, password := range []string{"", "wrong", "secret"} {
			r := httptest.NewRequest("GET", path, nil)
			if password != "" {
				r.SetBasicAuth("admin", password)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if password != "secret" && (w.Code != 401 || (strings.HasPrefix(path, "/v2/") && w.Header().Get("WWW-Authenticate") == "")) {
				t.Errorf("%s got %d", path, w.Code)
			}
			if password == "secret" && w.Code == 401 {
				t.Errorf("valid credentials rejected on %s", path)
			}
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != 200 {
		t.Errorf("liveness requires auth: %d", w.Code)
	}
}

func TestStreamingProxyPreservesUploadProtocol(t *testing.T) {
	payload := strings.Repeat("image-layer-", 10000)
	h := handler(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != payload || r.Method != "PATCH" || r.URL.RawQuery != "_state=token" {
			t.Error("upload request changed")
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("frontend credentials leaked to upstream")
		}
		if r.Header.Get("X-Forwarded-Host") != "registry.example" {
			t.Error("missing external host")
		}
		w.Header().Set("Location", "/v2/app/blobs/uploads/upload-id?_state=next")
		w.Header().Set("Docker-Upload-UUID", "upload-id")
		w.Header().Set("Range", "0-119999")
		w.WriteHeader(202)
	}, Config{})
	r := httptest.NewRequest("PATCH", "http://registry.example/v2/app/blobs/uploads/upload-id?_state=token", strings.NewReader(payload))
	r.SetBasicAuth("admin", "secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 202 || w.Header().Get("Docker-Upload-UUID") != "upload-id" || !strings.HasPrefix(w.Header().Get("Location"), "/v2/") {
		t.Fatalf("broken upload response: %d %v", w.Code, w.Header())
	}
}

func TestDeleteValidationAndSameOrigin(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	calls := 0
	h := handler(t, func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(202) }, Config{})
	for _, tt := range []struct {
		query, origin string
		status        int
	}{
		{"repository=app&digest=" + digest, "http://malicious.example", 403},
		{"repository=../app&digest=" + digest, "", 400},
		{"repository=app&digest=latest", "", 400},
		{"repository=app&digest=" + digest, "http://example.com", 204},
	} {
		r := httptest.NewRequest("DELETE", "http://example.com/api/manifests?"+tt.query, nil)
		r.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tt.status {
			t.Errorf("%s origin %s got %d want %d", tt.query, tt.origin, w.Code, tt.status)
		}
	}
	if calls != 1 {
		t.Errorf("rejected deletes reached registry: %d", calls)
	}
}

func TestStaticFilesAndMissingAssets(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>Magistry</html>"), 0600); err != nil {
		t.Fatal(err)
	}
	h := handler(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }, Config{WebDir: dir})
	for _, tt := range []struct {
		path   string
		status int
	}{{"/", 200}, {"/repositories", 200}, {"/missing.js", 404}, {"/api/unknown", 404}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", tt.path, nil))
		if w.Code != tt.status {
			t.Errorf("%s got %d", tt.path, w.Code)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("missing content security header")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/status", nil))
	if w.Code != 502 || !strings.Contains(w.Body.String(), "error") {
		t.Fatalf("upstream failure lost: %d %s", w.Code, w.Body.String())
	}
}
