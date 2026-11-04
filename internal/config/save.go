package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultConfigYAML = `# WOL 网络唤醒服务配置
# 本文件在首次启动且配置不存在时自动生成。
# 快速面板：http://<本机IP>:8090/
# 配置中心：http://<本机IP>:8090/config
#
# 访问密码、SSH 私钥等敏感值保存在同目录的 <配置名>.secrets.yaml。
# 默认开启配置面板且未设置访问密码，任何能访问该端口的人都能修改配置。
# 暴露到不可信网络前，请在 /config 的「信任凭据」中设置访问密码并勾选
# 需要密码的入口（快速面板/配置面板/API），或启用 HTTPS / 反向代理，
# 也可用可信网段限制来源地址。

server:
  listen: "8090"
  title: "网络唤醒"

config_panel:
  enabled: true

log:
  level: "info"
  format: "text"

probe:
  interval: "15s"
  timeout: "2s"

auth:
  quick_panel: false
  config_panel: true
  trusted_networks_enabled: false
  trusted_networks: []

devices: []
`

// ParseOrCreate parses path and creates a default configuration when the file
// does not exist. Validation is deferred to the caller.
func ParseOrCreate(path string) (*Config, bool, error) {
	cfg, err := Parse(path)
	if err == nil {
		return cfg, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, false, fmt.Errorf("create config directory: %w", err)
		}
	}
	if err := writeAtomic(path, []byte(defaultConfigYAML)); err != nil {
		return nil, false, fmt.Errorf("generate default config: %w", err)
	}
	cfg, err = Parse(path)
	if err != nil {
		return nil, true, fmt.Errorf("load generated config: %w", err)
	}
	return cfg, true, nil
}

// LoadOrCreate loads path, creates a default configuration if missing, and validates it.
func LoadOrCreate(path string) (*Config, bool, error) {
	cfg, created, err := ParseOrCreate(path)
	if err != nil {
		return nil, created, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, created, err
	}
	return cfg, created, nil
}

// Save validates cfg, backs up the current file and writes a new one atomically.
// It returns the backup path (empty when the original file did not exist).
func Save(path string, cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	cfg.Normalize()
	if err := cfg.Validate(); err != nil {
		return "", err
	}
	return Write(path, cfg)
}

// Write backs up the current file and writes cfg without validating it.
// Callers must validate the merged configuration (including secrets) first.
func Write(path string, cfg *Config) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("config is nil")
	}
	cfg.Normalize()
	data, err := marshalConfig(cfg)
	if err != nil {
		return "", err
	}

	backup := ""
	if existing, err := os.ReadFile(path); err == nil {
		if oldCfg, parseErr := parseBytes(existing); parseErr == nil {
			oldSanitized := oldCfg.WithoutSecrets()
			oldData, marshalErr := marshalConfig(&oldSanitized)
			if marshalErr == nil {
				backup = path + ".bak"
				backupData := append([]byte("# WOL configuration backup (credentials stripped)\n\n"), oldData...)
				if err := os.WriteFile(backup, backupData, 0o600); err != nil {
					return "", fmt.Errorf("write backup: %w", err)
				}
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read existing config: %w", err)
	}

	backupName := "none"
	if backup != "" {
		backupName = filepath.Base(backup)
	}
	header := fmt.Sprintf("# WOL configuration\n# Rewritten at %s\n# Previous configuration backup: %s\n# Secrets are stored separately; see the secrets file.\n\n",
		time.Now().Format(time.RFC3339), backupName)

	if err := writeAtomic(path, append([]byte(header), data...)); err != nil {
		return backup, err
	}
	return backup, nil
}

func marshalConfig(cfg *Config) ([]byte, error) {
	out := *cfg
	if strings.HasPrefix(out.Server.Listen, ":") && allDigits(strings.TrimPrefix(out.Server.Listen, ":")) {
		out.Server.Listen = strings.TrimPrefix(out.Server.Listen, ":")
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return data, nil
}

func Restore(path, backup string) error {
	if backup == "" {
		return fmt.Errorf("no backup available")
	}
	data, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wol-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}
