//go:build linux

package platform

import (
	"net"
	"os"
	"path/filepath"
	"strings"
)

// EthernetHasLink ist wahr, wenn eine kabelgebundene Schnittstelle Träger meldet.
func EthernetHasLink(sysRoot string) bool {
	entries, err := os.ReadDir(filepath.Join(sysRoot, "class", "net"))
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if name == "lo" || isWifiIn(sysRoot, name) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(sysRoot, "class", "net", name, "carrier"))
		if err == nil && strings.TrimSpace(string(b)) == "1" {
			return true
		}
	}
	return false
}

// InterfaceForIP liefert die Schnittstelle, der ip lokal zugeordnet ist.
func InterfaceForIP(ip net.IP) (name string, isWifi bool) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return "", false
	}
	for _, ifc := range ifaces {
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipn, ok := a.(*net.IPNet); ok && ipn.IP.Equal(ip) {
				return ifc.Name, isWifiIn("/sys", ifc.Name)
			}
		}
	}
	return "", false
}

func isWifiIn(sysRoot, name string) bool {
	_, err := os.Stat(filepath.Join(sysRoot, "class", "net", name, "wireless"))
	return err == nil
}
