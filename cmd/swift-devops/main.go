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
	"syscall"
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
	case "hash-pwd":
		runHashPwd(args)
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
	cfgPath := "/etc/swift-devops/config.yaml"
	for i := 0; i < len(args); i++ {
		if args[i] == "--config" && i+1 < len(args) {
			cfgPath = args[i+1]
			i++
		}
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		os.Exit(1)
	}
	setupLogger(cfg)
	slog.Info("starting swift-devops", "version", Version, "config", cfgPath)

	db, err := store.Open(cfg.Database.DSN)
	if err != nil {
		slog.Error("open db failed", "err", err)
		os.Exit(1)
	}
	if err := store.AutoMigrate(db); err != nil {
		slog.Error("migrate failed", "err", err)
		os.Exit(1)
	}

	aes, err := crypto.NewAESGCM(cfg.Security.MasterKey)
	if err != nil {
		slog.Error("master_key invalid", "err", err)
		os.Exit(1)
	}

	dist, err := fs.Sub(web.FS, "dist")
	if err != nil {
		slog.Error("embed dist failed", "err", err)
		os.Exit(1)
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

	go func() {
		slog.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server died", "err", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	slog.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	slog.Info("bye")
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
