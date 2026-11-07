package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"unicode"
	"unicode/utf8"
)

var hostRe = regexp.MustCompile(`^[A-Za-z0-9._:\[\]][A-Za-z0-9._:\-\[\]]{0,252}$`)

// hmiIDRe begrenzt Kennungen, weil sie in Adressen der Admin-Seite stehen und per
// Import von außen kommen können. "new" ist dort ein eigener Pfad.
var hmiIDRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

const reservedHMIID = "new"

const maxNameLen = 120

func checkName(what, s string) error {
	if s == "" {
		return fmt.Errorf("%s darf nicht leer sein", what)
	}
	if utf8.RuneCountInString(s) > maxNameLen {
		return fmt.Errorf("%s ist länger als %d Zeichen", what, maxNameLen)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s enthält Steuerzeichen", what)
		}
	}
	return nil
}

func checkHost(what, s string) error {
	if !hostRe.MatchString(s) {
		return fmt.Errorf("%s ist ungültig: %q", what, s)
	}
	return nil
}

func validMode(m Mode) bool { return m == ModeTakeover || m == ModeLock }

// Validate checks a configuration for values that would be unsafe or
// unusable downstream (shell/ini injection, duplicate IDs, dangling refs).
func Validate(c Config) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("Unbekannte Schemaversion %d (erwartet %d)", c.SchemaVersion, SchemaVersion)
	}
	if err := checkName("Terminalname", c.Terminal.Name); err != nil {
		return err
	}
	if c.Terminal.Role != RolePrimary && c.Terminal.Role != RoleNormal {
		return fmt.Errorf("Terminalrolle ungültig: %q", c.Terminal.Role)
	}
	if !validMode(c.DefaultMode) {
		return fmt.Errorf("Standardmodus ungültig: %q", c.DefaultMode)
	}
	ids := map[string]bool{}
	for i, h := range c.HMIs {
		if h.ID == "" {
			return fmt.Errorf("HMI %d: Kennung fehlt", i+1)
		}
		if !hmiIDRe.MatchString(h.ID) || h.ID == reservedHMIID {
			return fmt.Errorf("HMI %d: Kennung ungültig: %q", i+1, h.ID)
		}
		if ids[h.ID] {
			return fmt.Errorf("HMI-Kennung doppelt: %q", h.ID)
		}
		ids[h.ID] = true
		if err := checkName(fmt.Sprintf("HMI %q: Name", h.ID), h.Name); err != nil {
			return err
		}
		if err := checkHost(fmt.Sprintf("HMI %q: Host", h.ID), h.Host); err != nil {
			return err
		}
		if h.Port < 1 || h.Port > 65535 {
			return fmt.Errorf("HMI %q: Port außerhalb 1-65535: %d", h.ID, h.Port)
		}
		if h.Mode != nil && !validMode(*h.Mode) {
			return fmt.Errorf("HMI %q: Modus ungültig: %q", h.ID, *h.Mode)
		}
		if h.Takeover != "" && h.Takeover != StyleConfirm && h.Takeover != StyleImmediate {
			return fmt.Errorf("HMI %q: Übernahmeart ungültig: %q", h.ID, h.Takeover)
		}
		if h.Icon != "" && !slices.Contains(HMIIcons, h.Icon) {
			return fmt.Errorf("HMI %q: unbekanntes Symbol %q", h.ID, h.Icon)
		}
		if h.Color != "" && !slices.Contains(HMIColors, h.Color) {
			return fmt.Errorf("HMI %q: unbekannte Farbe %q", h.ID, h.Color)
		}
	}
	peerNames := map[string]bool{}
	for i, p := range c.Peers {
		if err := checkName(fmt.Sprintf("Peer %d: Name", i+1), p.Name); err != nil {
			return err
		}
		if p.Name == c.Terminal.Name {
			return fmt.Errorf("Peer %q entspricht dem eigenen Terminalnamen", p.Name)
		}
		if err := checkHost(fmt.Sprintf("Peer %q: Adresse", p.Name), p.Address); err != nil {
			return err
		}
		if peerNames[p.Name] {
			return fmt.Errorf("Terminalname doppelt in der Liste: %q", p.Name)
		}
		peerNames[p.Name] = true
	}
	if c.Kiosk.PermanentHMI != "" && !ids[c.Kiosk.PermanentHMI] {
		return fmt.Errorf("Dauer-HMI unbekannt: %q", c.Kiosk.PermanentHMI)
	}
	if c.Kiosk.HideBar && c.Kiosk.PermanentHMI == "" {
		return errors.New(ErrHideBarWithoutPermanent)
	}
	if c.Kiosk.InactivityMinutes < 0 {
		return fmt.Errorf("Rückkehr bei Inaktivität darf nicht negativ sein: %d", c.Kiosk.InactivityMinutes)
	}
	switch c.Kiosk.InputMode {
	case "", InputTouch, InputMouse:
	default:
		return fmt.Errorf("Unbekannte Bedienart: %q (erlaubt: Touch oder Maus)", c.Kiosk.InputMode)
	}
	return validateClientAndDisplay(c)
}

// ErrHideBarWithoutPermanent ist auch die Meldung des Kiosk-Formulars.
const ErrHideBarWithoutPermanent = "Die Leiste lässt sich nur bei einer Daueranzeige ausblenden. Bitte zuerst ein Dauer-HMI wählen."

func validateClientAndDisplay(c Config) error {
	if c.Client.Profile != "remmina" && c.Client.Profile != "tigervnc" {
		return fmt.Errorf("Unbekanntes Client-Profil: %q", c.Client.Profile)
	}
	if !slices.Contains([]int{0, 1, 2, 9}, c.Client.Quality) {
		return fmt.Errorf("Unbekannte Qualitätsstufe: %d", c.Client.Quality)
	}
	if !slices.Contains([]int{8, 16, 32}, c.Client.ColorDepth) {
		return fmt.Errorf("Unbekannte Farbtiefe: %d", c.Client.ColorDepth)
	}
	w, h := c.Display.OverrideW, c.Display.OverrideH
	if (w != 0 || h != 0) && (w <= 0 || h <= 0) {
		return fmt.Errorf("Auflösung unvollständig: %d×%d (beide Werte leer oder beide größer 0)", w, h)
	}
	return nil
}
