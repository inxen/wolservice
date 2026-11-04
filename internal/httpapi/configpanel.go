package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"wol/internal/config"
	"wol/internal/version"
)

const (
	configCookieName       = "wol_config_session"
	configSessionTTL       = 12 * time.Hour
	configMaxLoginFailures = 8
	configLoginWindow      = 5 * time.Minute
	configLoginBlock       = 5 * time.Minute
	configMaxSessions      = 128
)

type configPanelAuth struct {
	mu       sync.Mutex
	sessions map[string]configSession
	attempts map[string]*loginAttempt
}

type configSession struct {
	expires  time.Time
	passHash [32]byte
}

type loginAttempt struct {
	failures     int
	windowStart  time.Time
	blockedUntil time.Time
}

func newConfigPanelAuth() *configPanelAuth {
	return &configPanelAuth{
		sessions: make(map[string]configSession),
		attempts: make(map[string]*loginAttempt),
	}
}

func hashPassword(passwordHash string) [32]byte {
	return sha256.Sum256([]byte(passwordHash))
}

func (a *configPanelAuth) authenticated(r *http.Request, passwordHash string) bool {
	if passwordHash == "" {
		return true
	}
	cookie, err := r.Cookie(configCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	expected := hashPassword(passwordHash)
	a.mu.Lock()
	defer a.mu.Unlock()
	session, ok := a.sessions[cookie.Value]
	if !ok || time.Now().After(session.expires) {
		delete(a.sessions, cookie.Value)
		return false
	}
	if subtle.ConstantTimeCompare(session.passHash[:], expected[:]) != 1 {
		delete(a.sessions, cookie.Value)
		return false
	}
	session.expires = time.Now().Add(configSessionTTL)
	a.sessions[cookie.Value] = session
	return true
}

func (a *configPanelAuth) newSession(passwordHash string) (string, bool) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", false
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cleanupLocked()
	if len(a.sessions) >= configMaxSessions {
		return "", false
	}
	a.sessions[token] = configSession{
		expires:  time.Now().Add(configSessionTTL),
		passHash: hashPassword(passwordHash),
	}
	return token, true
}

func (a *configPanelAuth) deleteSession(r *http.Request) {
	cookie, err := r.Cookie(configCookieName)
	if err != nil {
		return
	}
	a.mu.Lock()
	delete(a.sessions, cookie.Value)
	a.mu.Unlock()
}

func (a *configPanelAuth) cleanupLocked() {
	now := time.Now()
	for token, session := range a.sessions {
		if now.After(session.expires) {
			delete(a.sessions, token)
		}
	}
	if len(a.attempts) > 1024 {
		for ip, attempt := range a.attempts {
			if now.After(attempt.blockedUntil) && now.Sub(attempt.windowStart) > configLoginWindow {
				delete(a.attempts, ip)
			}
		}
	}
}

func (a *configPanelAuth) loginAllowed(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	attempt, ok := a.attempts[ip]
	if !ok {
		return true
	}
	if now.Before(attempt.blockedUntil) {
		return false
	}
	if now.Sub(attempt.windowStart) > configLoginWindow {
		delete(a.attempts, ip)
		return true
	}
	return attempt.failures < configMaxLoginFailures
}

func (a *configPanelAuth) loginFailed(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	attempt, ok := a.attempts[ip]
	if !ok || now.Sub(attempt.windowStart) > configLoginWindow {
		attempt = &loginAttempt{windowStart: now}
		a.attempts[ip] = attempt
	}
	attempt.failures++
	if attempt.failures >= configMaxLoginFailures {
		attempt.blockedUntil = now.Add(configLoginBlock)
		return true
	}
	return false
}

func (a *configPanelAuth) loginSucceeded(ip string) {
	a.mu.Lock()
	delete(a.attempts, ip)
	a.mu.Unlock()
}

func (s *Server) configPanelEnabled() bool {
	return s.manager.Config().ConfigPanel.Enabled
}

func (s *Server) handleConfigStatus(w http.ResponseWriter, r *http.Request) {
	if !s.configPanelEnabled() {
		writeError(w, http.StatusNotFound, "config panel disabled")
		return
	}
	hash := s.manager.Secrets().PanelPasswordHash()
	authRequired := s.configAuthRequired()
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":       true,
		"password_set":  hash != "",
		"auth_required": authRequired,
		"authenticated": !authRequired || s.panelAuth.authenticated(r, hash),
	})
}

