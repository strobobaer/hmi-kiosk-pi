package config

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"unicode"
	"unicode/utf8"

	"hmikiosk/internal/localport"
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
// Sie ist ValidateCore + ValidateHMI + ValidatePDH + ValidateCard + ValidateStatusDisplay; ein Produkt ruft nur seine Teile.
func Validate(c Config) error {
	if err := ValidateCore(c); err != nil {
		return err
	}
	if err := ValidateHMI(c); err != nil {
		return err
	}
	if err := ValidatePDH(c.PDH); err != nil {
		return err
	}
	if err := ValidateCard(c.Card); err != nil {
		return err
	}
	return ValidateStatusDisplay(c.StatusDisplay)
}

// ValidateCore prüft, was beide Produkte teilen: Schema, Terminal, Netzwerk, Anzeige, Update.
func ValidateCore(c Config) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf("Unbekannte Schemaversion %d (erwartet %d)", c.SchemaVersion, SchemaVersion)
	}
	if err := checkName("Terminalname", c.Terminal.Name); err != nil {
		return err
	}
	if c.Terminal.Role != RolePrimary && c.Terminal.Role != RoleNormal {
		return fmt.Errorf("Terminalrolle ungültig: %q", c.Terminal.Role)
	}
	if !ValidUpdateRepo(c.Update.Repo) {
		return fmt.Errorf("Update-Quelle ungültig: %q (erwartet Besitzer/Repository)", c.Update.Repo)
	}
	if err := ValidateEthernet(c.Network.Ethernet); err != nil {
		return fmt.Errorf("Ethernet: %w", err)
	}
	if err := ValidateLocalPort(c.Kiosk.LocalPort, DefaultAdminPort); err != nil {
		return err
	}
	return validateDisplay(c)
}

// DefaultAdminPort ist der Port der Admin-Seite über HTTPS (Vorgabe -admin :8443).
const DefaultAdminPort = 8443

// ValidateLocalPort prüft den Port der lokalen Startseite (0 = Grundwert 8080): 1024 bis 65534, und
// weder er noch der Port darüber (lokale Admin-Seite) darf der Port der Admin-Seite über HTTPS sein.
func ValidateLocalPort(port, adminPort int) error {
	if port == 0 {
		port = localport.Default
	} else if port < localport.Min || port > localport.Max {
		return fmt.Errorf("Port der lokalen Startseite muss zwischen %d und %d liegen: %d", localport.Min, localport.Max, port)
	}
	if adminPort == port || adminPort == port+1 {
		return fmt.Errorf("Port der lokalen Startseite: %d und %d (lokale Admin-Seite) dürfen nicht der Port der Admin-Seite (%d) sein", port, port+1, adminPort)
	}
	return nil
}

// ValidateHMI prüft die Teile des HMI-Kiosks: Modus, Kacheln, Peers, Web-Ursprünge, Kiosk, Client, Fernsteuerung.
func ValidateHMI(c Config) error {
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
		switch h.Kind {
		case "", KindVNC:
			if err := checkHost(fmt.Sprintf("HMI %q: Host", h.ID), h.Host); err != nil {
				return err
			}
			if h.Port < 1 || h.Port > 65535 {
				return fmt.Errorf("HMI %q: Port außerhalb 1-65535: %d", h.ID, h.Port)
			}
		case KindWeb:
			// Host, Port und Passwort gelten nur für VNC; die Adresse trägt alles.
			if err := checkWebURL(h.ID, h.URL); err != nil {
				return err
			}
		default:
			return fmt.Errorf("HMI %q: unbekannte Art %q (erlaubt: vnc, web)", h.ID, h.Kind)
		}
		if h.Mode != nil && !validMode(*h.Mode) {
			return fmt.Errorf("HMI %q: Modus ungültig: %q", h.ID, *h.Mode)
		}
		if h.Takeover != "" && h.Takeover != StyleConfirm && h.Takeover != StyleImmediate {
			return fmt.Errorf("HMI %q: Übernahmeart ungültig: %q", h.ID, h.Takeover)
		}
		if h.Reach != "" && !slices.Contains([]string{ReachPing, ReachPort, ReachOff}, h.Reach) {
			return fmt.Errorf("HMI %q: Erreichbarkeitsprüfung ungültig: %q (erlaubt: ping, port, off)", h.ID, h.Reach)
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
	if err := checkWebOrigins(c); err != nil {
		return err
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
	case "", InputTouch, InputMouse, InputAuto:
	default:
		return fmt.Errorf("Unbekannte Bedienart: %q (erlaubt: Touch, Maus oder Automatisch)", c.Kiosk.InputMode)
	}
	if err := checkRemoteKeyHash(c.Admin.RemoteKeyHash); err != nil {
		return err
	}
	return validateClient(c)
}

// ErrHideBarWithoutPermanent ist auch die Meldung des Kiosk-Formulars.
const ErrHideBarWithoutPermanent = "Die Leiste lässt sich nur bei einer Daueranzeige ausblenden. Bitte zuerst ein Dauer-HMI wählen."

func validateClient(c Config) error {
	if c.Client.Profile != "remmina" && c.Client.Profile != "tigervnc" {
		return fmt.Errorf("Unbekanntes Client-Profil: %q", c.Client.Profile)
	}
	if !slices.Contains([]int{0, 1, 2, 9}, c.Client.Quality) {
		return fmt.Errorf("Unbekannte Qualitätsstufe: %d", c.Client.Quality)
	}
	if !slices.Contains([]int{8, 16, 32}, c.Client.ColorDepth) {
		return fmt.Errorf("Unbekannte Farbtiefe: %d", c.Client.ColorDepth)
	}
	return nil
}

func validateDisplay(c Config) error {
	w, h := c.Display.OverrideW, c.Display.OverrideH
	if (w != 0 || h != 0) && (w <= 0 || h <= 0) {
		return fmt.Errorf("Auflösung unvollständig: %d×%d (beide Werte leer oder beide größer 0)", w, h)
	}
	return nil
}

// CheckName und CheckHost sind die Regeln für Namen und Hosts der Konfiguration, etwa für
// die Eingaben der Fernbefehle von PDH.
func CheckName(what, s string) error { return checkName(what, s) }

func CheckHost(what, s string) error { return checkHost(what, s) }

var remoteKeyHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func checkRemoteKeyHash(h string) error {
	if h != "" && !remoteKeyHashRe.MatchString(h) {
		return errors.New("Schlüssel der Fernsteuerung (PDH) ungültig gespeichert")
	}
	return nil
}
