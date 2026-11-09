package core

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"time"

	"wol/internal/config"
	"wol/internal/iot"
	"wol/internal/reach"
	"wol/internal/secrets"
	"wol/internal/sshpwr"
	"wol/internal/tplink"
	"wol/internal/wol"
)

const cameraStateTTL = 10 * time.Second

type Manager struct {
	log            *slog.Logger
	configPath     string
	secretsPath    string
	secretsKeyPath string

	mu        sync.RWMutex
	cfg       *config.Config
	secrets   *secrets.Store
	devices   map[string]*Device
	order     []string
	iot       *iot.Manager
	probeStop context.CancelFunc
	probeWG   sync.WaitGroup
	baseCtx   context.Context
	started   bool
	startedAt time.Time
	reloadMu  sync.Mutex
}

type Device struct {
	cfg config.Device
	m   *Manager

	mu          sync.RWMutex
	reachState  string
	reachProbes []reach.Probe
	lastProbe   time.Time
	probing     bool
	nextProbe   time.Time
	actionMu    sync.Mutex

	camera         *tplink.Client
	maskState      string
	maskError      string
	maskCheckedAt  time.Time
	maskRefreshing bool
}

func NewManager(log *slog.Logger, configPath string, cfg *config.Config) *Manager {
	secretsPath := secrets.DefaultPath(configPath)
	keyPath := secrets.DefaultKeyPath(secretsPath)
	store, err := secrets.Load(secretsPath, keyPath)
	if err != nil {
		log.Warn("load secrets file", "path", secretsPath, "error", err)
		store = secrets.NewStoreWithKey(secretsPath, keyPath)
	}
	if cfg != nil {
		if _, err := store.ImportLegacy(cfg); err != nil {
			log.Warn("import legacy secrets", "error", err)
		}
		store.ApplyToConfig(cfg)
	}
	return NewManagerWithSecrets(log, configPath, secretsPath, keyPath, cfg, store)
}

func NewManagerWithSecrets(log *slog.Logger, configPath, secretsPath, keyPath string, cfg *config.Config, store *secrets.Store) *Manager {
	if store == nil {
		store = secrets.NewStoreWithKey(secretsPath, keyPath)
	}
	m := &Manager{
		log:            log,
		configPath:     configPath,
		secretsPath:    secretsPath,
		secretsKeyPath: keyPath,
		secrets:        store,
		startedAt:      time.Now(),
	}
	m.applyConfig(cfg)
	return m
}

func (m *Manager) Start(ctx context.Context) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.baseCtx = ctx
	m.started = true
	m.mu.Unlock()
	m.startSubsystems(ctx)
}

func (m *Manager) Config() *config.Config {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Manager) Secrets() *secrets.Store {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.secrets
}

func (m *Manager) SecretsPath() string { return m.secretsPath }

func (m *Manager) SecretsKeyPath() string { return m.secretsKeyPath }

func (m *Manager) StartedAt() time.Time { return m.startedAt }

func (m *Manager) applyConfig(cfg *config.Config) {
	devices := make(map[string]*Device, len(cfg.Devices))
	order := make([]string, 0, len(cfg.Devices))
	for i := range cfg.Devices {
		device := &Device{
			cfg:        cfg.Devices[i],
			m:          m,
			reachState: reach.StateUnknown,
			nextProbe:  time.Now(),
		}
		devices[device.cfg.Key] = device
		order = append(order, device.cfg.Key)
	}
	m.mu.Lock()
	m.cfg = cfg
	m.devices = devices
	m.order = order
	m.mu.Unlock()
}

func (m *Manager) startSubsystems(ctx context.Context) {
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()

	probeCtx, probeStop := context.WithCancel(ctx)
	m.probeStop = probeStop
	m.probeWG.Add(1)
	go m.probeLoop(probeCtx)

	iotManager := iot.NewManager(m.log.With("component", "iot"))
	iotManager.Start(ctx, cfg.Devices, func(key string) iot.Actions {
		if device, ok := m.Device(key); ok && device.cfg.HasCamera() {
			return iot.Actions{
				Wake: func() error {
					_, err := m.SetMask(key, false)
					return err
				},
				Shutdown: func() error {
					_, err := m.SetMask(key, true)
					return err
				},
			}
		}
		return iot.Actions{
			Wake: func() error {
				_, err := m.Wake(key)
				return err
			},
			Shutdown: func() error {
				_, err := m.Shutdown(key)
				return err
			},
		}
	})
	m.mu.Lock()
	m.iot = iotManager
	m.mu.Unlock()
}

