package iot

import "testing"

func TestNormalizePowerState(t *testing.T) {
	cases := map[string]string{
		"on": "on", "ON": "on", "true": "on", "True": "on",
		"off": "off", "OFF": "off", "false": "off",
		"": "", "x": "",
	}
	for input, want := range cases {
		if got := normalizePowerState(input); got != want {
			t.Fatalf("normalizePowerState(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDuerType(t *testing.T) {
	cases := map[string]string{
		"outlet": "SOCKET", "light": "LIGHT", "multi_outlet": "MULTI_SOCKET",
		"sensor": "AIR_MONITOR", "fan": "FAN", "aircondition": "AIR_CONDITION",
	}
	for input, want := range cases {
		if got := duerType(input); got != want {
			t.Fatalf("duerType(%q) = %q, want %q", input, got, want)
		}
	}
}
