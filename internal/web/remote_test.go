package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/session"
)

type fakeRemoteSession struct {
	mu      sync.Mutex
	snap    session.Snapshot
	res     session.RemoteResult
	err     error
	got     []session.RemoteRequest
	closeOK bool
	closes  int
}

func (f *fakeRemoteSession) Snapshot() session.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snap
}

func (f *fakeRemoteSession) OpenRemote(_ context.Context, r session.RemoteRequest) (session.RemoteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, r)
	return f.res, f.err
}

func (f *fakeRemoteSession) CloseRemote() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closes++
	return f.closeOK
}

type remoteEnv struct {
	t        *testing.T
	cfg      config.Config
	sess     *fakeRemoteSession
	clock    *fakeClock
	notReady string
	h        http.Handler
	key      string
}

func newRemoteEnv(t *testing.T) *remoteEnv {
	t.Helper()
	key, hash, err := NewRemoteKey()
	if err != nil {
		t.Fatal(err)
	}
	e := &remoteEnv{t: t, cfg: config.Default(), sess: &fakeRemoteSession{snap: session.Snapshot{State: session.StateTiles}}, key: key,
		clock: &fakeClock{t: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}}
	e.cfg.Terminal.Name = "Presse Nord"
	e.cfg.Admin.RemoteKeyHash = hash
	var mu sync.Mutex
	e.h = NewRemoteAPI(RemoteDeps{
		Config: func() config.Config {
			mu.Lock()
			defer mu.Unlock()
			return e.cfg
		},
		Session:  e.sess,
		Version:  "0.1.7",
		NotReady: func(context.Context) string { return e.notReady },
		Clock:    e.clock,
	})
	return e
}

func (e *remoteEnv) do(method, path, auth, body, from string) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(method, "https://terminal:8443"+path, strings.NewReader(body))
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	if from != "" {
		r.RemoteAddr = from
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func (e *remoteEnv) call(method, path, body string) *httptest.ResponseRecorder {
	return e.do(method, path, "Bearer "+e.key, body, "")
}

func jsonOf(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type %q (Status %d, %s)", ct, w.Code, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("kein JSON: %q", w.Body.String())
	}
	return m
}

const validOpen = `{"host":"10.1.2.3","port":5900,"password":"geheim","mode":"view","label":"Linie 3 / Presse 2","user":"M. Muster"}`

func TestRemoteKeyFormatAndHash(t *testing.T) {
	key, hash, err := NewRemoteKey()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, "hmik_") || len(key) != len("hmik_")+43 || strings.ContainsAny(key, "+/=") {
		t.Fatalf("Schlüssel %q", key)
	}
	if hash != RemoteKeyHash(key) || len(hash) != 64 {
		t.Fatalf("Hash %q", hash)
	}
	cfg := config.Default()
	cfg.Admin.RemoteKeyHash = hash
	if err := config.Validate(cfg); err != nil {
		t.Fatal(err)
	}
	other, _, _ := NewRemoteKey()
	if other == key {
		t.Fatal("gleicher Schlüssel")
	}
}

func TestRemoteAuthRequired(t *testing.T) {
	e := newRemoteEnv(t)
	for name, auth := range map[string]string{
		"ohne":           "",
		"falsch":         "Bearer hmik_falsch",
		"ohne Bearer":    e.key,
		"Basic":          "Basic " + e.key,
		"Hash statt Key": "Bearer " + e.cfg.Admin.RemoteKeyHash,
	} {
		for _, p := range []struct{ m, path string }{{"GET", "/api/remote/v1/status"}, {"POST", "/api/remote/v1/open"}, {"POST", "/api/remote/v1/close"}} {
			w := e.do(p.m, p.path, auth, validOpen, "192.0.2.1:4000")
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("%s %s: %d", name, p.path, w.Code)
			}
			if jsonOf(t, w)["error"] == "" {
				t.Fatalf("%s: keine Meldung", name)
			}
		}
		// Zurücksetzen zwischen den Fällen: Nur die Sperre selbst wird unten geprüft.
		e.h = newRemoteEnvWithKey(t, e).h
	}
	if len(e.sess.got) != 0 || e.sess.closes != 0 {
		t.Fatal("ohne Anmeldung ausgeführt")
	}
	// Ein Cookie ersetzt den Schlüssel nicht.
	r := httptest.NewRequest("GET", "/api/remote/v1/status", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "egal"})
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("Cookie: %d", w.Code)
	}
	// Scheme ohne Rücksicht auf Groß- und Kleinschreibung.
	if w := e.do("GET", "/api/remote/v1/status", "bearer "+e.key, "", ""); w.Code != http.StatusOK {
		t.Fatalf("bearer klein: %d", w.Code)
	}
}

