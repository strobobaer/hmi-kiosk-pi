package helper

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

func TestKeyfileStatic(t *testing.T) {
	got, err := NMKeyfile("eth0", config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.10", Prefix: 24, Gateway: "192.168.1.1", DNS: []string{"192.168.1.2", "9.9.9.9"}})
	if err != nil {
		t.Fatal(err)
	}
	want := "[connection]\nid=hmi-kiosk-eth\nuuid=" + NMConnUUID + "\ntype=ethernet\ninterface-name=eth0\nautoconnect=true\nautoconnect-priority=100\n\n" +
		"[ethernet]\n\n" +
		"[ipv4]\nmethod=manual\naddress1=192.168.1.10/24,192.168.1.1\ndns=192.168.1.2;9.9.9.9;\nignore-auto-dns=true\n\n" +
		"[ipv6]\nmethod=auto\n"
	if string(got) != want {
		t.Fatalf("Inhalt:\n%s\nerwartet:\n%s", got, want)
	}
}

func TestKeyfileStaticWithoutGatewayAndDNS(t *testing.T) {
	got, err := NMKeyfile("enp1s0", config.EthernetCfg{Method: config.EthStatic, Address: "10.0.0.5", Prefix: 8})
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)
	if !strings.Contains(s, "\naddress1=10.0.0.5/8\n") || strings.Contains(s, "dns=") || !strings.Contains(s, "interface-name=enp1s0\n") {
		t.Fatalf("Inhalt:\n%s", s)
	}
}

func TestKeyfileDHCP(t *testing.T) {
	got, err := NMKeyfile("eth0", config.EthernetCfg{Method: config.EthDHCP})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "[ipv4]\nmethod=auto\n\n[ipv6]\nmethod=auto\n") || strings.Contains(string(got), "address1") {
		t.Fatalf("Inhalt:\n%s", got)
	}
}

func TestKeyfileRejectsBadValues(t *testing.T) {
	for name, c := range map[string]struct {
		iface string
		e     config.EthernetCfg
	}{
		"leer":           {"eth0", config.EthernetCfg{}},
		"netzadresse":    {"eth0", config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.0", Prefix: 24}},
		"name umbruch":   {"eth0\nid=x", config.EthernetCfg{Method: config.EthDHCP}},
		"name semikolon": {"eth;0", config.EthernetCfg{Method: config.EthDHCP}},
		"name lang":      {"eth0123456789abc", config.EthernetCfg{Method: config.EthDHCP}},
		"name leer":      {"", config.EthernetCfg{Method: config.EthDHCP}},
		"name bindestr":  {"-eth0", config.EthernetCfg{Method: config.EthDHCP}},
	} {
		if _, err := NMKeyfile(c.iface, c.e); err == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
}

func mkNetIf(t *testing.T, root, name, typ string, wireless, device bool) {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(d, "type"), []byte(typ+"\n"), 0o644)
	if wireless {
		os.Mkdir(filepath.Join(d, "wireless"), 0o755)
	}
	if device {
		os.Mkdir(filepath.Join(d, "device"), 0o755)
	}
}

func TestFirstEthernet(t *testing.T) {
	root := t.TempDir()
	mkNetIf(t, root, "lo", "772", false, false)
	mkNetIf(t, root, "wlan0", "1", true, true)
	mkNetIf(t, root, "docker0", "1", false, false) // Brücke ohne Gerät
	mkNetIf(t, root, "eth1", "1", false, true)
	mkNetIf(t, root, "eth0", "1", false, true)
	got, err := FirstEthernet(root)
	if err != nil || got != "eth0" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestFirstEthernetNone(t *testing.T) {
	root := t.TempDir()
	mkNetIf(t, root, "wlan0", "1", true, true)
	mkNetIf(t, root, "eth x", "1", false, true) // ungültiger Name wird übergangen
	if got, err := FirstEthernet(root); err == nil {
		t.Fatalf("gefunden: %q", got)
	}
	if _, err := FirstEthernet(filepath.Join(root, "fehlt")); err == nil {
		t.Fatal("fehlender Ordner angenommen")
	}
}
