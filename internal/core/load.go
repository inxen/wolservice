package core

import (
	"fmt"
	"log/slog"

	"wol/internal/config"
	"wol/internal/secrets"
)

// LoadConfig parses the main config, loads the encrypted secrets file, migrates
// legacy plaintext credentials when requested, and validates the merged result.
func LoadConfig(configPath, secretsPath, keyPath string, migrate bool) (*config.Config, *secrets.Store, bool, bool, error) {
	if secretsPath == "" {
		secretsPath = secrets.DefaultPath(configPath)
	}
	if keyPath == "" {
		keyPath = secrets.DefaultKeyPath(secretsPath)
	}
	cfg, created, err := config.ParseOrCreate(configPath)
	if err != nil {
		return nil, nil, created, false, err
	}
	store, err := secrets.Load(secretsPath, keyPath)
	if err != nil {
		return nil, nil, created, false, err
	}
	legacy := hasLegacySecrets(cfg)
	imported, err := store.ImportLegacy(cfg)
	if err != nil {
		return nil, nil, created, false, err
	}
	needsEncryption := store.NeedsEncryption()
	needsRewrap := store.NeedsRewrap()
	store.ApplyToConfig(cfg)
	if err := cfg.Validate(); err != nil {
		return nil, nil, created, false, err
	}

	migrated := false
	if migrate && (imported || legacy || needsEncryption || needsRewrap) {
		if imported || needsEncryption || needsRewrap {
			if _, err := store.Save(); err != nil {
				slog.Default().Error("save migrated secrets", "path", secretsPath, "error", err)
				return cfg, store, created, false, nil
			}
			migrated = true
			if needsRewrap {
				if legacy, err := store.RetireFileKey(); err != nil {
					slog.Default().Warn("retire legacy secrets key", "path", store.KeyPath(), "error", err)
				} else if legacy != "" {
					slog.Default().Warn("secrets re-encrypted with embedded key; legacy key file renamed", "path", legacy)
				}
			}
		}
		if legacy {
			sanitized := cfg.WithoutSecrets()
			if _, err := config.Write(configPath, &sanitized); err != nil {
				slog.Default().Error("write sanitized config after migration", "path", configPath, "error", err)
			} else {
				migrated = true
			}
		}
	}
	return cfg, store, created, migrated, nil
}

func hasLegacySecrets(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	if cfg.ConfigPanel.Password != "" {
		return true
	}
	for i := range cfg.Devices {
		device := cfg.Devices[i]
		if device.SSH.Password != "" || device.SSH.PrivateKey != "" || device.SSH.Passphrase != "" ||
			device.IoT.Bemfa.SecretKey != "" || device.IoT.Blinker.AuthKey != "" {
			return true
		}
	}
	return false
}

func ValidateConfig(configPath, secretsPath, keyPath string) error {
	_, _, _, _, err := LoadConfig(configPath, secretsPath, keyPath, false)
	if err != nil {
		return fmt.Errorf("configuration error: %w", err)
	}
	return nil
}
