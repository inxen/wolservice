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
	"strings"
	"time"

	"wol/internal/config"
	"wol/internal/core"
	"wol/internal/httpapi"
	"wol/internal/secrets"
	"wol/internal/version"
)

func main() {
	var configPath string
	var secretsPath string
	var secretsKeyPath string
	var showVersion bool
	var checkOnly bool
	flag.StringVar(&configPath, "config", "wol.yaml", "path to the YAML configuration file")
	flag.StringVar(&secretsPath, "secrets", "", "path to the encrypted secrets file (default: <config>.secrets.yaml)")
	flag.StringVar(&secretsKeyPath, "secrets-key", "", "legacy secrets key file (ignored when an embedded key or WOL_SECRETS_KEY is set; default: <secrets>.key)")
	flag.BoolVar(&showVersion, "version", false, "print version and exit")
	flag.BoolVar(&checkOnly, "check", false, "validate the configuration and exit")
	flag.Parse()

	if showVersion {
		fmt.Println(version.Version)
		return
	}
	if secretsPath == "" {
		secretsPath = secrets.DefaultPath(configPath)
	}
	if secretsKeyPath == "" {
		secretsKeyPath = secrets.DefaultKeyPath(secretsPath)
	}

	cfg, store, created, migrated, err := core.LoadConfig(configPath, secretsPath, secretsKeyPath, !checkOnly)
	if err != nil {
		fmt.Fprintf(os.Stderr, "configuration error: %v\n", err)
		os.Exit(1)
	}
	if created {
		fmt.Fprintf(os.Stderr, "configuration not found, generated default config: %s\n", configPath)
	}
	if migrated {
		fmt.Fprintf(os.Stderr, "credentials moved/encrypted in secrets file: %s\n", secretsPath)
	}
	if checkOnly {
		fmt.Printf("configuration OK: %s (%d devices)\n", configPath, len(cfg.Devices))
		return
	}

	logger := buildLogger(cfg.Log)
	if created {
		logger.Info("default configuration generated", "path", configPath)
	}
	if migrated {
		logger.Info("credentials migrated/encrypted", "path", secretsPath, "key", secretsKeyPath)
	}
	auth := cfg.Auth
	if auth == nil {
		auth = &config.AuthConfig{}
	}
	if !store.PanelPasswordSet() {
		if auth.QuickPanel || (cfg.ConfigPanel.Enabled && auth.ConfigPanel) {
			logger.Warn("access password scopes are enabled but no access password is set; protected surfaces are open")
		}
	} else if cfg.ConfigPanel.Enabled && auth.ConfigPanel && !auth.QuickPanel {
		logger.Info("access password only protects the config panel; quick panel and API are open")
	}
	if store.KeySource() == "file" {
		logger.Warn("secrets master key is stored on disk next to the secrets file; build with an embedded key to avoid this")
	}
	logger.Info("starting WOL service", "version", version.Version, "config", configPath, "secrets", secretsPath, "secrets_key", secretsKeyPath, "key_source", store.KeySource(), "devices", len(cfg.Devices))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	manager := core.NewManagerWithSecrets(logger, configPath, secretsPath, secretsKeyPath, cfg, store)
	manager.Start(ctx)
	defer manager.Stop()

	apiServer, err := httpapi.New(logger, manager)
	if err != nil {
		logger.Error("initialize http server", "error", err)
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           apiServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.Server.ReadTimeout.Duration,
		IdleTimeout:       120 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("http server listening", "address", cfg.Server.Listen, "tls", cfg.Server.TLSCert != "")
		var err error
		if cfg.Server.TLSCert != "" {
			cert := config.ResolvePath(configPath, cfg.Server.TLSCert)
			key := config.ResolvePath(configPath, cfg.Server.TLSKey)
			err = server.ListenAndServeTLS(cert, key)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	watchStop := make(chan struct{})
	go watchConfig(ctx, logger, manager, []string{configPath, secretsPath}, watchStop)

	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, shutdownSignals()...)
	defer signal.Stop(sigCh)

	panelURL := panelAddress(cfg.Server.Listen, cfg.Server.TLSCert != "")
	logger.Info("quick control panel ready", "url", panelURL)

	exitCode := 0
run:
	for {
		select {
		case sig := <-sigCh:
			if reloadSignal(sig) {
				logger.Info("reload signal received")
				if err := manager.Reload(); err != nil {
					logger.Error("reload configuration", "error", err)
				}
				continue
			}
			logger.Info("shutdown signal received", "signal", sig.String())
			break run
		case err := <-serveErr:
			logger.Error("http server stopped", "error", err)
			exitCode = 1
			break run
		}
	}

	close(watchStop)
	shutdownTimeout := cfg.Server.ShutdownTimeout.Duration
	if shutdownTimeout <= 0 {
		shutdownTimeout = 15 * time.Second
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http shutdown", "error", err)
	}
	cancel()
	manager.Stop()
	logger.Info("stopped")
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

func buildLogger(cfg config.LogConfig) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(cfg.Format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

func watchConfig(ctx context.Context, logger *slog.Logger, manager *core.Manager, paths []string, stop <-chan struct{}) {
	type stamp struct {
		exists bool
		mod    time.Time
		size   int64
	}
	last := make(map[string]stamp, len(paths))
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil {
			last[path] = stamp{exists: true, mod: info.ModTime(), size: info.Size()}
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-ticker.C:
			changed := ""
			for _, path := range paths {
				info, err := os.Stat(path)
				current := stamp{}
				if err == nil {
					current = stamp{exists: true, mod: info.ModTime(), size: info.Size()}
				}
				if current != last[path] {
					last[path] = current
					if changed == "" {
						changed = path
					}
				}
			}
			if changed == "" {
				continue
			}
			time.Sleep(350 * time.Millisecond)
			logger.Info("configuration change detected, reloading", "file", changed)
			if err := manager.Reload(); err != nil {
				logger.Error("reload configuration", "error", err)
				continue
			}
		}
	}
}

func panelAddress(listen string, tls bool) string {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	host := listen
	if strings.HasPrefix(host, ":") {
		host = "127.0.0.1" + host
	} else if strings.HasPrefix(host, "0.0.0.0:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "0.0.0.0:")
	} else if strings.HasPrefix(host, "[::]:") {
		host = "127.0.0.1:" + strings.TrimPrefix(host, "[::]:")
	}
	return fmt.Sprintf("%s://%s/", scheme, host)
}
