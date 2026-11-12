package server

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"magistry/internal/auth"
)

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}
func readJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		respond(w, 415, map[string]string{"error": "Expected application/json"})
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16384)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		respond(w, 400, map[string]string{"error": "Invalid JSON request"})
		return false
	}
	return true
}
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		return err == nil && u.Host == r.Host && (u.Scheme == "http" || u.Scheme == "https")
	}
	return true
}

type attempt struct {
	Count int
	Until time.Time
}
type limiter struct {
	mu       sync.Mutex
	attempts map[string]attempt
}

func remote(r *http.Request) string {
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
func (l *limiter) blocked(r *http.Request) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.attempts[remote(r)]
	return a.Count >= 10 && time.Now().Before(a.Until)
}
func (l *limiter) fail(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.attempts) >= 1024 {
		for ip, a := range l.attempts {
			if time.Now().After(a.Until) {
				delete(l.attempts, ip)
			}
		}
		if len(l.attempts) >= 1024 {
			return
		}
	}
	ip := remote(r)
	a := l.attempts[ip]
	if time.Now().After(a.Until) {
		a = attempt{Until: time.Now().Add(15 * time.Minute)}
	}
	a.Count++
	l.attempts[ip] = a
}
func registerAuth(mux *http.ServeMux, m *auth.Manager, secure bool, l *limiter) {
	mux.HandleFunc("GET /api/auth/session", func(w http.ResponseWriter, r *http.Request) {
		user := m.SessionUser(r)
		respond(w, 200, map[string]any{"authenticated": user != "", "username": user, "setupRequired": !m.Configured(), "managed": m.Managed()})
	})
	for _, mode := range []string{"login", "setup"} {
		mux.HandleFunc("POST /api/auth/"+mode, func(w http.ResponseWriter, r *http.Request) {
			if l.blocked(r) {
				w.Header().Set("Retry-After", "900")
				respond(w, 429, map[string]string{"error": "Too many failed attempts. Try again in 15 minutes."})
				return
			}
			var input struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if !readJSON(w, r, &input) {
				return
			}
			if mode == "setup" {
				if err := m.Setup(input.Username, input.Password); err != nil {
					status := 400
					if errors.Is(err, auth.ErrConfigured) {
						status = 409
					}
					respond(w, status, map[string]string{"error": err.Error()})
					return
				}
			}
			token, ok := m.Login(input.Username, input.Password)
			if !ok {
				l.fail(r)
				respond(w, 401, map[string]string{"error": "Invalid username or password"})
				return
			}
			auth.SetCookie(w, r, token, secure)
			respond(w, 200, map[string]any{"authenticated": true, "username": input.Username, "setupRequired": false, "managed": m.Managed()})
		})
	}
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		m.Logout(r)
		auth.SetCookie(w, r, "", secure)
		w.WriteHeader(204)
	})
}
