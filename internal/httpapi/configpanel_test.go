package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wol/internal/config"
	"wol/internal/core"
)

func newConfigPanelAPI(t *testing.T, content string) (*httptest.Server, *core.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	manager := core.NewManager(testLogger(), path, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	manager.Start(ctx)
	t.Cleanup(func() {
		cancel()
		manager.Stop()
	})
	api, err := New(testLogger(), manager)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	server := httptest.NewServer(api.Handler())
	t.Cleanup(server.Close)
	return server, manager, path
}

func configClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return &http.Client{Jar: jar}
}

func configDo(t *testing.T, client *http.Client, method, url string, payload any, csrf bool) (*http.Response, []byte) {
	t.Helper()
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		body = bytes.NewReader(raw)
	}
	request, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf {
		request.Header.Set("X-WOL-Config", "1")
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	return response, data
}

func getConfigPayload(t *testing.T, client *http.Client, baseURL string) (config.Config, []byte) {
	t.Helper()
	response, body := configDo(t, client, http.MethodGet, baseURL+"/api/config", nil, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/config status = %d body=%s", response.StatusCode, body)
	}
	var payload struct {
		Config config.Config `json:"config"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	return payload.Config, body
}

func TestConfigPanelDisabled(t *testing.T) {
	server, _, _ := newConfigPanelAPI(t, `
server:
  listen: ":0"
config_panel:
  enabled: false
`)
	client := configClient(t)
	for _, path := range []string{"/config", "/api/config/status", "/api/config"} {
		response, body := configDo(t, client, http.MethodGet, server.URL+path, nil, false)
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status = %d body=%s", path, response.StatusCode, body)
		}
	}
	response, _ := configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": ""}, true)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("login status = %d", response.StatusCode)
	}
}

func TestConfigPanelOpenSaveAndDisable(t *testing.T) {
	server, _, path := newConfigPanelAPI(t, `
server:
  listen: ":0"
  title: "Panel Test"
config_panel:
  enabled: true
  password: ""
devices: []
`)
	client := configClient(t)

	response, body := configDo(t, client, http.MethodGet, server.URL+"/api/config/status", nil, false)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"authenticated":true`) {
		t.Fatalf("status = %d body=%s", response.StatusCode, body)
	}
	response, body = configDo(t, client, http.MethodGet, server.URL+"/config", nil, false)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "配置中心") {
		t.Fatalf("config page = %d len=%d", response.StatusCode, len(body))
	}

	cfg, _ := getConfigPayload(t, client, server.URL)
	cfg.Server.Title = "Panel Test Updated"
	cfg.Devices = []config.Device{{
		Key:  "pc",
		Name: "Office PC",
		MACs: []string{"AA:BB:CC:DD:EE:01"},
	}}
	response, body = configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"saved":true`) {
		t.Fatalf("save = %d body=%s", response.StatusCode, body)
	}
	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(saved), "Panel Test Updated") {
		t.Fatalf("saved file missing title:\n%s", saved)
	}
	updated, _ := getConfigPayload(t, client, server.URL)
	if updated.Server.Title != "Panel Test Updated" || len(updated.Devices) != 1 {
		t.Fatalf("updated config = %+v", updated)
	}

	updated.ConfigPanel.Enabled = false
	response, body = configDo(t, client, http.MethodPut, server.URL+"/api/config", updated, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("disable save = %d body=%s", response.StatusCode, body)
	}
	response, _ = configDo(t, client, http.MethodGet, server.URL+"/config", nil, false)
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled page status = %d", response.StatusCode)
	}
}

func TestConfigPanelPasswordAuth(t *testing.T) {
	server, _, _ := newConfigPanelAPI(t, `
server:
  listen: ":0"
  title: "Protected"
config_panel:
  enabled: true
  password: "secret"
