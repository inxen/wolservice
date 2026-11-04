package reach

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestEmptyTargets(t *testing.T) {
	result := Check(context.Background(), nil, "ping", 0, time.Second)
	if result.State != StateUnknown {
		t.Fatalf("state = %q", result.State)
	}
}

func TestTCPProbeReachable(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	result := Check(context.Background(), []string{"127.0.0.1"}, "tcp", port, time.Second)
	if result.State != StateReachable {
		t.Fatalf("state = %q, probes = %+v", result.State, result.Probes)
	}
}

func TestTCPProbeUnreachable(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()

	result := Check(context.Background(), []string{"127.0.0.1"}, "tcp", port, 300*time.Millisecond)
	if result.State != StateUnreachable {
		t.Fatalf("state = %q, probes = %+v", result.State, result.Probes)
	}
}

func TestPingLoopback(t *testing.T) {
	result := Check(context.Background(), []string{"127.0.0.1"}, "ping", 0, 2*time.Second)
	if result.State != StateReachable {
		t.Skipf("ping loopback unavailable in this environment: %+v", result.Probes)
	}
}
