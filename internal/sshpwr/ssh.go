package sshpwr

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type Config struct {
	Host                string
	Port                int
	User                string
	Password            string
	PrivateKey          string
	PrivateKeyFile      string
	Passphrase          string
	HostKey             string
	AllowUnknownHostKey bool
	Timeout             time.Duration
}

type HostKeyInfo struct {
	Fingerprint string `json:"fingerprint"`
	Configured  string `json:"configured,omitempty"`
	Trusted     bool   `json:"trusted"`
}

type Result struct {
	Output      string
	ExitCode    int
	Uncertain   bool
	Fingerprint string
}

const maxOutput = 32 * 1024

func (c Config) address() string {
	port := c.Port
	if port <= 0 {
		port = 22
	}
	return net.JoinHostPort(c.Host, fmt.Sprintf("%d", port))
}

func (c Config) timeout() time.Duration {
	if c.Timeout <= 0 {
		return 10 * time.Second
	}
	return c.Timeout
}

// Probe performs a handshake and returns the server host key fingerprint.
// Authentication errors are ignored because the fingerprint is available before login.
func Probe(ctx context.Context, cfg Config) (HostKeyInfo, error) {
	var observed string
	clientConfig := &ssh.ClientConfig{
		User: cfg.User,
		Auth: []ssh.AuthMethod{ssh.Password("probe")},
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			observed = Fingerprint(key)
			return errStopHandshake
		},
		Timeout: cfg.timeout(),
	}
	if clientConfig.User == "" {
		clientConfig.User = "probe"
	}
	dialer := net.Dialer{Timeout: cfg.timeout()}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout())
	defer cancel()
	conn, err := dialer.DialContext(ctx, "tcp", cfg.address())
	if err != nil {
		return HostKeyInfo{}, err
	}
	defer conn.Close()
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, cfg.address(), clientConfig)
	if err == nil {
		_ = sshConn.Close()
		go ssh.DiscardRequests(reqs)
		go func() {
			for range chans {
			}
		}()
	} else if !errors.Is(err, errStopHandshake) && observed == "" {
		return HostKeyInfo{}, err
	}
	if observed == "" {
		return HostKeyInfo{}, fmt.Errorf("server did not present a host key")
	}
	info := HostKeyInfo{
		Fingerprint: observed,
		Configured:  cfg.HostKey,
		Trusted:     hostKeyMatches(cfg.HostKey, observed),
	}
	if cfg.AllowUnknownHostKey {
		info.Trusted = true
	}
	return info, nil
}

var errStopHandshake = errors.New("host key captured")

// Run executes a command over SSH and returns its combined output.
func Run(ctx context.Context, cfg Config, command string) (Result, error) {
	if strings.TrimSpace(command) == "" {
		return Result{}, fmt.Errorf("empty ssh command")
	}
	auth, err := authMethods(cfg)
	if err != nil {
		return Result{}, err
	}
	var observed string
	hostKeyCallback := func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		observed = Fingerprint(key)
		if cfg.AllowUnknownHostKey {
			return nil
		}
		if !hostKeyMatches(cfg.HostKey, observed) {
			if cfg.HostKey == "" {
				return fmt.Errorf("ssh host key not configured, observed %s; set ssh.host_key or allow_unknown_host_key", observed)
			}
			return fmt.Errorf("ssh host key mismatch: expected %s, got %s", cfg.HostKey, observed)
		}
		return nil
	}

	clientConfig := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback,
		Timeout:         cfg.timeout(),
	}

	dialer := net.Dialer{Timeout: cfg.timeout()}
	conn, err := dialer.DialContext(ctx, "tcp", cfg.address())
	if err != nil {
		return Result{}, fmt.Errorf("dial %s: %w", cfg.address(), err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(cfg.timeout()))

	sshConn, chans, reqs, err := ssh.NewClientConn(conn, cfg.address(), clientConfig)
	if err != nil {
		return Result{}, err
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	_ = conn.SetDeadline(time.Time{})

	session, err := client.NewSession()
	if err != nil {
		return Result{Uncertain: true, Fingerprint: observed}, fmt.Errorf("open ssh session: %w", err)
	}
	defer session.Close()

	var output limitedBuffer
	session.Stdout = &output
	session.Stderr = &output

	done := make(chan error, 1)
	go func() {
		done <- session.Run(command)
	}()

	timer := time.NewTimer(cfg.timeout())
	defer timer.Stop()
	select {
	case <-ctx.Done():
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		return Result{Output: output.String(), Uncertain: true, Fingerprint: observed}, ctx.Err()
	case <-timer.C:
		_ = session.Signal(ssh.SIGTERM)
		_ = session.Close()
		return Result{Output: output.String(), Uncertain: true, Fingerprint: observed}, fmt.Errorf("ssh command timed out after %s", cfg.timeout())
	case err := <-done:
		result := Result{Output: output.String(), ExitCode: 0, Fingerprint: observed}
		if err == nil {
			return result, nil
		}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitStatus()
			return result, fmt.Errorf("ssh command exited with status %d: %s", result.ExitCode, strings.TrimSpace(result.Output))
		}
		// A dropped connection usually means the remote side started the
		// command (for example a reboot) before delivering an exit status.
		result.Uncertain = true
		return result, nil
	}
}

func authMethods(cfg Config) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
		methods = append(methods, ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = cfg.Password
			}
			return answers, nil
		}))
	}
	keyData := cfg.PrivateKey
	if strings.TrimSpace(keyData) == "" && cfg.PrivateKeyFile != "" {
		path := expandHome(cfg.PrivateKeyFile)
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read private key: %w", err)
		}
		keyData = string(raw)
	}
	if strings.TrimSpace(keyData) != "" {
		var signer ssh.Signer
		var err error
		if cfg.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(keyData), []byte(cfg.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(keyData))
		}
		if err != nil {
			return nil, fmt.Errorf("parse private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if len(methods) == 0 {
		return nil, fmt.Errorf("no ssh authentication method configured")
	}
	return methods, nil
}

func Fingerprint(key ssh.PublicKey) string {
	sum := sha256.Sum256(key.Marshal())
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(sum[:])
}

func hostKeyMatches(configured, observed string) bool {
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return false
	}
	if strings.HasPrefix(strings.ToUpper(configured), "SHA256:") {
		configured = "SHA256:" + configured[7:]
	} else {
		configured = "SHA256:" + configured
	}
	return subtle.ConstantTimeCompare([]byte(configured), []byte(observed)) == 1
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		if path == "~" {
			return home
		}
		return filepath.Join(home, path[2:])
	}
	return path
}

type limitedBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	original := len(p)
	if len(b.data) >= maxOutput {
		return original, nil
	}
	remaining := maxOutput - len(b.data)
	if len(p) > remaining {
		p = p[:remaining]
	}
	b.data = append(b.data, p...)
	return original, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

var _ io.Writer = (*limitedBuffer)(nil)
