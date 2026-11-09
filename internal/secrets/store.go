package secrets

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"
	"gopkg.in/yaml.v3"

	"wol/internal/config"
)

// EmbeddedKey is injected at build time, e.g.
//
//	-ldflags "-X wol/internal/secrets.EmbeddedKey=<64-char-hex>"
//
// It is the default master key so deployments no longer need to keep a key
// file next to the secrets file. WOL_SECRETS_KEY still overrides it.
var EmbeddedKey = ""

const fileHeader = `# WOL secrets - encrypted (AES-256-GCM)
# Keep this file private (chmod 600).
# The master key is embedded in the wol binary, or supplied via WOL_SECRETS_KEY.
# Legacy <secrets>.key files are still supported for migration only.
`

const (
	envelopeVersion = 1
	envelopeCipher  = "aes-256-gcm"
	envelopeAAD     = "wol-secrets-v1"
	keyEnv          = "WOL_SECRETS_KEY"
)

const (
	argonMemory  = 32 * 1024
	argonTime    = 3
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

type envelope struct {
	Version int    `yaml:"version"`
	Cipher  string `yaml:"cipher"`
	Data    string `yaml:"data"`
}

type File struct {
	ConfigPanel ConfigPanelSecrets       `yaml:"config_panel,omitempty"`
	Devices     map[string]DeviceSecrets `yaml:"devices,omitempty"`
}

type ConfigPanelSecrets struct {
	PasswordHash string `yaml:"password_hash,omitempty"`
}

type DeviceSecrets struct {
	SSH    SSHSec        `yaml:"ssh,omitempty"`
	IoT    IoTSecrets    `yaml:"iot,omitempty"`
	Camera CameraSecrets `yaml:"camera,omitempty"`
}

type CameraSecrets struct {
	Password string `yaml:"password,omitempty"`
}

type SSHSec struct {
	Password   string `yaml:"password,omitempty"`
	PrivateKey string `yaml:"private_key,omitempty"`
	Passphrase string `yaml:"passphrase,omitempty"`
}

type IoTSecrets struct {
	BemfaSecretKey string `yaml:"bemfa_secret_key,omitempty"`
	BlinkerAuthKey string `yaml:"blinker_auth_key,omitempty"`
}

type DeviceStatus struct {
	SSHPassword    bool `json:"ssh_password"`
	SSHPrivateKey  bool `json:"ssh_private_key"`
	SSHPassphrase  bool `json:"ssh_passphrase"`
	BemfaSecretKey bool `json:"bemfa_secret_key"`
	BlinkerAuthKey bool `json:"blinker_auth_key"`
	CameraPassword bool `json:"camera_password"`
}

type Status struct {
	ConfigPanelPassword bool                    `json:"config_panel_password"`
	Devices             map[string]DeviceStatus `json:"devices,omitempty"`
}

type Store struct {
	path          string
	keyPath       string
	encrypted     bool
	rewrap        bool
	legacyKeyUsed bool
	mu            sync.RWMutex
	file          File
}

func DefaultPath(configPath string) string {
	ext := filepath.Ext(configPath)
	base := strings.TrimSuffix(configPath, ext)
	if base == "" {
		base = configPath
	}
	return base + ".secrets.yaml"
}

func DefaultKeyPath(secretsPath string) string {
	ext := filepath.Ext(secretsPath)
	base := strings.TrimSuffix(secretsPath, ext)
	if base == "" {
		base = secretsPath
	}
	return base + ".key"
}

func NewStore(path string) *Store {
	return NewStoreWithKey(path, DefaultKeyPath(path))
}

func NewStoreWithKey(path, keyPath string) *Store {
	if keyPath == "" {
		keyPath = DefaultKeyPath(path)
	}
	return &Store{path: path, keyPath: keyPath, file: File{}}
}

func Load(path, keyPath string) (*Store, error) {
	if keyPath == "" {
		keyPath = DefaultKeyPath(path)
	}
	store := NewStoreWithKey(path, keyPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return store, nil
		}
		return nil, fmt.Errorf("read secrets: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return store, nil
	}
	var env envelope
	if err := yaml.Unmarshal(raw, &env); err == nil && strings.TrimSpace(env.Data) != "" {
		if env.Version != envelopeVersion || env.Cipher != envelopeCipher {
			return nil, fmt.Errorf("unsupported secrets format: version=%d cipher=%q", env.Version, env.Cipher)
		}
		key, err := loadKey(keyPath, false)
		if err != nil {
			return nil, fmt.Errorf("load secrets key: %w", err)
		}
		plain, err := decrypt(key, env.Data)
		if err != nil {
			// Migration path: an embedded key may have replaced the old key
			// file. Only fall back to the file when the embedded key is the
			// primary source and it differs from the file key.
			if primaryKeySource() == "embedded" {
				legacyKey, legacyErr := readKeyFile(keyPath)
				if legacyErr == nil && !bytes.Equal(legacyKey, key) {
					if legacyPlain, decryptErr := decrypt(legacyKey, env.Data); decryptErr == nil {
						plain = legacyPlain
						store.rewrap = true
						store.legacyKeyUsed = true
					} else {
						return nil, fmt.Errorf("decrypt secrets: %w", err)
					}
				} else {
					return nil, fmt.Errorf("decrypt secrets: %w", err)
				}
			} else {
				return nil, fmt.Errorf("decrypt secrets: %w", err)
			}
		}
		var file File
		decoder := yaml.NewDecoder(strings.NewReader(string(plain)))
		decoder.KnownFields(true)
		if err := decoder.Decode(&file); err != nil {
			return nil, fmt.Errorf("parse decrypted secrets: %w", err)
		}
		store.file = file
		store.encrypted = true
		return store, nil
	}
	var file File
	decoder := yaml.NewDecoder(strings.NewReader(string(raw)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("parse secrets: %w", err)
	}
	store.file = file
	return store, nil
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *Store) KeyPath() string {
	if s == nil {
		return ""
	}
	return s.keyPath
}

// KeySource reports where the active master key comes from.
func (s *Store) KeySource() string {
	if strings.TrimSpace(os.Getenv(keyEnv)) != "" {
		return "env"
	}
	if strings.TrimSpace(EmbeddedKey) != "" {
		return "embedded"
	}
	if s != nil && s.keyPath != "" {
		if _, err := os.Stat(s.keyPath); err == nil {
			return "file"
		}
	}
	return "none"
}

// LegacyKeyFileExists reports whether an old on-disk key file is still present.
func (s *Store) LegacyKeyFileExists() bool {
	if s == nil || s.keyPath == "" {
		return false
	}
	_, err := os.Stat(s.keyPath)
	return err == nil
}

// NeedsRewrap reports whether secrets were decrypted with a legacy file key
// while an embedded key is configured; the caller should Save() to re-encrypt.
func (s *Store) NeedsRewrap() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rewrap
}

// RetireFileKey renames the legacy key file after a successful rewrap.
// It is intentionally non-destructive: the file becomes <key>.legacy.
func (s *Store) RetireFileKey() (string, error) {
	if s == nil || s.keyPath == "" {
		return "", fmt.Errorf("no secrets key path")
	}
	s.mu.RLock()
	used := s.legacyKeyUsed
	s.mu.RUnlock()
	if !used {
		return "", nil
	}
	if _, err := os.Stat(s.keyPath); errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	legacy := s.keyPath + ".legacy"
	if err := os.Rename(s.keyPath, legacy); err != nil {
		return "", err
	}
	_ = os.Chmod(legacy, 0o600)
	return legacy, nil
}

// NeedsEncryption reports whether the store was loaded from a legacy plaintext file.
func (s *Store) NeedsEncryption() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return !s.encrypted && !reflect.DeepEqual(s.file, File{})
}

