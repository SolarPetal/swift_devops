package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"swift-devops/internal/api"
	"swift-devops/internal/config"
	"swift-devops/internal/pkg/crypto"
	"swift-devops/internal/store"
	"swift-devops/web"
)

// Version is injected at build time via -ldflags "-X main.Version=..."
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		runServe(args)
	case "service-run":
		runService(args)
	case "hash-pwd":
		runHashPwd(args)
	case "init-config":
		runInitConfig(args)
	case "cleanup-apps":
		runCleanupApps(args)
	case "version", "-v", "--version":
		fmt.Println(Version)
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `swift-devops - Java Service Automation Platform

Usage:
  swift-devops serve --config <path>      Run the HTTP server
  swift-devops service-run --config <p>   Run as Windows Service
  swift-devops init-config --config <p>   Generate an initial config.yaml
  swift-devops cleanup-apps --config <p>  Wipe app data (apps/artifacts/deployments/runs)
                                          and drop legacy unique indexes (Sprint X.1)
  swift-devops hash-pwd <password>        Output bcrypt hash for a password
  swift-devops version                    Show version`)
}

func runHashPwd(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: swift-devops hash-pwd <password>")
		os.Exit(2)
	}
	h, err := crypto.HashPassword(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(h)
}

func runServe(args []string) {
	cfgPath := parseConfigPath(args)
	stop := make(chan struct{})
	go func() {
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, shutdownSignals()...)
		<-signals
		close(stop)
	}()
	if err := serve(cfgPath, stop); err != nil {
		fmt.Fprintf(os.Stderr, "serve failed: %v\n", err)
		os.Exit(1)
	}
}

func parseConfigPath(args []string) string {
	cfgPath := "/etc/swift-devops/config.yaml"
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
		}
	}
	return cfgPath
}

func serve(cfgPath string, stop <-chan struct{}) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	setupLogger(cfg)
	slog.Info("starting swift-devops", "version", Version, "config", cfgPath)

	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	if err := store.AutoMigrate(db); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	aes, err := crypto.NewAESGCM(cfg.Security.MasterKey)
	if err != nil {
		return fmt.Errorf("master_key invalid: %w", err)
	}

	dist, err := fs.Sub(web.FS, "dist")
	if err != nil {
		return fmt.Errorf("embed dist: %w", err)
	}

	gin.SetMode(cfg.Server.Mode)
	r, cleanup := api.NewRouter(cfg, db, aes, dist)
	defer cleanup()

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-stop:
		slog.Info("shutting down...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-errCh
		slog.Info("bye")
		return nil
	case err := <-errCh:
		if err != nil {
			slog.Error("server died", "err", err)
			return err
		}
		return nil
	}
}

func setupLogger(cfg *config.Config) {
	var level slog.Level
	switch strings.ToLower(cfg.Log.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	h := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	slog.SetDefault(slog.New(h))
}
