//go:build linux

package platform

import (
	"bufio"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// HasEstablished meldet, ob eine ESTABLISHED-Verbindung zu ip:port besteht.
func HasEstablished(procRoot string, ip net.IP, port int) (bool, error) {
	for _, name := range []string{"tcp", "tcp6"} {
		ok, err := scanProcNet(filepath.Join(procRoot, "net", name), ip, port)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func scanProcNet(path string, ip net.IP, port int) (bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // Kopfzeile
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 || fields[3] != "01" {
			continue
		}
		rip, rport, ok := parseProcAddr(fields[2])
		if ok && rport == port && rip.Equal(ip) {
			return true, nil
		}
	}
	return false, sc.Err()
}

// parseProcAddr zerlegt "HEXADDR:HEXPORT". Der Kernel schreibt jedes
// 32-Bit-Wort der Adresse in Host-Byte-Reihenfolge (little endian).
func parseProcAddr(s string) (net.IP, int, bool) {
	addrHex, portHex, ok := strings.Cut(s, ":")
	if !ok {
		return nil, 0, false
	}
	p, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return nil, 0, false
	}
	raw, err := hex.DecodeString(addrHex)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return nil, 0, false
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	return net.IP(raw), int(p), true // IP.Equal gleicht ::ffff:a.b.c.d mit a.b.c.d ab
}
