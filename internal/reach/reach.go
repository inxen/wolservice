package reach

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Method string

const (
	MethodPing Method = "ping"
	MethodTCP  Method = "tcp"
	MethodAuto Method = "auto"
)

type Probe struct {
	Target  string `json:"target"`
	OK      bool   `json:"ok"`
	Method  string `json:"method"`
	Latency int64  `json:"latency_ms"`
	Error   string `json:"error,omitempty"`
}

type Result struct {
	State  string  `json:"state"`
	Probes []Probe `json:"probes"`
}

const (
	StateReachable          = "Reachable"
	StatePartiallyReachable = "PartiallyReachable"
	StateUnreachable        = "Unreachable"
	StateUnknown            = "Unknown"
)

// Check probes all targets concurrently and derives the aggregate state.
func Check(ctx context.Context, targets []string, method string, tcpPort int, timeout time.Duration) Result {
	if len(targets) == 0 {
		return Result{State: StateUnknown}
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	probes := make([]Probe, len(targets))
	done := make(chan int, len(targets))
	for i, target := range targets {
		go func(idx int, host string) {
			probes[idx] = probeOne(ctx, host, method, tcpPort, timeout)
			done <- idx
		}(i, target)
	}
	for range targets {
		<-done
	}

	okCount := 0
	for _, probe := range probes {
		if probe.OK {
			okCount++
		}
	}
	switch {
	case okCount == len(probes):
		return Result{State: StateReachable, Probes: probes}
	case okCount > 0:
		return Result{State: StatePartiallyReachable, Probes: probes}
	default:
		return Result{State: StateUnreachable, Probes: probes}
	}
}

func probeOne(ctx context.Context, target, method string, tcpPort int, timeout time.Duration) Probe {
	probe := Probe{Target: target, Method: method}
	useTCP := method == string(MethodTCP) || (method == string(MethodAuto) && tcpPort > 0)
	start := time.Now()
	var err error
	if useTCP {
		probe.Method = string(MethodTCP)
		err = tcpProbe(ctx, target, tcpPort, timeout)
	} else {
		probe.Method = string(MethodPing)
		err = pingProbe(ctx, target, timeout)
	}
	probe.Latency = time.Since(start).Milliseconds()
	if err != nil {
		probe.Error = err.Error()
		return probe
	}
	probe.OK = true
	return probe
}

func tcpProbe(ctx context.Context, host string, port int, timeout time.Duration) error {
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	d := net.Dialer{}
	conn, err := d.DialContext(dialCtx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return err
	}
	_ = conn.Close()
	return nil
}

func pingProbe(ctx context.Context, host string, timeout time.Duration) error {
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var cmd *exec.Cmd
	ms := int(timeout.Milliseconds())
	if ms < 100 {
		ms = 100
	}
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(pingCtx, "ping", "-n", "1", "-w", strconv.Itoa(ms), host)
	case "darwin":
		seconds := int(timeout.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-t", strconv.Itoa(seconds), host)
	default:
		seconds := int(timeout.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		cmd = exec.CommandContext(pingCtx, "ping", "-c", "1", "-W", strconv.Itoa(seconds), host)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		if pingCtx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("ping timeout")
		}
		text := string(output)
		if len(text) > 160 {
			text = text[:160]
		}
		text = strings.TrimSpace(text)
		if text == "" {
			return err
		}
		return fmt.Errorf("%s", text)
	}
	return nil
}
