//go:build linux

package platform

import (
	"net"
	"testing"
)

func TestEthernetHasLinkReadsCarrier(t *testing.T) {
	if EthernetHasLink("testdata/sysnet") {
		t.Fatal("lo and wireless must not count, eth0 has no carrier")
	}
	if !EthernetHasLink("testdata/sysnet2") {
		t.Fatal("eth0 carrier=1 must count")
	}
	if EthernetHasLink("testdata/nope") {
		t.Fatal("missing root must be false")
	}
}

func TestIsWifi(t *testing.T) {
	if !isWifiIn("testdata/sysnet", "wlan0") || isWifiIn("testdata/sysnet", "eth0") {
		t.Fatal("wireless directory detection wrong")
	}
}

func TestInterfaceForIPLoopback(t *testing.T) {
	name, _ := InterfaceForIP(net.ParseIP("127.0.0.1"))
	if name == "" {
		t.Skip("no loopback interface")
	}
	if name != "lo" {
		t.Fatalf("got %q", name)
	}
	if n, w := InterfaceForIP(net.ParseIP("203.0.113.77")); n != "" || w {
		t.Fatalf("unknown ip gave %q %v", n, w)
	}
}
