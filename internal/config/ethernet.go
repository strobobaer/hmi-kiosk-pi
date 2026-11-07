package config

import (
	"errors"
	"fmt"
	"math/bits"
	"net/netip"
	"strconv"
	"strings"
)

// Arten der Ethernet-Einstellung. Leer heißt: nicht verwaltet, das System bleibt unberührt.
const (
	EthDHCP   = "dhcp"
	EthStatic = "static"
)

const (
	MinEthPrefix     = 8
	MaxEthPrefix     = 30
	DefaultEthPrefix = 24
	MaxEthDNS        = 2
)

// EthernetCfg ist die IPv4-Einstellung der ersten Ethernet-Schnittstelle. Sie wird erst
// gespeichert, wenn die Änderung am Gerät bestätigt ist; der Import übernimmt sie nicht.
type EthernetCfg struct {
	Method  string   `json:"method,omitempty"`
	Address string   `json:"address,omitempty"`
	Prefix  int      `json:"prefix,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	DNS     []string `json:"dns,omitempty"`
}

// ParsePrefix nimmt eine Präfixlänge ("24") oder eine Netzmaske ("255.255.255.0"). Leer ist 24.
func ParsePrefix(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return DefaultEthPrefix, nil
	}
	var n int
	if strings.Contains(s, ".") {
		m, err := netip.ParseAddr(s)
		if err != nil || !m.Is4() {
			return 0, fmt.Errorf("Netzmaske ungültig: %q", s)
		}
		b := m.As4()
		v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
		n = bits.LeadingZeros32(^v)
		if n < 32 && v<<n != 0 { // nach den Einsen dürfen nur Nullen folgen
			return 0, fmt.Errorf("Netzmaske ungültig: %q", s)
		}
	} else {
		v, err := strconv.Atoi(s)
		if err != nil || strings.HasPrefix(s, "+") || strings.HasPrefix(s, "-") {
			return 0, fmt.Errorf("Präfix ungültig: %q", s)
		}
		n = v
	}
	if n < MinEthPrefix || n > MaxEthPrefix {
		return 0, fmt.Errorf("Präfix muss zwischen %d und %d liegen (Netzmaske 255.0.0.0 bis 255.255.255.252).", MinEthPrefix, MaxEthPrefix)
	}
	return n, nil
}

// parseIPv4 lässt nur eine IPv4-Adresse in Normalform durch (keine Zone, kein IPv6).
func parseIPv4(what, s string) (netip.Addr, error) {
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, fmt.Errorf("%s ist ungültig: %q", what, s)
	}
	if !a.Is4() {
		return netip.Addr{}, fmt.Errorf("%s muss eine IPv4-Adresse sein: %q", what, s)
	}
	return a, nil
}

func usableUnicast(a netip.Addr) bool {
	return !(a.IsUnspecified() || a.IsLoopback() || a.IsMulticast() || a.IsLinkLocalUnicast() ||
		a == netip.AddrFrom4([4]byte{255, 255, 255, 255}))
}

func broadcastOf(p netip.Prefix) netip.Addr {
	b := p.Masked().Addr().As4()
	host := uint32(1)<<(32-p.Bits()) - 1
	v := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) | host
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// ValidateEthernet prüft die Ethernet-Einstellung. Admin-Seite und root-Helfer nutzen dieselbe Prüfung.
func ValidateEthernet(e EthernetCfg) error {
	switch e.Method {
	case "", EthDHCP:
		if e.Address != "" || e.Prefix != 0 || e.Gateway != "" || len(e.DNS) != 0 {
			return errors.New("Bei DHCP (oder ohne Einstellung) dürfen Adresse, Präfix, Gateway und DNS nicht gesetzt sein.")
		}
		return nil
	case EthStatic:
	default:
		return fmt.Errorf("Unbekannte Art der Ethernet-Einstellung: %q (erlaubt: dhcp, static)", e.Method)
	}
	if e.Address == "" {
		return errors.New("Bitte eine IP-Adresse angeben.")
	}
	addr, err := parseIPv4("Die IP-Adresse", e.Address)
	if err != nil {
		return err
	}
	if !usableUnicast(addr) {
		return fmt.Errorf("Die IP-Adresse %s ist nicht verwendbar (0.0.0.0, Loopback, Multicast oder Link-Local).", addr)
	}
	if e.Prefix < MinEthPrefix || e.Prefix > MaxEthPrefix {
		return fmt.Errorf("Präfix muss zwischen %d und %d liegen.", MinEthPrefix, MaxEthPrefix)
	}
	net := netip.PrefixFrom(addr, e.Prefix).Masked()
	bcast := broadcastOf(net)
	if addr == net.Addr() {
		return fmt.Errorf("Die IP-Adresse %s ist die Netzadresse von %s.", addr, net)
	}
	if addr == bcast {
		return fmt.Errorf("Die IP-Adresse %s ist die Broadcast-Adresse von %s.", addr, net)
	}
	if e.Gateway != "" {
		gw, err := parseIPv4("Das Gateway", e.Gateway)
		if err != nil {
			return err
		}
		if !net.Contains(gw) {
			return fmt.Errorf("Das Gateway %s liegt nicht im selben Netz wie die Adresse (%s).", gw, net)
		}
		if gw == addr || gw == net.Addr() || gw == bcast {
			return fmt.Errorf("Das Gateway %s darf weder die eigene Adresse noch Netz- oder Broadcast-Adresse sein.", gw)
		}
	}
	if len(e.DNS) > MaxEthDNS {
		return fmt.Errorf("Höchstens %d DNS-Server.", MaxEthDNS)
	}
	for _, d := range e.DNS {
		a, err := parseIPv4("Der DNS-Server", d)
		if err != nil {
			return err
		}
		if !usableUnicast(a) {
			return fmt.Errorf("Der DNS-Server %s ist keine verwendbare Unicast-Adresse.", a)
		}
	}
	return nil
}
