package config

import (
	"net/netip"
	"regexp"
	"strings"
)

var ntpHostRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)

// ValidNTPServer lässt nur Rechnernamen und IP-Adressen durch, nie ein
// führendes '-' und keine Zonenkennung. Admin-Seite und root-Helfer prüfen gleich.
func ValidNTPServer(s string) bool {
	if _, err := netip.ParseAddr(s); err == nil {
		return !strings.Contains(s, "%")
	}
	return ntpHostRe.MatchString(s)
}
