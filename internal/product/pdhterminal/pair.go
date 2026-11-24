package pdhterminal

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"hmikiosk/internal/localport"
	"hmikiosk/internal/release"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const (
	requestTimeout = 10 * time.Second
	maxResponse    = 64 << 10
)

// State ist der Zustand der Kopplung aus Sicht des PDH-Servers.
type State int

// Zustände der Kopplung.
const (
	StateOK State = iota
	StateRevoked
	StateUnreachable
	StateUntrusted
)

// ErrUntrusted: Das Zertifikat des PDH-Servers ist dem Gerät unbekannt.
var ErrUntrusted = errors.New("Zertifikat von PDH wird nicht vertraut")

var errUnreachable = errors.New("PDH nicht erreichbar")

// Client spricht die Terminal-API von PDH. HTTP nil: Standardclient (10 s, keine
// Weiterleitungen, Systemzertifikate).
type Client struct {
	HTTP *http.Client
	// Route wählt zwischen Haupt- und Ausweichadresse (Tailnet); nil: immer d.URL, wie bisher.
	Route *Route
}

// base ist die Adresse, an die Anfragen für d gehen: die aktive der Route, wenn d.URL deren Hauptadresse ist.
func (c Client) base(d Device) string {
	if c.Route != nil && sameBase(d.URL, c.Route.Primary()) {
		return c.Route.Base()
	}
	return strings.TrimRight(d.URL, "/")
}

// routeName ist der Weg für base: "tailnet", wenn base nicht die Hauptadresse der Route ist, sonst "lan".
func (c Client) routeName(base string) string {
	if c.Route != nil && !sameBase(base, c.Route.Primary()) {
		return "tailnet"
	}
	return "lan"
}

// fail meldet einen Transportfehler auf base an die Route; wahr, wenn auf die Ausweichadresse gewechselt wurde.
func (c Client) fail(base string, err error) bool {
	return c.Route != nil && c.Route.Fail(base, err)
}

// cancelBody gibt den Zeitgeber der Anfrage frei, sobald der Rumpf geschlossen wird.
type cancelBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}

// send führt die Anfrage gegen die aktive Adresse aus (Frist requestTimeout je Versuch). Bei einem Netzfehler, der
// auf die Ausweichadresse umschaltet, wird genau einmal gegen die neue Adresse wiederholt; build baut dafür die
// Anfrage neu (auch den Rumpf). Antworten von PDH (auch Fehlerstatus) und TLS-Fehler schalten nie um.
func (c Client) send(ctx context.Context, d Device, build func(base string) (*http.Request, error)) (*http.Response, error) {
	base := c.base(d)
	resp, err := c.attempt(ctx, base, build)
	if err != nil && c.fail(base, err) {
		base = c.base(d)
		resp, err = c.attempt(ctx, base, build)
	}
	return resp, err
}

