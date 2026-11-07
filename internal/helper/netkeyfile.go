package helper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"hmikiosk/internal/config"
)

// Das eigene NetworkManager-Profil. Die UUID ist fest, damit das Profil auf jedem Gerät
// gleich heißt und nmcli es eindeutig findet.
const (
	NMConnID       = "hmi-kiosk-eth"
	NMConnUUID     = "6f1d2c3b-8a47-4e5c-9b21-4d7e0a3c5f18"
	NMConnFile     = NMConnID + ".nmconnection"
	nmConnPriority = 100
)

// ifaceRe: Schnittstellennamen des Kernels (höchstens 15 Zeichen), hier nur harmlose Zeichen.
var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.]{0,14}$`)

// ValidIface prüft einen Schnittstellennamen streng (er steht im Keyfile und in nmcli-Argumenten).
func ValidIface(s string) bool { return ifaceRe.MatchString(s) }

// FirstEthernet liefert die erste kabelgebundene Schnittstelle unter sysClassNet (/sys/class/net):
// Typ 1 (Ethernet), kein wireless-Ordner und ein echtes Gerät (keine Brücke, kein veth).
func FirstEthernet(sysClassNet string) (string, error) {
	entries, err := os.ReadDir(sysClassNet)
	if err != nil {
		return "", fmt.Errorf("Netzwerkschnittstellen nicht lesbar: %v", err)
	}
	var names []string
	for _, e := range entries {
		n := e.Name()
		if !ValidIface(n) {
			continue
		}
		d := filepath.Join(sysClassNet, n)
		typ, err := os.ReadFile(filepath.Join(d, "type"))
		if err != nil || strings.TrimSpace(string(typ)) != "1" {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "wireless")); err == nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(d, "device")); err != nil {
			continue
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "", errors.New("Keine Ethernet-Schnittstelle gefunden.")
	}
	sort.Strings(names)
	return names[0], nil
}

// NMKeyfile erzeugt das Keyfile des eigenen Profils. IPv6 bleibt bei method=auto, dem Standard
// des Systemprofils von Raspberry Pi OS: Diese Seite verwaltet nur IPv4.
func NMKeyfile(iface string, e config.EthernetCfg) ([]byte, error) {
	if !ValidIface(iface) {
		return nil, fmt.Errorf("Schnittstellenname ungültig: %q", iface)
	}
	if e.Method != config.EthDHCP && e.Method != config.EthStatic {
		return nil, errors.New("Die Ethernet-Einstellung braucht DHCP oder eine feste Adresse.")
	}
	if err := config.ValidateEthernet(e); err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[connection]\nid=%s\nuuid=%s\ntype=ethernet\ninterface-name=%s\nautoconnect=true\nautoconnect-priority=%d\n\n",
		NMConnID, NMConnUUID, iface, nmConnPriority)
	b.WriteString("[ethernet]\n\n[ipv4]\n")
	if e.Method == config.EthDHCP {
		b.WriteString("method=auto\n")
	} else {
		b.WriteString("method=manual\n")
		fmt.Fprintf(&b, "address1=%s/%d", e.Address, e.Prefix)
		if e.Gateway != "" {
			b.WriteString("," + e.Gateway)
		}
		b.WriteString("\n")
		if len(e.DNS) > 0 {
			b.WriteString("dns=" + strings.Join(e.DNS, ";") + ";\nignore-auto-dns=true\n")
		}
	}
	b.WriteString("\n[ipv6]\nmethod=auto\n")
	return []byte(b.String()), nil
}
