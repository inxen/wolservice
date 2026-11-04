package iot

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"

	"wol/internal/config"
)

const (
	blinkerHost        = "https://iot.diandeng.tech"
	blinkerAuthAPI     = blinkerHost + "/api/v1/user/device/diy/auth"
	blinkerHeartbeat   = blinkerHost + "/api/v1/user/device/heartbeat"
	blinkerVoiceAPI    = blinkerHost + "/api/v1/user/device/voice_assistant"
	blinkerHeartbeatIn = 599 * time.Second
)

var iotHTTPClient = &http.Client{Timeout: 15 * time.Second}

type blinkerDetail struct {
	Broker     string `json:"broker"`
	DeviceName string `json:"deviceName"`
	Host       string `json:"host"`
	IotID      string `json:"iotId"`
	IotToken   string `json:"iotToken"`
	Port       string `json:"port"`
	ProductKey string `json:"productKey"`
	UUID       string `json:"uuid"`
}

type blinkerAuthResponse struct {
	Message int           `json:"message"`
	Detail  blinkerDetail `json:"detail"`
}

type blinkerRunner struct {
	cfg     config.BlinkerConfig
	actions Actions
	log     *slog.Logger
	status  func(string)

	stateMu sync.Mutex
	state   string

	clientMu sync.Mutex
	client   MQTT.Client
}

func newBlinkerRunner(cfg config.BlinkerConfig, actions Actions, log *slog.Logger, status func(string)) *blinkerRunner {
	return &blinkerRunner{cfg: cfg, actions: actions, log: log, status: status, state: "off"}
}

func (b *blinkerRunner) run(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		b.status(statusConnect)
		err := b.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		b.status(fmt.Sprintf("连接失败: %v", err))
		b.log.Warn("blinker session ended", "error", err)
		if !sleepContext(ctx, backoff) {
			return
		}
		if backoff < 60*time.Second {
			backoff *= 2
		}
	}
}

func (b *blinkerRunner) runOnce(ctx context.Context) error {
	detail, err := b.fetchDetail(ctx)
	if err != nil {
		return err
	}
	if err := b.syncAssistants(ctx, detail); err != nil {
		b.log.Warn("blinker voice assistant sync failed", "error", err)
	}

	broker := detail.Host
	if !strings.Contains(broker, "://") {
		broker = "tcp://" + broker
	}
	if !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(broker, "tcp://"), "ssl://"), ":") {
		broker += ":" + detail.Port
	}

	connected := make(chan struct{}, 1)
	lost := make(chan struct{}, 1)
	opts := MQTT.NewClientOptions().
		AddBroker(broker).
		SetClientID(detail.DeviceName).
		SetUsername(detail.IotID).
		SetPassword(detail.IotToken).
		SetConnectTimeout(8 * time.Second).
		SetKeepAlive(30 * time.Second).
		SetAutoReconnect(false)
	topic := "/device/" + detail.DeviceName + "/r"
	opts.SetOnConnectHandler(func(client MQTT.Client) {
		if token := client.Subscribe(topic, 0, func(_ MQTT.Client, msg MQTT.Message) {
			b.onMessage(detail, msg)
		}); token.Wait() && token.Error() != nil {
			b.log.Error("blinker subscribe failed", "topic", topic, "error", token.Error())
			b.status(fmt.Sprintf("订阅失败: %v", token.Error()))
			return
		}
		b.status(statusOnline)
		b.log.Info("blinker connected", "device", detail.DeviceName, "broker", broker)
		select {
		case connected <- struct{}{}:
		default:
		}
	})
	opts.SetConnectionLostHandler(func(_ MQTT.Client, err error) {
		b.log.Warn("blinker connection lost", "error", err)
		b.status(statusRetry)
		select {
		case lost <- struct{}{}:
		default:
		}
	})

	client := MQTT.NewClient(opts)
	b.clientMu.Lock()
	b.client = client
	b.clientMu.Unlock()
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		b.clientMu.Lock()
		b.client = nil
		b.clientMu.Unlock()
		return fmt.Errorf("mqtt connect: %w", err)
	}
	defer func() {
		client.Disconnect(250)
		b.clientMu.Lock()
		b.client = nil
		b.clientMu.Unlock()
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-connected:
	case <-time.After(12 * time.Second):
		return fmt.Errorf("mqtt connect timeout")
	}

	ticker := time.NewTicker(blinkerHeartbeatIn)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-lost:
			return fmt.Errorf("mqtt connection lost")
		case <-ticker.C:
			if err := b.heartbeat(ctx, detail.DeviceName); err != nil {
				b.log.Warn("blinker heartbeat failed", "error", err)
			}
		}
	}
}

func (b *blinkerRunner) fetchDetail(ctx context.Context) (blinkerDetail, error) {
	endpoint := blinkerAuthAPI + "?authKey=" + url.QueryEscape(b.cfg.AuthKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return blinkerDetail{}, err
	}
	resp, err := iotHTTPClient.Do(req)
	if err != nil {
		return blinkerDetail{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return blinkerDetail{}, err
	}
	var parsed blinkerAuthResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return blinkerDetail{}, fmt.Errorf("parse auth response: %w", err)
	}
	if parsed.Detail.DeviceName == "" || parsed.Detail.Host == "" {
		return blinkerDetail{}, fmt.Errorf("blinker auth returned no device detail: %s", strings.TrimSpace(string(body)))
	}
	return parsed.Detail, nil
}

