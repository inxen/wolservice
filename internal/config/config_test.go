package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadDefaults(t *testing.T) {
	path := writeConfig(t, `
devices:
  - name: "Living Room PC"
    macs: ["aa-bb-cc-dd-ee-ff"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != DefaultListen {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Server.Title == "" {
		t.Fatal("title not defaulted")
	}
	if cfg.Probe.Interval.Duration != DefaultProbeInterval {
		t.Fatalf("probe interval = %s", cfg.Probe.Interval.Duration)
	}
	if cfg.Auth == nil || !cfg.Auth.ConfigPanel {
		t.Fatalf("auth defaults = %#v, want config_panel enabled", cfg.Auth)
	}
	d := cfg.Devices[0]
	if d.Port != DefaultPort || d.Repeat != DefaultRepeat {
		t.Fatalf("port/repeat = %d/%d", d.Port, d.Repeat)
	}
	if d.MACs[0] != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("mac = %q", d.MACs[0])
	}
	if d.Key == "" || !strings.HasPrefix(d.Key, "living-room-pc") {
		t.Fatalf("key = %q", d.Key)
	}
	if d.Name != "Living Room PC" {
		t.Fatalf("name = %q", d.Name)
	}
}

func TestLoadDurations(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: ":9000"
probe:
  interval: "7s"
  timeout: 3
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:FF"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Probe.Interval.Duration != 7*time.Second {
		t.Fatalf("interval = %s", cfg.Probe.Interval.Duration)
	}
	if cfg.Probe.Timeout.Duration != 3*time.Second {
		t.Fatalf("timeout = %s", cfg.Probe.Timeout.Duration)
	}
}

func TestLoadRejectsInvalidMAC(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "pc"
    macs: ["invalid"]
`)
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "invalid mac") {
		t.Fatalf("expected invalid mac error, got %v", err)
	}
}

func TestSSHHostKeyRequired(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:FF"]
    ssh:
      enabled: true
      host: "192.168.1.10"
      user: "root"
      password: "secret"
      shutdown_command: "poweroff"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "ssh.host_key") {
		t.Fatalf("expected host_key error, got %v", err)
	}
}

func TestDuplicateBemfaTopicRejected(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "a"
    macs: ["AA:BB:CC:DD:EE:01"]
    iot:
      bemfa:
        enabled: true
        secret_key: "same-key"
        topic: "pc001"
  - key: "b"
    macs: ["AA:BB:CC:DD:EE:02"]
    iot:
      bemfa:
        enabled: true
        secret_key: "same-key"
        topic: "pc001"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected duplicate topic error, got %v", err)
	}
}

func TestDuplicateBlinkerKeyRejected(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "a"
    macs: ["AA:BB:CC:DD:EE:01"]
    iot:
      blinker:
        enabled: true
        auth_key: "same-key"
  - key: "b"
    macs: ["AA:BB:CC:DD:EE:02"]
    iot:
      blinker:
        enabled: true
        auth_key: "same-key"
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected duplicate blinker key error, got %v", err)
	}
}

func TestDuplicateKeysGetSuffix(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:01"]
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:02"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].Key == cfg.Devices[1].Key {
		t.Fatalf("duplicate keys: %q", cfg.Devices[0].Key)
	}
}

func TestResolvePath(t *testing.T) {
	got := ResolvePath(filepath.Join("etc", "wol", "wol.yaml"), "keys/id")
	if got != filepath.Join("etc", "wol", "keys", "id") {
		t.Fatalf("got %q", got)
	}
	abs := filepath.Join(t.TempDir(), "key")
	if ResolvePath("wol.yaml", abs) != abs {
		t.Fatal("absolute path changed")
	}
}

func TestSecretRefDefaultsToKey(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "device-0174"
    macs: ["AA:BB:CC:DD:EE:FF"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].SecretRef != "device-0174" {
		t.Fatalf("secret_ref = %q", cfg.Devices[0].SecretRef)
	}
}