func (m *Manager) stopSubsystems() {
	m.mu.Lock()
	probeStop := m.probeStop
	iotManager := m.iot
	m.probeStop = nil
	m.iot = nil
	m.mu.Unlock()

	if probeStop != nil {
		probeStop()
		m.probeWG.Wait()
	}
	if iotManager != nil {
		iotManager.Stop()
	}
}

func (m *Manager) Stop() {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	m.mu.Lock()
	m.started = false
	m.mu.Unlock()
	m.stopSubsystems()
}

// Reload re-reads the configuration and secrets files and restarts affected subsystems.
// The HTTP listener itself is not restarted; listen/TLS changes need a restart.
func (m *Manager) Reload() error {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	cfg, store, _, migrated, err := LoadConfig(m.configPath, m.secretsPath, m.secretsKeyPath, true)
	if err != nil {
		return err
	}
	if migrated {
		m.log.Info("plaintext secrets migrated", "path", m.secretsPath)
	}
	current := m.Config()
	currentStore := m.Secrets()
	if current != nil && reflect.DeepEqual(current, cfg) && currentStore != nil && currentStore.Equal(store) {
		m.log.Debug("configuration unchanged, reload skipped")
		return nil
	}
	m.applyLoadedConfig(cfg, store)
	m.log.Info("configuration reloaded", "devices", len(cfg.Devices))
	return nil
}

// SaveConfig validates and writes the config and secrets files, then applies
// them. Reversible credentials are extracted into the secrets file; the main
// config never contains them. The previous files are kept as .bak backups.
func (m *Manager) SaveConfig(cfg *config.Config) (SaveResult, error) {
	m.reloadMu.Lock()
	defer m.reloadMu.Unlock()
	if cfg == nil {
		return SaveResult{}, fmt.Errorf("config is nil")
	}
	cfg.Normalize()
	beforeCfg := m.Config()
	beforeStore := m.Secrets()
	nextStore := beforeStore.Clone()

	if cfg.ConfigPanel.Password != "" {
		if err := nextStore.SetPanelPassword(cfg.ConfigPanel.Password); err != nil {
			return SaveResult{}, fmt.Errorf("hash config panel password: %w", err)
		}
	} else if cfg.ConfigPanel.ClearPassword {
		nextStore.ClearPanelPassword()
	}
	nextStore.ReplaceRuntimeSecretsFromConfig(cfg)

	sanitized := cfg.WithoutSecrets()
	merged := cfg.WithoutSecrets()
	nextStore.ApplyToConfig(&merged)
	if err := merged.Validate(); err != nil {
		return SaveResult{}, err
	}

	secretsChanged := !beforeStore.Equal(nextStore)
	secretsBackup := ""
	if secretsChanged {
		var err error
		secretsBackup, err = nextStore.Save()
		if err != nil {
			return SaveResult{}, err
		}
	}
	backup, err := config.Write(m.configPath, &sanitized)
	if err != nil {
		if secretsChanged {
			if _, restoreErr := beforeStore.Save(); restoreErr != nil {
				m.log.Error("restore secrets after failed config save", "error", restoreErr)
			}
		}
		return SaveResult{}, err
	}

	panelChanged := beforeStore.PanelPasswordHash() != nextStore.PanelPasswordHash()
	result := SaveResult{
		Backup:         backup,
		SecretsBackup:  secretsBackup,
		ConfigPath:     m.configPath,
		SecretsPath:    m.secretsPath,
		RestartFields:  restartFields(beforeCfg, &merged),
		ReauthRequired: panelChanged && nextStore.PanelPasswordSet(),
	}
	result.RestartRequired = len(result.RestartFields) > 0
	m.applyLoadedConfig(&merged, nextStore)
	m.log.Info("configuration saved", "devices", len(merged.Devices), "backup", backup)
	return result, nil
}

