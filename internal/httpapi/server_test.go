package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"wol/internal/config"
	"wol/internal/core"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestAPI(t *testing.T, token string) (*httptest.Server, *core.Manager, int) {
	t.Helper()
	content := `
server:
  listen: ":0"
  title: "Test Panel"
config_panel:
  enabled: true
  password: "` + token + `"
auth:
  quick_panel: true
  config_panel: true
devices:
  - key: "demo"
    name: "Demo Device"
    macs: ["01:02:03:04:05:06"]
    broadcast_ips: ["127.0.0.1"]
    port: 9
    repeat: 1
    reachability:
      enabled: false
`
	return newTestAPIWithContent(t, content)
}

func newTestAPIWithContent(t *testing.T, content string) (*httptest.Server, *core.Manager, int) {
	t.Helper()
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { listener.Close() })
	udpPort := listener.LocalAddr().(*net.UDPAddr).Port

	dir := t.TempDir()
	path := filepath.Join(dir, "wol.yaml")
	content = strings.ReplaceAll(content, "{{PORT}}", strconv.Itoa(udpPort))
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
	return server, manager, udpPort
}

func request(t *testing.T, method, url, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("X-WOL-Config", "1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, url, err)
	}
	return resp
}

func TestAPIHealthIsPublic(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	resp := request(t, http.MethodGet, server.URL+"/api/health", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAPIAuth(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()

	resp := request(t, http.MethodGet, server.URL+"/api/devices", "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token status = %d", resp.StatusCode)
	}
	resp = request(t, http.MethodGet, server.URL+"/api/devices", "wrong")
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d", resp.StatusCode)
	}
	resp = request(t, http.MethodGet, server.URL+"/api/devices", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("valid token status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"demo"`) || !strings.Contains(string(body), `"summary"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestAPIPanelAndSecurityHeaders(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	resp := request(t, http.MethodGet, server.URL+"/quickcontrol", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Test Panel") {
		t.Fatalf("panel title missing")
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing CSP header")
	}
}

func TestAPIWake(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()

	resp := request(t, http.MethodPost, server.URL+"/api/devices/demo/wake", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"mode":"wol"`) || !strings.Contains(string(body), `"packets":1`) {
		t.Fatalf("body = %s", body)
	}

	resp = request(t, http.MethodPost, server.URL+"/api/devices/missing/wake", "secret")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("missing device status = %d", resp.StatusCode)
	}
}

func TestAPIProbeDisabled(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	resp := request(t, http.MethodPost, server.URL+"/api/devices/demo/probe", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"reachability_state":"Unknown"`) {
		t.Fatalf("body = %s", body)
	}
}

func TestMutationsRequireCSRFHeader(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/devices/demo/wake", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("missing CSRF header status = %d, want 403", resp.StatusCode)
	}
}

func TestQueryTokenIsNotAccepted(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	resp, err := http.Get(server.URL + "/api/devices?token=secret")
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("query token status = %d, want 401", resp.StatusCode)
	}
}

func TestAPIUnknownRoute(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	resp := request(t, http.MethodGet, server.URL+"/api/nope", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestAPIReload(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()
	resp := request(t, http.MethodPost, server.URL+"/api/reload", "secret")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body=%s", resp.StatusCode, body)
	}
	time.Sleep(20 * time.Millisecond)
}

func TestPWAEndpoints(t *testing.T) {
	server, _, _ := newTestAPI(t, "secret")
	defer server.Close()

	resp, err := http.Get(server.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "manifest+json") {
		t.Fatalf("manifest content-type = %q", ct)
	}
	var manifest struct {
		Name  string `json:"name"`
		Icons []struct {
			Src     string `json:"src"`
			Purpose string `json:"purpose"`
		} `json:"icons"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if manifest.Name != "Test Panel" {
		t.Fatalf("manifest name = %q", manifest.Name)
	}
	maskable := false
	for _, icon := range manifest.Icons {
		if !strings.HasPrefix(icon.Src, "/assets/") {
			t.Fatalf("icon src = %q", icon.Src)
		}
		if icon.Purpose == "maskable" {
			maskable = true
		}
	}
	if len(manifest.Icons) < 4 || !maskable {
		t.Fatalf("manifest icons = %+v", manifest.Icons)
	}

	for _, src := range []string{"/assets/icon-192.png", "/assets/icon-maskable-512.png", "/assets/apple-touch-icon.png"} {
		icon, err := http.Get(server.URL + src)
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		icon.Body.Close()
		if icon.StatusCode != http.StatusOK || icon.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("%s = %d %q", src, icon.StatusCode, icon.Header.Get("Content-Type"))
		}
	}

	resp, err = http.Get(server.URL + "/sw.js")
	if err != nil {
		t.Fatalf("sw: %v", err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Service-Worker-Allowed"); got != "/" {
		t.Fatalf("Service-Worker-Allowed = %q", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read sw: %v", err)
	}
	if strings.Contains(string(body), "{{VERSION}}") || !strings.Contains(string(body), "wol-shell-") {
		t.Fatalf("service worker not versioned:\n%s", body[:200])
	}
}
