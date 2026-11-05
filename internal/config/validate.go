package config

import (
	"fmt"
	"regexp"
	"unicode"
	"unicode/utf8"
)

var hostRe = regexp.MustCompile(`^[A-Za-z0-9._:\-\[\]]{1,253}$`)

const maxNameLen = 120

func checkName(what, s string) error {
	if s == "" {
		return fmt.Errorf("%s darf nicht leer sein", what)
	}
	if utf8.RuneCountInString(s) > maxNameLen {
		return fmt.Errorf("%s ist laenger als %d Zeichen", what, maxNameLen)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s enthaelt Steuerzeichen", what)
		}
	}
	return nil
}

func checkHost(what, s string) error {
	if !hostRe.MatchString(s) {
		return fmt.Errorf("%s ist ungueltig: %q", what, s)
	}
	return nil
}

func validMode(m Mode) bool { return m == ModeTakeover || m == ModeLock }

// Validate checks a configuration for values that would be unsafe or
// unusable downstream (shell/ini injection, duplicate IDs, dangling refs).
func Validate(c Config) error {
	if err := checkName("Terminalname", c.Terminal.Name); err != nil {
		return err
	}
	if c.Terminal.Role != RolePrimary && c.Terminal.Role != RoleNormal {
		return fmt.Errorf("Terminalrolle ungueltig: %q", c.Terminal.Role)
	}
	if !validMode(c.DefaultMode) {
		return fmt.Errorf("Standardmodus ungueltig: %q", c.DefaultMode)
	}
	ids := map[string]bool{}
	for i, h := range c.HMIs {
		if h.ID == "" {
			return fmt.Errorf("HMI %d: Kennung fehlt", i+1)
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
			return fmt.Errorf("HMI %q: Port ausserhalb 1-65535: %d", h.ID, h.Port)
		}
		if h.Mode != nil && !validMode(*h.Mode) {
			return fmt.Errorf("HMI %q: Modus ungueltig: %q", h.ID, *h.Mode)
		}
		if h.Takeover != "" && h.Takeover != StyleConfirm && h.Takeover != StyleImmediate {
			return fmt.Errorf("HMI %q: Uebernahmeart ungueltig: %q", h.ID, h.Takeover)
		}
	}
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
	}
	if c.Kiosk.PermanentHMI != "" && !ids[c.Kiosk.PermanentHMI] {
		return fmt.Errorf("Dauer-HMI unbekannt: %q", c.Kiosk.PermanentHMI)
	}
	return nil
}