type SaveResult struct {
	Backup          string   `json:"backup,omitempty"`
	SecretsBackup   string   `json:"secrets_backup,omitempty"`
	ConfigPath      string   `json:"config_path"`
	SecretsPath     string   `json:"secrets_path"`
	RestartRequired bool     `json:"restart_required"`
	RestartFields   []string `json:"restart_fields,omitempty"`
	ReauthRequired  bool     `json:"reauth_required"`
}

func (m *Manager) ConfigPath() string { return m.configPath }

func (m *Manager) applyLoadedConfig(cfg *config.Config, store *secrets.Store) {
	m.mu.RLock()
	current := m.cfg
	currentStore := m.secrets
	ctx := m.baseCtx
	started := m.started
	m.mu.RUnlock()

	if current != nil &&
		reflect.DeepEqual(current.Devices, cfg.Devices) &&
		current.Probe == cfg.Probe &&
		currentStore != nil && currentStore.RuntimeEqual(store) {
		m.mu.Lock()
		m.cfg = cfg
		m.secrets = store
		m.mu.Unlock()
		return
	}

	m.stopSubsystems()
	m.applyConfig(cfg)
	m.mu.Lock()
	m.secrets = store
	m.mu.Unlock()
	if started && ctx != nil && ctx.Err() == nil {
		m.startSubsystems(ctx)
	}
}

func restartFields(before, after *config.Config) []string {
	if before == nil || after == nil {
		return nil
	}
	var fields []string
	if before.Server.Listen != after.Server.Listen {
		fields = append(fields, "server.listen")
	}
	if before.Server.TLSCert != after.Server.TLSCert || before.Server.TLSKey != after.Server.TLSKey {
		fields = append(fields, "server.tls_cert", "server.tls_key")
	}
	if before.Server.ReadTimeout != after.Server.ReadTimeout {
		fields = append(fields, "server.read_timeout")
	}
	if before.Server.ShutdownTimeout != after.Server.ShutdownTimeout {
		fields = append(fields, "server.shutdown_timeout")
	}
	if before.Log.Level != after.Log.Level || before.Log.Format != after.Log.Format {
		fields = append(fields, "log.level", "log.format")
	}
	return fields
}

func (m *Manager) probeLoop(ctx context.Context) {
	defer m.probeWG.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, device := range m.deviceSnapshot() {
				if device.cfg.HasCamera() {
					continue
				}
				if !device.cfg.Reachability.Enabled || len(device.cfg.Reachability.Targets) == 0 {
					continue
				}
				device.mu.Lock()
				due := !device.probing && (device.nextProbe.IsZero() || !now.Before(device.nextProbe))
				if due {
					device.probing = true
				}
				device.mu.Unlock()
				if !due {
					continue
				}
				m.probeWG.Add(1)
				go func(d *Device) {
					defer m.probeWG.Done()
					m.runProbe(ctx, d)
				}(device)
			}
		}
	}
}

func (m *Manager) runProbe(ctx context.Context, d *Device) {
	cfg := d.snapshot()
	global := m.Config().Probe
	timeout := cfg.ReachabilityTimeout(global.Timeout.Duration)
	result := reach.Check(ctx, cfg.Reachability.Targets, cfg.Reachability.Method, cfg.Reachability.TCPPort, timeout)
	interval := cfg.ReachabilityInterval(global.Interval.Duration)

	d.mu.Lock()
	d.reachState = result.State
	d.reachProbes = result.Probes
	d.lastProbe = time.Now()
	d.probing = false
	d.nextProbe = time.Now().Add(interval)
	d.mu.Unlock()
}

func (m *Manager) deviceSnapshot() []*Device {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Device, 0, len(m.order))
	for _, key := range m.order {
		if d, ok := m.devices[key]; ok {
			out = append(out, d)
		}
	}
	return out
}

func (m *Manager) Device(key string) (*Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[key]
	return d, ok
}

func (d *Device) snapshot() config.Device {
	return d.cfg
}