func (s *Server) handleConfigLogin(w http.ResponseWriter, r *http.Request) {
	if !s.configPanelEnabled() {
		writeError(w, http.StatusNotFound, "config panel disabled")
		return
	}
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, "missing X-WOL-Config header")
		return
	}
	store := s.manager.Secrets()
	if !store.PanelPasswordSet() {
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

func (s *Server) handleConfigLogout(w http.ResponseWriter, r *http.Request) {
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

func (s *Server) configAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.configPanelEnabled() {
			writeError(w, http.StatusNotFound, "config panel disabled")
			return
		}
		if s.configAuthRequired() && !s.authenticated(r) {
			writeError(w, http.StatusUnauthorized, "config panel authentication required")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleConfigGet(w http.ResponseWriter, r *http.Request) {
	cfg := s.manager.Config()
	sanitized := cfg.WithoutSecrets()
	writeJSON(w, http.StatusOK, map[string]any{
		"config":  &sanitized,
		"meta":    s.configMeta(cfg, r),
		"secrets": s.manager.Secrets().Status(cfg),
	})
}

func (s *Server) handleConfigSave(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, "missing X-WOL-Config header")
		return
	}
	body := http.MaxBytesReader(w, r.Body, 2<<20)
	defer body.Close()
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	var cfg config.Config
	if err := decoder.Decode(&cfg); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("配置解析失败: %v", err))
		return
	}
	cfg.Normalize()
	if cfg.Auth != nil && cfg.Auth.TrustedNetworksEnabled && !s.sourceAllowed(cfg.Auth.TrustedNetworks, clientIP(r)) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"当前访问 IP %s 不在新的可信网段中，保存后会立即失去访问；请先把它加入列表", clientIP(r)))
		return
	}
	result, err := s.manager.SaveConfig(&cfg)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	loaded := s.manager.Config()
	sanitized := loaded.WithoutSecrets()
	writeJSON(w, http.StatusOK, map[string]any{
		"saved":            true,
		"backup":           result.Backup,
		"secrets_backup":   result.SecretsBackup,
		"config_path":      result.ConfigPath,
		"secrets_path":     result.SecretsPath,
		"restart_required": result.RestartRequired,
		"restart_fields":   result.RestartFields,
		"config":           &sanitized,
		"meta":             s.configMeta(loaded, r),
		"secrets":          s.manager.Secrets().Status(loaded),
		"reauth_required":  result.ReauthRequired,
	})
}

func (s *Server) configMeta(cfg *config.Config, r *http.Request) map[string]any {
	store := s.manager.Secrets()
	passwordSet := store != nil && store.PanelPasswordSet()
	return map[string]any{
		"version":              version.Version,
		"config_path":          s.manager.ConfigPath(),
		"secrets_path":         s.manager.SecretsPath(),
		"secrets_key_path":     s.manager.SecretsKeyPath(),
		"config_panel_enabled": cfg.ConfigPanel.Enabled,
		"password_set":         passwordSet,
		"client_ip":            clientIP(r),
	}
}

func (s *Server) handleConfigHostKey(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		writeError(w, http.StatusForbidden, "missing X-WOL-Config header")
		return
	}
	var sshCfg config.SSHConfig
	body := http.MaxBytesReader(w, r.Body, 256<<10)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&sshCfg); err != nil {
		writeError(w, http.StatusBadRequest, "请求解析失败")
		return
	}
	sshCfg.Port = cfgPortOr(sshCfg.Port, 22)
	info, err := s.manager.ProbeSSHHostKey(sshCfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleConfigPage(w http.ResponseWriter, r *http.Request) {
	if !s.configPanelEnabled() {
		http.NotFound(w, r)
		return
	}
	title := s.manager.Config().Server.Title
	page := strings.ReplaceAll(string(s.configPage), "{{TITLE}}", html.EscapeString(title))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(page))
}

func csrfOK(r *http.Request) bool {
	return r.Header.Get("X-WOL-Config") == "1"
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func cfgPortOr(port, def int) int {
	if port <= 0 || port > 65535 {
		return def
	}
	return port
}
