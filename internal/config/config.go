package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	DefaultListen          = ":8090"
	DefaultPort            = 9
	DefaultRepeat          = 5
	DefaultProbeInterval   = 15 * time.Second
	DefaultProbeTimeout    = 2 * time.Second
	DefaultSSHTimeout      = 10 * time.Second
	DefaultShutdownTimeout = 15 * time.Second

	DeviceTypeComputer     = "computer"
	DeviceTypeTPLinkCamera = "tplink-camera"
)

type Config struct {
	Server      ServerConfig      `yaml:"server" json:"server"`
	ConfigPanel ConfigPanelConfig `yaml:"config_panel" json:"config_panel"`
	Log         LogConfig         `yaml:"log" json:"log"`
	Probe       ProbeConfig       `yaml:"probe" json:"probe"`
	Devices     []Device          `yaml:"devices" json:"devices"`
	Auth        *AuthConfig       `yaml:"auth,omitempty" json:"auth"`
}

type AuthConfig struct {
	QuickPanel  bool `yaml:"quick_panel" json:"quick_panel"`
	ConfigPanel bool `yaml:"config_panel" json:"config_panel"`
	// Deprecated: API protection follows QuickPanel; retained so older configs still parse.
	API                    bool     `yaml:"api,omitempty" json:"api,omitempty"`
	TrustedNetworksEnabled bool     `yaml:"trusted_networks_enabled" json:"trusted_networks_enabled"`
	TrustedNetworks        []string `yaml:"trusted_networks,omitempty" json:"trusted_networks"`
}

type ConfigPanelConfig struct {
	Enabled       bool   `yaml:"enabled" json:"enabled"`
	Password      string `yaml:"password,omitempty" json:"password"`
	ClearPassword bool   `yaml:"-" json:"clear_password,omitempty"`
}

type ServerConfig struct {
	Listen          string   `yaml:"listen" json:"listen"`
	Title           string   `yaml:"title" json:"title"`
	TLSCert         string   `yaml:"tls_cert,omitempty" json:"tls_cert"`
	TLSKey          string   `yaml:"tls_key,omitempty" json:"tls_key"`
	ReadTimeout     Duration `yaml:"read_timeout,omitempty" json:"read_timeout"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout,omitempty" json:"shutdown_timeout"`
}

type LogConfig struct {
	Level  string `yaml:"level" json:"level"`
	Format string `yaml:"format" json:"format"`
}

type ProbeConfig struct {
	Interval Duration `yaml:"interval" json:"interval"`
	Timeout  Duration `yaml:"timeout" json:"timeout"`
}

type Device struct {
	Key          string             `yaml:"key" json:"key"`
	Type         string             `yaml:"type" json:"type"`
	SecretRef    string             `yaml:"secret_ref,omitempty" json:"secret_ref"`
	Name         string             `yaml:"name" json:"name"`
	MACs         []string           `yaml:"macs" json:"macs"`
	BroadcastIPs []string           `yaml:"broadcast_ips,omitempty" json:"broadcast_ips"`
	Port         int                `yaml:"port" json:"port"`
	Repeat       int                `yaml:"repeat" json:"repeat"`
	Reachability ReachabilityConfig `yaml:"reachability" json:"reachability"`
	SSH          SSHConfig          `yaml:"ssh" json:"ssh"`
	IoT          IoTConfig          `yaml:"iot" json:"iot"`
	Camera       CameraConfig       `yaml:"camera" json:"camera"`
	ClearSecrets bool               `yaml:"-" json:"clear_secrets,omitempty"`
}

type CameraConfig struct {
	// Enabled 由 type 推导，保留用于兼容旧配置和配置面板提交的数据。
	Enabled  bool   `yaml:"enabled,omitempty" json:"enabled"`
	Host     string `yaml:"host,omitempty" json:"host"`
	Port     int    `yaml:"port" json:"port"`
	Username string `yaml:"username,omitempty" json:"username"`
	Password string `yaml:"password,omitempty" json:"password"`
}

type ReachabilityConfig struct {
	Enabled  bool     `yaml:"enabled" json:"enabled"`
	Targets  []string `yaml:"targets,omitempty" json:"targets"`
	Method   string   `yaml:"method" json:"method"`
	TCPPort  int      `yaml:"tcp_port,omitempty" json:"tcp_port"`
	Interval Duration `yaml:"interval,omitempty" json:"interval"`
	Timeout  Duration `yaml:"timeout,omitempty" json:"timeout"`
}

