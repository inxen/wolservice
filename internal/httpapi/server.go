package httpapi

import (
	"embed"
	"encoding/json"
	"fmt"
	"html"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"wol/internal/core"
	"wol/internal/version"
)

//go:embed assets/*
var embeddedAssets embed.FS

type Server struct {
	log        *slog.Logger
	manager    *core.Manager
	assets     fs.FS
	index      []byte
	configPage []byte
	loginPage  []byte
	panelAuth  *configPanelAuth
	argonSem   chan struct{}
}

func New(log *slog.Logger, manager *core.Manager) (*Server, error) {
	assets, err := fs.Sub(embeddedAssets, "assets")
	if err != nil {
		return nil, err
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, err
	}
	configPage, err := fs.ReadFile(assets, "config.html")
	if err != nil {
		return nil, err
	}
	loginPage, err := fs.ReadFile(assets, "login.html")
	if err != nil {
		return nil, err
	}
	return &Server{
		log:        log,
		manager:    manager,
		assets:     assets,
		index:      index,
		configPage: configPage,
		loginPage:  loginPage,
		panelAuth:  newConfigPanelAuth(),
		argonSem:   make(chan struct{}, 2),
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	mux.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	mux.HandleFunc("GET /api/devices", s.auth(s.handleDevices))
	mux.HandleFunc("GET /api/devices/{key}", s.auth(s.handleDevice))
	mux.HandleFunc("POST /api/devices/{key}/wake", s.auth(s.handleWake))
	mux.HandleFunc("POST /api/devices/{key}/shutdown", s.auth(s.handleShutdown))
	mux.HandleFunc("POST /api/devices/{key}/probe", s.auth(s.handleProbe))
	mux.HandleFunc("GET /api/devices/{key}/ssh/hostkey", s.auth(s.handleHostKey))
	mux.HandleFunc("POST /api/reload", s.auth(s.handleReload))
	mux.HandleFunc("GET /api/config/status", s.handleConfigStatus)
	mux.HandleFunc("POST /api/config/login", s.handleConfigLogin)
	mux.HandleFunc("POST /api/config/logout", s.handleConfigLogout)
	mux.HandleFunc("GET /api/config", s.configAuth(s.handleConfigGet))
	mux.HandleFunc("PUT /api/config", s.configAuth(s.handleConfigSave))
	mux.HandleFunc("POST /api/config/ssh/hostkey", s.configAuth(s.handleConfigHostKey))
	mux.HandleFunc("GET /api/", s.handleAPINotFound)
	mux.HandleFunc("GET /assets/{name}", s.handleAsset)
	mux.HandleFunc("GET /favicon.svg", s.handleFavicon)
	mux.HandleFunc("GET /manifest.webmanifest", s.handleManifest)
	mux.HandleFunc("GET /sw.js", s.handleServiceWorker)
	mux.HandleFunc("GET /config", s.handleConfigPage)
	mux.HandleFunc("GET /", s.handleIndex)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Robots-Tag", "noindex, nofollow")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; script-src 'self'; worker-src 'self'; manifest-src 'self'; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		recorder := &statusRecorder{ResponseWriter: w, status: 0}
		defer func() {
			if recovered := recover(); recovered != nil {
				s.log.Error("http panic", "panic", recovered, "stack", string(debug.Stack()))
				if recorder.status == 0 {
					writeError(recorder, http.StatusInternalServerError, "internal server error")
				}
			}
			status := recorder.status
			if status == 0 {
				status = http.StatusOK
			}
			s.log.Debug("http request", "method", r.Method, "path", r.URL.Path, "status", status, "duration", time.Since(start))
		}()
		if !s.networkAllowed(r) {
			writeError(recorder, http.StatusForbidden, "source address is not in trusted networks")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !csrfOK(r) {
			writeError(recorder, http.StatusForbidden, "missing X-WOL-Config header")
			return
		}
		next.ServeHTTP(recorder, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if flusher, ok := r.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return s.apiAuth(next)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"version": version.Version,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	cfg := s.manager.Config()
	store := s.manager.Secrets()
	writeJSON(w, http.StatusOK, map[string]any{
		"title":                cfg.Server.Title,
		"version":              version.Version,
		"uptime_seconds":       int(time.Since(s.manager.StartedAt()).Seconds()),
		"auth_required":        s.apiAuthRequired(),
		"auth":                 s.authInfo(),
		"config_panel_enabled": cfg.ConfigPanel.Enabled,
		"key_source":           store.KeySource(),
		"legacy_key_file":      store.LegacyKeyFileExists(),
	})
}

func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	devices := s.manager.List()
	writeJSON(w, http.StatusOK, map[string]any{
		"devices": devices,
		"summary": summarize(devices),
		"server": map[string]any{
			"title":                s.manager.Config().Server.Title,
			"version":              version.Version,
			"config_panel_enabled": s.manager.Config().ConfigPanel.Enabled,
		},
	})
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	status, ok := s.manager.Status(r.PathValue("key"))
	if !ok {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": status})
}

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	result, err := s.manager.Wake(key)
	if err != nil {
		s.log.Warn("wake failed", "device", key, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	result, err := s.manager.Shutdown(key)
	if err != nil {
		s.log.Warn("shutdown failed", "device", key, "error", err)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	status, err := s.manager.Probe(r.PathValue("key"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": status})
}

func (s *Server) handleHostKey(w http.ResponseWriter, r *http.Request) {
	info, err := s.manager.ProbeHostKey(r.PathValue("key"))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleReload(w http.ResponseWriter, r *http.Request) {
	if err := s.manager.Reload(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reloaded": true})
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/quickcontrol" {
		http.NotFound(w, r)
		return
	}
	if s.quickAuthRequired() && !s.authenticated(r) {
		s.serveLoginPage(w, r)
		return
	}
	title := html.EscapeString(s.manager.Config().Server.Title)
	page := strings.ReplaceAll(string(s.index), "{{TITLE}}", title)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(page)))
	_, _ = w.Write([]byte(page))
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.Contains(name, "..") || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(s.assets, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(name, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(name, ".js"):
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	case strings.HasSuffix(name, ".png"):
		w.Header().Set("Content-Type", "image/png")
	case strings.HasSuffix(name, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (s *Server) handleFavicon(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(s.assets, "favicon.svg")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write(data)
}

// handleManifest 生成 PWA manifest，名称跟随配置里的 server.title。
func (s *Server) handleManifest(w http.ResponseWriter, r *http.Request) {
	title := s.manager.Config().Server.Title
	short := []rune(title)
	if len(short) > 12 {
		short = short[:12]
	}
	manifest := map[string]any{
		"id":               "/",
		"name":             title,
		"short_name":       string(short),
		"description":      "局域网设备网络唤醒与关机",
		"lang":             "zh-CN",
		"dir":              "ltr",
		"start_url":        "/",
		"scope":            "/",
		"display":          "standalone",
		"background_color": "#f3f6fb",
		"theme_color":      "#f3f6fb",
		"icons": []map[string]string{
			{"src": "/assets/icon-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any"},
			{"src": "/assets/icon-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any"},
			{"src": "/assets/icon-maskable-192.png", "sizes": "192x192", "type": "image/png", "purpose": "maskable"},
			{"src": "/assets/icon-maskable-512.png", "sizes": "512x512", "type": "image/png", "purpose": "maskable"},
		},
	}
	w.Header().Set("Content-Type", "application/manifest+json")
	w.Header().Set("Cache-Control", "no-cache")
	_ = json.NewEncoder(w).Encode(manifest)
}

// handleServiceWorker 把版本号注入缓存名，升级后旧缓存自动失效。
func (s *Server) handleServiceWorker(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(s.assets, "sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	quoted, _ := json.Marshal(version.Version)
	body := strings.ReplaceAll(string(data), "{{VERSION}}", string(quoted))
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write([]byte(body))
}

func (s *Server) handleAPINotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "api not found")
}

type Summary struct {
	Total     int `json:"total"`
	Online    int `json:"online"`
	Offline   int `json:"offline"`
	Attention int `json:"attention"`
}

func summarize(devices []core.Status) Summary {
	var summary Summary
	for _, d := range devices {
		summary.Total++
		switch deviceTone(d) {
		case "online", "partial":
			summary.Online++
		case "offline":
			summary.Offline++
		default:
			summary.Attention++
		}
	}
	return summary
}

func deviceTone(d core.Status) string {
	switch d.ReachabilityState {
	case "Reachable":
		return "online"
	case "PartiallyReachable":
		return "partial"
	case "Unreachable":
		return "offline"
	}
	return "unknown"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