func (m *Manager) List() []Status {
	devices := m.deviceSnapshot()
	out := make([]Status, 0, len(devices))
	for _, d := range devices {
		out = append(out, m.statusOf(d))
	}
	return out
}

func (m *Manager) Status(key string) (Status, bool) {
	d, ok := m.Device(key)
	if !ok {
		return Status{}, false
	}
	return m.statusOf(d), true
}

func (m *Manager) Probe(key string) (Status, error) {
	d, ok := m.Device(key)
	if !ok {
		return Status{}, fmt.Errorf("device %q not found", key)
	}
	if d.cfg.HasCamera() {
		return m.statusOf(d), nil
	}
	if !d.cfg.Reachability.Enabled || len(d.cfg.Reachability.Targets) == 0 {
		return m.statusOf(d), nil
	}
	d.mu.Lock()
	if d.probing {
		d.mu.Unlock()
		return m.statusOf(d), nil
	}
	d.probing = true
	d.mu.Unlock()
	ctx := m.baseCtx
	if ctx == nil {
		ctx = context.Background()
	}
	m.runProbe(ctx, d)
	return m.statusOf(d), nil
}

func (m *Manager) statusOf(d *Device) Status {
	cfg := d.cfg

	d.mu.RLock()
	reachState := d.reachState
	if reachState == "" {
		reachState = reach.StateUnknown
	}
	probes := append([]reach.Probe(nil), d.reachProbes...)
	lastProbe := d.lastProbe
	maskState := d.maskState
	maskError := d.maskError
	maskRefreshDue := cfg.HasCamera() && !d.maskRefreshing &&
		(d.maskCheckedAt.IsZero() || time.Since(d.maskCheckedAt) >= cameraStateTTL)
	d.mu.RUnlock()

	if maskRefreshDue {
		go m.refreshCameraState(d)
	}

	// The wake action is intentionally always available: reachability can be
	// stale, and users may need to resend a magic packet while the host is
	// still shutting down or reporting a false positive.
	canWake := true
	canShutdown := cfg.HasSSHShutdown()

	wakeMode := "wol"
	if cfg.HasSSHWake() {
		wakeMode = "ssh"
	}
	shutdownMode := "none"
	if cfg.HasSSHShutdown() {
		shutdownMode = "ssh"
	}

	status := Status{
		Key:                 cfg.Key,
		Name:                cfg.Name,
		MACs:                append([]string(nil), cfg.MACs...),
		BroadcastIPs:        append([]string(nil), cfg.BroadcastIPs...),
		Port:                cfg.Port,
		Repeat:              cfg.Repeat,
		ReachabilityState:   reachState,
		ReachabilityProbes:  probes,
		CanWakeup:           canWake,
		CanShutdown:         canShutdown,
		WakeMode:            wakeMode,
		ShutdownMode:        shutdownMode,
		ReachabilityEnabled: cfg.Reachability.Enabled,
	}
	if cfg.HasCamera() {
		status.CanWakeup = false
		status.WakeMode = "none"
		status.CanShutdown = false
		status.ShutdownMode = "none"
		status.ReachabilityEnabled = false
		switch {
		case maskError != "":
			status.ReachabilityState = reach.StateUnreachable
		case maskState == "on" || maskState == "off":
			status.ReachabilityState = reach.StateReachable
		default:
			status.ReachabilityState = reach.StateUnknown
		}
		status.CameraEnabled = true
		status.CameraHost = cfg.Camera.Host
		status.CameraPort = cfg.Camera.Port
		status.MaskState = maskState
		if status.MaskState == "" {
			status.MaskState = "unknown"
		}
		status.MaskError = maskError
	}
	if !lastProbe.IsZero() {
		status.LastProbe = &lastProbe
	}
	m.mu.RLock()
	iotManager := m.iot
	m.mu.RUnlock()
	if iotManager != nil {
		status.IoT = iotManager.Status(cfg.Key)
	}
	return status
}