func (s *Store) Snapshot() File {
	if s == nil {
		return File{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneFile(s.file)
}

func (s *Store) Clone() *Store {
	if s == nil {
		return NewStore("")
	}
	return &Store{
		path:          s.path,
		keyPath:       s.keyPath,
		encrypted:     s.encrypted,
		rewrap:        s.rewrap,
		legacyKeyUsed: s.legacyKeyUsed,
		file:          s.Snapshot(),
	}
}

func (s *Store) IsEmpty() bool {
	return reflect.DeepEqual(s.Snapshot(), File{})
}

func (s *Store) Equal(other *Store) bool {
	if s == nil || other == nil {
		return s == other
	}
	return reflect.DeepEqual(s.Snapshot(), other.Snapshot())
}

// RuntimeEqual compares only credentials used by device runtime code.
func (s *Store) RuntimeEqual(other *Store) bool {
	if s == nil || other == nil {
		return s == other
	}
	a := s.Snapshot()
	b := other.Snapshot()
	a.ConfigPanel = ConfigPanelSecrets{}
	b.ConfigPanel = ConfigPanelSecrets{}
	return reflect.DeepEqual(a, b)
}

// Save encrypts the secrets file, writes it atomically with mode 0600 and
// returns the backup path. The master key is created on first save.
func (s *Store) Save() (string, error) {
	if s == nil {
		return "", fmt.Errorf("secrets store is nil")
	}
	s.mu.RLock()
	file := cloneFile(s.file)
	s.mu.RUnlock()
	key, err := loadKey(s.keyPath, true)
	if err != nil {
		return "", fmt.Errorf("load secrets key: %w", err)
	}
	plain, err := yaml.Marshal(file)
	if err != nil {
		return "", fmt.Errorf("marshal secrets: %w", err)
	}
	data, err := encrypt(key, plain)
	if err != nil {
		return "", fmt.Errorf("encrypt secrets: %w", err)
	}
	env := envelope{Version: envelopeVersion, Cipher: envelopeCipher, Data: data}
	encoded, err := yaml.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("marshal secrets envelope: %w", err)
	}

	backup := ""
	if existing, err := os.ReadFile(s.path); err == nil {
		backup = s.path + ".bak"
		backupData := existing
		if !isEncrypted(existing) {
			var old File
			if err := yaml.Unmarshal(existing, &old); err == nil {
				oldPlain, _ := yaml.Marshal(old)
				oldCipher, encErr := encrypt(key, oldPlain)
				if encErr != nil {
					return "", fmt.Errorf("encrypt secrets backup: %w", encErr)
				}
				oldEnvelope, _ := yaml.Marshal(envelope{Version: envelopeVersion, Cipher: envelopeCipher, Data: oldCipher})
				backupData = append([]byte(fileHeader+"\n"), oldEnvelope...)
			}
		}
		if err := os.WriteFile(backup, backupData, 0o600); err != nil {
			return "", fmt.Errorf("write secrets backup: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read existing secrets: %w", err)
	}
	if err := writeSecretsAtomic(s.path, append([]byte(fileHeader+"\n"), encoded...)); err != nil {
		return backup, err
	}
	s.mu.Lock()
	s.encrypted = true
	s.rewrap = false
	s.mu.Unlock()
	return backup, nil
}

func (s *Store) ApplyToConfig(cfg *config.Config) {
	if s == nil || cfg == nil {
		return
	}
	file := s.Snapshot()
	cfg.ConfigPanel.Password = ""
	for i := range cfg.Devices {
		deviceSecrets := file.Devices[deviceSecretRef(cfg.Devices[i])]
		cfg.Devices[i].SSH.Password = deviceSecrets.SSH.Password
		cfg.Devices[i].SSH.PrivateKey = deviceSecrets.SSH.PrivateKey
		cfg.Devices[i].SSH.Passphrase = deviceSecrets.SSH.Passphrase
		cfg.Devices[i].IoT.Bemfa.SecretKey = deviceSecrets.IoT.BemfaSecretKey
		cfg.Devices[i].IoT.Blinker.AuthKey = deviceSecrets.IoT.BlinkerAuthKey
		cfg.Devices[i].Camera.Password = deviceSecrets.Camera.Password
	}
}

// ImportLegacy moves old plaintext config values into the store in memory.
// It does not modify cfg or write files.
func (s *Store) ImportLegacy(cfg *config.Config) (bool, error) {
	if s == nil || cfg == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false

	if cfg.ConfigPanel.Password != "" && s.file.ConfigPanel.PasswordHash == "" {
		hash, err := HashPanelPassword(cfg.ConfigPanel.Password)
		if err != nil {
			return changed, err
		}
		s.file.ConfigPanel.PasswordHash = hash
		changed = true
	}
	for i := range cfg.Devices {
		device := cfg.Devices[i]
		ref := deviceSecretRef(device)
		entry := s.file.Devices[ref]
		before := entry
		if device.SSH.Password != "" && entry.SSH.Password == "" {
			entry.SSH.Password = device.SSH.Password
		}
		if device.SSH.PrivateKey != "" && entry.SSH.PrivateKey == "" {
			entry.SSH.PrivateKey = device.SSH.PrivateKey
		}
		if device.SSH.Passphrase != "" && entry.SSH.Passphrase == "" {
			entry.SSH.Passphrase = device.SSH.Passphrase
		}
		if device.IoT.Bemfa.SecretKey != "" && entry.IoT.BemfaSecretKey == "" {
			entry.IoT.BemfaSecretKey = device.IoT.Bemfa.SecretKey
		}
		if device.IoT.Blinker.AuthKey != "" && entry.IoT.BlinkerAuthKey == "" {
			entry.IoT.BlinkerAuthKey = device.IoT.Blinker.AuthKey
		}
		if device.Camera.Password != "" && entry.Camera.Password == "" {
			entry.Camera.Password = device.Camera.Password
		}
		if !reflect.DeepEqual(before, entry) {
			if s.file.Devices == nil {
				s.file.Devices = make(map[string]DeviceSecrets)
			}
			s.file.Devices[ref] = entry
			changed = true
		}
	}
	return changed, nil
}

// ReplaceRuntimeSecretsFromConfig replaces reversible credentials with values
// from cfg. Empty values clear the corresponding secret.
func (s *Store) ReplaceRuntimeSecretsFromConfig(cfg *config.Config) {
	if s == nil || cfg == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	devices := make(map[string]DeviceSecrets, len(cfg.Devices))
	for i := range cfg.Devices {
		device := cfg.Devices[i]
		ref := deviceSecretRef(device)
		entry := s.file.Devices[ref]
		if device.ClearSecrets {
			entry = DeviceSecrets{}
		}
		if device.SSH.Password != "" {
			entry.SSH.Password = device.SSH.Password
		}
		if device.SSH.PrivateKey != "" {
			entry.SSH.PrivateKey = device.SSH.PrivateKey
		}
		if device.SSH.Passphrase != "" {
			entry.SSH.Passphrase = device.SSH.Passphrase
		}
		if device.IoT.Bemfa.SecretKey != "" {
			entry.IoT.BemfaSecretKey = device.IoT.Bemfa.SecretKey
		}
		if device.IoT.Blinker.AuthKey != "" {
			entry.IoT.BlinkerAuthKey = device.IoT.Blinker.AuthKey
		}
		if device.Camera.Password != "" {
			entry.Camera.Password = device.Camera.Password
		}
		if !reflect.DeepEqual(entry, DeviceSecrets{}) {
			devices[ref] = entry
		}
	}
	s.file.Devices = devices
}

func (s *Store) SetPanelPassword(password string) error {
	hash, err := HashPanelPassword(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.file.ConfigPanel.PasswordHash = hash
	s.mu.Unlock()
	return nil
}

func (s *Store) ClearPanelPassword() {
	s.mu.Lock()
	s.file.ConfigPanel.PasswordHash = ""
	s.mu.Unlock()
}

func (s *Store) PanelPasswordSet() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.file.ConfigPanel.PasswordHash != ""
}

func (s *Store) PanelPasswordHash() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.file.ConfigPanel.PasswordHash
}

func (s *Store) PanelPasswordFingerprint() [32]byte {
	hash := s.PanelPasswordHash()
	return sha256.Sum256([]byte(hash))
}

func (s *Store) VerifyPanelPassword(password string) bool {
	hash := s.PanelPasswordHash()
	if hash == "" {
		return true
	}
	return verifyPanelPassword(hash, password)
}

func (s *Store) Status(cfg *config.Config) Status {
	status := Status{Devices: make(map[string]DeviceStatus)}
	if s == nil {
		return status
	}
	file := s.Snapshot()
	status.ConfigPanelPassword = file.ConfigPanel.PasswordHash != ""
	if cfg != nil {
		for i := range cfg.Devices {
			ref := deviceSecretRef(cfg.Devices[i])
			device := file.Devices[ref]
			status.Devices[ref] = DeviceStatus{
				SSHPassword:    device.SSH.Password != "",
				SSHPrivateKey:  device.SSH.PrivateKey != "",
				SSHPassphrase:  device.SSH.Passphrase != "",
				BemfaSecretKey: device.IoT.BemfaSecretKey != "",
				BlinkerAuthKey: device.IoT.BlinkerAuthKey != "",
				CameraPassword: device.Camera.Password != "",
			}
		}
	}
	return status
}

func deviceSecretRef(device config.Device) string {
	if ref := strings.TrimSpace(device.SecretRef); ref != "" {
		return ref
	}
	return device.Key
}

func HashPanelPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func verifyPanelPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" || parts[2] != "v=19" {
		return false
	}
	var memory uint32
	var iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	if memory == 0 || iterations == 0 || threads == 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func cloneFile(file File) File {
	out := file
	if file.Devices != nil {
		out.Devices = make(map[string]DeviceSecrets, len(file.Devices))
		for key, device := range file.Devices {
			out.Devices[key] = device
		}
	}
	return out
}

func writeSecretsAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wol-secrets-*.yaml")
	if err != nil {
		return fmt.Errorf("create temp secrets: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp secrets: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp secrets: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp secrets: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp secrets: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace secrets: %w", err)
	}
	return nil
}

func primaryKeySource() string {
	if strings.TrimSpace(os.Getenv(keyEnv)) != "" {
		return "env"
	}
	if strings.TrimSpace(EmbeddedKey) != "" {
		return "embedded"
	}
	return "file"
}

func embeddedKey() ([]byte, bool, error) {
	value := strings.TrimSpace(EmbeddedKey)
	if value == "" {
		return nil, false, nil
	}
	key, err := decodeKey(value)
	if err != nil {
		return nil, true, fmt.Errorf("embedded key: %w", err)
	}
	return key, true, nil
}

func readKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeKey(strings.TrimSpace(string(raw)))
}

func loadKey(path string, create bool) ([]byte, error) {
	if env := strings.TrimSpace(os.Getenv(keyEnv)); env != "" {
		key, err := decodeKey(env)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", keyEnv, err)
		}
		return key, nil
	}
	if key, ok, err := embeddedKey(); err != nil {
		return nil, err
	} else if ok {
		return key, nil
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		key, decodeErr := decodeKey(strings.TrimSpace(string(raw)))
		if decodeErr != nil {
			return nil, fmt.Errorf("invalid secrets key %s: %w", path, decodeErr)
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if !create {
		return nil, fmt.Errorf("secrets key not found: %s", path)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generate secrets key: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create secrets key directory: %w", err)
		}
	}
	if err := writeKeyAtomic(path, []byte(hex.EncodeToString(key)+"\n")); err != nil {
		return nil, err
	}
	return key, nil
}

