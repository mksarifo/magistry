package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"magistry/internal/auth"
	"magistry/internal/registry"
	"magistry/internal/server"
	"magistry/internal/storage"
	"path/filepath"
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func main() {
	client, err := registry.New(env("REGISTRY_URL", "http://127.0.0.1:5001"))
	if err != nil {
		slog.Error(err.Error())
		os.Exit(1)
	}
	user, password := os.Getenv("AUTH_USERNAME"), os.Getenv("AUTH_PASSWORD")
	if (user == "") != (password == "") {
		slog.Error("Set both AUTH_USERNAME and AUTH_PASSWORD, or neither")
		os.Exit(1)
	}
	dataDir := env("DATA_DIR", ".magistry")
	authManager, err := auth.New(filepath.Join(dataDir, "account.json"), user, password)
	if err != nil {
		slog.Error("authentication setup", "error", err)
		os.Exit(1)
	}
	storageManager, err := storage.New(os.Getenv("REGISTRY_DATA_DIR"), filepath.Join(dataDir, "storage.json"), os.Getenv("REGISTRY_BINARY"), os.Getenv("REGISTRY_CONFIG"))
	if err != nil {
		slog.Error("storage setup", "error", err)
		os.Exit(1)
	}
	app := server.HTTPServer(env("LISTEN_ADDR", "127.0.0.1:5050"), server.New(client, server.Config{
		WebDir: env("WEB_DIR", "web/dist"), Username: user, Password: password, PublicRegistry: os.Getenv("PUBLIC_REGISTRY"),
		Auth: authManager, Storage: storageManager, CookieSecure: os.Getenv("COOKIE_SECURE") == "true",
	}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() {
		slog.Info("Magistry listening", "address", app.Addr, "authentication", user != "")
		serveErr <- app.ListenAndServe()
	}()
	select {
	case err := <-serveErr:
		if !errors.Is(err, http.ErrServerClosed) {
			slog.Error("serve", "error", err)
			os.Exit(1)
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := app.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown", "error", err)
		app.Close()
	}
}