type Status struct {
	Key                 string           `json:"key"`
	Name                string           `json:"name"`
	MACs                []string         `json:"macs"`
	BroadcastIPs        []string         `json:"broadcast_ips,omitempty"`
	Port                int              `json:"port"`
	Repeat              int              `json:"repeat"`
	ReachabilityEnabled bool             `json:"reachability_enabled"`
	ReachabilityState   string           `json:"reachability_state"`
	ReachabilityProbes  []reach.Probe    `json:"reachability_probes,omitempty"`
	LastProbe           *time.Time       `json:"last_probe,omitempty"`
	CanWakeup           bool             `json:"can_wakeup"`
	CanShutdown         bool             `json:"can_shutdown"`
	WakeMode            string           `json:"wake_mode"`
	ShutdownMode        string           `json:"shutdown_mode"`
	CameraEnabled       bool             `json:"camera_enabled,omitempty"`
	CameraHost          string           `json:"camera_host,omitempty"`
	CameraPort          int              `json:"camera_port,omitempty"`
	MaskState           string           `json:"mask_state,omitempty"`
	MaskError           string           `json:"mask_error,omitempty"`
	IoT                 iot.DeviceStatus `json:"iot,omitempty"`
}

type WakeResult struct {
	Mode       string   `json:"mode"`
	Packets    int      `json:"packets,omitempty"`
	Broadcasts []string `json:"broadcasts,omitempty"`
	Output     string   `json:"output,omitempty"`
	Uncertain  bool     `json:"uncertain,omitempty"`
	Warnings   []string `json:"warnings,omitempty"`
}

