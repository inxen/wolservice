package iot

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"strings"
	"time"

	MQTT "github.com/eclipse/paho.mqtt.golang"
)

const (
	bemfaBroker   = "mqtts://bemfa.com:9503"
	statusIdle    = "未启用"
	statusConnect = "连接中"
	statusOnline  = "已连接"
	statusRetry   = "重连中"
)

type Actions struct {
	Wake     func() error
	Shutdown func() error
}

type bemfaSubscription struct {
	Topic   string
	Actions Actions
	Status  func(string)
}

type bemfaRunner struct {
	secretKey string
	subs      map[string]*bemfaSubscription
	log       *slog.Logger
}

func newBemfaRunner(secretKey string, subs []*bemfaSubscription, log *slog.Logger) *bemfaRunner {
	byTopic := make(map[string]*bemfaSubscription, len(subs))
	for _, sub := range subs {
		byTopic[sub.Topic] = sub
	}
	return &bemfaRunner{secretKey: secretKey, subs: byTopic, log: log}
}

func (b *bemfaRunner) run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		b.setAllStatus(statusConnect)
		lost := make(chan struct{}, 1)
		opts := MQTT.NewClientOptions().
			AddBroker(bemfaBroker).
			SetClientID(b.secretKey).
			SetUsername("").
			SetPassword("").
			SetConnectTimeout(5 * time.Second).
			SetKeepAlive(30 * time.Second).
			SetAutoReconnect(false).
			SetTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12})
		opts.SetOnConnectHandler(func(client MQTT.Client) {
			b.setAllStatus(statusOnline)
			for topic := range b.subs {
				topic := topic
				if token := client.Subscribe(topic, 1, b.handler(topic)); token.Wait() && token.Error() != nil {
					b.log.Error("bemfa subscribe failed", "topic", topic, "error", token.Error())
					if sub := b.subs[topic]; sub != nil {
						sub.Status(fmt.Sprintf("订阅失败: %v", token.Error()))
					}
				} else {
					b.log.Info("bemfa subscribed", "topic", topic)
				}
			}
		})
		opts.SetConnectionLostHandler(func(_ MQTT.Client, err error) {
			b.setAllStatus(statusRetry)
			b.log.Warn("bemfa connection lost", "error", err)
			select {
			case lost <- struct{}{}:
			default:
			}
		})

		client := MQTT.NewClient(opts)
		token := client.Connect()
		token.Wait()
		if err := token.Error(); err != nil {
			b.setAllStatus(fmt.Sprintf("连接失败: %v", err))
			b.log.Warn("bemfa connect failed", "error", err)
			if !sleepContext(ctx, 10*time.Second) {
				return
			}
			continue
		}

		select {
		case <-ctx.Done():
			client.Disconnect(250)
			return
		case <-lost:
			client.Disconnect(250)
			if !sleepContext(ctx, 5*time.Second) {
				return
			}
		}
	}
}

func (b *bemfaRunner) handler(topic string) MQTT.MessageHandler {
	return func(client MQTT.Client, msg MQTT.Message) {
		payload := string(msg.Payload())
		sub := b.subs[topic]
		if sub == nil {
			return
		}
		b.log.Info("bemfa message", "topic", topic, "payload", payload)
		client.Publish(topic+"/up", 1, true, msg.Payload())
		go b.apply(sub, payload)
	}
}

func (b *bemfaRunner) apply(sub *bemfaSubscription, payload string) {
	switch strings.ToLower(strings.TrimSpace(payload)) {
	case "on", "true":
		sub.Status(statusOnline + " · 执行唤醒")
		if err := sub.Actions.Wake(); err != nil {
			b.log.Error("bemfa wake failed", "topic", sub.Topic, "error", err)
			sub.Status(statusOnline + " · 唤醒失败")
		} else {
			sub.Status(statusOnline)
		}
	case "off", "false":
		sub.Status(statusOnline + " · 执行关机")
		if err := sub.Actions.Shutdown(); err != nil {
			b.log.Error("bemfa shutdown failed", "topic", sub.Topic, "error", err)
			sub.Status(statusOnline + " · 关机失败")
		} else {
			sub.Status(statusOnline)
		}
	default:
		b.log.Debug("bemfa ignored payload", "topic", sub.Topic, "payload", payload)
	}
}

func (b *bemfaRunner) setAllStatus(status string) {
	for _, sub := range b.subs {
		sub.Status(status)
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
