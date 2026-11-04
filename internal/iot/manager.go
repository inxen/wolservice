package iot

import (
	"context"
	"log/slog"
	"sync"

	"wol/internal/config"
)

type DeviceStatus struct {
	Bemfa   string `json:"bemfa,omitempty"`
	Blinker string `json:"blinker,omitempty"`
}

type Manager struct {
	log    *slog.Logger
	mu     sync.RWMutex
	status map[string]DeviceStatus
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewManager(log *slog.Logger) *Manager {
	return &Manager{log: log, status: make(map[string]DeviceStatus)}
}

// Start launches IoT runners for every configured device. Actions returns the
// wake/shutdown callbacks for a device key; it is invoked once per runner.
func (m *Manager) Start(parent context.Context, devices []config.Device, actions func(key string) Actions) {
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel

	bemfaGroups := make(map[string][]*bemfaSubscription)
	for i := range devices {
		d := devices[i]
		key := d.Key
		if d.HasBemfa() {
			m.updateStatus(key, func(s *DeviceStatus) { s.Bemfa = statusIdle })
			sub := &bemfaSubscription{
				Topic:   d.IoT.Bemfa.Topic,
				Actions: actions(key),
				Status: func(value string) {
					m.updateStatus(key, func(s *DeviceStatus) { s.Bemfa = value })
				},
			}
			bemfaGroups[d.IoT.Bemfa.SecretKey] = append(bemfaGroups[d.IoT.Bemfa.SecretKey], sub)
		}
		if d.HasBlinker() {
			m.updateStatus(key, func(s *DeviceStatus) { s.Blinker = statusIdle })
			runner := newBlinkerRunner(d.IoT.Blinker, actions(key), m.log.With("device", key, "iot", "blinker"), func(value string) {
				m.updateStatus(key, func(s *DeviceStatus) { s.Blinker = value })
			})
			m.wg.Add(1)
			go func() {
				defer m.wg.Done()
				runner.run(ctx)
			}()
		}
	}

	for secret, subs := range bemfaGroups {
		runner := newBemfaRunner(secret, subs, m.log.With("iot", "bemfa"))
		m.wg.Add(1)
		go func() {
			defer m.wg.Done()
			runner.run(ctx)
		}()
	}
}

func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

func (m *Manager) Status(key string) DeviceStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status[key]
}

func (m *Manager) updateStatus(key string, update func(*DeviceStatus)) {
	m.mu.Lock()
	status := m.status[key]
	update(&status)
	m.status[key] = status
	m.mu.Unlock()
}