type ShutdownResult struct {
	Mode      string   `json:"mode"`
	Output    string   `json:"output,omitempty"`
	Uncertain bool     `json:"uncertain,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

func (m *Manager) Wake(key string) (WakeResult, error) {
	d, ok := m.Device(key)
	if !ok {
		return WakeResult{}, fmt.Errorf("device %q not found", key)
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()
	cfg := d.cfg
	if cfg.HasCamera() {
		return WakeResult{}, fmt.Errorf("camera device %q does not support wake", key)
	}
	var result WakeResult
	var warnings []string

	if cfg.HasSSHWake() {
		output, uncertain, err := m.runSSH(cfg, cfg.SSH.WakeCommand)
		if err == nil {
			result.Mode = "ssh"
			result.Output = output
			result.Uncertain = uncertain
			result.Warnings = warnings
			m.log.Info("device wake via ssh", "device", key, "uncertain", uncertain)
			return result, nil
		}
		warnings = append(warnings, fmt.Sprintf("SSH 唤醒失败，尝试局域网唤醒: %v", err))
		m.log.Warn("ssh wake failed, falling back to magic packet", "device", key, "error", err)
	}

	sendResult, err := wol.Send(cfg.MACs, cfg.BroadcastIPs, cfg.Port, cfg.Repeat)
	if err != nil {
		warnings = append(warnings, fmt.Sprintf("局域网唤醒包发送失败: %v", err))
	} else {
		result.Mode = "wol"
		result.Packets = sendResult.Packets
		result.Broadcasts = sendResult.Broadcasts
	}

	result.Warnings = warnings
	if result.Mode == "" {
		if len(warnings) > 0 {
			return result, fmt.Errorf("%s", strings.Join(warnings, "; "))
		}
		return result, fmt.Errorf("no wake method succeeded")
	}
	m.log.Info("device wake", "device", key, "mode", result.Mode, "packets", result.Packets)
	return result, nil
}

func (m *Manager) Shutdown(key string) (ShutdownResult, error) {
	d, ok := m.Device(key)
	if !ok {
		return ShutdownResult{}, fmt.Errorf("device %q not found", key)
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()
	cfg := d.cfg
	if cfg.HasCamera() {
		return ShutdownResult{}, fmt.Errorf("camera device %q does not support shutdown", key)
	}
	var result ShutdownResult
	var warnings []string

	if cfg.HasSSHShutdown() {
		output, uncertain, err := m.runSSH(cfg, cfg.SSH.ShutdownCommand)
		if err == nil {
			result.Mode = "ssh"
			result.Output = output
			result.Uncertain = uncertain
			result.Warnings = warnings
			m.log.Info("device shutdown via ssh", "device", key, "uncertain", uncertain)
			return result, nil
		}
		warnings = append(warnings, fmt.Sprintf("SSH 关机失败: %v", err))
		m.log.Warn("ssh shutdown failed", "device", key, "error", err)
	}

	result.Warnings = warnings
	if !cfg.HasSSHShutdown() {
		return result, fmt.Errorf("设备未配置 SSH 关机")
	}
	if len(warnings) > 0 {
		return result, fmt.Errorf("%s", strings.Join(warnings, "; "))
	}
	return result, fmt.Errorf("没有可用的关机方式")
}

func (m *Manager) ProbeHostKey(key string) (HostKeyInfo, error) {
	d, ok := m.Device(key)
	if !ok {
		return HostKeyInfo{}, fmt.Errorf("device %q not found", key)
	}
	cfg := d.cfg
	if !cfg.SSH.Enabled {
		return HostKeyInfo{}, fmt.Errorf("device %q does not have ssh enabled", key)
	}
	sshCfg := m.sshConfig(cfg)
	info, err := sshpwr.Probe(m.baseContext(), sshCfg)
	if err != nil {
		return HostKeyInfo{}, err
	}
	return HostKeyInfo{
		Fingerprint: info.Fingerprint,
		Configured:  cfg.SSH.HostKey,
		Trusted:     info.Trusted,
	}, nil
}

type HostKeyInfo struct {
	Fingerprint string `json:"fingerprint"`
	Configured  string `json:"configured,omitempty"`
	Trusted     bool   `json:"trusted"`
}

func (m *Manager) cameraDevice(key string) (*Device, error) {
	d, ok := m.Device(key)
	if !ok {
		return nil, fmt.Errorf("device %q not found", key)
	}
	if !d.cfg.HasCamera() {
		return nil, fmt.Errorf("device %q does not have a camera configured", key)
	}
	return d, nil
}

func (d *Device) cameraClient() *tplink.Client {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.camera == nil {
		cfg := d.cfg.Camera
		d.camera = tplink.New(cfg.Host, cfg.Port, cfg.Username, cfg.Password)
	}
	return d.camera
}

func (d *Device) applyMaskResult(masked bool, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.maskCheckedAt = time.Now()
	if err != nil {
		d.maskError = err.Error()
		return
	}
	d.maskError = ""
	if masked {
		d.maskState = "on"
	} else {
		d.maskState = "off"
	}
}

func (m *Manager) refreshCameraState(d *Device) {
	d.mu.Lock()
	if d.maskRefreshing {
		d.mu.Unlock()
		return
	}
	d.maskRefreshing = true
	d.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.baseContext(), 15*time.Second)
	masked, err := d.cameraClient().GetMask(ctx)
	cancel()
	d.applyMaskResult(masked, err)

	d.mu.Lock()
	d.maskRefreshing = false
	d.mu.Unlock()
}

// SetMask enables or disables lens masking and returns the refreshed status.
func (m *Manager) SetMask(key string, masked bool) (Status, error) {
	d, err := m.cameraDevice(key)
	if err != nil {
		return Status{}, err
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()

	ctx, cancel := context.WithTimeout(m.baseContext(), 15*time.Second)
	err = d.cameraClient().SetMask(ctx, masked)
	cancel()
	if err != nil {
		d.mu.Lock()
		d.maskError = err.Error()
		d.mu.Unlock()
		return m.statusOf(d), err
	}
	d.applyMaskResult(masked, nil)
	m.log.Info("camera lens mask updated", "device", key, "masked", masked)
	return m.statusOf(d), nil
}

// RefreshMask reads the live lens mask state from the camera.
func (m *Manager) RefreshMask(key string) (Status, error) {
	d, err := m.cameraDevice(key)
	if err != nil {
		return Status{}, err
	}
	d.actionMu.Lock()
	defer d.actionMu.Unlock()

	ctx, cancel := context.WithTimeout(m.baseContext(), 15*time.Second)
	masked, err := d.cameraClient().GetMask(ctx)
	cancel()
	d.applyMaskResult(masked, err)
	return m.statusOf(d), err
}

func (m *Manager) baseContext() context.Context {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.baseCtx == nil {
		return context.Background()
	}
	return m.baseCtx
}
