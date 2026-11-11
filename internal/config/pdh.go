package config

import (
	"fmt"
	"strings"
)

// PDHCfg ist die Anbindung an den PDH-Server. Sie gilt nur für das PDH-Terminal; beim
// HMI-Kiosk bleibt der Abschnitt leer und fehlt in der Datei (omitzero).
type PDHCfg struct {
	// URL ist der Ursprung des PDH-Servers (Schema, Rechner, optional Port; kein Pfad).
	URL string `json:"url,omitempty"`
	// AllowHTTP erlaubt http:// statt https:// (nur für Tests im lokalen Netz).
	AllowHTTP bool `json:"allowHttp,omitempty"`
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
	if len(p.ExtraOrigins) > MaxWebOrigins-1 {
		return fmt.Errorf("Zu viele zusätzliche Adressen (höchstens %d)", MaxWebOrigins-1)
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

// PDHOrigins liefert den Ursprung des PDH-Servers (zuerst) und die Zusatzursprünge ohne Doppel.
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
	for _, o := range p.ExtraOrigins {
		add(o)
	}
	return out
}
