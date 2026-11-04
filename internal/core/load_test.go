package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"wol/internal/config"
	"wol/internal/secrets"
)

func TestLoadConfigMigratesLegacySecrets(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wol.yaml")
	secretsPath := filepath.Join(dir, "wol.secrets.yaml")
	content := `
server:
  listen: ":0"
config_panel:
  enabled: true
  password: "legacy-panel-password"
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:FF"]
    ssh:
      enabled: true
      host: "192.168.1.10"
      user: "root"
      password: "legacy-ssh-password"
      host_key: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
      shutdown_command: "poweroff"
    iot:
      bemfa:
        enabled: true
        secret_key: "legacy-bemfa-key"
        topic: "pc001"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, store, created, migrated, err := LoadConfig(configPath, secretsPath, secrets.DefaultKeyPath(secretsPath), true)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if created {
		t.Fatal("created should be false for existing config")
	}
	if !migrated {
		t.Fatal("expected migration")
	}
	if _, err := os.Stat(secretsPath); err != nil {
		t.Fatalf("secrets file missing: %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	for _, secret := range []string{
		"legacy-panel-password", "legacy-ssh-password", "legacy-bemfa-key",
	} {
		if strings.Contains(string(configData), secret) {
			t.Fatalf("config still contains secret %q:\n%s", secret, configData)
		}
	}
	secretsData, err := os.ReadFile(secretsPath)
	if err != nil {
		t.Fatalf("read secrets: %v", err)
	}
	if !strings.Contains(string(secretsData), "data:") {
		t.Fatalf("secrets file is not encrypted:\n%s", secretsData)
	}
	for _, secret := range []string{
		"legacy-panel-password", "legacy-ssh-password", "legacy-bemfa-key",
	} {
		if strings.Contains(string(secretsData), secret) {
			t.Fatalf("encrypted secrets file leaks %q", secret)
		}
	}
	if _, err := os.Stat(secrets.DefaultKeyPath(secretsPath)); err != nil {
		t.Fatalf("secrets key file missing: %v", err)
	}
	if !store.PanelPasswordSet() {
		t.Fatal("hashed credentials not stored")
	}
	if !store.VerifyPanelPassword("legacy-panel-password") {
		t.Fatal("legacy panel password does not verify")
	}
	if cfg.ConfigPanel.Password != "" {
		t.Fatal("legacy auth secrets still present in merged config")
	}
	if cfg.Devices[0].SSH.Password != "legacy-ssh-password" {
		t.Fatal("ssh secret not merged into runtime config")
	}
	snapshot := store.Snapshot()
	if snapshot.Devices["pc"].SSH.Password != "legacy-ssh-password" ||
		snapshot.Devices["pc"].IoT.BemfaSecretKey != "legacy-bemfa-key" {
		t.Fatalf("secrets snapshot incomplete: %+v", snapshot)
	}
	configBackup, err := os.ReadFile(configPath + ".bak")
	if err != nil {
		t.Fatalf("read config backup: %v", err)
	}
	for _, secret := range []string{"legacy-access-token", "legacy-panel-password", "legacy-ssh-password"} {
		if strings.Contains(string(configBackup), secret) {
			t.Fatalf("config backup leaked %q:\n%s", secret, configBackup)
		}
	}

	reloaded, _, _, migratedAgain, err := LoadConfig(configPath, secretsPath, secrets.DefaultKeyPath(secretsPath), true)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if migratedAgain {
		t.Fatal("second load should not migrate again")
	}
	if reloaded.Devices[0].SSH.Password != "legacy-ssh-password" {
		t.Fatal("ssh secret lost on reload")
	}
}

func TestLoadConfigSanitizesLegacyEvenWhenSecretsExist(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wol.yaml")
	secretsPath := filepath.Join(dir, "wol.secrets.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  listen: ":0"
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:FF"]
    ssh:
      enabled: true
      host: "192.168.1.10"
      user: "root"
      password: "legacy-ssh-password"
      host_key: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
      shutdown_command: "poweroff"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	store := secrets.NewStore(secretsPath)
	store.ReplaceRuntimeSecretsFromConfig(&config.Config{
		Devices: []config.Device{{
			Key: "pc",
			SSH: config.SSHConfig{Password: "legacy-ssh-password"},
		}},
	})
	if _, err := store.Save(); err != nil {
		t.Fatalf("save secrets: %v", err)
	}
	_, _, _, migrated, err := LoadConfig(configPath, secretsPath, secrets.DefaultKeyPath(secretsPath), true)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if !migrated {
		t.Fatal("legacy config with existing secrets should still be sanitized")
	}
	data, _ := os.ReadFile(configPath)
	if strings.Contains(string(data), "legacy-ssh-password") {
		t.Fatalf("config still contains plaintext secret:\n%s", data)
	}
}

func TestLoadConfigWithoutSecrets(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "wol.yaml")
	secretsPath := filepath.Join(dir, "custom.secrets.yaml")
	if err := os.WriteFile(configPath, []byte(`
server:
  listen: ":0"
devices:
  - key: "pc"
    macs: ["AA:BB:CC:DD:EE:FF"]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, store, created, migrated, err := LoadConfig(configPath, secretsPath, secrets.DefaultKeyPath(secretsPath), false)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if created || migrated {
		t.Fatalf("created=%v migrated=%v", created, migrated)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("devices = %d", len(cfg.Devices))
	}
	if store == nil || store.Path() != secretsPath {
		t.Fatalf("store path = %v", store)
	}
	if _, err := os.Stat(secretsPath); !os.IsNotExist(err) {
		t.Fatalf("secrets file should not exist: %v", err)
	}
	_ = config.DefaultListen
	_ = secrets.DefaultPath
}