func TestDuplicateSecretRefRejected(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "a"
    secret_ref: "shared"
    macs: ["AA:BB:CC:DD:EE:01"]
  - key: "b"
    secret_ref: "shared"
    macs: ["AA:BB:CC:DD:EE:02"]
`)
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "secret_ref") {
		t.Fatalf("expected duplicate secret_ref error, got %v", err)
	}
}

func TestListenAcceptsNumericPort(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "8090"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != ":8090" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
}

func TestListenAcceptsHostPort(t *testing.T) {
	path := writeConfig(t, `
server:
  listen: "127.0.0.1:8090"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Listen != "127.0.0.1:8090" {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
}

func TestListenRejectsInvalid(t *testing.T) {
	for _, value := range []string{"abc", ":abc", "127.0.0.1", ":70000"} {
		path := writeConfig(t, "server:\n  listen: \""+value+"\"\n")
		if _, err := Load(path); err == nil {
			t.Fatalf("listen %q should be rejected", value)
		}
	}
}

func TestLegacyAPIScopeMigratesToQuickPanel(t *testing.T) {
	path := writeConfig(t, `
auth:
  api: true
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Auth.QuickPanel || cfg.Auth.API {
		t.Fatalf("auth = %#v, want quick_panel enabled and api cleared", cfg.Auth)
	}
}

func TestAuthTrustedNetworks(t *testing.T) {
	path := writeConfig(t, `
auth:
  quick_panel: true
  trusted_networks_enabled: true
  trusted_networks: [" 10.1.0.0/16 ", "192.168.1.5"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Auth.QuickPanel || cfg.Auth.ConfigPanel || cfg.Auth.API {
		t.Fatalf("auth = %#v", cfg.Auth)
	}
	if cfg.Auth.TrustedNetworks[0] != "10.1.0.0/16" {
		t.Fatalf("network not normalized: %#v", cfg.Auth.TrustedNetworks)
	}
	if !NetworkContains("10.1.0.0/16", "10.1.2.3") || NetworkContains("192.168.1.5", "192.168.1.6") {
		t.Fatal("NetworkContains mismatch")
	}

	path = writeConfig(t, `
auth:
  trusted_networks_enabled: true
`)
	if _, err := Load(path); err == nil {
		t.Fatal("empty trusted_networks should be rejected when enabled")
	}
	path = writeConfig(t, `
auth:
  trusted_networks_enabled: true
  trusted_networks: ["not-a-network"]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("invalid trusted network should be rejected")
	}
}

func TestCameraDeviceAllowsEmptyMACs(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "camera"
    type: "tplink-camera"
    name: "Camera"
    macs: ["not-a-mac"]
    broadcast_ips: ["not-an-ip"]
    reachability:
      enabled: true
      targets: ["127.0.0.1"]
    ssh:
      enabled: true
    camera:
      host: " 192.0.2.11 "
      password: "secret"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	device := cfg.Devices[0]
	if device.Type != DeviceTypeTPLinkCamera || !device.HasCamera() || !device.Camera.Enabled {
		t.Fatalf("camera type = %q, enabled = %v", device.Type, device.Camera.Enabled)
	}
	if device.Reachability.Enabled || device.SSH.Enabled {
		t.Fatalf("camera must not use reachability or ssh: %#v", device)
	}
	camera := device.Camera
	if camera.Host != "192.0.2.11" {
		t.Fatalf("camera.host = %q", camera.Host)
	}
	if camera.Port != 80 || camera.Username != "admin" {
		t.Fatalf("camera defaults = %#v", camera)
	}
}

func TestLegacyCameraEnabledInfersType(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "camera"
    camera:
      enabled: true
      host: "192.0.2.11"
      password: "secret"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].Type != DeviceTypeTPLinkCamera {
		t.Fatalf("type = %q, want %q", cfg.Devices[0].Type, DeviceTypeTPLinkCamera)
	}
	if cfg.Devices[0].MACs == nil {
		t.Fatal("macs should be normalized to an empty slice")
	}
}

func TestComputerTypeDisablesCamera(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "pc"
    type: "computer"
    macs: ["01:02:03:04:05:06"]
    camera:
      enabled: true
      host: "192.0.2.11"
      password: "secret"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].HasCamera() || cfg.Devices[0].Camera.Enabled {
		t.Fatal("computer device must not be treated as a camera")
	}
}

func TestUnknownDeviceTypeRejected(t *testing.T) {
	path := writeConfig(t, `
devices:
  - key: "pc"
    type: "printer"
    macs: ["01:02:03:04:05:06"]
`)
	if _, err := Load(path); err == nil {
		t.Fatal("unknown device type should be rejected")
	}
}

func TestCameraRequiresHostAndPassword(t *testing.T) {
	cases := []string{
		`
devices:
  - key: "camera"
    type: "tplink-camera"
    camera:
      password: "secret"
`,
		`
devices:
  - key: "camera"
    type: "tplink-camera"
    camera:
      host: "192.0.2.11"
`,
		`
devices:
  - key: "camera"
    type: "tplink-camera"
    camera:
      host: "bad host"
      password: "secret"
`,
	}
	for _, content := range cases {
		path := writeConfig(t, content)
		if _, err := Load(path); err == nil {
			t.Fatalf("config should be rejected:\n%s", content)
		}
	}
}
