package wol

import (
	"bytes"
	"net"
	"testing"
	"time"
)

func TestMagicPacket(t *testing.T) {
	packet, err := MagicPacket("01:02:03:04:05:06")
	if err != nil {
		t.Fatalf("MagicPacket: %v", err)
	}
	if len(packet) != 102 {
		t.Fatalf("packet length = %d, want 102", len(packet))
	}
	if !bytes.Equal(packet[:6], []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Fatalf("header = % X", packet[:6])
	}
	mac := []byte{1, 2, 3, 4, 5, 6}
	for i := 0; i < 16; i++ {
		if !bytes.Equal(packet[6+i*6:12+i*6], mac) {
			t.Fatalf("payload block %d = % X", i, packet[6+i*6:12+i*6])
		}
	}
}

func TestMagicPacketRejectsBadMAC(t *testing.T) {
	if _, err := MagicPacket("not-a-mac"); err == nil {
		t.Fatal("expected error for invalid mac")
	}
}

func TestSendToUDPLoopback(t *testing.T) {
	listener, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	defer listener.Close()
	port := listener.LocalAddr().(*net.UDPAddr).Port

	repeat := 3
	result, err := Send([]string{"01:02:03:04:05:06"}, []string{"127.0.0.1"}, port, repeat)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if result.Packets != repeat {
		t.Fatalf("packets = %d, want %d", result.Packets, repeat)
	}
	if len(result.Broadcasts) != 1 || result.Broadcasts[0] != "127.0.0.1" {
		t.Fatalf("broadcasts = %v", result.Broadcasts)
	}

	buf := make([]byte, 128)
	for i := 0; i < repeat; i++ {
		_ = listener.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := listener.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("read packet %d: %v", i, err)
		}
		if n != 102 {
			t.Fatalf("packet %d length = %d", i, n)
		}
	}
}

func TestSendRequiresMAC(t *testing.T) {
	if _, err := Send(nil, []string{"127.0.0.1"}, 9, 1); err == nil {
		t.Fatal("expected error for empty mac list")
	}
}

func TestLocalIPv4Broadcasts(t *testing.T) {
	broadcasts, err := LocalIPv4Broadcasts()
	if err != nil {
		t.Skipf("no IPv4 interface available: %v", err)
	}
	for _, broadcast := range broadcasts {
		if net.ParseIP(broadcast).To4() == nil {
			t.Fatalf("non-IPv4 broadcast %q", broadcast)
		}
	}
}
