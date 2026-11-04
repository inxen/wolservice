package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"wol/internal/config"
)

func TestPanelPasswordHash(t *testing.T) {
	hash, err := HashPanelPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPanelPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("hash = %q", hash)
	}
	if !verifyPanelPassword(hash, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if verifyPanelPassword(hash, "wrong") {
		t.Fatal("wrong password accepted")
	}
	if verifyPanelPassword("not-a-hash", "anything") {
		t.Fatal("invalid hash accepted")
	}
}

func TestPanelPasswordStore(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "wol.secrets.yaml"))
	if store.PanelPasswordSet() || !store.VerifyPanelPassword("anything") {
		t.Fatal("empty panel password should be open")
	}
	if err := store.SetPanelPassword("secret"); err != nil {
		t.Fatalf("SetPanelPassword: %v", err)
	}
	if !store.PanelPasswordSet() || !store.VerifyPanelPassword("secret") {
		t.Fatal("panel password not verified")
	}
	if store.VerifyPanelPassword("wrong") {
		t.Fatal("wrong panel password accepted")
	}
	fingerprint := store.PanelPasswordFingerprint()
	store.ClearPanelPassword()
	if store.PanelPasswordSet() || !store.VerifyPanelPassword("anything") {
		t.Fatal("clear panel password failed")
	}
	if store.PanelPasswordFingerprint() == fingerprint {
		t.Fatal("password fingerprint did not change")
	}
}

func TestImportLegacyAndApply(t *testing.T) {
	cfg := &config.Config{
		ConfigPanel: config.ConfigPanelConfig{Enabled: true, Password: "panel-pass"},
		Devices: []config.Device{{
			Key:  "pc",
			MACs: []string{"AA:BB:CC:DD:EE:FF"},
			SSH: config.SSHConfig{
				Enabled: true, Password: "ssh-pass", PrivateKey: "key", Passphrase: "pass",
			},
			IoT: config.IoTConfig{
				Bemfa:   config.BemfaConfig{Enabled: true, SecretKey: "bemfa-key"},
				Blinker: config.BlinkerConfig{Enabled: true, AuthKey: "blinker-key"},
			},
		}},
	}
	store := NewStore(filepath.Join(t.TempDir(), "wol.secrets.yaml"))
	changed, err := store.ImportLegacy(cfg)
	if err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true")
	}
	if !store.PanelPasswordSet() || !store.VerifyPanelPassword("panel-pass") {
		t.Fatal("panel password not imported")
	}
	file := store.Snapshot()
	if file.Devices["pc"].SSH.Password != "ssh-pass" {
		t.Fatalf("secrets not imported: %+v", file)
	}

	merged := *cfg
	merged.Devices = append([]config.Device(nil), cfg.Devices...)
	store.ApplyToConfig(&merged)
	if merged.ConfigPanel.Password != "" {
		t.Fatal("auth secrets should not be applied to config")
	}
	if merged.Devices[0].SSH.Password != "ssh-pass" || merged.Devices[0].SSH.PrivateKey != "key" ||
		merged.Devices[0].IoT.Bemfa.SecretKey != "bemfa-key" || merged.Devices[0].IoT.Blinker.AuthKey != "blinker-key" {
		t.Fatalf("device secrets not applied: %+v", merged.Devices[0])
	}
}

func TestStoreSaveLoadAndPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.secrets.yaml")
	store := NewStore(path)
	if err := store.SetPanelPassword("panel"); err != nil {
		t.Fatalf("SetPanelPassword: %v", err)
	}
	store.ReplaceRuntimeSecretsFromConfig(&config.Config{
		Devices: []config.Device{{
			Key:  "pc",
			MACs: []string{"AA:BB:CC:DD:EE:FF"},
			SSH:  config.SSHConfig{Password: "ssh-pass"},
		}},
	})
	if _, err := store.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("secrets mode = %o", info.Mode().Perm())
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read encrypted secrets: %v", err)
	}
	for _, plain := range []string{"panel", "ssh-pass", "password_hash"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("encrypted secrets file leaks %q:\n%s", plain, raw)
		}
	}
	if !strings.Contains(string(raw), "data:") {
		t.Fatalf("secrets file is not encrypted:\n%s", raw)
	}
	if _, err := os.Stat(DefaultKeyPath(path)); err != nil {
		t.Fatalf("key file missing: %v", err)
	}
	loaded, err := Load(path, DefaultKeyPath(path))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !loaded.VerifyPanelPassword("panel") {
		t.Fatal("panel password not persisted")
	}
	if loaded.Snapshot().Devices["pc"].SSH.Password != "ssh-pass" {
		t.Fatal("device secret not persisted")
	}
	backup, err := loaded.Save()
	if err != nil {
		t.Fatalf("second Save: %v", err)
	}
	if backup != path+".bak" {
		t.Fatalf("backup = %q", backup)
	}

	wrongKey := filepath.Join(dir, "wrong.key")
	if err := os.WriteFile(wrongKey, []byte(strings.Repeat("ab", 32)+"\n"), 0o600); err != nil {
		t.Fatalf("write wrong key: %v", err)
	}
	if _, err := Load(path, wrongKey); err == nil {
		t.Fatal("encrypted secrets loaded with a wrong key")
	}
}

