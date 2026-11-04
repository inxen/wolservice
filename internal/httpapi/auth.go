package httpapi

import (
	"encoding/json"
	"html"
	"net/http"
	"strings"
	"time"

	"wol/internal/config"
)

func (s *Server) authConfig() config.AuthConfig {
	cfg := s.manager.Config()
	if cfg != nil && cfg.Auth != nil {
		return *cfg.Auth
	}
	return config.AuthConfig{ConfigPanel: true}
}

func (s *Server) accessPasswordHash() string {
	store := s.manager.Secrets()
	if store == nil {
		return ""
	}
	return store.PanelPasswordHash()
}

func (s *Server) scopeRequired(scope string) bool {
	if s.accessPasswordHash() == "" {
		return false
	}
	auth := s.authConfig()
	switch scope {
	case "quick":
		return auth.QuickPanel
	case "config":
		return auth.ConfigPanel
	}
	return false
}

func (s *Server) quickAuthRequired() bool { return s.scopeRequired("quick") }

// The quick panel and its APIs share one scope: enabling the panel password
// protects /api/devices and /api/reload as well.
func (s *Server) apiAuthRequired() bool { return s.scopeRequired("quick") }

func (s *Server) configAuthRequired() bool {
	return s.configPanelEnabled() && s.scopeRequired("config")
}

func (s *Server) authenticated(r *http.Request) bool {
	return s.panelAuth.authenticated(r, s.accessPasswordHash())
}

func (s *Server) trustedNetworksEnabled() bool {
	return s.authConfig().TrustedNetworksEnabled
}

func (s *Server) networkAllowed(r *http.Request) bool {
	if !s.trustedNetworksEnabled() {
		return true
	}
	if strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/favicon.svg" {
		return true
	}
	ip := clientIP(r)
	if ip == "" {
		return false
	}
	for _, network := range s.authConfig().TrustedNetworks {
		if config.NetworkContains(network, ip) {
			return true
		}
	}
	return false
}

func (s *Server) sourceAllowed(networks []string, ip string) bool {
	if len(networks) == 0 {
		return false
	}
	for _, network := range networks {
		if config.NetworkContains(network, ip) {
			return true
		}
	}
	return false
}

func (s *Server) authInfo() map[string]any {
	auth := s.authConfig()
	hash := s.accessPasswordHash()
	return map[string]any{
		"quick_panel":              auth.QuickPanel,
		"config_panel":             auth.ConfigPanel,
		"trusted_networks_enabled": auth.TrustedNetworksEnabled,
		"trusted_networks_count":   len(auth.TrustedNetworks),
		"password_set":             hash != "",
		"quick_required":           auth.QuickPanel && hash != "",
		"config_required":          auth.ConfigPanel && hash != "",
	}
}

func requestPassword(r *http.Request) string {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Access-Token"))
}

func (s *Server) verifyAccessPassword(password string) bool {
	select {
	case s.argonSem <- struct{}{}:
		defer func() { <-s.argonSem }()
	case <-time.After(3 * time.Second):
		return false
	}
	store := s.manager.Secrets()
	return store != nil && store.VerifyPanelPassword(password)
}

func (s *Server) apiAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.apiAuthRequired() {
			next(w, r)
			return
		}
		if s.authenticated(r) {
			next(w, r)
			return
		}
		// Direct API clients may send the access password as a bearer token.
		// Interactive panels use the session cookie created by /api/auth/login.
		password := requestPassword(r)
		if password == "" {
			writeError(w, http.StatusUnauthorized, "access password required")
			return
		}
		ip := clientIP(r)
		if !s.panelAuth.loginAllowed(ip) {
			writeError(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
			return
		}
		if s.verifyAccessPassword(password) {
			s.panelAuth.loginSucceeded(ip)
			next(w, r)
			return
		}
		if s.panelAuth.loginFailed(ip) {
			writeError(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
			return
		}
		writeError(w, http.StatusUnauthorized, "access password required")
	}
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"password_set":             s.accessPasswordHash() != "",
		"authenticated":            s.authenticated(r),
		"quick_required":           s.quickAuthRequired(),
		"config_required":          s.configAuthRequired(),
		"trusted_networks_enabled": s.trustedNetworksEnabled(),
	})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, "missing X-WOL-Config header")
		return
	}
	store := s.manager.Secrets()
	if store == nil || !store.PanelPasswordSet() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	ip := clientIP(r)
	if !s.panelAuth.loginAllowed(ip) {
		writeError(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
		return
	}
	var payload struct {
		Password string `json:"password"`
	}
	body := http.MaxBytesReader(w, r.Body, 4096)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "请求解析失败")
		return
	}
	if !s.verifyAccessPassword(payload.Password) {
		if s.panelAuth.loginFailed(ip) {
			writeError(w, http.StatusTooManyRequests, "登录失败次数过多，请稍后再试")
			return
		}
		writeError(w, http.StatusUnauthorized, "访问密码错误")
		return
	}
	s.panelAuth.loginSucceeded(ip)
	token, ok := s.panelAuth.newSession(store.PanelPasswordHash())
	if !ok {
		writeError(w, http.StatusInternalServerError, "创建会话失败")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     configCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(configSessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, "missing X-WOL-Config header")
		return
	}
	s.panelAuth.deleteSession(r)
	http.SetCookie(w, &http.Cookie{
		Name:     configCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
		MaxAge:   -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) serveLoginPage(w http.ResponseWriter, r *http.Request) {
	title := ""
	if cfg := s.manager.Config(); cfg != nil {
		title = cfg.Server.Title
	}
	page := strings.ReplaceAll(string(s.loginPage), "{{TITLE}}", html.EscapeString(title))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}
