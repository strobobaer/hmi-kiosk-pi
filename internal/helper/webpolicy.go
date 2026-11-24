package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"hmikiosk/internal/config"
	"hmikiosk/internal/localport"
)

// CmdSetWebOrigins schreibt die Chromium-Richtlinie mit den Ursprüngen der Web-Kacheln.
// Wert: JSON-Liste von Ursprüngen in der Form von config.WebOrigin, auch leer ("[]").
const CmdSetWebOrigins = "set-web-origins"

const (
	// maxWebOrigins begrenzt die Liste wie config.Validate; die Anfrage ist ohnehin auf 4 KiB begrenzt.
	maxWebOrigins       = config.MaxWebOrigins
	maxWebOriginsState  = 4096
	defaultPolicyPath   = "/etc/chromium/policies/managed/hmi-kiosk.json"
	defaultWebOriginsSt = "/data/system/web-origins"
)

// CmdSetLocalPort schreibt den Port der lokalen Startseite (einstellbar im Admin-Bereich) in die
// Chromium-Richtlinie. Wert: die Dezimalzahl, 1024 bis 65534. Die lokale Admin-Seite liegt einen Port darüber.
const CmdSetLocalPort = "set-local-port"

const maxLocalPortState = 16

// LocalPortStatePathFor liegt neben der Liste der Web-Ursprünge (gleicher Ordner, Name local-port).
func LocalPortStatePathFor(webOriginsState string) string {
	return filepath.Join(filepath.Dir(webOriginsState), "local-port")
}

var portDigits = regexp.MustCompile(`^[0-9]{1,5}$`)

// parseLocalPort liest den Port streng: nur Ziffern, 1024 bis 65534. Der Helfer kennt den Port der
// Admin-Seite nicht; die Kollision prüft der Dienst beim Speichern (config.ValidateLocalPort).
func parseLocalPort(s string) (int, error) {
	if !portDigits.MatchString(s) {
		return 0, errors.New("Port der lokalen Startseite ungültig")
	}
	p, _ := strconv.Atoi(s)
	if p < localport.Min || p > localport.Max {
		return 0, fmt.Errorf("Port der lokalen Startseite muss zwischen %d und %d liegen", localport.Min, localport.Max)
	}
	return p, nil
}

// fixedAllow bleibt immer erlaubt: Kachelseite und Leiste (Port der Startseite, Grundwert 8080) und
// Admin-Seite am Terminal (der Port darüber, Grundwert 8081). Genau diese vier Einträge, kein Muster.
func fixedAllow(port int) []string {
	p, q := strconv.Itoa(port), strconv.Itoa(port+1)
	return []string{"http://127.0.0.1:" + p, "http://localhost:" + p, "http://127.0.0.1:" + q, "http://localhost:" + q}
}