func TestLoadLegacyPlaintextAndEncrypt(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.secrets.yaml")
	keyPath := DefaultKeyPath(path)
	plain := []byte(`
devices:
  device-0174:
    ssh:
      password: legacy-plain
`)
	if err := os.WriteFile(path, plain, 0o600); err != nil {
		t.Fatalf("write plaintext secrets: %v", err)
	}
	store, err := Load(path, keyPath)
	if err != nil {
		t.Fatalf("Load plaintext: %v", err)
	}
	if !store.NeedsEncryption() {
		t.Fatal("plaintext secrets should need encryption")
	}
	if store.Snapshot().Devices["device-0174"].SSH.Password != "legacy-plain" {
		t.Fatal("plaintext secret not loaded")
	}
	if _, err := store.Save(); err != nil {
		t.Fatalf("encrypt Save: %v", err)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "legacy-plain") || !strings.Contains(string(raw), "data:") {
		t.Fatalf("secrets not encrypted:\n%s", raw)
	}
	reloaded, err := Load(path, keyPath)
	if err != nil {
		t.Fatalf("Load encrypted: %v", err)
	}
	if reloaded.NeedsEncryption() {
		t.Fatal("encrypted secrets still report NeedsEncryption")
	}
	if reloaded.Snapshot().Devices["device-0174"].SSH.Password != "legacy-plain" {
		t.Fatal("secret lost after encryption")
	}
}

func TestRuntimeEqualIgnoresAuthHashes(t *testing.T) {
	a := NewStore("")
	if err := a.SetPanelPassword("panel"); err != nil {
		t.Fatalf("SetPanelPassword: %v", err)
	}
	b := NewStore("")
	if err := b.SetPanelPassword("other"); err != nil {
		t.Fatalf("SetPanelPassword: %v", err)
	}
	if !a.RuntimeEqual(b) {
		t.Fatal("runtime equal should ignore auth hashes")
	}
	a.ReplaceRuntimeSecretsFromConfig(&config.Config{
		Devices: []config.Device{{Key: "pc", MACs: []string{"AA:BB:CC:DD:EE:FF"}, SSH: config.SSHConfig{Password: "x"}}},
	})
	if a.RuntimeEqual(b) {
		t.Fatal("runtime secrets differ but RuntimeEqual returned true")
	}
}

func TestDefaultPath(t *testing.T) {
	cases := map[string]string{
		"wol.yaml":          "wol.secrets.yaml",
		"/etc/wol/wol.yaml": "/etc/wol/wol.secrets.yaml",
		"/etc/wol/config":   "/etc/wol/config.secrets.yaml",
	}
	for input, want := range cases {
		if got := DefaultPath(input); got != want {
			t.Fatalf("DefaultPath(%q) = %q, want %q", input, got, want)
		}
	}
	if got := DefaultKeyPath("/etc/wol/wol.secrets.yaml"); got != "/etc/wol/wol.secrets.key" {
		t.Fatalf("DefaultKeyPath = %q", got)
	}
}

func TestEmbeddedKeyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.secrets.yaml")
	keyPath := DefaultKeyPath(path)

	EmbeddedKey = strings.Repeat("cd", 32)
	t.Cleanup(func() { EmbeddedKey = "" })

	store := NewStoreWithKey(path, keyPath)
	if err := store.SetPanelPassword("password"); err != nil {
		t.Fatalf("SetPanelPassword: %v", err)
	}
	if _, err := store.Save(); err != nil {
		t.Fatalf("Save with embedded key: %v", err)
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("embedded key must not create a key file, stat err = %v", err)
	}
	if got := store.KeySource(); got != "embedded" {
		t.Fatalf("KeySource = %q", got)
	}

	loaded, err := Load(path, keyPath)
	if err != nil {
		t.Fatalf("Load with embedded key: %v", err)
	}
	if !loaded.VerifyPanelPassword("password") {
		t.Fatal("panel password not loaded with embedded key")
	}
	if loaded.NeedsRewrap() {
		t.Fatal("fresh embedded-key store must not need rewrap")
	}
}

func TestEmbeddedKeyMigratesLegacyFileKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wol.secrets.yaml")
	keyPath := DefaultKeyPath(path)

	EmbeddedKey = ""
	legacy := NewStoreWithKey(path, keyPath)
	if err := legacy.SetPanelPassword("password"); err != nil {
		t.Fatalf("SetPanelPassword: %v", err)
	}
	if _, err := legacy.Save(); err != nil {
		t.Fatalf("Save with file key: %v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("legacy key file missing: %v", err)
	}

	EmbeddedKey = strings.Repeat("ef", 32)
	t.Cleanup(func() { EmbeddedKey = "" })

	loaded, err := Load(path, keyPath)
	if err != nil {
		t.Fatalf("Load should fall back to legacy key: %v", err)
	}
	if !loaded.NeedsRewrap() {
		t.Fatal("store should need rewrap after legacy key fallback")
	}
	if !loaded.LegacyKeyFileExists() {
		t.Fatal("legacy key file should exist before retire")
	}
	if _, err := loaded.Save(); err != nil {
		t.Fatalf("rewrap Save: %v", err)
	}
	legacyPath, err := loaded.RetireFileKey()
	if err != nil {
		t.Fatalf("RetireFileKey: %v", err)
	}
	if legacyPath != keyPath+".legacy" {
		t.Fatalf("legacy path = %q", legacyPath)
	}
	if loaded.LegacyKeyFileExists() {
		t.Fatal("key file should be renamed after retire")
	}

	reloaded, err := Load(path, keyPath)
	if err != nil {
		t.Fatalf("Load rewrapped secrets: %v", err)
	}
	if !reloaded.VerifyPanelPassword("password") {
		t.Fatal("panel password lost after rewrap")
	}
	if reloaded.NeedsRewrap() {
		t.Fatal("rewrapped secrets still need rewrap")
	}
}
