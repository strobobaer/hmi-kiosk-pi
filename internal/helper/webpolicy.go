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
	"slices"
	"strings"

	"hmikiosk/internal/config"
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

// fixedAllow bleibt immer erlaubt: Kachelseite und Leiste (8080), Admin-Seite am Terminal (8081).
var fixedAllow = []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://127.0.0.1:8081", "http://localhost:8081"}

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

// ChromiumPolicy ist der Inhalt der Richtlinie: alles gesperrt außer den festen
// Einträgen und den Ursprüngen der Web-Kacheln; kein Passwortmanager (die Anmeldung
// einer Web-Kachel bleibt als Cookie im Profil, Passwörter werden nie gespeichert).
func ChromiumPolicy(origins []string) []byte {
	allow := slices.Clone(fixedAllow)
	for _, o := range origins {
		allow = append(allow, allowFilter(o))
	}
	quoted := make([]string, len(allow))
	for i, a := range allow {
		b, _ := json.Marshal(a)
		quoted[i] = string(b)
	}
	return []byte("{\n" +
		"  \"URLBlocklist\": [\"*\"],\n" +
		"  \"URLAllowlist\": [" + strings.Join(quoted, ", ") + "],\n" +
		"  \"PasswordManagerEnabled\": false\n" +
		"}\n")
}

func (s *Server) policyPaths() (policy, state string) {
	policy, state = s.ChromiumPolicyPath, s.WebOriginsStatePath
	if policy == "" {
		policy = defaultPolicyPath
	}
	if state == "" {
		state = defaultWebOriginsSt
	}
	return policy, state
}

// setWebOrigins speichert die Liste zuerst dauerhaft (das Wurzeldateisystem des Abbilds
// ist ein tmpfs-Overlay) und schreibt dann die Richtlinie. Chromium liest sie im Lauf nach.
func (s *Server) setWebOrigins(_ context.Context, arg string) error {
	origins, err := parseWebOrigins([]byte(arg))
	if err != nil {
		return err
	}
	policy, state := s.policyPaths()
	b, _ := json.Marshal(origins)
	if err := writeStateFile(state, append(b, '\n')); err != nil {
		return fmt.Errorf("Web-Ursprünge nicht speicherbar: %v", err)
	}
	if err := writeAtomic(policy, ChromiumPolicy(origins), 0o644); err != nil {
		return fmt.Errorf("Chromium-Richtlinie nicht schreibbar: %v", err)
	}
	return nil
}

// ApplyWebPolicy schreibt beim Start die Richtlinie aus der gespeicherten Liste
// (hmi-kiosk-helper web-policy, vor dem Kiosk-Dienst). Ohne Liste bleibt die Richtlinie
// aus dem Paket. Eine ungültige Liste ergibt die Richtlinie nur mit den festen Einträgen.
func (s *Server) ApplyWebPolicy() error {
	return ApplyWebPolicy(s.policyPaths())
}

// ApplyWebPolicy ist der Startschritt mit festen Pfaden (ohne Server).
func ApplyWebPolicy(policy, state string) error {
	raw, err := readStateFile(state, maxWebOriginsState)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var origins []string
	if err == nil {
		origins, err = parseWebOrigins(raw)
	}
	if werr := writeAtomic(policy, ChromiumPolicy(origins), 0o644); werr != nil {
		return fmt.Errorf("Chromium-Richtlinie nicht schreibbar: %v", werr)
	}
	if err != nil {
		return fmt.Errorf("Gespeicherte Web-Ursprünge nicht lesbar, nur die festen Einträge gelten: %v", err)
	}
	return nil
}
