package tplink

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type fakeCamera struct {
	password   string
	nonce      string
	stok       string
	enabled    string
	logins     int
	attempts   int
	expireNext bool
}

func (f *fakeCamera) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pc/Content.htm", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": map[string]any{
				"nonce":            f.nonce,
				"encrypt_type":     []string{"1", "2", "3"},
				"md5_encrypt_type": 1,
				"key":              "unused",
			},
			"error_code": authErrorCode,
		})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Method string `json:"method"`
			Login  struct {
				Username    string `json:"username"`
				Password    string `json:"password"`
				EncryptType string `json:"encrypt_type"`
			} `json:"login"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		f.attempts++
		sum := md5.Sum([]byte(f.password + ":" + f.nonce))
		if payload.Login.Username != "admin" || payload.Login.Password != hex.EncodeToString(sum[:]) {
			_ = json.NewEncoder(w).Encode(map[string]any{"error_code": authErrorCode, "time": 1, "max_time": 10})
			return
		}
		f.logins++
		_ = json.NewEncoder(w).Encode(map[string]any{"user_group": "root", "stok": f.stok, "error_code": 0})
	})
	mux.HandleFunc("/stok="+f.stok+"/ds", func(w http.ResponseWriter, r *http.Request) {
		if f.expireNext {
			f.expireNext = false
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var payload struct {
			Method   string `json:"method"`
			LensMask struct {
				Name         []string `json:"name"`
				LensMaskInfo struct {
					Enabled string `json:"enabled"`
				} `json:"lens_mask_info"`
			} `json:"lens_mask"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		switch payload.Method {
		case "get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"lens_mask": map[string]any{
					"lens_mask_info": map[string]any{"enabled": f.enabled},
				},
				"error_code": 0,
			})
		case "set":
			f.enabled = payload.LensMask.LensMaskInfo.Enabled
			_ = json.NewEncoder(w).Encode(map[string]any{"error_code": 0})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"error_code": -1})
		}
	})
	return mux
}

func clientFor(t *testing.T, server *httptest.Server, password string) *Client {
	t.Helper()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(server.URL, "http://"))
	if err != nil {
		t.Fatalf("split server address: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return New(host, port, "admin", password)
}

func TestClientLensMask(t *testing.T) {
	fake := &fakeCamera{password: "secret", nonce: "n0nce", stok: "tok123", enabled: "on"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	client := clientFor(t, server, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	masked, err := client.GetMask(ctx)
	if err != nil {
		t.Fatalf("GetMask: %v", err)
	}
	if !masked {
		t.Fatal("GetMask = false, want true")
	}

	if err := client.SetMask(ctx, false); err != nil {
		t.Fatalf("SetMask: %v", err)
	}
	masked, err = client.GetMask(ctx)
	if err != nil {
		t.Fatalf("GetMask after SetMask: %v", err)
	}
	if masked {
		t.Fatal("GetMask = true after disabling mask")
	}
	if fake.logins != 1 {
		t.Fatalf("logins = %d, want 1 (stok should be reused)", fake.logins)
	}
}

func TestClientReloginOnExpiredSession(t *testing.T) {
	fake := &fakeCamera{password: "secret", nonce: "n0nce", stok: "tok123", enabled: "off"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	client := clientFor(t, server, "secret")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.GetMask(ctx); err != nil {
		t.Fatalf("first GetMask: %v", err)
	}
	fake.expireNext = true
	if _, err := client.GetMask(ctx); err != nil {
		t.Fatalf("GetMask after expiry: %v", err)
	}
	if fake.logins != 2 {
		t.Fatalf("logins = %d, want 2 (one relogin after 401)", fake.logins)
	}
}

func TestClientLoginFailureIsNotRetried(t *testing.T) {
	fake := &fakeCamera{password: "secret", nonce: "n0nce", stok: "tok123", enabled: "off"}
	server := httptest.NewServer(fake.handler())
	defer server.Close()

	client := clientFor(t, server, "wrong")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.GetMask(ctx)
	if err == nil || !strings.Contains(err.Error(), "登录失败") {
		t.Fatalf("GetMask error = %v, want login failure", err)
	}
	if fake.logins != 0 {
		t.Fatalf("logins = %d, want 0", fake.logins)
	}
	if _, err := client.GetMask(ctx); err == nil {
		t.Fatal("second GetMask should keep failing")
	}
	if fake.attempts != 1 {
		t.Fatalf("login attempts = %d, want 1 (failures should be cached)", fake.attempts)
	}
}
