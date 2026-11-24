package config

import (
	"fmt"
	"net"
	"strings"
)

// PDHCfg ist die Anbindung an den PDH-Server. Sie gilt nur für das PDH-Terminal; beim
// HMI-Kiosk bleibt der Abschnitt leer und fehlt in der Datei (omitzero).
type PDHCfg struct {
	// URL ist der Ursprung des PDH-Servers (Schema, Rechner, optional Port; kein Pfad).
	URL string `json:"url,omitempty"`
	// AllowHTTP erlaubt http:// statt https:// (nur für Tests im lokalen Netz).
	AllowHTTP bool `json:"allowHttp,omitempty"`
	// FallbackURL ist die Ausweichadresse des PDH-Servers (z. B. im Tailnet), wenn die
	// Hauptadresse nicht erreichbar ist: nur https://Rechner[:Port], kein Pfad.
	FallbackURL string `json:"fallbackUrl,omitempty"`
	// DeviceName ist der Name, unter dem sich das Terminal beim PDH-Server meldet.
	DeviceName string `json:"deviceName,omitempty"`
	// ExtraOrigins sind weitere Ursprünge, die die Webansicht laden darf (z. B. ein CDN).
	ExtraOrigins []string `json:"extraOrigins,omitempty"`
}

// ValidatePDH prüft den PDH-Abschnitt; ein leerer Abschnitt ist gültig.
func ValidatePDH(p PDHCfg) error {
	if p.URL != "" {
		if !strings.HasPrefix(p.URL, "https://") && !strings.HasPrefix(p.URL, "http://") {
			return fmt.Errorf("PDH-Adresse muss mit https:// beginnen")
		}
		if strings.HasPrefix(p.URL, "http://") && !p.AllowHTTP {
			return fmt.Errorf("PDH-Adresse: http:// nur mit „unverschlüsselt erlauben“")
		}
		u, err := ParseWebURL(p.URL)
		if err != nil {
			return fmt.Errorf("PDH-Adresse: %v", err)
		}
		canon := u.Scheme + "://" + strings.ToLower(u.Hostname())
		if strings.Contains(u.Hostname(), ":") {
			canon = u.Scheme + "://[" + strings.ToLower(u.Hostname()) + "]"
		}
		if u.Port() != "" {
			canon += ":" + u.Port()
		}
		if p.URL != canon {
			return fmt.Errorf("PDH-Adresse: nur Adresse ohne Pfad (z. B. https://pdh.firma.de)")
		}
	}
	maxExtra := MaxWebOrigins - 1
	if p.FallbackURL != "" {
		if err := validateFallback(p); err != nil {
			return err
		}
		maxExtra-- // Die Ausweichadresse zählt gegen die Ursprungsgrenze.
	}
	if len(p.ExtraOrigins) > maxExtra {
		return fmt.Errorf("Zu viele zusätzliche Adressen (höchstens %d)", maxExtra)
	}
	for _, o := range p.ExtraOrigins {
		if !ValidOrigin(o) {
			return fmt.Errorf("Zusätzliche Adresse ungültig: %q (erwartet z. B. https://cdn.firma.de:443)", o)
		}
	}
	if p.DeviceName != "" {
		if err := CheckName("Gerätename", p.DeviceName); err != nil {
			return err
		}
	}
	return nil
}

// validateFallback prüft die Ausweichadresse: nur https, nur Ursprung, anders als die Hauptadresse.
func validateFallback(p PDHCfg) error {
	f := p.FallbackURL
	if !strings.HasPrefix(f, "https://") {
		return fmt.Errorf("Ausweichadresse ungültig: muss mit https:// beginnen")
	}
	u, err := ParseWebURL(f)
	if err != nil {
		return fmt.Errorf("Ausweichadresse ungültig: %v", err)
	}
	canon := u.Scheme + "://" + strings.ToLower(u.Hostname())
	if strings.Contains(u.Hostname(), ":") {
		canon = u.Scheme + "://[" + strings.ToLower(u.Hostname()) + "]"
	}
	if u.Port() != "" {
		canon += ":" + u.Port()
	}
	if f != canon {
		return fmt.Errorf("Ausweichadresse ungültig: nur Adresse ohne Pfad (z. B. https://pdh.tailnet.ts.net)")
	}
	if net.ParseIP(u.Hostname()) != nil {
		return fmt.Errorf("Ausweichadresse ungültig: IP-Adressen gehen nicht – bitte den ts.net-Namen eintragen (Zertifikat)")
	}
	if p.URL != "" {
		fo, _ := WebOrigin(f)
		if mo, err := WebOrigin(p.URL); err == nil && fo == mo {
			return fmt.Errorf("Ausweichadresse ungültig: muss sich von der PDH-Adresse unterscheiden")
		}
	}
	return nil
}

// PDHOrigins liefert den Ursprung des PDH-Servers (zuerst), die Ausweichadresse und die Zusatzursprünge ohne Doppel.
func PDHOrigins(p PDHCfg) []string {
	out := []string{}
	add := func(o string) {
		for _, x := range out {
			if x == o {
				return
			}
		}
		out = append(out, o)
	}
	if p.URL != "" {
		if o, err := WebOrigin(p.URL); err == nil {
			add(o)
		}
	}
	if p.FallbackURL != "" {
		if o, err := WebOrigin(p.FallbackURL); err == nil {
			add(o)
		}
	}
	for _, o := range p.ExtraOrigins {
		add(o)
	}
	return out
}
