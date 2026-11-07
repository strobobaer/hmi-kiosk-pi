package config

import (
	"strings"
	"testing"
)

func static(addr string, prefix int, gw string, dns ...string) EthernetCfg {
	return EthernetCfg{Method: EthStatic, Address: addr, Prefix: prefix, Gateway: gw, DNS: dns}
}

func TestValidateEthernetAccepts(t *testing.T) {
	for _, e := range []EthernetCfg{
		{},
		{Method: EthDHCP},
		static("192.168.1.10", 24, ""),
		static("192.168.1.10", 24, "192.168.1.1", "192.168.1.2", "9.9.9.9"),
		static("10.1.2.3", 8, "10.0.0.1"),
		static("172.16.5.6", 30, "172.16.5.5"),
	} {
		if err := ValidateEthernet(e); err != nil {
			t.Errorf("%+v: %v", e, err)
		}
	}
}

func TestValidateEthernetRejects(t *testing.T) {
	for name, c := range map[string]struct {
		e    EthernetCfg
		want string
	}{
		"methode":         {EthernetCfg{Method: "manual"}, "Art"},
		"dhcp mit werten": {EthernetCfg{Method: EthDHCP, Address: "192.168.1.10"}, "DHCP"},
		"leer mit werten": {EthernetCfg{Prefix: 24}, "DHCP"},
		"keine adresse":   {static("", 24, ""), "IP-Adresse"},
		"kaputt":          {static("192.168.1.300", 24, ""), "IP-Adresse"},
		"ipv6":            {static("fe80::1", 24, ""), "IPv4"},
		"v4in6":           {static("::ffff:192.168.1.10", 24, ""), "IPv4"},
		"führende null":   {static("192.168.01.10", 24, ""), "IP-Adresse"},
		"zone":            {static("192.168.1.10%eth0", 24, ""), "IP-Adresse"},
		"präfix klein":    {static("192.168.1.10", 7, ""), "Präfix"},
		"präfix groß":     {static("192.168.1.10", 31, ""), "Präfix"},
		"netzadresse":     {static("192.168.1.0", 24, ""), "Netzadresse"},
		"broadcast":       {static("192.168.1.255", 24, ""), "Broadcast"},
		"null":            {static("0.0.0.0", 8, ""), "nicht verwendbar"},
		"loopback":        {static("127.0.0.5", 8, ""), "nicht verwendbar"},
		"multicast":       {static("224.0.0.5", 8, ""), "nicht verwendbar"},
		"link-local":      {static("169.254.3.4", 16, ""), "nicht verwendbar"},
		"gw außerhalb":    {static("192.168.1.10", 24, "192.168.2.1"), "selben Netz"},
		"gw gleich":       {static("192.168.1.10", 24, "192.168.1.10"), "Gateway"},
		"gw broadcast":    {static("192.168.1.10", 24, "192.168.1.255"), "Gateway"},
		"gw kaputt":       {static("192.168.1.10", 24, "x"), "Gateway"},
		"dns kaputt":      {static("192.168.1.10", 24, "", "dns.example"), "DNS"},
		"dns multicast":   {static("192.168.1.10", 24, "", "239.1.1.1"), "DNS"},
		"dns null":        {static("192.168.1.10", 24, "", "0.0.0.0"), "DNS"},
		"dns broadcast":   {static("192.168.1.10", 24, "", "255.255.255.255"), "DNS"},
		"dns drei":        {static("192.168.1.10", 24, "", "1.1.1.1", "8.8.8.8", "9.9.9.9"), "DNS"},
		"zeilenumbruch":   {static("192.168.1.10\n", 24, ""), "IP-Adresse"},
	} {
		err := ValidateEthernet(c.e)
		if err == nil {
			t.Errorf("%s: angenommen", name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %q enthält nicht %q", name, err, c.want)
		}
	}
}

func TestParsePrefix(t *testing.T) {
	for in, want := range map[string]int{"24": 24, " 16 ": 16, "8": 8, "30": 30, "255.255.255.0": 24, "255.255.0.0": 16, "255.255.255.252": 30, "255.0.0.0": 8, "": 24} {
		got, err := ParsePrefix(in)
		if err != nil || got != want {
			t.Errorf("%q: %d %v, erwartet %d", in, got, err, want)
		}
	}
	for _, in := range []string{"7", "31", "32", "abc", "255.255.0.255", "255.255.255.254", "0.0.0.0", "24.5", "-24", "255.255.255.0.0"} {
		if _, err := ParsePrefix(in); err == nil {
			t.Errorf("%q angenommen", in)
		}
	}
}

func TestValidateChecksEthernet(t *testing.T) {
	c := Default()
	c.Network.Ethernet = static("192.168.1.0", 24, "")
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "Netzadresse") {
		t.Fatalf("Validate: %v", err)
	}
	c.Network.Ethernet = static("192.168.1.10", 24, "192.168.1.1")
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}
