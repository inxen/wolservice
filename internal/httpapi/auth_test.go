package httpapi

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"
)

func authTestContent(authYAML string) string {
	return `
server:
  listen: ":0"
  title: "Auth Test"
config_panel:
  enabled: true
  password: "secret"
auth:
` + authYAML + `
devices: []
`
}

func loginClient(t *testing.T, baseURL, password string) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	if err != nil {
		t.Fatalf("new login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-WOL-Config", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	return client
}

func TestQuickScopeProtectsPanelAndAPI(t *testing.T) {
	server, _, _ := newTestAPIWithContent(t, authTestContent(`  quick_panel: true
  config_panel: false
  trusted_networks_enabled: false`))
	defer server.Close()

	resp := request(t, http.MethodGet, server.URL+"/", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "loginForm") {
		t.Fatalf("quick login page status = %d", resp.StatusCode)
	}

	resp = request(t, http.MethodGet, server.URL+"/api/devices", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("api without password status = %d, want 401", resp.StatusCode)
	}

	resp = request(t, http.MethodGet, server.URL+"/api/devices", "secret")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api bearer status = %d, want 200", resp.StatusCode)
	}

	resp = request(t, http.MethodGet, server.URL+"/api/config", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config without password status = %d, want 200", resp.StatusCode)
	}

	client := loginClient(t, server.URL, "secret")
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatalf("quick page: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "deviceGrid") {
		t.Fatalf("quick page after login status = %d", resp.StatusCode)
	}
	resp, err = client.Get(server.URL + "/api/devices")
	if err != nil {
		t.Fatalf("api session: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api session status = %d, want 200", resp.StatusCode)
	}
}

func TestConfigScopeIsIndependent(t *testing.T) {
	server, _, _ := newTestAPIWithContent(t, authTestContent(`  quick_panel: false
  config_panel: true
  trusted_networks_enabled: false`))
	defer server.Close()

	resp := request(t, http.MethodGet, server.URL+"/", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.Contains(string(body), "loginForm") {
		t.Fatalf("quick page should be open, status = %d", resp.StatusCode)
	}

	resp = request(t, http.MethodGet, server.URL+"/api/devices", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("api should follow quick scope, status = %d", resp.StatusCode)
	}

	resp = request(t, http.MethodGet, server.URL+"/api/config", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("config without password status = %d, want 401", resp.StatusCode)
	}

	client := loginClient(t, server.URL, "secret")
	resp, err := client.Get(server.URL + "/api/config")
	if err != nil {
		t.Fatalf("config request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("config after login status = %d", resp.StatusCode)
	}
}

func TestTrustedNetworksAllowlist(t *testing.T) {
	server, _, _ := newTestAPIWithContent(t, authTestContent(`  quick_panel: false
  config_panel: false
  trusted_networks_enabled: true
  trusted_networks: ["10.0.0.0/8"]`))
	defer server.Close()

	resp := request(t, http.MethodGet, server.URL+"/api/health", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outside trusted networks status = %d, want 403", resp.StatusCode)
	}

	server2, _, _ := newTestAPIWithContent(t, authTestContent(`  quick_panel: false
  config_panel: false
  trusted_networks_enabled: true
  trusted_networks: ["127.0.0.1/32"]`))
	defer server2.Close()

	resp = request(t, http.MethodGet, server2.URL+"/api/health", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("inside trusted networks status = %d, want 200", resp.StatusCode)
	}
}
