// Package tplink controls TP-Link IPC cameras through their local HTTP API.
package tplink

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	authErrorCode  = -40401
	maxBodyBytes   = 1 << 20
	userAgent      = "TP-LINK_APP"
	loginRetryWait = time.Minute
)

// Client is safe for concurrent use. The session token is cached and reused
// until the camera rejects it.
type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	mu       sync.Mutex
	stok     string

	loginBlockedUntil time.Time
	loginFailure      error
}

func New(host string, port int, username, password string) *Client {
	host = strings.TrimSpace(host)
	if port <= 0 {
		port = 80
	}
	if strings.TrimSpace(username) == "" {
		username = "admin"
	}
	return &Client{
		baseURL:  "http://" + net.JoinHostPort(host, strconv.Itoa(port)),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 12 * time.Second},
	}
}

// GetMask reports whether lens masking is enabled.
func (c *Client) GetMask(ctx context.Context) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := c.callLocked(ctx, map[string]any{
		"method":    "get",
		"lens_mask": map[string]any{"name": []string{"lens_mask_info"}},
	})
	if err != nil {
		return false, err
	}
	lensMask, _ := data["lens_mask"].(map[string]any)
	info, _ := lensMask["lens_mask_info"].(map[string]any)
	switch enabled, _ := info["enabled"].(string); enabled {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, fmt.Errorf("TP-Link 响应缺少 lens_mask_info.enabled")
	}
}

// SetMask enables or disables lens masking.
func (c *Client) SetMask(ctx context.Context, masked bool) error {
	value := "off"
	if masked {
		value = "on"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.callLocked(ctx, map[string]any{
		"method":    "set",
		"lens_mask": map[string]any{"lens_mask_info": map[string]any{"enabled": value}},
	})
	return err
}

func (c *Client) callLocked(ctx context.Context, payload map[string]any) (map[string]any, error) {
	if c.stok == "" {
		if err := c.loginLocked(ctx); err != nil {
			return nil, err
		}
	}
	data, status, err := c.postDSLocked(ctx, payload)
	if err != nil && status != http.StatusUnauthorized {
		return nil, err
	}
	if status == http.StatusUnauthorized || errorCode(data) == authErrorCode {
		c.stok = ""
		if loginErr := c.loginLocked(ctx); loginErr != nil {
			return nil, loginErr
		}
		if data, status, err = c.postDSLocked(ctx, payload); err != nil {
			return nil, err
		}
		if status == http.StatusUnauthorized {
			return nil, fmt.Errorf("TP-Link 会话失效且重新登录后仍被拒绝（HTTP 401）")
		}
	}
	if err != nil {
		return nil, err
	}
	if code := errorCode(data); code != 0 {
		return nil, &apiError{code: code}
	}
	return data, nil
}

func (c *Client) loginLocked(ctx context.Context) error {
	// 密码错误后不要连续重试，摄像头连续失败 10 次会锁定。
	if c.loginFailure != nil && time.Now().Before(c.loginBlockedUntil) {
		return c.loginFailure
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/pc/Content.htm", nil)
	if err != nil {
		return err
	}
	body, status, err := c.do(req)
	if err != nil {
		return fmt.Errorf("TP-Link 连接失败: %w", err)
	}
	var content struct {
		Data struct {
			Nonce string `json:"nonce"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &content); err != nil || content.Data.Nonce == "" {
		return fmt.Errorf("TP-Link 握手失败（HTTP %d）", status)
	}
	sum := md5.Sum([]byte(c.password + ":" + content.Data.Nonce))
	payload := map[string]any{
		"method": "do",
		"login": map[string]any{
			"username":         c.username,
			"password":         hex.EncodeToString(sum[:]),
			"encrypt_type":     "3",
			"md5_encrypt_type": "1",
		},
	}
	data, err := c.postJSONLocked(ctx, c.baseURL+"/", payload)
	if err != nil {
		return fmt.Errorf("TP-Link 登录失败: %w", err)
	}
	if code := errorCode(data); code != 0 {
		failure := fmt.Errorf("TP-Link 登录失败（error_code %d），请检查设备密码", code)
		c.loginFailure = failure
		c.loginBlockedUntil = time.Now().Add(loginRetryWait)
		return failure
	}
	stok, _ := data["stok"].(string)
	if stok == "" {
		return fmt.Errorf("TP-Link 登录响应缺少 stok")
	}
	c.stok = stok
	c.loginFailure = nil
	c.loginBlockedUntil = time.Time{}
	return nil
}

func (c *Client) postDSLocked(ctx context.Context, payload map[string]any) (map[string]any, int, error) {
	url := c.baseURL + "/stok=" + c.stok + "/ds"
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	data, status, err := c.do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("TP-Link 请求失败: %w", err)
	}
	decoded, err := decodeObject(data)
	if err != nil {
		return nil, status, fmt.Errorf("TP-Link 响应解析失败（HTTP %d）", status)
	}
	return decoded, status, nil
}

func (c *Client) postJSONLocked(ctx context.Context, url string, payload map[string]any) (map[string]any, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	data, status, err := c.do(req)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeObject(data)
	if err != nil {
		return nil, fmt.Errorf("响应解析失败（HTTP %d）", status)
	}
	return decoded, nil
}

func (c *Client) do(req *http.Request) ([]byte, int, error) {
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

func decodeObject(data []byte) (map[string]any, error) {
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func errorCode(data map[string]any) int {
	if code, ok := data["error_code"].(float64); ok {
		return int(code)
	}
	return 0
}

type apiError struct {
	code int
}

func (e *apiError) Error() string {
	return fmt.Sprintf("TP-Link 接口返回错误（error_code %d）", e.code)
}