type SSHConfig struct {
	Enabled             bool     `yaml:"enabled" json:"enabled"`
	Host                string   `yaml:"host,omitempty" json:"host"`
	Port                int      `yaml:"port" json:"port"`
	User                string   `yaml:"user,omitempty" json:"user"`
	Password            string   `yaml:"password,omitempty" json:"password"`
	PrivateKey          string   `yaml:"private_key,omitempty" json:"private_key"`
	PrivateKeyFile      string   `yaml:"private_key_file,omitempty" json:"private_key_file"`
	Passphrase          string   `yaml:"passphrase,omitempty" json:"passphrase"`
	HostKey             string   `yaml:"host_key,omitempty" json:"host_key"`
	AllowUnknownHostKey bool     `yaml:"allow_unknown_host_key,omitempty" json:"allow_unknown_host_key"`
	ShutdownCommand     string   `yaml:"shutdown_command,omitempty" json:"shutdown_command"`
	WakeCommand         string   `yaml:"wake_command,omitempty" json:"wake_command"`
	Timeout             Duration `yaml:"timeout,omitempty" json:"timeout"`
}

type IoTConfig struct {
	Bemfa   BemfaConfig   `yaml:"bemfa" json:"bemfa"`
	Blinker BlinkerConfig `yaml:"blinker" json:"blinker"`
}

type BemfaConfig struct {
	Enabled   bool   `yaml:"enabled" json:"enabled"`
	SecretKey string `yaml:"secret_key,omitempty" json:"secret_key"`
	Topic     string `yaml:"topic,omitempty" json:"topic"`
}

type BlinkerConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	AuthKey    string `yaml:"auth_key,omitempty" json:"auth_key"`
	DeviceType string `yaml:"device_type" json:"device_type"`
}

var keySanitizer = regexp.MustCompile(`[^a-z0-9_-]+`)

// Parse reads and normalizes the configuration file without validating it.
// Validation is deferred until secrets have been merged in.
func Parse(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return parseBytes(raw)
}

func parseBytes(raw []byte) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	cfg.Normalize()
	return &cfg, nil
}

