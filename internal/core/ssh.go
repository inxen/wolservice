package core

import (
	"fmt"
	"strings"

	"wol/internal/config"
	"wol/internal/sshpwr"
)

func (m *Manager) sshConfig(cfg config.Device) sshpwr.Config {
	return sshpwr.Config{
		Host:                cfg.SSH.Host,
		Port:                cfg.SSH.Port,
		User:                cfg.SSH.User,
		Password:            cfg.SSH.Password,
		PrivateKey:          cfg.SSH.PrivateKey,
		PrivateKeyFile:      config.ResolvePath(m.configPath, cfg.SSH.PrivateKeyFile),
		Passphrase:          cfg.SSH.Passphrase,
		HostKey:             cfg.SSH.HostKey,
		AllowUnknownHostKey: cfg.SSH.AllowUnknownHostKey,
		Timeout:             cfg.SSHTimeout(),
	}
}

func (m *Manager) runSSH(cfg config.Device, command string) (string, bool, error) {
	result, err := sshpwr.Run(m.baseContext(), m.sshConfig(cfg), command)
	output := strings.TrimSpace(result.Output)
	if err != nil {
		return output, result.Uncertain, err
	}
	return output, result.Uncertain, nil
}

// ProbeSSHHostKey retrieves a host key fingerprint using credentials from the
// config panel, before the device has been saved.
func (m *Manager) ProbeSSHHostKey(cfg config.SSHConfig) (HostKeyInfo, error) {
	if strings.TrimSpace(cfg.Host) == "" {
		return HostKeyInfo{}, fmt.Errorf("ssh.host is required")
	}
	if cfg.Port <= 0 || cfg.Port > 65535 {
		cfg.Port = 22
	}
	sshCfg := m.sshConfig(config.Device{SSH: cfg})
	info, err := sshpwr.Probe(m.baseContext(), sshCfg)
	if err != nil {
		return HostKeyInfo{}, err
	}
	return HostKeyInfo{
		Fingerprint: info.Fingerprint,
		Configured:  info.Configured,
		Trusted:     info.Trusted,
	}, nil
}
