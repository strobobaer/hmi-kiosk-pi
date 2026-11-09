package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
)

// Arten einer Kachel: VNC (Standard, auch leer) oder eine Webseite.
const (
	KindVNC = "vnc"
	KindWeb = "web"
)

// IsWeb meldet eine Web-Kachel; leer oder "vnc" ist VNC.
func (h HMI) IsWeb() bool { return h.Kind == KindWeb }

const maxWebURLLen = 2048

// labelRe ist ein Namensteil: Buchstaben, Ziffern, innen auch „-“ und „_“.
var labelRe = regexp.MustCompile(`^[A-Za-z0-9_]([A-Za-z0-9_-]{0,61}[A-Za-z0-9_])?$`)

// validDNSName lässt nur echte Rechnernamen zu (kein „*“, kein Punkt am Ende).
func validDNSName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if !labelRe.MatchString(l) {
			return false
		}
	}
	return true
}

// numericLabelRe: So endende Namen liest Chromium als IPv4-Adresse (auch oktal, hex,
// verkürzt), netip aber nicht. Sie sind nur als echte IPv4-Adresse erlaubt.
var numericLabelRe = regexp.MustCompile(`(?i)^(0x[0-9a-f]*|[0-9]+)$`)

// validWebHost: IP-Adresse ohne Zone oder Rechnername, der nicht wie eine Zahl aussieht.
func validWebHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return ip.Zone() == ""
	}
	if !validDNSName(host) {
		return false
	}
	labels := strings.Split(host, ".")
	for _, l := range labels {
		if strings.HasPrefix(strings.ToLower(l), "0x") && numericLabelRe.MatchString(l) {
			return false
		}
	}
	return !numericLabelRe.MatchString(labels[len(labels)-1])
}

// isLocalHost: das Terminal selbst (Loopback, unbestimmte Adresse, localhost). Dort laufen
// Kachelseite und Admin-Seite; eine Web-Kachel darauf hebelte Richtlinie und Fensterregel aus.
func isLocalHost(host string) bool {
	h := strings.ToLower(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	ip, err := netip.ParseAddr(h)
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsUnspecified()
}

// ParseWebURL prüft die Adresse einer Web-Kachel: nur http/https mit Rechner, optional
// Port, Pfad, Abfrage und Anker; keine Zugangsdaten, keine Leer- oder Steuerzeichen.
func ParseWebURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("Adresse fehlt")
	}
	if len(raw) > maxWebURLLen {
		return nil, fmt.Errorf("Adresse ist länger als %d Zeichen", maxWebURLLen)
	}
	for _, r := range raw {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return nil, errors.New("Adresse enthält Leer- oder Steuerzeichen")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("Adresse ist ungültig")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("Adresse muss mit http:// oder https:// beginnen")
	}
	if u.User != nil {
		return nil, errors.New("Adresse darf keinen Benutzer und kein Passwort enthalten (Anmeldung bitte in der Webseite)")
	}
	if u.Opaque != "" || u.Host == "" {
		return nil, errors.New("Adresse ohne Rechnernamen")
	}
	if !validWebHost(u.Hostname()) {
		return nil, fmt.Errorf("Rechnername der Adresse ist ungültig: %q", u.Hostname())
	}
	if isLocalHost(u.Hostname()) {
		return nil, errors.New("Adresse zeigt auf das Terminal selbst (localhost, 127.0.0.1, ::1); das ist für Web-Kacheln nicht erlaubt")
	}
	if p := u.Port(); p != "" || strings.HasSuffix(u.Host, ":") {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != p {
			return nil, fmt.Errorf("Port der Adresse außerhalb 1-65535: %q", p)
		}
	}
	return u, nil
}

func checkWebURL(id, raw string) error {
	if _, err := ParseWebURL(raw); err != nil {
		return fmt.Errorf("HMI %q: %v", id, err)
	}
	return nil
}

func defaultPort(u *url.URL) int {
	if p := u.Port(); p != "" {
		n, _ := strconv.Atoi(p)
		return n
	}
	if u.Scheme == "https" {
		return 443
	}
	return 80
}

// Endpoint liefert Rechner und Port für die Erreichbarkeit: bei VNC Host und Port, bei
// einer Web-Kachel die der Adresse (Standardport je Schema). ok ist false bei
// ungültiger Adresse.
func (h HMI) Endpoint() (host string, port int, ok bool) {
	if !h.IsWeb() {
		return h.Host, h.Port, true
	}
	u, err := ParseWebURL(h.URL)
	if err != nil {
		return "", 0, false
	}
	return strings.ToLower(u.Hostname()), defaultPort(u), true
}

// WebOrigin ist der Ursprung scheme://host:port einer Adresse, klein geschrieben und
// immer mit Port.
func WebOrigin(raw string) (string, error) {
	u, err := ParseWebURL(raw)
	if err != nil {
		return "", err
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host + ":" + strconv.Itoa(defaultPort(u)), nil
}

// WebOrigins sind die Ursprünge aller Web-Kacheln, sortiert und ohne Doppel. Nur sie
// gibt die Chromium-Richtlinie zusätzlich frei („nur derselbe Server“).
func WebOrigins(c Config) []string {
	out := []string{}
	for _, h := range c.HMIs {
		if !h.IsWeb() {
			continue
		}
		if o, err := WebOrigin(h.URL); err == nil && !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	slices.Sort(out)
	return out
}

// Grenzen der Ursprungsliste; der Helfer prüft dieselben. MaxWebOriginsJSON begrenzt die
// JSON-Liste so, dass die Anfrage an den Helfer (höchstens 4096 Byte, Anführungszeichen
// maskiert) und seine Zustandsdatei sicher passen.
const (
	MaxWebOrigins     = 32
	MaxWebOriginsJSON = 3000
)

// checkWebOrigins prüft die Grenzen beim Speichern: lieber eine klare Meldung als eine
// Richtlinie, die der Helfer ablehnt.
func checkWebOrigins(c Config) error {
	origins := WebOrigins(c)
	if len(origins) > MaxWebOrigins {
		return fmt.Errorf("Höchstens %d verschiedene Web-Server (Schema, Rechner, Port) sind möglich, eingetragen sind %d", MaxWebOrigins, len(origins))
	}
	if b, _ := json.Marshal(origins); len(b) > MaxWebOriginsJSON {
		return fmt.Errorf("Die Adressen der Web-Server sind zusammen zu lang (%d von höchstens %d Zeichen); bitte weniger oder kürzere Rechnernamen verwenden", len(b), MaxWebOriginsJSON)
	}
	return nil
}

var originRe = regexp.MustCompile(`^(https?)://(\[[0-9a-f:.]+\]|[a-z0-9_.-]+):([1-9][0-9]{0,4})$`)

// ValidOrigin prüft einen Ursprung in genau der Form von WebOrigin (für den Helfer,
// der damit die Chromium-Richtlinie schreibt).
func ValidOrigin(s string) bool {
	m := originRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	if n, _ := strconv.Atoi(m[3]); n > 65535 {
		return false
	}
	host := m[2]
	if strings.HasPrefix(host, "[") {
		ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
		return err == nil && ip.Is6() && ip.Zone() == "" && !isLocalHost(ip.String())
	}
	if ip, err := netip.ParseAddr(host); err == nil && !ip.Is4() {
		return false
	}
	return validWebHost(host) && !isLocalHost(host)
}
