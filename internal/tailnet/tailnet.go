// Package tailnet steuert Tailscale auf dem Terminal: Status lesen, mit einem Auth-Key anmelden, abmelden.
// Unter Linux (Pi) läuft das über den root-Helfer, unter Windows über tailscale.exe.
//
// Der Auth-Key ist ein Geheimnis: Er wird nie gespeichert, nie protokolliert, steht in keiner Fehlermeldung
// und nie in einer Befehlszeile. Er geht nur über eine Datei (--auth-key=file:<Pfad>), die danach gelöscht wird.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	// MaxKeyLen ist die größte erlaubte Länge eines Auth-Keys in Bytes.
	MaxKeyLen = 512
	// MaxHostLen ist die größte Länge eines Hostnamens (ein DNS-Label).
	MaxHostLen = 63
	// DefaultHost gilt, wenn der Gerätename keinen brauchbaren Namen ergibt.
	DefaultHost = "pdh-terminal"
)

// Status ist der Stand von Tailscale auf diesem Gerät.
type Status struct {
	Installed, LoggedIn      bool
	Name, DNSName, IP, State string
}

// Ctl steuert Tailscale. Up und Down brauchen unter Windows Administratorrechte oder den unbeaufsichtigten Modus.
type Ctl interface {
	Status(ctx context.Context) (Status, error)
	Up(ctx context.Context, authKey, hostname string) error
	Down(ctx context.Context) error
}

// ParseStatus liest die Ausgabe von "tailscale status --json" (oder die gekürzte Fassung des Helfers).
// {"installed":false} heißt: Tailscale ist nicht installiert.
func ParseStatus(b []byte) (Status, error) {
	var v struct {
		Installed    *bool  `json:"installed"`
		BackendState string `json:"BackendState"`
		Self         struct {
			HostName     string
			DNSName      string
			TailscaleIPs []string
		}
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return Status{}, errors.New("Tailscale-Status nicht lesbar")
	}
	if v.Installed != nil && !*v.Installed {
		return Status{}, nil
	}
	st := Status{
		Installed: true,
		LoggedIn:  v.BackendState == "Running",
		Name:      v.Self.HostName,
		DNSName:   strings.TrimSuffix(v.Self.DNSName, "."),
		State:     v.BackendState,
	}
	for _, s := range v.Self.TailscaleIPs {
		if ip := net.ParseIP(s); ip != nil && ip.To4() != nil {
			st.IP = ip.String()
			break
		}
	}
	return st, nil
}

// HostnameFrom leitet den Tailscale-Hostnamen aus dem Gerätenamen ab: Kleinschreibung, jedes Zeichen außerhalb
// von [a-z0-9-] wird einzeln zu "-", höchstens 63 Zeichen. Leer ergibt "pdh-terminal".
func HostnameFrom(deviceName string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(deviceName) {
		if b.Len() >= MaxHostLen {
			break
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return DefaultHost
	}
	return b.String()
}

// ValidHostname prüft einen Hostnamen: 1 bis 63 Zeichen aus [a-z0-9-].
func ValidHostname(h string) bool {
	if h == "" || len(h) > MaxHostLen {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}

// ValidateKey prüft einen Auth-Key auf das, was ihn als Dateiinhalt unschädlich macht: nicht leer, höchstens
// 512 Bytes, keine Steuerzeichen (auch kein Zeilenumbruch) und keine Leerzeichen. Der Key steht nie im Fehlertext.
func ValidateKey(key string) error {
	switch {
	case key == "":
		return errors.New("Bitte den Auth-Key eingeben.")
	case len(key) > MaxKeyLen:
		return fmt.Errorf("Der Auth-Key ist zu lang (höchstens %d Zeichen).", MaxKeyLen)
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c <= 0x20 || c == 0x7f {
			return errors.New("Der Auth-Key enthält ein ungültiges Zeichen (Leerzeichen, Zeilenumbruch oder Steuerzeichen).")
		}
	}
	return nil
}

// redact entfernt den Key aus einer Fehlermeldung (sicherheitshalber, falls ein Programm ihn zurückgibt).
func redact(err error, key string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if key != "" && strings.Contains(msg, key) {
		msg = strings.ReplaceAll(msg, key, "***")
	}
	return errors.New(msg)
}
