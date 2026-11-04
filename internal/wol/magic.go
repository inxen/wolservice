package wol

import (
	"context"
	"fmt"
	"net"
)

// MagicPacket builds the 102-byte Wake-on-LAN payload for a MAC address.
func MagicPacket(mac string) ([]byte, error) {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return nil, fmt.Errorf("parse mac %q: %w", mac, err)
	}
	if len(hw) != 6 {
		return nil, fmt.Errorf("mac %q is not a 48-bit address", mac)
	}
	packet := make([]byte, 102)
	for i := 0; i < 6; i++ {
		packet[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:], hw)
	}
	return packet, nil
}

// LocalIPv4Broadcasts returns the directed broadcast address of every active
// non-loopback IPv4 interface, e.g. 192.168.1.255/24 -> 192.168.1.255.
func LocalIPv4Broadcasts() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var broadcasts []string
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipNet.IP.To4()
			if ip4 == nil || len(ipNet.Mask) != net.IPv4len {
				continue
			}
			if !ip4.IsGlobalUnicast() {
				continue
			}
			bcast := make(net.IP, net.IPv4len)
			for i := 0; i < net.IPv4len; i++ {
				bcast[i] = ip4[i] | ^ipNet.Mask[i]
			}
			value := bcast.String()
			if _, exists := seen[value]; exists {
				continue
			}
			seen[value] = struct{}{}
			broadcasts = append(broadcasts, value)
		}
	}
	if len(broadcasts) == 0 {
		return nil, fmt.Errorf("no active IPv4 interface found")
	}
	return broadcasts, nil
}

// Result describes what was actually sent.
type Result struct {
	Packets    int
	Broadcasts []string
}

// Send transmits magic packets to every broadcast address and MAC combination.
// When broadcastIPs is empty all local directed broadcast addresses are used.
func Send(macs []string, broadcastIPs []string, port, repeat int) (Result, error) {
	var result Result
	if len(macs) == 0 {
		return result, fmt.Errorf("no mac address configured")
	}
	if port <= 0 || port > 65535 {
		port = 9
	}
	if repeat <= 0 {
		repeat = 1
	}
	if repeat > 10 {
		repeat = 10
	}
	targets := broadcastIPs
	if len(targets) == 0 {
		var err error
		targets, err = LocalIPv4Broadcasts()
		if err != nil {
			return result, err
		}
	}

	packets := make([][]byte, 0, len(macs))
	for _, mac := range macs {
		pkt, err := MagicPacket(mac)
		if err != nil {
			return result, err
		}
		packets = append(packets, pkt)
	}

	conn, err := listenBroadcast()
	if err != nil {
		return result, fmt.Errorf("open udp socket: %w", err)
	}
	defer conn.Close()

	var firstErr error
	for _, target := range targets {
		ip := net.ParseIP(target)
		if ip == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("invalid broadcast ip %q", target)
			}
			continue
		}
		addr := &net.UDPAddr{IP: ip.To4(), Port: port}
		if addr.IP == nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("broadcast ip %q is not IPv4", target)
			}
			continue
		}
		sent := false
		for _, pkt := range packets {
			for i := 0; i < repeat; i++ {
				if _, err := conn.WriteToUDP(pkt, addr); err != nil {
					if firstErr == nil {
						firstErr = fmt.Errorf("send to %s:%d: %w", target, port, err)
					}
					break
				}
				result.Packets++
				sent = true
			}
		}
		if sent {
			result.Broadcasts = append(result.Broadcasts, target)
		}
	}
	if result.Packets == 0 {
		if firstErr == nil {
			firstErr = fmt.Errorf("no magic packet sent")
		}
		return result, firstErr
	}
	return result, nil
}

func listenBroadcast() (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: enableBroadcast}
	pc, err := lc.ListenPacket(context.Background(), "udp4", ":0")
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		pc.Close()
		return nil, fmt.Errorf("unexpected packet conn type %T", pc)
	}
	return conn, nil
}
