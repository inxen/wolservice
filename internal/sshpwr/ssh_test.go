package sshpwr

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestFingerprintAndMatch(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	fingerprint := Fingerprint(signer.PublicKey())
	if !strings.HasPrefix(fingerprint, "SHA256:") {
		t.Fatalf("fingerprint = %q", fingerprint)
	}
	if !hostKeyMatches(fingerprint, fingerprint) {
		t.Fatal("exact fingerprint did not match")
	}
	if !hostKeyMatches(strings.TrimPrefix(fingerprint, "SHA256:"), fingerprint) {
		t.Fatal("fingerprint without prefix did not match")
	}
	if !hostKeyMatches("sha256:"+strings.TrimPrefix(fingerprint, "SHA256:"), fingerprint) {
		t.Fatal("lowercase sha256 prefix did not match")
	}
	if hostKeyMatches("SHA256:wrong", fingerprint) {
		t.Fatal("mismatched fingerprint matched")
	}
	if hostKeyMatches("", fingerprint) {
		t.Fatal("empty fingerprint matched")
	}
}

func TestAuthMethodsFromPrivateKey(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "test")
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	methods, err := authMethods(Config{
		PrivateKey: string(pem.EncodeToMemory(block)),
	})
	if err != nil {
		t.Fatalf("authMethods: %v", err)
	}
	if len(methods) != 1 {
		t.Fatalf("methods = %d", len(methods))
	}
}

func TestAuthMethodsRequireCredential(t *testing.T) {
	if _, err := authMethods(Config{}); err == nil {
		t.Fatal("expected error without credentials")
	}
}

func TestHostKeyPrefixCaseNormalization(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	signer, _ := ssh.NewSignerFromKey(privateKey)
	fingerprint := Fingerprint(signer.PublicKey())
	configured := "sha256:" + strings.TrimPrefix(fingerprint, "SHA256:")
	if !hostKeyMatches(configured, fingerprint) {
		t.Fatalf("lowercase prefix should match: configured=%s observed=%s", configured, fingerprint)
	}
}

func TestProbeAndRunAgainstSSHServer(t *testing.T) {
	host, port, fingerprint, stop := startTestSSHServer(t)
	defer stop()

	base := Config{
		Host:     host,
		Port:     port,
		User:     "tester",
		Password: "secret",
		Timeout:  5 * time.Second,
	}

	info, err := Probe(context.Background(), base)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if info.Fingerprint != fingerprint {
		t.Fatalf("probe fingerprint = %q, want %q", info.Fingerprint, fingerprint)
	}
	if info.Trusted {
		t.Fatal("probe should not trust an unconfigured key")
	}

	base.HostKey = fingerprint
	result, err := Run(context.Background(), base, "echo hello")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Output, "ran:echo hello") {
		t.Fatalf("result = %+v", result)
	}

	base.HostKey = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := Run(context.Background(), base, "echo hello"); err == nil || !strings.Contains(err.Error(), "host key mismatch") {
		t.Fatalf("expected host key mismatch, got %v", err)
	}

	base.HostKey = fingerprint
	base.Password = "wrong"
	if _, err := Run(context.Background(), base, "echo hello"); err == nil {
		t.Fatal("expected authentication error")
	}
}

func startTestSSHServer(t *testing.T) (host string, port int, fingerprint string, stop func()) {
	t.Helper()
	_, hostPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate host key: %v", err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostPrivateKey)
	if err != nil {
		t.Fatalf("host signer: %v", err)
	}
	serverConfig := &ssh.ServerConfig{
		PasswordCallback: func(meta ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
			if meta.User() == "tester" && string(password) == "secret" {
				return nil, nil
			}
			return nil, fmt.Errorf("authentication denied")
		},
	}
	serverConfig.AddHostKey(hostSigner)

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go serveTestSSHConn(conn, serverConfig)
		}
	}()
	addr := listener.Addr().(*net.TCPAddr)
	return addr.IP.String(), addr.Port, Fingerprint(hostSigner.PublicKey()), func() { listener.Close() }
}

func serveTestSSHConn(conn net.Conn, serverConfig *ssh.ServerConfig) {
	defer conn.Close()
	serverConn, channels, requests, err := ssh.NewServerConn(conn, serverConfig)
	if err != nil {
		return
	}
	defer serverConn.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "unsupported channel")
			continue
		}
		channel, channelRequests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer channel.Close()
			for request := range channelRequests {
				if request.Type != "exec" {
					_ = request.Reply(false, nil)
					continue
				}
				var payload struct{ Command string }
				if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
					_ = request.Reply(false, nil)
					continue
				}
				_ = request.Reply(true, nil)
				_, _ = io.WriteString(channel, "ran:"+payload.Command+"\n")
				_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
				return
			}
		}()
	}
}