devices: []
`)
	client := configClient(t)

	response, body := configDo(t, client, http.MethodGet, server.URL+"/api/config/status", nil, false)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"authenticated":false`) {
		t.Fatalf("status = %d body=%s", response.StatusCode, body)
	}
	response, _ = configDo(t, client, http.MethodGet, server.URL+"/api/config", nil, false)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized config status = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "secret"}, false)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("login without csrf = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "wrong"}, true)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong login = %d", response.StatusCode)
	}
	response, body = configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "secret"}, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login = %d body=%s", response.StatusCode, body)
	}
	cookie := response.Header.Get("Set-Cookie")
	if !strings.Contains(cookie, "HttpOnly") || !strings.Contains(cookie, "SameSite=Strict") {
		t.Fatalf("cookie = %q", cookie)
	}

	cfg, _ := getConfigPayload(t, client, server.URL)
	response, _ = configDo(t, client, http.MethodGet, server.URL+"/api/config/status", nil, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, false)
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("save without csrf = %d", response.StatusCode)
	}

	cfg.Server.Title = "Protected Updated"
	response, body = configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save = %d body=%s", response.StatusCode, body)
	}

	cfg.ConfigPanel.Password = "next-secret"
	response, body = configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"reauth_required":true`) {
		t.Fatalf("password change = %d body=%s", response.StatusCode, body)
	}
	response, _ = configDo(t, client, http.MethodGet, server.URL+"/api/config", nil, false)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old session status = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "secret"}, true)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old password login = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "next-secret"}, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("new password login = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodGet, server.URL+"/api/config", nil, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("new session config = %d", response.StatusCode)
	}

	response, _ = configDo(t, client, http.MethodPost, server.URL+"/api/config/logout", nil, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("logout = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodGet, server.URL+"/api/config", nil, false)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after logout = %d", response.StatusCode)
	}
}

func TestConfigPanelSetAndClearPassword(t *testing.T) {
	server, manager, path := newConfigPanelAPI(t, `
server:
  listen: ":0"
config_panel:
  enabled: true
  password: ""
devices:
  - key: "demo"
    macs: ["AA:BB:CC:DD:EE:FF"]
    ssh:
      enabled: true
      host: "192.168.1.10"
      user: "root"
      password: "ssh-secret-password"
      host_key: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
      shutdown_command: "poweroff"
