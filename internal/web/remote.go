package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/session"
)

// Fernbefehle von PDH (Version 1) auf dem HTTPS-Listener, siehe
// docs/superpowers/specs/2026-11-09-pdh-integration.md. Anmeldung nur per Bearer-Schlüssel;
// Cookies zählen nicht, deshalb braucht es auch keinen CSRF-Schutz.

const (
	remoteKeyPrefix   = "hmik_"
	remoteBodyLimit   = 4 << 10
	remotePasswordMax = 64
	// remoteLockMax begrenzt die Merkliste der Quelladressen mit Fehlversuchen.
	remoteLockMax = 1024
)

// RemoteSession ist der Teil der Sitzungsverwaltung, den die Fernbefehle brauchen.
type RemoteSession interface {
	Snapshot() session.Snapshot
	OpenRemote(ctx context.Context, r session.RemoteRequest) (session.RemoteResult, error)
	CloseRemote() bool
}

// RemoteDeps sind die Abhängigkeiten der Fernbefehle. NotReady nennt einen Grund, warum
// gerade nichts geöffnet werden darf, der nicht in der Sitzungsverwaltung liegt (Sicherung).
type RemoteDeps struct {
	Config   func() config.Config
	Session  RemoteSession
	Version  string
	NotReady func(ctx context.Context) string
	Clock    coord.Clock
}

// NewRemoteKey erzeugt einen Schlüssel für die Fernbefehle und seinen gespeicherten Hash.
func NewRemoteKey() (key, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	key = remoteKeyPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return key, RemoteKeyHash(key), nil
}

// RemoteKeyHash ist SHA-256 des Schlüssels, hex klein (so steht er in der Konfiguration).
func RemoteKeyHash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

type remoteAPI struct {
	d    RemoteDeps
	lock addrLockout
}

// NewRemoteAPI liefert den Handler für /api/remote/; er gehört nur auf den HTTPS-Listener.
func NewRemoteAPI(d RemoteDeps) http.Handler {
	if d.Clock == nil {
		d.Clock = coord.SystemClock{}
	}
	return &remoteAPI{d: d, lock: addrLockout{m: map[string]*addrLock{}}}
}

