package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSetupPersistsHashAndRejectsReplacement(t *testing.T) {
	file := filepath.Join(t.TempDir(), "account.json")
	m, err := New(file, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Configured() {
		t.Fatal("new install unexpectedly configured")
	}
	if err := m.Setup("admin", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := m.Setup("admin", "a-long-secret-password"); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), "a-long-secret-password") {
		t.Fatal("stored plaintext password")
	}
	info, _ := os.Stat(file)
	if info.Mode().Perm() != 0600 {
		t.Fatal("credentials not owner-only")
	}
	reloaded, err := New(file, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Verify("admin", "a-long-secret-password") || reloaded.Verify("other", "a-long-secret-password") || reloaded.Verify("admin", "wrong") {
		t.Fatal("credential verification failed")
	}
	if err := reloaded.Setup("other", "some-other-password"); err != ErrConfigured {
		t.Fatalf("setup replaced account: %v", err)
	}
}

func TestSessionRevocationExpiryAndDockerBasic(t *testing.T) {
	m, _ := New("", "admin", "secret")
	token, ok := m.Login("admin", "secret")
	if !ok {
		t.Fatal("login failed")
	}
	r := httptest.NewRequest("GET", "/api/status", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	if m.User(r, true) != "admin" || m.User(r, false) != "" {
		t.Fatal("session scope incorrect")
	}
	m.Logout(r)
	if m.User(r, true) != "" {
		t.Fatal("revoked cookie still valid")
	}
	token, _ = m.Login("admin", "secret")
	r = httptest.NewRequest("GET", "/api/status", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	for key, s := range m.sessions {
		s.Expires = time.Now().Add(-time.Minute)
		m.sessions[key] = s
	}
	if m.User(r, true) != "" {
		t.Fatal("expired session accepted")
	}
	r.SetBasicAuth("admin", "secret")
	if m.User(r, false) != "admin" {
		t.Fatal("Docker Basic authentication failed")
	}
	w := httptest.NewRecorder()
	SetCookie(w, r, "token", true)
	c := w.Result().Cookies()[0]
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("weak session cookie flags")
	}
}

func TestConcurrentSetupOnlyCreatesOneAccount(t *testing.T) {
	m, _ := New(filepath.Join(t.TempDir(), "account.json"), "", "")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func() { defer wg.Done(); results <- m.Setup(name, "a-long-secret-password") }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("%d concurrent setups succeeded", success)
	}
}