// newRemoteEnvWithKey baut den Handler neu (frische Sperre) mit demselben Zustand.
func newRemoteEnvWithKey(t *testing.T, e *remoteEnv) *remoteEnv {
	t.Helper()
	e.h = NewRemoteAPI(RemoteDeps{
		Config: func() config.Config { return e.cfg }, Session: e.sess, Version: "0.1.7",
		NotReady: func(context.Context) string { return e.notReady }, Clock: e.clock,
	})
	return e
}

func TestRemoteOffRejectsEveryKey(t *testing.T) {
	e := newRemoteEnv(t)
	e.cfg.Admin.RemoteKeyHash = ""
	w := e.call("GET", "/api/remote/v1/status", "")
	if w.Code != http.StatusUnauthorized || !strings.Contains(jsonOf(t, w)["error"].(string), "ausgeschaltet") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRemoteLockoutPerSourceAddress(t *testing.T) {
	e := newRemoteEnv(t)
	for i := 0; i < maxFailures-1; i++ {
		if w := e.do("GET", "/api/remote/v1/status", "Bearer hmik_falsch", "", "192.0.2.1:1000"); w.Code != http.StatusUnauthorized {
			t.Fatalf("Versuch %d: %d", i+1, w.Code)
		}
	}
	w := e.do("GET", "/api/remote/v1/status", "Bearer hmik_falsch", "", "192.0.2.1:1001")
	if w.Code != http.StatusTooManyRequests || !strings.Contains(jsonOf(t, w)["error"].(string), "gesperrt") {
		t.Fatalf("Sperre: %d %s", w.Code, w.Body.String())
	}
	// Gesperrt gilt auch der richtige Schlüssel, aber nur für diese Adresse.
	if w := e.do("GET", "/api/remote/v1/status", "Bearer "+e.key, "", "192.0.2.1:1002"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("richtiger Schlüssel während der Sperre: %d", w.Code)
	}
	if w := e.do("GET", "/api/remote/v1/status", "Bearer "+e.key, "", "192.0.2.2:1000"); w.Code != http.StatusOK {
		t.Fatalf("andere Adresse: %d", w.Code)
	}
	e.clock.advance(lockoutDuration)
	if w := e.do("GET", "/api/remote/v1/status", "Bearer "+e.key, "", "192.0.2.1:1003"); w.Code != http.StatusOK {
		t.Fatalf("nach Ablauf: %d", w.Code)
	}
}

func TestRemoteLockoutMapIsBounded(t *testing.T) {
	e := newRemoteEnv(t)
	for i := 0; i < 3*remoteLockMax; i++ {
		e.do("GET", "/api/remote/v1/status", "", "", "[2001:db8::"+strings.ToLower(strings.TrimLeft(hex4(i), "0"))+"1]:1")
	}
	api := e.h.(*remoteAPI)
	api.lock.mu.Lock()
	n := len(api.lock.m)
	api.lock.mu.Unlock()
	if n > remoteLockMax {
		t.Fatalf("%d Einträge", n)
	}
}

func hex4(i int) string {
	const digits = "0123456789abcdef"
	b := []byte{digits[i>>12&15], digits[i>>8&15], digits[i>>4&15], digits[i&15]}
	return string(b)
}

func TestRemoteStatusFree(t *testing.T) {
	e := newRemoteEnv(t)
	w := e.call("GET", "/api/remote/v1/status", "")
	if w.Code != http.StatusOK {
		t.Fatalf("%d", w.Code)
	}
	if got := strings.TrimSpace(w.Body.String()); got != `{"version":"0.1.7","terminal":"Presse Nord","busy":false}` {
		t.Fatalf("Antwort %s", got)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("Cache-Control")
	}
}

func TestRemoteStatusBusy(t *testing.T) {
	e := newRemoteEnv(t)
	since := time.Date(2026, 11, 9, 11, 30, 0, 0, time.UTC)
	e.sess.snap = session.Snapshot{State: session.StateConnected, HMI: "a", HMIName: "Ofen", Since: since}
	m := jsonOf(t, e.call("GET", "/api/remote/v1/status", ""))
	if m["busy"] != true || m["hmi"] != "Ofen" || m["since"] != "2026-11-09T11:30:00Z" || m["source"] != "local" {
		t.Fatalf("lokal: %v", m)
	}
	e.sess.snap = session.Snapshot{State: session.StateStarting, HMI: session.RemoteAdHocID, HMIName: "L – von PDH: U", Remote: true, RemoteLabel: "L", Since: since}
	m = jsonOf(t, e.call("GET", "/api/remote/v1/status", ""))
	if m["busy"] != true || m["hmi"] != "L" || m["source"] != "pdh" {
		t.Fatalf("PDH: %v", m)
	}
	// Die Daueranzeige zwischen zwei Versuchen ist belegt.
	e.sess.snap = session.Snapshot{State: session.StateReconnecting, HMIName: "Presse", Since: since}
	if m = jsonOf(t, e.call("GET", "/api/remote/v1/status", "")); m["busy"] != true {
		t.Fatalf("Neuverbinden: %v", m)
	}
}

func TestRemoteOpenPassesRequest(t *testing.T) {
	e := newRemoteEnv(t)
	w := e.call("POST", "/api/remote/v1/open", validOpen)
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"opened"}` {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	want := session.RemoteRequest{Host: "10.1.2.3", Port: 5900, Password: "geheim", ViewOnly: true, Label: "Linie 3 / Presse 2", User: "M. Muster"}
	if len(e.sess.got) != 1 || e.sess.got[0] != want {
		t.Fatalf("Befehl %+v", e.sess.got)
	}
	e.call("POST", "/api/remote/v1/open", strings.Replace(validOpen, `"view"`, `"control"`, 1))
	if e.sess.got[1].ViewOnly {
		t.Fatal("control als nur ansehen")
	}
	// Leeres Passwort ist erlaubt.
	if w := e.call("POST", "/api/remote/v1/open", strings.Replace(validOpen, `"geheim"`, `""`, 1)); w.Code != http.StatusOK {
		t.Fatalf("leeres Passwort: %d", w.Code)
	}
}

func TestRemoteOpenValidation(t *testing.T) {
	e := newRemoteEnv(t)
	repl := func(old, nw string) string { return strings.Replace(validOpen, old, nw, 1) }
	cases := map[string]string{
		"kein JSON":              "host=x",
		"unbekanntes Feld":       repl(`"user"`, `"extra":1,"user"`),
		"zwei Objekte":           validOpen + validOpen,
		"Host leer":              repl(`"10.1.2.3"`, `""`),
		"Host mit Leerzeichen":   repl(`"10.1.2.3"`, `"10.1.2.3 x"`),
		"Host mit Zeilenumbruch": repl(`"10.1.2.3"`, `"10.1.2.3\nx"`),
		"Port 0":                 repl(`5900`, `0`),
		"Port zu groß":           repl(`5900`, `65536`),
		"Port als Text":          repl(`5900`, `"5900"`),
		"Port fehlt":             repl(`"port":5900,`, ``),
		"Passwort zu lang":       repl(`"geheim"`, `"`+strings.Repeat("ä", 33)+`"`),
		"Modus unbekannt":        repl(`"view"`, `"admin"`),
		"Modus fehlt":            repl(`"mode":"view",`, ``),
		"Label fehlt":            repl(`"label":"Linie 3 / Presse 2",`, ``),
		"Label Steuerzeichen":    repl(`Linie 3`, `Linie\u00073`),
		"Label zu lang":          repl(`Linie 3 / Presse 2`, strings.Repeat("x", 121)),
		"Benutzer leer":          repl(`"M. Muster"`, `""`),
		"zu groß":                repl(`"M. Muster"`, `"`+strings.Repeat("x", 5000)+`"`),
	}
	for name, body := range cases {
		w := e.call("POST", "/api/remote/v1/open", body)
		if w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
			continue
		}
		if msg, _ := jsonOf(t, w)["error"].(string); msg == "" {
			t.Errorf("%s: keine Meldung", name)
		}
	}
	if len(e.sess.got) != 0 {
		t.Fatalf("ungültig ausgeführt: %+v", e.sess.got)
	}
	// Genau 64 Byte Passwort gehen.
	if w := e.call("POST", "/api/remote/v1/open", repl(`"geheim"`, `"`+strings.Repeat("ä", 32)+`"`)); w.Code != http.StatusOK {
		t.Fatalf("64 Byte: %d %s", w.Code, w.Body.String())
	}
}

func TestRemoteOpenBusy(t *testing.T) {
	e := newRemoteEnv(t)
	e.sess.res = session.RemoteResult{Outcome: session.RemoteBusy, HMI: "Ofen", Since: time.Date(2026, 11, 9, 11, 0, 0, 0, time.UTC)}
	w := e.call("POST", "/api/remote/v1/open", validOpen)
	if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != `{"status":"busy","hmi":"Ofen","since":"2026-11-09T11:00:00Z"}` {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	// Ein anderes Terminal hält das HMI: Beginn unbekannt.
	e.sess.res = session.RemoteResult{Outcome: session.RemoteBusy, HMI: "Presse (an Halle 3)"}
	w = e.call("POST", "/api/remote/v1/open", validOpen)
	if w.Code != http.StatusConflict || strings.TrimSpace(w.Body.String()) != `{"status":"busy","hmi":"Presse (an Halle 3)"}` {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRemoteOpenNotReady(t *testing.T) {
	e := newRemoteEnv(t)
	e.notReady = "Eine Sicherung läuft."
	w := e.call("POST", "/api/remote/v1/open", validOpen)
	if w.Code != http.StatusServiceUnavailable || jsonOf(t, w)["error"] != "Eine Sicherung läuft." || len(e.sess.got) != 0 {
		t.Fatalf("Sicherung: %d %s", w.Code, w.Body.String())
	}
	e.notReady = ""
	e.sess.res = session.RemoteResult{Outcome: session.RemoteNotReady, Message: "Wartungsmodus"}
	w = e.call("POST", "/api/remote/v1/open", validOpen)
	if w.Code != http.StatusServiceUnavailable || jsonOf(t, w)["error"] != "Wartungsmodus" {
		t.Fatalf("Sitzung: %d %s", w.Code, w.Body.String())
	}
	e.sess.res, e.sess.err = session.RemoteResult{}, session.ErrStopped
	if w = e.call("POST", "/api/remote/v1/open", validOpen); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("beendet: %d", w.Code)
	}
}

func TestRemoteClose(t *testing.T) {
	e := newRemoteEnv(t)
	w := e.call("POST", "/api/remote/v1/close", "")
	if w.Code != http.StatusConflict || jsonOf(t, w)["error"] == "" {
		t.Fatalf("ohne PDH-Sitzung: %d %s", w.Code, w.Body.String())
	}
	e.sess.closeOK = true
	w = e.call("POST", "/api/remote/v1/close", "")
	if w.Code != http.StatusOK || strings.TrimSpace(w.Body.String()) != `{"status":"closed"}` {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRemoteMethodsAndUnknownPaths(t *testing.T) {
	e := newRemoteEnv(t)
	for _, c := range []struct {
		m, path string
		code    int
	}{
		{"POST", "/api/remote/v1/status", http.StatusMethodNotAllowed},
		{"GET", "/api/remote/v1/open", http.StatusMethodNotAllowed},
		{"GET", "/api/remote/v1/close", http.StatusMethodNotAllowed},
		{"GET", "/api/remote/v2/status", http.StatusNotFound},
		{"GET", "/api/remote/", http.StatusNotFound},
	} {
		w := e.call(c.m, c.path, "")
		if w.Code != c.code {
			t.Errorf("%s %s: %d", c.m, c.path, w.Code)
		}
		jsonOf(t, w)
	}
	// Unbekannte Pfade verraten ohne Schlüssel nichts.
	if w := e.do("GET", "/api/remote/v2/status", "", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("ohne Schlüssel: %d", w.Code)
	}
}

func TestRemoteLogNamesNoPassword(t *testing.T) {
	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()
	e := newRemoteEnv(t)
	e.do("POST", "/api/remote/v1/open", "Bearer "+e.key, strings.Replace(validOpen, "geheim", "Sup3rGeheim", 1), "192.0.2.9:4444")
	out := buf.String()
	wantContains(t, out, "Linie 3 / Presse 2", "M. Muster", "nur ansehen", "192.0.2.9")
	wantNotContains(t, out, "Sup3rGeheim", e.key)
}

func captureLog(w io.Writer) func() {
	log.SetOutput(w)
	return func() { log.SetOutput(os.Stderr) }
}

func TestRemoteInvalidJSONDoesNotEchoPassword(t *testing.T) {
	var buf bytes.Buffer
	restore := captureLog(&buf)
	defer restore()
	e := newRemoteEnv(t)
	w := e.call("POST", "/api/remote/v1/open", `{"host":"10.1.2.3","port":5900,"password":"Q\qGeheim","mode":"view","label":"L","user":"U"}`)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("%d", w.Code)
	}
	wantNotContains(t, w.Body.String()+buf.String(), "'q'", "Geheim")
	w = e.call("POST", "/api/remote/v1/open", strings.Replace(validOpen, `"user"`, `"extra":1,"user"`, 1))
	wantContains(t, jsonOf(t, w)["error"].(string), `"extra"`)
}

func TestRemoteOpenInvalidFromSession(t *testing.T) {
	e := newRemoteEnv(t)
	e.sess.res = session.RemoteResult{Outcome: session.RemoteInvalid, Message: "VNC-Anmeldung am HMI fehlgeschlagen: abgelehnt"}
	w := e.call("POST", "/api/remote/v1/open", validOpen)
	if w.Code != http.StatusUnprocessableEntity || jsonOf(t, w)["error"] != "VNC-Anmeldung am HMI fehlgeschlagen: abgelehnt" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestRemoteOpenRejectsUnsafeInput(t *testing.T) {
	e := newRemoteEnv(t)
	repl := func(old, nw string) string { return strings.Replace(validOpen, old, nw, 1) }
	cases := map[string]string{
		"Passwort mit Zeilenumbruch": repl(`"geheim"`, `"geh\neim"`),
		"Passwort mit NUL":           repl(`"geheim"`, `"geh\u0000eim"`),
		"Loopback v4":                repl(`"10.1.2.3"`, `"127.0.0.1"`),
		"Loopback anderer":           repl(`"10.1.2.3"`, `"127.8.9.1"`),
		"Loopback v6":                repl(`"10.1.2.3"`, `"::1"`),
		"Loopback v6 Klammern":       repl(`"10.1.2.3"`, `"[::1]"`),
		"unspezifiziert":             repl(`"10.1.2.3"`, `"0.0.0.0"`),
		"unspezifiziert v6":          repl(`"10.1.2.3"`, `"::"`),
		"localhost":                  repl(`"10.1.2.3"`, `"LocalHost"`),
		"localhost Punkt":            repl(`"10.1.2.3"`, `"localhost."`),
		"Unter-localhost":            repl(`"10.1.2.3"`, `"x.localhost"`),
		"Label Bidi":                 repl(`Linie 3`, `Linie ‮3`),
		"Label Zeilentrenner":        repl(`Linie 3`, `Linie 3`),
		"Benutzer Absatztrenner":     repl(`M. Muster`, `M. Muster`),
		"Benutzer Nullbreite":        repl(`M. Muster`, `M.​Muster`),
	}
	for name, body := range cases {
		if w := e.call("POST", "/api/remote/v1/open", body); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if len(e.sess.got) != 0 {
		t.Fatalf("ausgeführt: %+v", e.sess.got)
	}
	// Umlaute und Sonderzeichen ohne Steuerwirkung bleiben erlaubt.
	if w := e.call("POST", "/api/remote/v1/open", repl(`"geheim"`, `"Pä$s=;wört"`)); w.Code != http.StatusOK {
		t.Fatalf("Sonderzeichen: %d %s", w.Code, w.Body.String())
	}
}

// Gesperrte Einträge bleiben auch bei voller Liste; IPv6 zählt je /64.
func TestRemoteLockoutKeepsLockedAndGroupsIPv6(t *testing.T) {
	e := newRemoteEnv(t)
	for i := 0; i < maxFailures; i++ {
		e.do("GET", "/api/remote/v1/status", "Bearer hmik_x", "", "192.0.2.50:1")
	}
	for i := 0; i < 3*remoteLockMax; i++ {
		e.clock.advance(time.Millisecond)
		e.do("GET", "/api/remote/v1/status", "", "", "198.51."+itoa(i/250)+"."+itoa(i%250+1)+":1")
	}
	if w := e.do("GET", "/api/remote/v1/status", "Bearer "+e.key, "", "192.0.2.50:2"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("Sperre verdrängt: %d", w.Code)
	}
	// Wechselnde Adressen im selben /64 teilen eine Sperre.
	for i := 0; i < maxFailures; i++ {
		e.do("GET", "/api/remote/v1/status", "Bearer hmik_x", "", "[2001:db8:1:2::"+itoa(i+1)+"]:1")
	}
	if w := e.do("GET", "/api/remote/v1/status", "Bearer "+e.key, "", "[2001:db8:1:2:ffff::9]:1"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("/64 nicht gesperrt: %d", w.Code)
	}
	if w := e.do("GET", "/api/remote/v1/status", "Bearer "+e.key, "", "[2001:db8:1:3::1]:1"); w.Code != http.StatusOK {
		t.Fatalf("anderes /64: %d", w.Code)
	}
}