// parseWebOrigins liest die Liste streng: genau eine JSON-Liste von Zeichenketten,
// jede ein gültiger Ursprung. Ergebnis sortiert und ohne Doppel.
func parseWebOrigins(raw []byte) ([]string, error) {
	if len(bytes.TrimSpace(raw)) > config.MaxWebOriginsJSON {
		return nil, errors.New("Liste der Web-Ursprünge zu lang")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var list []string
	if err := dec.Decode(&list); err != nil || list == nil {
		return nil, errors.New("Liste der Web-Ursprünge ungültig")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("Liste der Web-Ursprünge ungültig")
	}
	if len(list) > maxWebOrigins {
		return nil, fmt.Errorf("Höchstens %d Web-Server sind möglich", maxWebOrigins)
	}
	for _, o := range list {
		if !config.ValidOrigin(o) {
			return nil, fmt.Errorf("Web-Ursprung ungültig: %q", o)
		}
	}
	slices.Sort(list)
	return slices.Compact(list), nil
}

// allowFilter macht aus einem Ursprung einen URL-Filter der Richtlinie. Ein Rechnername
// bekommt den führenden Punkt: Ohne ihn gälte der Filter auch für alle Unternamen.
func allowFilter(origin string) string {
	scheme, rest, _ := strings.Cut(origin, "://")
	host := rest[:strings.LastIndexByte(rest, ':')]
	if strings.HasPrefix(host, "[") {
		return origin
	}
	if ip, err := netip.ParseAddr(host); err == nil && ip.Is4() {
		return origin
	}
	return scheme + "://." + rest
}

// localNetworkURLs liefert die Ursprünge als Muster der Inhaltseinstellungen: in der Schreibweise
// der Web-Kacheln (mit Port) und zusätzlich ohne Standardport, wie Chromium den Ursprung der Seite führt.
func localNetworkURLs(origins []string) []string {
	var out []string
	for _, o := range origins {
		out = append(out, o)
		if strings.HasPrefix(o, "http://") {
			out = append(out, strings.TrimSuffix(o, ":80"))
		} else {
			out = append(out, strings.TrimSuffix(o, ":443"))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// ChromiumPolicy ist der Inhalt der Richtlinie: alles gesperrt außer den festen
// Einträgen und den Ursprüngen der Web-Kacheln; kein Passwortmanager (die Anmeldung
// einer Web-Kachel bleibt als Cookie im Profil, Passwörter werden nie gespeichert) und keine
// Downloads (DownloadRestrictions 3: alle gesperrt), keine Anmeldung bei Chromium und keine
// Entwicklerwerkzeuge (auch nicht in einem Tab-Fenster, das eine Web-Kachel mit target=_blank öffnet).
//
// Mit localNetwork (nur PDH-Terminal) steht zusätzlich LocalNetworkAccessAllowedForUrls mit den
// Ursprüngen der Seiten in der Richtlinie: Die PDH-Seite liest den Kartenstrom per EventSource
// von http://127.0.0.1:<Port der Startseite>, und Chromium (Local Network Access, ab etwa 142) fragte sonst nach
// einer Berechtigung, die ein Kiosk mit gelöschtem Profil nie erteilen kann. Ohne Ursprünge
// oder ohne localNetwork fehlt der Eintrag, die Richtlinie bleibt wie im Paket.
func ChromiumPolicy(origins []string, localNetwork bool) []byte {
	return ChromiumPolicyFor(localport.Default, origins, localNetwork)
}

// ChromiumPolicyFor ist ChromiumPolicy für den Port der lokalen Startseite port (Grundwert 8080).
func ChromiumPolicyFor(port int, origins []string, localNetwork bool) []byte {
	allow := fixedAllow(port)
	for _, o := range origins {
		allow = append(allow, allowFilter(o))
	}
	quoted := make([]string, len(allow))
	for i, a := range allow {
		b, _ := json.Marshal(a)
		quoted[i] = string(b)
	}
	lna := ""
	if urls := localNetworkURLs(origins); localNetwork && len(urls) > 0 {
		q := make([]string, len(urls))
		for i, u := range urls {
			b, _ := json.Marshal(u)
			q[i] = string(b)
		}
		lna = "  \"LocalNetworkAccessAllowedForUrls\": [" + strings.Join(q, ", ") + "],\n"
	}
	return []byte("{\n" +
		"  \"URLBlocklist\": [\"*\"],\n" +
		"  \"URLAllowlist\": [" + strings.Join(quoted, ", ") + "],\n" +
		lna +
		"  \"PasswordManagerEnabled\": false,\n" +
		"  \"DownloadRestrictions\": 3,\n" +
		"  \"BrowserSignin\": 0,\n" +
		"  \"DeveloperToolsAvailability\": 2\n" +
		"}\n")
}

func (s *Server) policyPaths() (policy, state, portState string) {
	policy, state, portState = s.ChromiumPolicyPath, s.WebOriginsStatePath, s.LocalPortStatePath
	if policy == "" {
		policy = defaultPolicyPath
	}
	if state == "" {
		state = defaultWebOriginsSt
	}
	if portState == "" {
		portState = LocalPortStatePathFor(state)
	}
	return policy, state, portState
}

// savedLocalPort liest den gespeicherten Port. Fehlt die Datei, gilt der Grundwert ohne Fehler; eine
// unlesbare oder ungültige Datei ergibt den Grundwert und einen Fehler (nie einen anderen Port).
func savedLocalPort(portState string) (int, error) {
	raw, err := readStateFile(portState, maxLocalPortState)
	if errors.Is(err, os.ErrNotExist) {
		return localport.Default, nil
	}
	if err != nil {
		return localport.Default, err
	}
	p, perr := parseLocalPort(strings.TrimSuffix(string(raw), "\n"))
	if perr != nil {
		return localport.Default, perr
	}
	return p, nil
}

// setWebOrigins speichert die Liste zuerst dauerhaft (das Wurzeldateisystem des Abbilds
// ist ein tmpfs-Overlay) und schreibt dann die Richtlinie. Chromium liest sie im Lauf nach.
func (s *Server) setWebOrigins(_ context.Context, arg string) error {
	origins, err := parseWebOrigins([]byte(arg))
	if err != nil {
		return err
	}
	policy, state, portState := s.policyPaths()
	b, _ := json.Marshal(origins)
	if err := writeStateFile(state, append(b, '\n')); err != nil {
		return fmt.Errorf("Web-Ursprünge nicht speicherbar: %v", err)
	}
	// Der gespeicherte Port bleibt erhalten; ist die Datei unbrauchbar, gilt nur der Grundwert (nie ein offener Port).
	port, _ := savedLocalPort(portState)
	if err := writeAtomic(policy, ChromiumPolicyFor(port, origins, s.LocalNetworkAccess), 0o644); err != nil {
		return fmt.Errorf("Chromium-Richtlinie nicht schreibbar: %v", err)
	}
	return nil
}

// setLocalPort speichert den Port der lokalen Startseite dauerhaft und schreibt die Richtlinie mit den
// gespeicherten Web-Ursprüngen neu (fehlende oder unlesbare Liste: nur die festen Einträge).
func (s *Server) setLocalPort(_ context.Context, arg string) error {
	port, err := parseLocalPort(arg)
	if err != nil {
		return err
	}
	policy, state, portState := s.policyPaths()
	if err := writeStateFile(portState, []byte(strconv.Itoa(port)+"\n")); err != nil {
		return fmt.Errorf("Port der lokalen Startseite nicht speicherbar: %v", err)
	}
	var origins []string
	if raw, rerr := readStateFile(state, maxWebOriginsState); rerr == nil {
		if o, perr := parseWebOrigins(raw); perr == nil {
			origins = o
		}
	}
	if err := writeAtomic(policy, ChromiumPolicyFor(port, origins, s.LocalNetworkAccess), 0o644); err != nil {
		return fmt.Errorf("Chromium-Richtlinie nicht schreibbar: %v", err)
	}
	return nil
}

// ApplyWebPolicy schreibt beim Start die Richtlinie aus der gespeicherten Liste
// (hmi-kiosk-helper web-policy, vor dem Kiosk-Dienst). Ohne Liste bleibt die Richtlinie
// aus dem Paket. Eine ungültige Liste ergibt die Richtlinie nur mit den festen Einträgen.
func (s *Server) ApplyWebPolicy() error {
	policy, state, portState := s.policyPaths()
	return applyWebPolicy(policy, state, portState, s.LocalNetworkAccess)
}

// ApplyWebPolicy ist der Startschritt mit festen Pfaden (ohne Server). Der gespeicherte Port der
// Startseite liegt neben der Liste der Web-Ursprünge (LocalPortStatePathFor).
func ApplyWebPolicy(policy, state string, localNetwork bool) error {
	return applyWebPolicy(policy, state, LocalPortStatePathFor(state), localNetwork)
}

func applyWebPolicy(policy, state, portState string, localNetwork bool) error {
	raw, err := readStateFile(state, maxWebOriginsState)
	noOrigins := errors.Is(err, os.ErrNotExist)
	var origins []string
	if err == nil {
		origins, err = parseWebOrigins(raw)
	}
	port, perr := savedLocalPort(portState)
	_, statErr := os.Lstat(portState)
	noPort := perr == nil && errors.Is(statErr, os.ErrNotExist)
	if noOrigins && noPort {
		return nil // nichts gespeichert: Die Richtlinie aus dem Paket gilt
	}
	if noOrigins {
		err = nil
	}
	if werr := writeAtomic(policy, ChromiumPolicyFor(port, origins, localNetwork), 0o644); werr != nil {
		return fmt.Errorf("Chromium-Richtlinie nicht schreibbar: %v", werr)
	}
	if err != nil {
		return fmt.Errorf("Gespeicherte Web-Ursprünge nicht lesbar, nur die festen Einträge gelten: %v", err)
	}
	if perr != nil {
		return fmt.Errorf("Gespeicherter Port der Startseite nicht lesbar, es gilt der Grundwert %d: %v", localport.Default, perr)
	}
	return nil
}
