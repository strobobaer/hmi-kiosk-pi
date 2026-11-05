//go:build linux

package platform

import (
	"net"
	"testing"
)

func TestHasEstablishedParsesProcNetTCP(t *testing.T) {
	cases := []struct {
		ip   string
		port int
		want bool
	}{
		{"127.0.0.1", 5900, true},     // state 01, little-endian 0100007F
		{"10.0.0.5", 5900, true},      // 0500000A
		{"10.0.0.5", 5901, false},     // wrong port
		{"192.168.0.10", 5900, false}, // FIN_WAIT (04)
		{"10.0.0.6", 5900, false},
	}
	for _, c := range cases {
		got, err := HasEstablished("testdata/proc", net.ParseIP(c.ip), c.port)
		if err != nil || got != c.want {
			t.Errorf("%s:%d got %v err %v, want %v", c.ip, c.port, got, err, c.want)
		}
	}
}

func TestHasEstablishedIPv6(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"::1", true},
		{"192.168.1.30", true}, // IPv4-mapped in tcp6
		{"2001:db8::1", false}, // FIN_WAIT
		{"2001:db8::2", false},
	}
	for _, c := range cases {
		got, err := HasEstablished("testdata/proc", net.ParseIP(c.ip), 5900)
		if err != nil || got != c.want {
			t.Errorf("%s got %v err %v, want %v", c.ip, got, err, c.want)
		}
	}
}

func TestHasEstablishedMissingFilesNoError(t *testing.T) {
	got, err := HasEstablished("testdata/nope", net.ParseIP("127.0.0.1"), 5900)
	if err != nil || got {
		t.Fatalf("got %v, %v", got, err)
	}
}
