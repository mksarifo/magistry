package server

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"magistry/internal/auth"
	"magistry/internal/registry"
	"magistry/internal/storage"
)

type Config struct {
	WebDir, Username, Password, PublicRegistry string
	Auth                                       *auth.Manager
	Storage                                    *storage.Manager
	CookieSecure                               bool
}

func New(client *registry.Client, config Config) http.Handler {
	mux := http.NewServeMux()
	manager := config.Auth
	if manager == nil {
		var err error
		manager, err = auth.New("", config.Username, config.Password)
		if err != nil {
			panic(err)
		}
	}
	attempts := &limiter{attempts: make(map[string]attempt)}
	registerAuth(mux, manager, config.CookieSecure, attempts)
	write := func(w http.ResponseWriter, status int, value any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(value)
	}
	fail := func(w http.ResponseWriter, err error) {
		status := http.StatusBadGateway
		var upstream *registry.UpstreamError
		if errors.As(err, &upstream) && upstream.Status == 404 {
			status = http.StatusNotFound
		}
		write(w, status, map[string]string{"error": err.Error()})
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { write(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		if err := client.Ping(r.Context()); err != nil {
			fail(w, err)
			return
		}
		write(w, 200, map[string]any{"connected": true, "registry": config.PublicRegistry, "authEnabled": true})
	})
	mux.HandleFunc("GET /api/repositories", func(w http.ResponseWriter, r *http.Request) {
		repos, err := client.Repositories(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, map[string]any{"repositories": repos})
	})
	mux.HandleFunc("GET /api/tags", func(w http.ResponseWriter, r *http.Request) {
		repo := r.URL.Query().Get("repository")
		if !registry.ValidRepository(repo) {
			write(w, 400, map[string]string{"error": "Invalid repository name"})
			return
		}
		tags, err := client.Tags(r.Context(), repo)
		if err != nil {
			fail(w, err)
			return
		}
		write(w, 200, map[string]any{"tags": tags})
	})
	mux.HandleFunc("DELETE /api/manifests", func(w http.ResponseWriter, r *http.Request) {
		if config.Storage != nil {
			if config.Storage.Job().State == "running" || !config.Storage.Gate.TryRLock() {
				write(w, 503, map[string]string{"error": "Storage maintenance is running. Retry after it finishes."})
				return
			}
			defer config.Storage.Gate.RUnlock()
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(origin)
			if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
				write(w, 403, map[string]string{"error": "Cross-origin deletion is not allowed"})
				return
			}
		}
		repo, digest := r.URL.Query().Get("repository"), r.URL.Query().Get("digest")
		if !registry.ValidRepository(repo) || !registry.ValidDigest(digest) {
			write(w, 400, map[string]string{"error": "Invalid repository or digest"})
			return
		}
		if err := client.Delete(r.Context(), repo, digest); err != nil {
			fail(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		write(w, 404, map[string]string{"error": "API endpoint not found"})
	})
	if config.Storage != nil {
		mux.HandleFunc("GET /api/storage", func(w http.ResponseWriter, r *http.Request) {
			usage, err := config.Storage.Usage(r.Context())
			if err != nil {
				write(w, 503, map[string]string{"error": err.Error()})
				return
			}
			write(w, 200, map[string]any{"usage": usage, "job": config.Storage.Job()})
		})
		mux.HandleFunc("PUT /api/storage/limit", func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				LimitBytes int64 `json:"limitBytes"`
			}
			if !readJSON(w, r, &input) {
				return
			}
			if err := config.Storage.SetLimit(input.LimitBytes); err != nil {
				write(w, 400, map[string]string{"error": err.Error()})
				return
			}
			w.WriteHeader(204)
		})
		mux.HandleFunc("POST /api/storage/cleanup", func(w http.ResponseWriter, r *http.Request) {
			var input struct {
				DryRun  bool `json:"dryRun"`
				Confirm bool `json:"confirm"`
			}
			if !readJSON(w, r, &input) {
				return
			}
			if !input.DryRun && !input.Confirm {
				write(w, 400, map[string]string{"error": "Confirm unused blob deletion first"})
				return
			}
			if err := config.Storage.StartCleanup(input.DryRun); err != nil {
				write(w, 409, map[string]string{"error": err.Error()})
				return
			}
			write(w, 202, config.Storage.Job())
		})
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(client.URL)
			pr.SetXForwarded()
			pr.Out.Header.Del("Authorization")
		},
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			slog.Error("registry proxy", "error", err)
			write(w, 502, map[string]string{"error": "Registry unavailable"})
		},
	}
	mux.Handle("/v2/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if config.Storage != nil {
			if config.Storage.Job().State == "running" || !config.Storage.Gate.TryRLock() {
				w.Header().Set("Retry-After", "10")
				write(w, 503, map[string]string{"error": "Storage maintenance is running. Retry after it finishes."})
				return
			}
			defer config.Storage.Gate.RUnlock()
			if strings.Contains(r.URL.Path, "/blobs/uploads/") && (r.Method == "POST" || r.Method == "PATCH" || r.Method == "PUT") {
				if err := config.Storage.CheckUpload(r.Context(), r.ContentLength); err != nil {
					write(w, 413, map[string]string{"error": err.Error()})
					return
				}
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	static := http.FileServer(http.Dir(config.WebDir))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean == "" {
			clean = "index.html"
		}
		if strings.HasPrefix(clean, ".") {
			http.NotFound(w, r)
			return
		}
		if _, err := fs.Stat(os.DirFS(config.WebDir), clean); err != nil {
			if path.Ext(clean) != "" {
				http.NotFound(w, r)
				return
			}
			http.ServeFile(w, r, config.WebDir+"/index.html")
			return
		}
		if strings.HasPrefix(clean, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		static.ServeHTTP(w, r)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		isAPI := strings.HasPrefix(r.URL.Path, "/api/")
		isRegistry := strings.HasPrefix(r.URL.Path, "/v2/")
		if isAPI && r.Method != "GET" && r.Method != "HEAD" && !sameOrigin(r) {
			write(w, 403, map[string]string{"error": "Cross-origin request is not allowed"})
			return
		}
		publicAuth := r.URL.Path == "/api/auth/session" || r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/auth/setup"
		if (isAPI && !publicAuth) || isRegistry {
			if attempts.blocked(r) {
				w.Header().Set("Retry-After", "900")
				write(w, 429, map[string]string{"error": "Too many failed attempts. Try again in 15 minutes."})
				return
			}
			if manager.User(r, !isRegistry) == "" {
				if r.Header.Get("Authorization") != "" {
					attempts.fail(r)
				}
				if isRegistry {
					w.Header().Set("WWW-Authenticate", `Basic realm="Magistry", charset="UTF-8"`)
				}
				write(w, 401, map[string]string{"error": "Authentication required"})
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func HTTPServer(addr string, handler http.Handler) *http.Server {
	// No WriteTimeout: large pushes and pulls are streamed, not buffered.
	return &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 1 << 20}
}
