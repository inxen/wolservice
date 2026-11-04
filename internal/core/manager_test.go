package core

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
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