func (b *blinkerRunner) syncAssistants(ctx context.Context, detail blinkerDetail) error {
	deviceType := b.cfg.DeviceType
	payloads := []map[string]string{
		{"token": detail.IotToken, "miType": deviceType},
		{"token": detail.IotToken, "aliType": deviceType},
		{"token": detail.IotToken, "duerType": duerType(deviceType)},
	}
	for _, payload := range payloads {
		raw, _ := json.Marshal(payload)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, blinkerVoiceAPI, bytes.NewReader(raw))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := iotHTTPClient.Do(req)
		if err != nil {
			return err
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
	}
	return nil
}

func duerType(deviceType string) string {
	switch deviceType {
	case "light":
		return "LIGHT"
	case "multi_outlet":
		return "MULTI_SOCKET"
	case "sensor":
		return "AIR_MONITOR"
	case "fan":
		return "FAN"
	case "aircondition":
		return "AIR_CONDITION"
	default:
		return "SOCKET"
	}
}

func (b *blinkerRunner) heartbeat(ctx context.Context, deviceName string) error {
	endpoint := fmt.Sprintf("%s?deviceName=%s&key=%s&heartbeat=600",
		blinkerHeartbeat, url.QueryEscape(deviceName), url.QueryEscape(b.cfg.AuthKey))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	resp, err := iotHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	return nil
}

func (b *blinkerRunner) onMessage(detail blinkerDetail, msg MQTT.Message) {
	var envelope struct {
		FromDevice string `json:"fromDevice"`
		Data       struct {
			From      string          `json:"from"`
			MessageID string          `json:"messageId"`
			Get       string          `json:"get"`
			Set       json.RawMessage `json:"set"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg.Payload(), &envelope); err != nil {
		return
	}
	if envelope.FromDevice == detail.UUID {
		b.handleOwnApp(detail, envelope.Data.Get)
		return
	}
	if envelope.FromDevice != "ServerSender" {
		return
	}
	if envelope.Data.From == "" {
		return
	}
	if len(envelope.Data.Set) > 0 {
		var set struct {
			PState string `json:"pState"`
		}
		if err := json.Unmarshal(envelope.Data.Set, &set); err != nil {
			return
		}
		state := normalizePowerState(set.PState)
		if state == "" {
			return
		}
		b.setState(state)
		b.replyAssistant(detail, envelope.Data.From, state)
		go b.applyState(state)
		return
	}
	if envelope.Data.Get != "" {
		b.replyAssistant(detail, envelope.Data.From, b.getState())
	}
}

func (b *blinkerRunner) handleOwnApp(detail blinkerDetail, get string) {
	if get == "" {
		return
	}
	data := map[string]any{}
	switch get {
	case "state":
		data["state"] = "online"
	case "timing":
		data["timing"] = map[string]any{"timing": []any{}}
	case "countdown":
		data["countdown"] = "false"
	default:
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"deviceType": "OwnApp",
		"data":       data,
		"fromDevice": detail.DeviceName,
		"toDevice":   detail.UUID,
	})
	b.publish(detail, payload)
}

func (b *blinkerRunner) replyAssistant(detail blinkerDetail, from, state string) {
	payload, _ := json.Marshal(map[string]any{
		"deviceType": "vAssistant",
		"data":       map[string]string{"pState": state},
		"fromDevice": detail.DeviceName,
		"toDevice":   from + "_r",
	})
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(payload)))
	base64.StdEncoding.Encode(encoded, payload)
	b.publish(detail, encoded)
}

func (b *blinkerRunner) publish(detail blinkerDetail, payload []byte) {
	// The MQTT client is stored by paho's connect handler; publish via a short-lived lookup.
	// paho does not expose a global client, so the runner keeps one below.
	b.clientMu.Lock()
	client := b.client
	b.clientMu.Unlock()
	if client == nil {
		return
	}
	topic := "/device/" + detail.DeviceName + "/s"
	token := client.Publish(topic, 0, false, payload)
	token.Wait()
}

func (b *blinkerRunner) setState(state string) {
	b.stateMu.Lock()
	b.state = state
	b.stateMu.Unlock()
}

func (b *blinkerRunner) getState() string {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	return b.state
}

func (b *blinkerRunner) applyState(state string) {
	if state == "on" {
		b.status("已连接 · 执行唤醒")
		if err := b.actions.Wake(); err != nil {
			b.log.Error("blinker wake failed", "error", err)
			b.status(statusOnline + " · 唤醒失败")
		} else {
			b.status(statusOnline)
		}
		return
	}
	b.status("已连接 · 执行关机")
	if err := b.actions.Shutdown(); err != nil {
		b.log.Error("blinker shutdown failed", "error", err)
		b.status(statusOnline + " · 关机失败")
	} else {
		b.status(statusOnline)
	}
}

func normalizePowerState(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true":
		return "on"
	case "off", "false":
		return "off"
	default:
		return ""
	}
}
