package platform

import (
	"os"
	"path/filepath"
	"strings"
)

const maxHardwareIDLen = 64

// HardwareID liefert die Kennung des Geräts: bei einem Raspberry Pi die
// Seriennummer aus dem Device-Tree, sonst die DMI-Produkt-UUID. Ohne lesbare
// Kennung kommt "" zurück. /etc/machine-id taugt nicht, sie wird mit der SD-Karte
// geklont. Die Kennung ist kleingeschrieben und enthält nur [0-9a-z-].
func HardwareID(procRoot, sysRoot string) string {
	for _, p := range []string{
		filepath.Join(procRoot, "device-tree", "serial-number"),
		filepath.Join(sysRoot, "class", "dmi", "id", "product_uuid"),
	} {
		if id := readHardwareID(p); id != "" {
			return id
		}
	}
	return ""
}

func readHardwareID(path string) string {
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return normalizeHardwareID(string(b))
}

func normalizeHardwareID(raw string) string {
	id := strings.ToLower(strings.TrimSpace(strings.TrimRight(raw, "\x00")))
	if id == "" || len(id) > maxHardwareIDLen || strings.Trim(id, "0-") == "" {
		return ""
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r == '-') {
			return ""
		}
	}
	return id
}
