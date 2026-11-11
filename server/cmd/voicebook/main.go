// Command voicebook runs the AI audiobook production platform (API, workers and web apps).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"voicebook/internal/api"
	"voicebook/internal/bus"
	"voicebook/internal/config"
	"voicebook/internal/core"
	"voicebook/internal/db"
	"voicebook/internal/storage"
	"voicebook/internal/version"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	cmd := "serve"
	if len(os.Args) > 1 && os.Args[1][0] != '-' {
		cmd = os.Args[1]
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}
	flag.Parse()
	cfg := config.Load()
	switch cmd {
	case "serve":
		if err := serve(cfg); err != nil {
			slog.Error("server stopped", "err", err)
			os.Exit(1)
		}
	case "migrate":
		gdb, err := db.Open(cfg.DBDriver, cfg.DBDSN, false)
		must(err)
		must(db.Migrate(gdb))
		fmt.Println("migration complete")
	case "version":
		fmt.Println(version.String())
	case "health": // for container health checks: exit 0 when /healthz answers ok
		os.Exit(health(cfg.Addr))
	default:
		fmt.Fprintf(os.Stderr, "usage: voicebook [serve|migrate|version|health]\n")
		os.Exit(2)
	}
}

func health(addr string) int {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		port = strings.TrimPrefix(addr, ":")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
	if err != nil {
		fmt.Fprintln(os.Stderr, "unhealthy:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy: HTTP", resp.StatusCode)
		return 1
	}
	return 0
}

func must(err error) {
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func serve(cfg *config.Config) error {
	if !cfg.DevMode && cfg.DefaultSecrets {
		return errors.New("生产模式（VB_DEV=false）必须设置 VB_JWT_SECRET 和 VB_SECRET_KEY（各用一串足够长的随机字符，例如 openssl rand -hex 32）；VB_SECRET_KEY 用于加密已保存的 API 密钥，设置后不要再改")
	}
	if cfg.DBDriver == "postgres" && cfg.DBDSN == "" {
		return errors.New("VB_DB_DRIVER=postgres 时需要设置 VB_DB_DSN，例如 host=db user=voicebook password=... dbname=voicebook sslmode=disable")
	}
	gdb, err := db.Open(cfg.DBDriver, cfg.DBDSN, cfg.DevMode)
	if err != nil {
		return err
	}
	// instances starting together take turns (no-op on SQLite)
	if err := db.Exclusive(context.Background(), gdb, db.LockMigrate, func() error { return db.Migrate(gdb) }); err != nil {
		return err
	}
	local, err := storage.NewLocal(filepath.Join(cfg.DataDir, "storage"), cfg.SecretKey)
	if err != nil {
		return err
	}
	app := core.New(cfg, gdb, storage.NewStore(local))
	b, err := bus.Open(context.Background(), cfg.RedisURL)
	if err != nil {
		return err
	}
	defer b.Close()
	app.SetBus(b)
	if !app.FF.Available() {
		slog.Warn("ffmpeg not found: rendering, exports and uploads of compressed audio will fail", "ffmpeg", cfg.FFmpeg)
	}
	if err := db.Exclusive(context.Background(), gdb, db.LockSeed, app.Seed); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	app.Start(ctx)

	srv := &http.Server{Addr: cfg.Addr, Handler: api.New(app).Router(), ReadHeaderTimeout: 15 * time.Second}
	go func() {
		<-ctx.Done()
		shut, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		srv.Shutdown(shut)
	}()
	slog.Info("voicebook listening", "version", version.Version, "commit", version.Commit, "addr", cfg.Addr, "public", cfg.PublicURL, "db", cfg.DBDriver, "redis", cfg.RedisURL != "", "dev", cfg.DevMode)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	app.Q.Wait()
	return nil
}
