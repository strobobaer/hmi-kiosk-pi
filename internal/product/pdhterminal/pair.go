package pdhterminal

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
type Client struct{ HTTP *http.Client }

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

// transportErr übersetzt Netzfehler in Text ohne Einzelheiten aus Go.
func transportErr(err error) (State, error) {
	var (
		cv  *tls.CertificateVerificationError
		ua  x509.UnknownAuthorityError
		hn  x509.HostnameError
		inv x509.CertificateInvalidError
	)
	if errors.As(err, &cv) || errors.As(err, &ua) || errors.As(err, &hn) || errors.As(err, &inv) {
		return StateUntrusted, ErrUntrusted
	}
	return StateUnreachable, errUnreachable
}

func readBody(r *http.Response) []byte {
	b, _ := io.ReadAll(io.LimitReader(r.Body, maxResponse))
	return b
}

// pdhMessage macht aus dem Fehlertext von PDH eine kurze, druckbare Zeile.
func pdhMessage(b []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(b, &e) != nil {
		return ""
	}
	s := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(e.Error))
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// Pair koppelt das Terminal mit dem Code aus PDH. Der Code wird nicht aufgehoben.
func (c Client) Pair(ctx context.Context, baseURL, code, name string) (Device, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	base := strings.TrimRight(baseURL, "/")
	body, _ := json.Marshal(map[string]string{"code": strings.TrimSpace(code), "name": name})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/terminal/v1/pair", bytes.NewReader(body))
	if err != nil {
		return Device{}, errors.New("PDH-Adresse ungültig")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		_, e := transportErr(err)
		return Device{}, e
	}
	defer resp.Body.Close()
	data := readBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var out struct {
			DeviceID string `json:"device_id"`
			Secret   string `json:"secret"`
			User     string `json:"user"`
		}
		if json.Unmarshal(data, &out) != nil || out.DeviceID == "" || out.Secret == "" {
			return Device{}, errors.New("Unerwartete Antwort von PDH")
		}
		return Device{DeviceID: out.DeviceID, Secret: out.Secret, User: out.User, URL: base, PairedAt: time.Now().UTC()}, nil
	case http.StatusUnprocessableEntity:
		if m := pdhMessage(data); m != "" {
			return Device{}, errors.New(m)
		}
		return Device{}, errors.New("PDH hat den Code abgelehnt")
	case http.StatusTooManyRequests:
		return Device{}, errors.New("Zu viele Fehlversuche, bitte später noch einmal versuchen")
	}
	return Device{}, fmt.Errorf("Unerwartete Antwort von PDH (Status %d)", resp.StatusCode)
}

// Check fragt PDH, ob die Kopplung noch gilt. Ein Fehler kommt nur mit
// StateUnreachable oder StateUntrusted; StateRevoked ist ein gewöhnliches Ergebnis.
func (c Client) Check(ctx context.Context, d Device) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.URL, "/")+"/api/terminal/v1/device", nil)
	if err != nil {
		return StateUnreachable, errUnreachable
	}
	req.Header.Set("Authorization", "Bearer "+d.Secret)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
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
