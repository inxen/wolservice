package core

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	"wol/internal/reach"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func loadTestConfig(t *testing.T, content string) (string, *config.Config) {
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
	return path, cfg
}

func TestManagerWakeAndReload(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer listener.Close()
	port := listener.LocalAddr().(*net.UDPAddr).Port

	content := `
server:
  listen: ":0"
devices:
  - key: "demo"
    name: "Demo"
    macs: ["01:02:03:04:05:06"]
    broadcast_ips: ["127.0.0.1"]
    port: ` + itoa(port) + `
    repeat: 2
    reachability:
      enabled: false
`
	path, cfg := loadTestConfig(t, content)
	mgr := NewManager(discardLogger(), path, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	status, ok := mgr.Status("demo")
	if !ok {
		t.Fatal("device not found")
	}
	if status.CanWakeup != true {
		t.Fatalf("can_wakeup = %v", status.CanWakeup)
	}
	if status.CanShutdown {
		t.Fatal("can_shutdown should be false without ssh")
	}
	if status.ReachabilityState != "Unknown" {
		t.Fatalf("reachability = %q", status.ReachabilityState)
	}

	result, err := mgr.Wake("demo")
	if err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if result.Mode != "wol" || result.Packets != 2 {
		t.Fatalf("result = %+v", result)
	}
	buf := make([]byte, 128)
	for i := 0; i < 2; i++ {
		_ = listener.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err := listener.ReadFromUDP(buf); err != nil {
			t.Fatalf("read packet %d: %v", i, err)
		}
	}

	if _, err := mgr.Wake("missing"); err == nil {
		t.Fatal("expected error for missing device")
	}

	updated := `
server:
  listen: ":0"
devices:
  - key: "demo"
    name: "Demo Renamed"
    macs: ["01:02:03:04:05:06"]
    broadcast_ips: ["127.0.0.1"]
    port: ` + itoa(port) + `
    repeat: 1
    reachability:
      enabled: false
`
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatalf("update config: %v", err)
	}
	if err := mgr.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	status, ok = mgr.Status("demo")
	if !ok || status.Name != "Demo Renamed" || status.Repeat != 1 {
		t.Fatalf("status after reload = %+v, ok=%v", status, ok)
	}
}

func TestCanWakeupWhenReachable(t *testing.T) {
	path, cfg := loadTestConfig(t, `
server:
  listen: ":0"
devices:
  - key: "demo"
    macs: ["01:02:03:04:05:06"]
    reachability:
      enabled: false
`)
	mgr := NewManager(discardLogger(), path, cfg)
	device, ok := mgr.Device("demo")
	if !ok {
		t.Fatal("device not found")
	}
	device.mu.Lock()
	device.reachState = reach.StateReachable
	device.mu.Unlock()

	status, ok := mgr.Status("demo")
	if !ok {
		t.Fatal("status not found")
	}
	if !status.CanWakeup {
		t.Fatal("online device must still allow manual wake")
	}
}

func TestManagerShutdownRequiresMethod(t *testing.T) {
	path, cfg := loadTestConfig(t, `
server:
  listen: ":0"
devices:
  - key: "demo"
    macs: ["01:02:03:04:05:06"]
    reachability:
      enabled: false
`)
	mgr := NewManager(discardLogger(), path, cfg)
	if _, err := mgr.Shutdown("demo"); err == nil {
		t.Fatal("expected shutdown error without ssh")
	}
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

type fakeCameraServer struct {
	password string
	nonce    string
	stok     string
	enabled  string
}

func (f *fakeCameraServer) start(t *testing.T) (string, int) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/pc/Content.htm", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data":       map[string]any{"nonce": f.nonce},
			"error_code": -40401,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Login struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"login"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		sum := md5.Sum([]byte(f.password + ":" + f.nonce))
		if payload.Login.Username != "admin" || payload.Login.Password != hex.EncodeToString(sum[:]) {
			_ = json.NewEncoder(w).Encode(map[string]any{"error_code": -40401})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"stok": f.stok, "error_code": 0})
	})
	mux.HandleFunc("/stok="+f.stok+"/ds", func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Method   string `json:"method"`
			LensMask struct {
				LensMaskInfo struct {
					Enabled string `json:"enabled"`
				} `json:"lens_mask_info"`
			} `json:"lens_mask"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		if payload.Method == "set" {
			f.enabled = payload.LensMask.LensMaskInfo.Enabled
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"lens_mask":  map[string]any{"lens_mask_info": map[string]any{"enabled": f.enabled}},
			"error_code": 0,
		})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split camera address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse camera port: %v", err)
	}
	return host, port
}

func TestManagerCameraLensMask(t *testing.T) {
	fake := &fakeCameraServer{password: "secret", nonce: "n0nce", stok: "tok123", enabled: "on"}
	host, port := fake.start(t)

	path, cfg := loadTestConfig(t, fmt.Sprintf(`
server:
  listen: ":0"
devices:
  - key: "camera"
    type: "tplink-camera"
    name: "Camera"
    camera:
      host: "%s"
      port: %d
      password: "secret"
`, host, port))
	mgr := NewManager(discardLogger(), path, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mgr.Start(ctx)
	defer mgr.Stop()

	status, err := mgr.RefreshMask("camera")
	if err != nil {
		t.Fatalf("RefreshMask: %v", err)
	}
	if !status.CameraEnabled || status.CameraHost != host || status.MaskState != "on" {
		t.Fatalf("status = %#v", status)
	}
	if status.CanWakeup || status.CanShutdown || status.WakeMode != "none" || status.ReachabilityEnabled {
		t.Fatalf("camera capabilities = %#v", status)
	}
	if status.ReachabilityState != reach.StateReachable {
		t.Fatalf("reachability state = %q, want %q", status.ReachabilityState, reach.StateReachable)
	}

	status, err = mgr.SetMask("camera", false)
	if err != nil {
		t.Fatalf("SetMask: %v", err)
	}
	if status.MaskState != "off" || fake.enabled != "off" {
		t.Fatalf("status = %#v, camera enabled = %q", status, fake.enabled)
	}

	if _, err := mgr.Wake("camera"); err == nil {
		t.Fatal("camera wake should be rejected")
	}
	if _, err := mgr.Shutdown("camera"); err == nil {
		t.Fatal("camera shutdown should be rejected")
	}

	if _, err := mgr.SetMask("nope", true); err == nil {
		t.Fatal("expected error for unknown device")
	}
}
