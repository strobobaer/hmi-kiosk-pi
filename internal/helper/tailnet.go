package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"hmikiosk/internal/helperapi"
)

// Die Befehle der Tailnet-Steuerung. tailnet-up nimmt JSON {"key","host"}: Der Auth-Key geht nur über eine
// 0600-Datei im root-eigenen Ordner des Helfers an tailscale (--auth-key=file:), nie als Argument, und die
// Datei wird auf jedem Weg gelöscht. Der Key steht in keinem Fehlertext und in keinem Protokoll.
const (
	CmdTailnetStatus = helperapi.CmdTailnetStatus
	CmdTailnetUp     = helperapi.CmdTailnetUp
	CmdTailnetDown   = helperapi.CmdTailnetDown
)

// defaultTailnetKeyPath liegt im Laufzeitordner des Helfers (systemd RuntimeDirectory, root, 0750): Der Dienst
// darf dort nichts anlegen oder tauschen. /run/hmi-kiosk gehört dem Dienstbenutzer und taugt dafür nicht.
const defaultTailnetKeyPath = "/run/hmi-kiosk-helper/tailnet-key"

const (
	maxTailnetKey  = 512
	tailnetBinary  = "tailscale"
	maxTailnetArgs = 2048
)

var tailnetHostRe = regexp.MustCompile(`^[a-z0-9-]{1,63}$`)

// parseTailnetUp liest den Wert von tailnet-up streng: genau die Felder key und host, nichts dahinter.
// Fehlertexte nennen den Inhalt nie.
func parseTailnetUp(arg string) (key, host string, err error) {
	bad := errors.New("Ungültige Anmeldedaten")
	if len(arg) > maxTailnetArgs {
		return "", "", bad
	}
	var v struct {
		Key  *string `json:"key"`
		Host *string `json:"host"`
	}
	dec := json.NewDecoder(strings.NewReader(arg))
	dec.DisallowUnknownFields()
	if dec.Decode(&v) != nil {
		return "", "", bad
	}
	if _, terr := dec.Token(); terr != io.EOF {
		return "", "", bad
	}
	if v.Key == nil || v.Host == nil {
		return "", "", bad
	}
	key, host = *v.Key, *v.Host
	if key == "" {
		return "", "", errors.New("Der Auth-Key fehlt")
	}
	if len(key) > maxTailnetKey {
		return "", "", errors.New("Der Auth-Key ist zu lang")
	}
	for i := 0; i < len(key); i++ {
		if c := key[i]; c <= 0x20 || c == 0x7f {
			return "", "", errors.New("Der Auth-Key enthält ein ungültiges Zeichen")
		}
	}
	if !tailnetHostRe.MatchString(host) {
		return "", "", errors.New("Der Hostname ist ungültig")
	}
	return key, host, nil
}

// tailnetUp meldet das Gerät mit dem Auth-Key an.
func (s *Server) tailnetUp(ctx context.Context, arg string) error {
	key, host, err := parseTailnetUp(arg)
	if err != nil {
		return err
	}
	path := s.TailnetKeyPath
	if path == "" {
		path = defaultTailnetKeyPath
	}
	err = withTailnetKeyFile(path, key, func() error {
		return s.run(ctx, tailnetBinary, "up", "--auth-key=file:"+path, "--hostname="+host)
	})
	if err != nil {
		return errors.New(strings.ReplaceAll(err.Error(), key, "***"))
	}
	return nil
}

// tailnetDown meldet das Gerät ab.
func (s *Server) tailnetDown(ctx context.Context, _ string) error {
	return s.run(ctx, tailnetBinary, "logout")
}

// tailnetStatus liefert den Stand aus "tailscale status --json", gekürzt auf das, was die Admin-Seite braucht
// (die Liste der Gegenstellen würde schnell die Antwortgröße sprengen). Fehlt das Programm: {"installed":false}.
func (s *Server) tailnetStatus(ctx context.Context, _ string) (any, error) {
	if s.ExecOut == nil {
		return nil, errors.New("Tailscale nicht abfragbar")
	}
	out, err := s.ExecOut(ctx, tailnetBinary, "status", "--json")
	if err != nil {
		var ni notInstalledError
		if errors.As(err, &ni) {
			return json.RawMessage(`{"installed":false}`), nil
		}
		return nil, errors.New("tailscale fehlgeschlagen: " + err.Error())
	}
	var in struct {
		BackendState string
		Self         struct {
			HostName     string
			DNSName      string
			TailscaleIPs []string
		}
	}
	if err := json.NewDecoder(bytes.NewReader([]byte(out))).Decode(&in); err != nil {
		return nil, errors.New("Tailscale-Status nicht lesbar")
	}
	b, _ := json.Marshal(in)
	return json.RawMessage(b), nil
}
