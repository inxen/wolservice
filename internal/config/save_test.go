package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDurationJSONRoundTrip(t *testing.T) {
	value := Duration{Duration: 90 * time.Second}
	raw, err := value.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	if string(raw) != `"1m30s"` {
		t.Fatalf("duration JSON = %s", raw)
	}
	var parsed Duration
	if err := parsed.UnmarshalJSON(raw); err != nil {
		t.Fatalf("UnmarshalJSON: %v", err)
	}
	if parsed.Duration != value.Duration {
		t.Fatalf("parsed = %s", parsed.Duration)
	}
	if err := parsed.UnmarshalJSON([]byte(`""`)); err != nil || parsed.Duration != 0 {
		t.Fatalf("empty duration = %s, %v", parsed.Duration, err)
	}
	if err := parsed.UnmarshalJSON([]byte(`2`)); err != nil || parsed.Duration != 2*time.Second {
		t.Fatalf("numeric duration = %s, %v", parsed.Duration, err)
	}
}

func TestSaveCreatesBackupAndWritesConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.yaml")
	original := `server:
  listen: ":8090"
  title: "Original"
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:FF"]
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write original: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Server.Title = "Updated"
	cfg.ConfigPanel = ConfigPanelConfig{Enabled: true, Password: "panel-secret"}
	cfg.Devices = append(cfg.Devices, Device{
		Key:  "nas",
		Name: "NAS",
		MACs: []string{"11:22:33:44:55:66"},
	})

	backup, err := Save(path, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backup != path+".bak" {
		t.Fatalf("backup = %q", backup)
	}
	backupData, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if !strings.Contains(string(backupData), "title: Original") {
		t.Fatalf("backup missing old values:\n%s", backupData)
	}
	if strings.Contains(string(backupData), "panel-secret") {
		t.Fatalf("backup leaked credentials:\n%s", backupData)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved: %v", err)
	}
	if !strings.Contains(string(data), "Rewritten at") {
		t.Fatalf("missing generated header:\n%s", data)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded.Server.Title != "Updated" {
		t.Fatalf("title = %q", loaded.Server.Title)
	}
	if !loaded.ConfigPanel.Enabled || loaded.ConfigPanel.Password != "panel-secret" {
		t.Fatalf("config panel = %+v", loaded.ConfigPanel)
	}
	if len(loaded.Devices) != 2 || loaded.Devices[1].Key != "nas" {
		t.Fatalf("devices = %+v", loaded.Devices)
	}
}

func TestSaveRejectsInvalidWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.yaml")
	original := `server:
  listen: ":8090"
  title: "Original"
`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.Devices = []Device{{Key: "broken", MACs: []string{"not-a-mac"}}}
	if _, err := Save(path, cfg); err == nil {
		t.Fatal("expected validation error")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != original {
		t.Fatalf("file was modified on invalid save:\n%s", data)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("unexpected backup: %v", err)
	}
}

func TestSaveCreatesFileWithoutBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.yaml")
	cfg := &Config{
		Server: ServerConfig{Listen: ":8090"},
		Devices: []Device{{
			Key:  "pc",
			MACs: []string{"AA:BB:CC:DD:EE:FF"},
		}},
	}
	backup, err := Save(path, cfg)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if backup != "" {
		t.Fatalf("backup = %q", backup)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("saved file: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Devices[0].Port != DefaultPort {
		t.Fatalf("port default = %d", loaded.Devices[0].Port)
	}
}

func TestLoadOrCreateCreatesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "wol.yaml")
	cfg, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatalf("LoadOrCreate: %v", err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	if cfg.Server.Listen != DefaultListen {
		t.Fatalf("listen = %q", cfg.Server.Listen)
	}
	if !cfg.ConfigPanel.Enabled || cfg.ConfigPanel.Password != "" {
		t.Fatalf("config panel = %+v", cfg.ConfigPanel)
	}
	if len(cfg.Devices) != 0 {
		t.Fatalf("devices = %d", len(cfg.Devices))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	if !strings.Contains(string(data), "首次启动且配置不存在时自动生成") {
		t.Fatalf("generated config header missing:\n%s", data)
	}

	cfg, created, err = LoadOrCreate(path)
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if created {
		t.Fatal("existing config should not be recreated")
	}
	if cfg.Server.Title != "网络唤醒" {
		t.Fatalf("title = %q", cfg.Server.Title)
	}
}

func TestLoadOrCreateDoesNotOverwriteInvalid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wol.yaml")
	invalid := "server:\n  listen: \"no-port\"\n"
	if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}
	_, created, err := LoadOrCreate(path)
	if err == nil {
		t.Fatal("expected validation error")
	}
	if created {
		t.Fatal("invalid existing config must not be replaced")
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read config: %v", readErr)
	}
	if string(data) != invalid {
		t.Fatalf("invalid config was overwritten:\n%s", data)
	}
}

func TestConfigPanelPasswordJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wol.yaml")
	cfg := &Config{
		Server:      ServerConfig{Listen: ":8090"},
		ConfigPanel: ConfigPanelConfig{Enabled: true, Password: "secret"},
	}
	if _, err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.ConfigPanel.Enabled || loaded.ConfigPanel.Password != "secret" {
		t.Fatalf("panel = %+v", loaded.ConfigPanel)
	}
}

func TestSaveWritesNumericListen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wol.yaml")
	cfg := &Config{Server: ServerConfig{Listen: ":8090"}}
	if _, err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), `listen: "8090"`) {
		t.Fatalf("listen not serialized as a number-like value:\n%s", data)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Server.Listen != ":8090" {
		t.Fatalf("in-memory listen = %q", loaded.Server.Listen)
	}
}