func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, fmt.Errorf("empty key")
	}
	if len(value) == 64 {
		if key, err := hex.DecodeString(value); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if key, err := encoding.DecodeString(value); err == nil && len(key) == 32 {
			return key, nil
		}
	}
	return nil, fmt.Errorf("key must be 32 bytes encoded as hex or base64")
}

func encrypt(key, plain []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plain, []byte(envelopeAAD))
	return base64.StdEncoding.EncodeToString(sealed), nil
}

func decrypt(key []byte, encoded string) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sealed, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	nonceSize := gcm.NonceSize()
	if len(sealed) < nonceSize {
		return nil, fmt.Errorf("encrypted secrets too short")
	}
	plain, err := gcm.Open(nil, sealed[:nonceSize], sealed[nonceSize:], []byte(envelopeAAD))
	if err != nil {
		return nil, err
	}
	return plain, nil
}

func isEncrypted(raw []byte) bool {
	var env envelope
	if err := yaml.Unmarshal(raw, &env); err != nil {
		return false
	}
	return strings.TrimSpace(env.Data) != ""
}

func writeKeyAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".wol-key-*")
	if err != nil {
		return fmt.Errorf("create temp secrets key: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("write temp secrets key: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		cleanup()
		return fmt.Errorf("chmod temp secrets key: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("sync temp secrets key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp secrets key: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replace secrets key: %w", err)
	}
	return nil
}