`)
	client := configClient(t)
	cfg, _ := getConfigPayload(t, client, server.URL)
	cfg.ConfigPanel.Password = "new-secret"
	response, body := configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"reauth_required":true`) {
		t.Fatalf("set password = %d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(mustRead(t, path)), "new-secret") {
		t.Fatal("password leaked into config file")
	}
	if strings.Contains(string(mustRead(t, path)), "ssh-secret-password") {
		t.Fatal("ssh password leaked into config file")
	}
	if strings.Contains(string(mustRead(t, path+".bak")), "ssh-secret-password") {
		t.Fatal("ssh password leaked into config backup")
	}
	secretsData := string(mustRead(t, manager.SecretsPath()))
	if strings.Contains(secretsData, "password_hash:") || strings.Contains(secretsData, "new-secret") ||
		strings.Contains(secretsData, "ssh-secret-password") {
		t.Fatalf("secrets file is not encrypted or leaked a value:\n%s", secretsData)
	}
	if !strings.Contains(secretsData, "data:") {
		t.Fatalf("secrets file has no encrypted payload:\n%s", secretsData)
	}
	if !manager.Secrets().VerifyPanelPassword("new-secret") {
		t.Fatal("stored panel password hash does not verify")
	}
	if snapshot := manager.Secrets().Snapshot(); snapshot.Devices["demo"].SSH.Password != "ssh-secret-password" {
		t.Fatal("ssh password not in encrypted store")
	}
	if _, err := os.Stat(manager.SecretsKeyPath()); err != nil {
		t.Fatalf("secrets key missing: %v", err)
	}

	freshClient := configClient(t)
	response, _ = configDo(t, freshClient, http.MethodGet, server.URL+"/api/config", nil, false)
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("config without login = %d", response.StatusCode)
	}
	response, body = configDo(t, freshClient, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "new-secret"}, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login = %d body=%s", response.StatusCode, body)
	}
	cfg, _ = getConfigPayload(t, freshClient, server.URL)
	cfg.ConfigPanel.Password = ""
	cfg.ConfigPanel.ClearPassword = true
	response, body = configDo(t, freshClient, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"password_set":false`) {
		t.Fatalf("clear password = %d body=%s", response.StatusCode, body)
	}
	anonymous := configClient(t)
	response, _ = configDo(t, anonymous, http.MethodGet, server.URL+"/api/config", nil, false)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("config after clearing password = %d", response.StatusCode)
	}
}

func TestConfigGetHidesSecretsAndKeepsThemOnSave(t *testing.T) {
	server, manager, _ := newConfigPanelAPI(t, `
server:
  listen: ":0"
config_panel:
  enabled: true
  password: "panel-secret"
devices:
  - key: "demo"
    macs: ["AA:BB:CC:DD:EE:FF"]
    ssh:
      enabled: true
      host: "192.168.1.10"
      user: "root"
      password: "ssh-secret-password"
      host_key: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
      shutdown_command: "poweroff"
`)
	client := configClient(t)
	response, body := configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "panel-secret"}, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login = %d body=%s", response.StatusCode, body)
	}

	cfg, body := getConfigPayload(t, client, server.URL)
	if strings.Contains(string(body), "ssh-secret-password") {
		t.Fatal("GET /api/config exposed an SSH password")
	}
	if cfg.Devices[0].SSH.Password != "" {
		t.Fatal("sanitized config still contains the SSH password")
	}
	if !strings.Contains(string(body), `"ssh_password":true`) {
		t.Fatal("secret status flag missing")
	}

	cfg.Devices[0].Name = "renamed"
	response, body = configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("save = %d body=%s", response.StatusCode, body)
	}
	if strings.Contains(string(body), "ssh-secret-password") {
		t.Fatal("PUT /api/config response exposed an SSH password")
	}
	if got := manager.Secrets().Snapshot().Devices["demo"].SSH.Password; got != "ssh-secret-password" {
		t.Fatalf("empty secret field should keep the stored password, got %q", got)
	}

	cfg.Devices[0].ClearSecrets = true
	cfg.Devices[0].SSH.Enabled = false
	response, body = configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("clear secrets = %d body=%s", response.StatusCode, body)
	}
	if _, ok := manager.Secrets().Snapshot().Devices["demo"]; ok {
		t.Fatal("clear_secrets did not remove the stored credentials")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func mustConfig(t *testing.T, client *http.Client, baseURL string) config.Config {
	cfg, _ := getConfigPayload(t, client, baseURL)
	return cfg
}

func TestConfigPanelRateLimit(t *testing.T) {
	server, _, _ := newConfigPanelAPI(t, `
server:
  listen: ":0"
config_panel:
  enabled: true
  password: "secret"
devices: []
`)
	client := configClient(t)
	for i := 0; i < configMaxLoginFailures-1; i++ {
		response, _ := configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "wrong"}, true)
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d", i+1, response.StatusCode)
		}
	}
	response, _ := configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "wrong"}, true)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("threshold status = %d", response.StatusCode)
	}
	response, _ = configDo(t, client, http.MethodPost, server.URL+"/api/config/login", map[string]string{"password": "secret"}, true)
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("blocked correct login status = %d", response.StatusCode)
	}
}

func TestConfigPanelRejectsInvalidSave(t *testing.T) {
	server, _, path := newConfigPanelAPI(t, `
server:
  listen: ":0"
  title: "Original"
config_panel:
  enabled: true
  password: ""
devices: []
`)
	original, _ := os.ReadFile(path)
	client := configClient(t)
	cfg, _ := getConfigPayload(t, client, server.URL)
	cfg.Server.Listen = "missing-port"
	response, body := configDo(t, client, http.MethodPut, server.URL+"/api/config", cfg, true)
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid save = %d body=%s", response.StatusCode, body)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("invalid save modified the config file")
	}
}