// Load reads, normalizes and validates the configuration file.
func Load(path string) (*Config, error) {
	cfg, err := Parse(path)
	if err != nil {
		return nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// WithoutSecrets returns a copy safe to write to the main configuration file.
// Reversible credentials live in the separate secrets file.
func (c Config) WithoutSecrets() Config {
	out := c
	out.ConfigPanel.Password = ""
	out.ConfigPanel.ClearPassword = false
	// make+copy 而不是 append(nil, ...)：零设备时后者会得到 nil，
	// 接口就返回 "devices": null，前端 push 第一个设备时直接报错。
	out.Devices = make([]Device, len(c.Devices))
	copy(out.Devices, c.Devices)
	for i := range out.Devices {
		out.Devices[i].SSH.Password = ""
		out.Devices[i].SSH.PrivateKey = ""
		out.Devices[i].SSH.Passphrase = ""
		out.Devices[i].IoT.Bemfa.SecretKey = ""
		out.Devices[i].IoT.Blinker.AuthKey = ""
		out.Devices[i].Camera.Password = ""
		out.Devices[i].ClearSecrets = false
	}
	return out
}

func (c *Config) Normalize() {
	listen := strings.TrimSpace(c.Server.Listen)
	if listen == "" {
		listen = DefaultListen
	} else if allDigits(listen) {
		listen = ":" + listen
	}
	c.Server.Listen = listen
	if strings.TrimSpace(c.Server.Title) == "" {
		c.Server.Title = "网络唤醒"
	}
	if c.Server.ReadTimeout.Duration <= 0 {
		c.Server.ReadTimeout.Duration = 15 * time.Second
	}
	if c.Server.ShutdownTimeout.Duration <= 0 {
		c.Server.ShutdownTimeout.Duration = DefaultShutdownTimeout
	}
	if c.Probe.Interval.Duration <= 0 {
		c.Probe.Interval.Duration = DefaultProbeInterval
	}
	if c.Probe.Timeout.Duration <= 0 {
		c.Probe.Timeout.Duration = DefaultProbeTimeout
	}
	if strings.TrimSpace(c.Log.Level) == "" {
		c.Log.Level = "info"
	}
	c.Log.Level = strings.ToLower(strings.TrimSpace(c.Log.Level))
	if strings.TrimSpace(c.Log.Format) == "" {
		c.Log.Format = "text"
	}
	c.Log.Format = strings.ToLower(strings.TrimSpace(c.Log.Format))
	if c.Devices == nil {
		c.Devices = []Device{}
	}
	if c.Auth == nil {
		c.Auth = &AuthConfig{ConfigPanel: true}
	}
	if c.Auth.API && !c.Auth.QuickPanel {
		c.Auth.QuickPanel = true
	}
	c.Auth.API = false
	for i := range c.Auth.TrustedNetworks {
		c.Auth.TrustedNetworks[i] = strings.TrimSpace(c.Auth.TrustedNetworks[i])
	}

	seen := make(map[string]int, len(c.Devices))
	for i := range c.Devices {
		d := &c.Devices[i]
		explicitSecretRef := strings.TrimSpace(d.SecretRef) != ""
		c.normalizeDevice(d)
		if prev, ok := seen[d.Key]; ok {
			d.Key = fmt.Sprintf("%s-%d", d.Key, prev+2)
			if !explicitSecretRef {
				d.SecretRef = d.Key
			}
		}
		seen[d.Key]++
	}
}

func (c *Config) normalizeDevice(d *Device) {
	d.Key = strings.TrimSpace(d.Key)
	d.Name = strings.TrimSpace(d.Name)
	for i := range d.MACs {
		d.MACs[i] = normalizeMAC(d.MACs[i])
	}
	for i := range d.BroadcastIPs {
		d.BroadcastIPs[i] = strings.TrimSpace(d.BroadcastIPs[i])
	}
	if d.Key == "" {
		base := d.Name
		if base == "" && len(d.MACs) > 0 {
			base = d.MACs[0]
		}
		base = keySanitizer.ReplaceAllString(strings.ToLower(strings.ReplaceAll(base, " ", "-")), "")
		base = strings.Trim(base, "-_")
		if base == "" {
			base = "device"
		}
		suffix := make([]byte, 2)
		if _, err := rand.Read(suffix); err == nil {
			d.Key = base + "-" + hex.EncodeToString(suffix)
		} else {
			d.Key = base
		}
	}
	if d.Name == "" {
		d.Name = d.Key
	}
	d.Type = strings.ToLower(strings.TrimSpace(d.Type))
	switch d.Type {
	case "":
		if d.Camera.Enabled {
			d.Type = DeviceTypeTPLinkCamera
		} else {
			d.Type = DeviceTypeComputer
		}
	case DeviceTypeComputer:
		d.Camera.Enabled = false
	case DeviceTypeTPLinkCamera:
		d.Camera.Enabled = true
		d.Reachability.Enabled = false
		d.SSH.Enabled = false
	}
	d.SecretRef = strings.TrimSpace(d.SecretRef)
	if d.SecretRef == "" {
		d.SecretRef = d.Key
	}
	if d.Port <= 0 || d.Port > 65535 {
		d.Port = DefaultPort
	}
	if d.Repeat <= 0 || d.Repeat > 10 {
		d.Repeat = DefaultRepeat
	}
	if strings.TrimSpace(d.Reachability.Method) == "" {
		d.Reachability.Method = "ping"
	}
	d.Reachability.Method = strings.ToLower(strings.TrimSpace(d.Reachability.Method))
	for i := range d.Reachability.Targets {
		d.Reachability.Targets[i] = strings.TrimSpace(d.Reachability.Targets[i])
	}
	if d.SSH.Port <= 0 || d.SSH.Port > 65535 {
		d.SSH.Port = 22
	}
	d.SSH.Host = strings.TrimSpace(d.SSH.Host)
	d.SSH.User = strings.TrimSpace(d.SSH.User)
	d.SSH.HostKey = strings.TrimSpace(d.SSH.HostKey)
	d.SSH.ShutdownCommand = strings.TrimSpace(d.SSH.ShutdownCommand)
	d.SSH.WakeCommand = strings.TrimSpace(d.SSH.WakeCommand)
	if d.SSH.Host == "" && len(d.Reachability.Targets) > 0 {
		d.SSH.Host = d.Reachability.Targets[0]
	}
	if d.SSH.Enabled {
		if len(d.Reachability.Targets) == 0 && d.SSH.Host != "" {
			d.Reachability.Targets = []string{d.SSH.Host}
		}
		if d.Reachability.Method == "" {
			d.Reachability.Method = "ping"
		}
	}
	if strings.TrimSpace(d.IoT.Blinker.DeviceType) == "" {
		d.IoT.Blinker.DeviceType = "outlet"
	}
	d.IoT.Blinker.DeviceType = strings.ToLower(strings.TrimSpace(d.IoT.Blinker.DeviceType))
	d.IoT.Bemfa.Topic = strings.TrimSpace(d.IoT.Bemfa.Topic)
	d.IoT.Bemfa.SecretKey = strings.TrimSpace(d.IoT.Bemfa.SecretKey)
	d.IoT.Blinker.AuthKey = strings.TrimSpace(d.IoT.Blinker.AuthKey)
	d.Camera.Host = strings.TrimSpace(d.Camera.Host)
	d.Camera.Username = strings.TrimSpace(d.Camera.Username)
	if d.Camera.Port <= 0 || d.Camera.Port > 65535 {
		d.Camera.Port = 80
	}
	if d.Camera.Username == "" {
		d.Camera.Username = "admin"
	}
	if d.MACs == nil {
		d.MACs = []string{}
	}
	if d.BroadcastIPs == nil {
		d.BroadcastIPs = []string{}
	}
	if d.Reachability.Targets == nil {
		d.Reachability.Targets = []string{}
	}
}

func (c *Config) Validate() error {
	var problems []string
	if err := checkListen(c.Server.Listen); err != nil {
		problems = append(problems, fmt.Sprintf("server.listen %q %s", c.Server.Listen, err.Error()))
	}
	if (c.Server.TLSCert == "") != (c.Server.TLSKey == "") {
		problems = append(problems, "server.tls_cert and server.tls_key must be set together")
	}
	if c.Auth != nil {
		for _, network := range c.Auth.TrustedNetworks {
			if !validNetwork(network) {
				problems = append(problems, fmt.Sprintf("auth.trusted_networks: invalid network %q", network))
			}
		}
		if c.Auth.TrustedNetworksEnabled && len(c.Auth.TrustedNetworks) == 0 {
			problems = append(problems, "auth.trusted_networks is required when trusted networks are enabled")
		}
	}
	bemfaTopics := make(map[string]string)
	blinkerKeys := make(map[string]string)
	secretRefs := make(map[string]string)
	for i := range c.Devices {
		d := &c.Devices[i]
		prefix := fmt.Sprintf("devices[%d] (%s)", i, d.Key)
		switch d.Type {
		case DeviceTypeComputer, DeviceTypeTPLinkCamera:
		default:
			problems = append(problems, fmt.Sprintf("%s: type must be %q or %q", prefix, DeviceTypeComputer, DeviceTypeTPLinkCamera))
		}
		if previous, exists := secretRefs[d.SecretRef]; exists {
			problems = append(problems, fmt.Sprintf("%s: secret_ref %q is already used by %s", prefix, d.SecretRef, previous))
		} else {
			secretRefs[d.SecretRef] = d.Key
		}
		if len(d.MACs) == 0 && !d.HasCamera() {
			problems = append(problems, prefix+": macs must not be empty")
		}
		if !d.HasCamera() {
			for _, mac := range d.MACs {
				if !validMAC(mac) {
					problems = append(problems, fmt.Sprintf("%s: invalid mac %q", prefix, mac))
				}
			}
			for _, ip := range d.BroadcastIPs {
				parsed := net.ParseIP(ip)
				if parsed == nil || parsed.To4() == nil {
					problems = append(problems, fmt.Sprintf("%s: broadcast ip %q must be IPv4", prefix, ip))
				}
			}
		}
		if strings.ContainsAny(d.Key, "/?#") {
			problems = append(problems, prefix+": key must not contain /, ? or #")
		}
		if d.Reachability.Enabled && !d.HasCamera() {
			if len(d.Reachability.Targets) == 0 {
				problems = append(problems, prefix+": reachability.targets must not be empty when enabled")
			}
			switch d.Reachability.Method {
			case "ping", "auto":
			case "tcp":
				if d.Reachability.TCPPort <= 0 || d.Reachability.TCPPort > 65535 {
					problems = append(problems, prefix+": reachability.tcp_port is required for method tcp")
				}
			default:
				problems = append(problems, prefix+": reachability.method must be ping, tcp or auto")
			}
			for _, target := range d.Reachability.Targets {
				if !validHost(target) {
					problems = append(problems, fmt.Sprintf("%s: invalid reachability target %q", prefix, target))
				}
			}
		}
		if d.SSH.Enabled && !d.HasCamera() {
			if d.SSH.Host == "" {
				problems = append(problems, prefix+": ssh.host is required")
			} else if !validHost(d.SSH.Host) {
				problems = append(problems, fmt.Sprintf("%s: invalid ssh.host %q", prefix, d.SSH.Host))
			}
			if d.SSH.User == "" {
				problems = append(problems, prefix+": ssh.user is required")
			}
			if d.SSH.Password == "" && d.SSH.PrivateKey == "" && d.SSH.PrivateKeyFile == "" {
				problems = append(problems, prefix+": ssh requires password or private key")
			}
			if d.SSH.ShutdownCommand == "" && d.SSH.WakeCommand == "" {
				problems = append(problems, prefix+": ssh.shutdown_command or ssh.wake_command is required")
			}
			if !d.SSH.AllowUnknownHostKey && d.SSH.HostKey == "" {
				problems = append(problems, prefix+": ssh.host_key is required (or set allow_unknown_host_key: true)")
			}
		}
		if d.IoT.Bemfa.Enabled {
			if d.IoT.Bemfa.SecretKey == "" {
				problems = append(problems, prefix+": iot.bemfa.secret_key is required")
			}
			if d.IoT.Bemfa.Topic == "" {
				problems = append(problems, prefix+": iot.bemfa.topic is required")
			} else if !strings.HasSuffix(d.IoT.Bemfa.Topic, "001") {
				problems = append(problems, prefix+": iot.bemfa.topic must end with 001 (switch device)")
			} else if previous, exists := bemfaTopics[d.IoT.Bemfa.Topic]; exists {
				problems = append(problems, fmt.Sprintf("%s: iot.bemfa.topic %q is already used by %s", prefix, d.IoT.Bemfa.Topic, previous))
			} else {
				bemfaTopics[d.IoT.Bemfa.Topic] = d.Key
			}
		}
		if d.IoT.Blinker.Enabled {
			if d.IoT.Blinker.AuthKey == "" {
				problems = append(problems, prefix+": iot.blinker.auth_key is required")
			} else if previous, exists := blinkerKeys[d.IoT.Blinker.AuthKey]; exists {
				problems = append(problems, fmt.Sprintf("%s: iot.blinker.auth_key is already used by %s", prefix, previous))
			} else {
				blinkerKeys[d.IoT.Blinker.AuthKey] = d.Key
			}
		}
		if d.HasCamera() {
			if d.Camera.Host == "" {
				problems = append(problems, prefix+": camera.host is required")
			} else if !validHost(d.Camera.Host) {
				problems = append(problems, fmt.Sprintf("%s: invalid camera.host %q", prefix, d.Camera.Host))
			}
			if d.Camera.Password == "" {
				problems = append(problems, prefix+": camera.password is required")
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("invalid config:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return nil
}

func (d Device) ReachabilityInterval(global time.Duration) time.Duration {
	return d.Reachability.Interval.Or(global)
}

func (d Device) ReachabilityTimeout(global time.Duration) time.Duration {
	return d.Reachability.Timeout.Or(global)
}

func (d Device) SSHTimeout() time.Duration {
	return d.SSH.Timeout.Or(DefaultSSHTimeout)
}

func (d Device) HasSSHShutdown() bool {
	return d.SSH.Enabled && d.SSH.ShutdownCommand != ""
}

func (d Device) HasSSHWake() bool {
	return d.SSH.Enabled && d.SSH.WakeCommand != ""
}

func (d Device) HasBemfa() bool {
	return d.IoT.Bemfa.Enabled
}

func (d Device) HasBlinker() bool {
	return d.IoT.Blinker.Enabled
}

func (d Device) HasCamera() bool {
	return d.Type == DeviceTypeTPLinkCamera
}

func validMAC(mac string) bool {
	hw, err := net.ParseMAC(mac)
	return err == nil && len(hw) == 6
}

func normalizeMAC(mac string) string {
	hw, err := net.ParseMAC(strings.TrimSpace(mac))
	if err != nil {
		return strings.TrimSpace(mac)
	}
	return strings.ToUpper(hw.String())
}

var hostPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]*[A-Za-z0-9])?$`)

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validNetwork(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	if _, _, err := net.ParseCIDR(value); err == nil {
		return true
	}
	return net.ParseIP(value) != nil
}

// NetworkContains reports whether ip is inside network (CIDR or single IP).
func NetworkContains(network, ip string) bool {
	addr := net.ParseIP(strings.TrimSpace(ip))
	if addr == nil {
		return false
	}
	network = strings.TrimSpace(network)
	if _, block, err := net.ParseCIDR(network); err == nil {
		return block.Contains(addr)
	}
	if single := net.ParseIP(network); single != nil {
		return single.Equal(addr)
	}
	return false
}

func checkListen(listen string) error {
	host, portText, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("must be a numeric port or host:port (e.g. \"8090\" or \":8090\")")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || !allDigits(portText) || port < 0 || port > 65535 {
		return fmt.Errorf("port must be a number between 0 and 65535")
	}
	if host != "" && !validHost(host) {
		return fmt.Errorf("host %q is invalid", host)
	}
	return nil
}

func validHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return len(host) <= 253 && hostPattern.MatchString(host)
}

// ResolvePath resolves a possibly relative path against the config directory.
func ResolvePath(configPath, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(filepath.Dir(configPath), value)
}