func (a *remoteAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	if !a.authorized(w, r) {
		return
	}
	var method string
	var handle func(http.ResponseWriter, *http.Request)
	switch r.URL.Path {
	case "/api/remote/v1/status":
		method, handle = http.MethodGet, a.handleStatus
	case "/api/remote/v1/open":
		method, handle = http.MethodPost, a.handleOpen
	case "/api/remote/v1/close":
		method, handle = http.MethodPost, a.handleClose
	default:
		writeJSONError(w, http.StatusNotFound, "Unbekannter Befehl.")
		return
	}
	if r.Method != method {
		h.Set("Allow", method)
		writeJSONError(w, http.StatusMethodNotAllowed, "Methode nicht erlaubt.")
		return
	}
	handle(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = http.StatusInternalServerError, []byte(`{"error":"Antwort nicht erzeugbar"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{msg})
}

func sourceAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// bearer liest den Schlüssel aus „Authorization: Bearer …“ (Schema ohne Rücksicht auf Groß-/Kleinschreibung).
func bearer(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}

// keyMatches vergleicht die Hashes in konstanter Zeit.
func keyMatches(token, wantHex string) bool {
	want, err := hex.DecodeString(wantHex)
	if err != nil || len(want) != sha256.Size {
		return false
	}
	got := sha256.Sum256([]byte(token))
	return subtle.ConstantTimeCompare(got[:], want) == 1
}

// authorized prüft den Schlüssel. Fehlversuche sperren wie die Admin-Anmeldung (5 Versuche,
// 5 Minuten), aber je Quelladresse: Ein falsch eingerichtetes PDH sperrt weder die
// Admin-Anmeldung noch andere Absender.
func (a *remoteAPI) authorized(w http.ResponseWriter, r *http.Request) bool {
	addr, now := sourceAddr(r), a.d.Clock.Now()
	if left := a.lock.remaining(addr, now); left > 0 {
		writeJSONError(w, http.StatusTooManyRequests, lockedMessage(left))
		return false
	}
	want := a.d.Config().Admin.RemoteKeyHash
	token, ok := bearer(r)
	if ok && want != "" && keyMatches(token, want) {
		a.lock.reset(addr)
		return true
	}
	a.lock.fail(addr, now)
	if left := a.lock.remaining(addr, now); left > 0 {
		log.Printf("Fernsteuerung (PDH): Anmeldung von %s gesperrt nach %d Fehlversuchen", addr, maxFailures)
		writeJSONError(w, http.StatusTooManyRequests, lockedMessage(left))
		return false
	}
	msg := "Schlüssel fehlt oder ist falsch."
	if want == "" {
		msg = "Die Fernsteuerung ist auf diesem Terminal ausgeschaltet."
	}
	writeJSONError(w, http.StatusUnauthorized, msg)
	return false
}

type remoteStatus struct {
	Version  string `json:"version"`
	Terminal string `json:"terminal"`
	Busy     bool   `json:"busy"`
	HMI      string `json:"hmi,omitempty"`
	Since    string `json:"since,omitempty"`
	Source   string `json:"source,omitempty"`
}

func formatSince(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

func (a *remoteAPI) handleStatus(w http.ResponseWriter, _ *http.Request) {
	snap := a.d.Session.Snapshot()
	st := remoteStatus{Version: a.d.Version, Terminal: a.d.Config().Terminal.Name, Busy: snap.State != session.StateTiles}
	if st.Busy {
		st.HMI, st.Source, st.Since = snap.HMIName, "local", formatSince(snap.Since)
		if snap.Remote {
			st.HMI, st.Source = snap.RemoteLabel, "pdh"
		}
	}
	writeJSON(w, http.StatusOK, st)
}

type remoteOpenBody struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Password string `json:"password"`
	Mode     string `json:"mode"`
	Label    string `json:"label"`
	User     string `json:"user"`
}

func decodeOpen(w http.ResponseWriter, r *http.Request) (remoteOpenBody, error) {
	var in remoteOpenBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, remoteBodyLimit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		if isTooLarge(err) {
			return in, fmt.Errorf("Anfrage größer als %d Byte.", remoteBodyLimit)
		}
		return in, fmt.Errorf("Anfrage ist kein gültiger Befehl: %v", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return in, errors.New("Anfrage enthält mehr als einen Befehl.")
	}
	return in, nil
}

func (in remoteOpenBody) validate() (session.RemoteRequest, error) {
	req := session.RemoteRequest{Host: in.Host, Port: in.Port, Password: in.Password, Label: in.Label, User: in.User}
	if err := config.CheckHost("Host", in.Host); err != nil {
		return req, err
	}
	if in.Port < 1 || in.Port > 65535 {
		return req, fmt.Errorf("Port außerhalb 1-65535: %d", in.Port)
	}
	if len(in.Password) > remotePasswordMax {
		return req, fmt.Errorf("Passwort länger als %d Byte", remotePasswordMax)
	}
	switch in.Mode {
	case "view":
		req.ViewOnly = true
	case "control":
	default:
		return req, fmt.Errorf("Modus ungültig: %q (erlaubt: view, control)", in.Mode)
	}
	if err := config.CheckName("Label", in.Label); err != nil {
		return req, err
	}
	if err := config.CheckName("Benutzer", in.User); err != nil {
		return req, err
	}
	return req, nil
}

func modeText(viewOnly bool) string {
	if viewOnly {
		return "nur ansehen"
	}
	return "bedienen"
}

// handleOpen: Das Passwort steht nie im Protokoll; Label und Benutzer sind frei von Steuerzeichen.
func (a *remoteAPI) handleOpen(w http.ResponseWriter, r *http.Request) {
	addr := sourceAddr(r)
	in, err := decodeOpen(w, r)
	var req session.RemoteRequest
	if err == nil {
		req, err = in.validate()
	}
	if err != nil {
		log.Printf("Fernsteuerung (PDH): ungültiger Befehl „öffnen“ von %s: %v", addr, err)
		writeJSONError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	what := fmt.Sprintf("„%s“ öffnen für %s, %s, von %s", req.Label, req.User, modeText(req.ViewOnly), addr)
	if a.d.NotReady != nil {
		if msg := a.d.NotReady(r.Context()); msg != "" {
			log.Printf("Fernsteuerung (PDH): %s – abgelehnt: %s", what, msg)
			writeJSONError(w, http.StatusServiceUnavailable, msg)
			return
		}
	}
	res, err := a.d.Session.OpenRemote(r.Context(), req)
	switch {
	case err != nil:
		log.Printf("Fernsteuerung (PDH): %s – fehlgeschlagen: %v", what, err)
		writeJSONError(w, http.StatusServiceUnavailable, "Das Terminal ist nicht bereit.")
	case res.Outcome == session.RemoteOpened:
		log.Printf("Fernsteuerung (PDH): %s – geöffnet", what)
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
		}{"opened"})
	case res.Outcome == session.RemoteBusy:
		log.Printf("Fernsteuerung (PDH): %s – belegt durch „%s“", what, res.HMI)
		writeJSON(w, http.StatusConflict, struct {
			Status string `json:"status"`
			HMI    string `json:"hmi"`
			Since  string `json:"since,omitempty"`
		}{"busy", res.HMI, formatSince(res.Since)})
	default:
		log.Printf("Fernsteuerung (PDH): %s – nicht bereit: %s", what, res.Message)
		writeJSONError(w, http.StatusServiceUnavailable, res.Message)
	}
}

func (a *remoteAPI) handleClose(w http.ResponseWriter, r *http.Request) {
	addr := sourceAddr(r)
	if !a.d.Session.CloseRemote() {
		writeJSONError(w, http.StatusConflict, "Es ist keine von PDH geöffnete Sitzung offen.")
		return
	}
	log.Printf("Fernsteuerung (PDH): Sitzung geschlossen von %s", addr)
	writeJSON(w, http.StatusOK, struct {
		Status string `json:"status"`
	}{"closed"})
}

// --- Sperre je Quelladresse ---

type addrLock struct {
	failures    int
	until, last time.Time
}

type addrLockout struct {
	mu sync.Mutex
	m  map[string]*addrLock
}

func (l *addrLockout) remaining(addr string, now time.Time) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[addr]
	if !ok || e.until.IsZero() {
		return 0
	}
	if !now.Before(e.until) {
		delete(l.m, addr)
		return 0
	}
	return e.until.Sub(now)
}

// fail zählt einen Fehlversuch. Fehlversuche verfallen nach der Sperrdauer ohne weiteren.
func (l *addrLockout) fail(addr string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.m[addr]
	if !ok {
		l.prune(now)
		e = &addrLock{}
		l.m[addr] = e
	}
	if e.until.IsZero() && now.Sub(e.last) >= lockoutDuration {
		e.failures = 0
	}
	e.failures++
	e.last = now
	if e.failures >= maxFailures && e.until.IsZero() {
		e.until = now.Add(lockoutDuration)
	}
}

func (l *addrLockout) reset(addr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.m, addr)
}

// prune entfernt Verfallenes und bei voller Liste den ältesten Eintrag; der Aufrufer hält mu.
func (l *addrLockout) prune(now time.Time) {
	for k, e := range l.m {
		if e.until.IsZero() && now.Sub(e.last) >= lockoutDuration || !e.until.IsZero() && !now.Before(e.until) {
			delete(l.m, k)
		}
	}
	for len(l.m) >= remoteLockMax {
		oldest, at := "", time.Time{}
		for k, e := range l.m {
			if oldest == "" || e.last.Before(at) {
				oldest, at = k, e.last
			}
		}
		delete(l.m, oldest)
	}
}