func (c Client) attempt(ctx context.Context, base string, build func(base string) (*http.Request, error)) (*http.Response, error) {
	actx, cancel := context.WithTimeout(ctx, requestTimeout)
	req, err := build(base)
	if err != nil {
		cancel()
		return nil, err
	}
	resp, err := c.client().Do(req.WithContext(actx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelBody{resp.Body, cancel}
	return resp, nil
}

// Probe prüft die Hauptadresse base mit dem Gerätegeheimnis (für Route.Run): erreichbar bei 200 oder 401.
func (c Client) Probe(ctx context.Context, d Device, base string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/terminal/v1/device", nil)
	if err != nil {
		return errUnreachable
	}
	req.Header.Set("Authorization", "Bearer "+d.Secret)
	req.Header.Set("Accept", "application/json")
	setClientHeaders(req, "lan")
	resp, err := c.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	readBody(resp)
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		return nil
	}
	return errUnreachable
}

func (c Client) client() *http.Client {
	var hc http.Client
	if c.HTTP != nil {
		hc = *c.HTTP
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if hc.Timeout == 0 {
		hc.Timeout = requestTimeout
	}
	return &hc
}

// isTLSErr erkennt Zertifikats-/TLS-Fehler.
func isTLSErr(err error) bool {
	var (
		cv  *tls.CertificateVerificationError
		ua  x509.UnknownAuthorityError
		hn  x509.HostnameError
		inv x509.CertificateInvalidError
		sr  x509.SystemRootsError
		rh  tls.RecordHeaderError
		al  tls.AlertError
	)
	return errors.As(err, &cv) || errors.As(err, &ua) || errors.As(err, &hn) || errors.As(err, &inv) ||
		errors.As(err, &sr) || errors.As(err, &rh) || errors.As(err, &al)
}

// transportErr übersetzt Netzfehler in Text ohne Einzelheiten aus Go.
func transportErr(err error) (State, error) {
	if isTLSErr(err) {
		return StateUntrusted, ErrUntrusted
	}
	return StateUnreachable, errUnreachable
}

func readBody(r *http.Response) []byte {
	b, _ := io.ReadAll(io.LimitReader(r.Body, maxResponse))
	return b
}

// cleanText macht aus Text von PDH eine kurze, druckbare Zeile (höchstens 200 Zeichen).
func cleanText(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 200 {
		s = strings.TrimSpace(string(r[:200]))
	}
	return s
}

var deviceIDRe = regexp.MustCompile(`^[0-9a-fA-F-]{1,64}$`)

// pdhMessage liest den Fehlertext aus einer Fehlerantwort von PDH.
func pdhMessage(b []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	return cleanText(e.Error)
}

// Pair koppelt das Terminal mit dem Code aus PDH. Der Code wird nicht aufgehoben.
func (c Client) Pair(ctx context.Context, baseURL, code, name string) (Device, error) {
	d, _, err := c.PairMove(ctx, baseURL, code, name, nil)
	return d, err
}

// sameBase: wahr, wenn beide PDH-Adressen (ohne Schrägstrich am Ende) gleich sind.
func sameBase(a, b string) bool {
	return a != "" && strings.EqualFold(strings.TrimRight(a, "/"), strings.TrimRight(b, "/"))
}

// PairMove koppelt wie Pair. Ist prev die bisherige Kopplung dieses Terminals und gehört sie zur selben
// PDH-Adresse, geht sie als "previous" mit, damit PDH das Terminal aus dem alten Benutzer übernimmt.
// Bei einer anderen Adresse wird das alte Geheimnis nicht gesendet. moved ist wahr, wenn PDH
// "moved": true meldet. Ältere PDH ignorieren "previous" und senden kein "moved".
func (c Client) PairMove(ctx context.Context, baseURL, code, name string, prev *Device) (Device, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	base := strings.TrimRight(baseURL, "/")
	payload := map[string]any{"code": strings.TrimSpace(code), "name": name}
	if prev != nil && prev.DeviceID != "" && prev.Secret != "" && sameBase(prev.URL, base) {
		payload["previous"] = map[string]string{"device_id": prev.DeviceID, "secret": prev.Secret}
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/terminal/v1/pair", bytes.NewReader(body))
	if err != nil {
		return Device{}, false, errors.New("PDH-Adresse ungültig")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	setClientHeaders(req, "")
	resp, err := c.client().Do(req)
	if err != nil {
		_, e := transportErr(err)
		return Device{}, false, e
	}
	defer resp.Body.Close()
	data := readBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			DeviceID string `json:"device_id"`
			Secret   string `json:"secret"`
			User     string `json:"user"`
			Moved    bool   `json:"moved"`
		}
		if json.Unmarshal(data, &out) != nil || !deviceIDRe.MatchString(out.DeviceID) || out.Secret == "" || len(out.Secret) > 256 || strings.IndexFunc(out.Secret, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
			return Device{}, false, errors.New("Ungültige Antwort von PDH")
		}
		return Device{DeviceID: out.DeviceID, Secret: out.Secret, User: cleanText(out.User), URL: base, PairedAt: time.Now().UTC()}, out.Moved, nil
	case http.StatusUnprocessableEntity:
		if m := pdhMessage(data); m != "" {
			return Device{}, false, errors.New(m)
		}
		return Device{}, false, errors.New("PDH hat den Code abgelehnt")
	case http.StatusTooManyRequests:
		return Device{}, false, errors.New("Zu viele Fehlversuche, bitte später noch einmal versuchen")
	}
	return Device{}, false, fmt.Errorf("Unerwartete Antwort von PDH (Status %d)", resp.StatusCode)
}

// Check fragt PDH, ob die Kopplung noch gilt. Ein Fehler kommt nur mit
// StateUnreachable oder StateUntrusted; StateRevoked ist ein gewöhnliches Ergebnis.
func (c Client) Check(ctx context.Context, d Device) (State, error) {
	resp, err := c.send(ctx, d, func(base string) (*http.Request, error) {
		req, err := http.NewRequest(http.MethodGet, base+"/api/terminal/v1/device", nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+d.Secret)
		req.Header.Set("Accept", "application/json")
		setClientHeaders(req, c.routeName(base))
		return req, nil
	})
	if err != nil {
		return transportErr(err)
	}
	defer resp.Body.Close()
	readBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		return StateOK, nil
	case http.StatusUnauthorized:
		return StateRevoked, nil
	}
	return StateUnreachable, errUnreachable
}

// setClientHeaders trägt die Version des Terminals in jede Anfrage an PDH ein (freiwillige Kopfzeilen für PDH).
func setClientHeaders(req *http.Request, route string) {
	v := release.Version
	if v == "" {
		v = "dev"
	}
	req.Header.Set("X-Terminal-Version", v)
	req.Header.Set("User-Agent", "pdh-terminal/"+v)
	setAdminURLHeader(req)
	if route == "" {
		route = "lan"
	}
	req.Header.Set("X-Terminal-Route", route) // lan oder tailnet; ältere PDH ignorieren die Kopfzeile
	// Nur bei einem eingestellten Port: Die PDH-Seiten erreichen das Terminal (Kartenstrom, Tastatur) unter
	// http://127.0.0.1:<Port>. Beim Grundwert 8080 bleibt die Anfrage wie bisher.
	if p := localport.Get(); p != localport.Default {
		req.Header.Set("X-Terminal-Local-Port", strconv.Itoa(p))
	}
}
