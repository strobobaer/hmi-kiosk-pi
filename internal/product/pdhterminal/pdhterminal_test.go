package pdhterminal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/web"
)

const testSecret = "pdht_geheimgeheimgeheim"

// fakePDH bildet die Terminal-API von PDH nach.
type fakePDH struct {
	srv     *httptest.Server
	revoked atomic.Bool
	status  atomic.Int32 // != 0: feste Antwort für /pair
	body    atomic.Value // string: Antworttext für /pair bei status
	gotName atomic.Value
	gotCode atomic.Value
	user    atomic.Value // string: Benutzername in der Antwort von /pair
}

func newFakePDH(t *testing.T) *fakePDH {
	t.Helper()
	p := &fakePDH{}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/terminal/v1/pair", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Code, Name string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		p.gotName.Store(in.Name)
		p.gotCode.Store(in.Code)
		if st := int(p.status.Load()); st != 0 {
			w.WriteHeader(st)
			b, _ := p.body.Load().(string)
			_, _ = w.Write([]byte(b))
			return
		}
		if in.Code != "ABCD-1234" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write([]byte(`{"error":"Code ungültig oder abgelaufen"}`))
			return
		}
		user := "Anna"
		if u, ok := p.user.Load().(string); ok {
			user = u
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"device_id": "a1-1", "secret": testSecret, "user": user})
	})
	mux.HandleFunc("GET /api/terminal/v1/device", func(w http.ResponseWriter, r *http.Request) {
		if p.revoked.Load() || r.Header.Get("Authorization") != "Bearer "+testSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"device_id":"a1-1","user":"Anna","name":"x"}`))
	})
	p.srv = httptest.NewTLSServer(mux)
	t.Cleanup(p.srv.Close)
	return p
}

func TestPairSuccess(t *testing.T) {
	p := newFakePDH(t)
	d, err := p.client().Pair(context.Background(), p.hostURL(), "  ABCD-1234 ", "Kasse 1")
	if err != nil {
		t.Fatal(err)
	}
	if d.DeviceID != "a1-1" || d.Secret != testSecret || d.User != "Anna" || d.URL != p.hostURL() || d.PairedAt.IsZero() {
		t.Errorf("Gerät: %+v", d)
	}
	if p.gotName.Load() != "Kasse 1" || p.gotCode.Load() != "ABCD-1234" {
		t.Errorf("Anfrage: %v %v", p.gotName.Load(), p.gotCode.Load())
	}
	dir := t.TempDir()
	if err := SaveDevice(dir, d); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "pdh", "device.json")
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), testSecret) {
		t.Fatalf("Datei: %v %s", err, b)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Errorf("Datei: %v", st.Mode())
		}
		if st, _ := os.Stat(filepath.Dir(path)); st.Mode().Perm() != 0o700 {
			t.Errorf("Ordner: %v", st.Mode())
		}
	}
	got, err := LoadDevice(dir)
	if err != nil || got == nil || got.Secret != testSecret || !got.PairedAt.Equal(d.PairedAt) {
		t.Errorf("Laden: %+v %v", got, err)
	}
	if err := RemoveDevice(dir); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadDevice(dir); got != nil || err != nil {
		t.Errorf("nach Entfernen: %+v %v", got, err)
	}
	if err := RemoveDevice(dir); err != nil {
		t.Errorf("zweites Entfernen: %v", err)
	}
}

func TestPairErrors(t *testing.T) {
	p := newFakePDH(t)
	c := p.client()
	if _, err := c.Pair(context.Background(), p.hostURL(), "FALSCH", "x"); err == nil || err.Error() != "Code ungültig oder abgelaufen" {
		t.Errorf("422: %v", err)
	}
	p.status.Store(429)
	p.body.Store(`{"error":"too many"}`)
	if _, err := c.Pair(context.Background(), p.hostURL(), "x", "x"); err == nil || !strings.HasPrefix(err.Error(), "Zu viele Fehlversuche") {
		t.Errorf("429: %v", err)
	}
	p.status.Store(200)
	p.body.Store(`kein json`)
	if _, err := c.Pair(context.Background(), p.hostURL(), "x", "x"); err == nil || !strings.Contains(err.Error(), "Ungültige Antwort") {
		t.Errorf("JSON: %v", err)
	}
	p.body.Store(`{"device_id":"d","user":"u"}`)
	if _, err := c.Pair(context.Background(), p.hostURL(), "x", "x"); err == nil {
		t.Errorf("ohne Geheimnis: kein Fehler")
	}
	p.status.Store(302)
	if _, err := c.Pair(context.Background(), p.hostURL(), "x", "x"); err == nil {
		t.Errorf("302: kein Fehler")
	}
	p.srv.Close()
	if _, err := c.Pair(context.Background(), p.hostURL(), "x", "x"); err == nil || err.Error() != "PDH nicht erreichbar" {
		t.Errorf("Verbindung: %v", err)
	}
}

func TestPairUntrustedCert(t *testing.T) {
	p := newFakePDH(t)
	_, err := p.untrustedClient().Pair(context.Background(), p.hostURL(), "ABCD-1234", "x")
	if err == nil || err.Error() != "Zertifikat von PDH wird nicht vertraut" {
		t.Errorf("Pair: %v", err)
	}
	st, err := p.untrustedClient().Check(context.Background(), Device{URL: p.hostURL(), Secret: testSecret})
	if st != StateUntrusted || err == nil {
		t.Errorf("Check: %v %v", st, err)
	}
}

func TestCheckStates(t *testing.T) {
	p := newFakePDH(t)
	d := Device{DeviceID: "a1-1", Secret: testSecret, URL: p.hostURL()}
	if st, err := p.client().Check(context.Background(), d); st != StateOK || err != nil {
		t.Errorf("200: %v %v", st, err)
	}
	p.revoked.Store(true)
	if st, err := p.client().Check(context.Background(), d); st != StateRevoked || err != nil {
		t.Errorf("401: %v %v", st, err)
	}
	p.srv.Close()
	if st, err := p.client().Check(context.Background(), d); st != StateUnreachable || err == nil {
		t.Errorf("Verbindung: %v %v", st, err)
	}
}

func TestDeviceStringHidesSecret(t *testing.T) {
	d := Device{DeviceID: "a1-1", Secret: testSecret, User: "Anna", URL: "https://pdh.test"}
	for _, s := range []string{
		d.String(), fmt.Sprintf("%v", d), fmt.Sprintf("%+v", d), fmt.Sprintf("%#v", d), fmt.Sprint(&d), fmt.Sprintf("%s", d),
	} {
		if strings.Contains(s, testSecret) || strings.Contains(s, "pdht_") {
			t.Errorf("Geheimnis in %q", s)
		}
	}
}

// --- Admin-Seiten ---

type adminEnv struct {
	t       *testing.T
	dir     string
	store   *config.Store
	h       http.Handler
	cookies map[string]string
}

const adminPassword = "Passwort-fuer-Tests-1"

func newAdminEnv(t *testing.T, client Client) *adminEnv {
	t.Helper()
	dir := t.TempDir()
	box, err := secrets.OpenBox(filepath.Join(dir, "secrets.key"), false)
	if err != nil {
		t.Fatal(err)
	}
	store := config.Open(dir)
	hash, err := secrets.HashPassword(adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	c := config.Default()
	c.Terminal.Name = "Kasse"
	c.Admin.PasswordHash = hash
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	h := web.NewAdmin(web.AdminDeps{
		Store: store, Box: box, Apply: func(config.Config) {}, Helper: nopHelper{},
		Status:  func() web.SystemStatus { return web.SystemStatus{} },
		KeyFile: filepath.Join(dir, "secrets.key"), TLSDir: t.TempDir(), LogDir: t.TempDir(),
		Product: Admin(dir, client),
	})
	e := &adminEnv{t: t, dir: dir, store: store, h: h, cookies: map[string]string{}}
	m := csrfRe.FindStringSubmatch(e.get("/admin/login").Body.String())
	if m == nil {
		t.Fatal("kein CSRF-Feld auf der Anmeldeseite")
	}
	r := e.do(formReq("/admin/login", url.Values{"password": {adminPassword}, "csrf": {m[1]}}))
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Anmeldung: %d", r.Code)
	}
	return e
}

type nopHelper struct{}

func (nopHelper) Call(context.Context, string, string) error { return nil }

func formReq(path string, v url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://admin.test"+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func (e *adminEnv) do(req *http.Request) *httptest.ResponseRecorder {
	for k, v := range e.cookies {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(e.cookies, c.Name)
		} else {
			e.cookies[c.Name] = c.Value
		}
	}
	return w
}

func (e *adminEnv) get(path string) *httptest.ResponseRecorder {
	return e.do(httptest.NewRequest(http.MethodGet, "https://admin.test"+path, nil))
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (e *adminEnv) post(path string, v url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	m := csrfRe.FindStringSubmatch(e.get("/admin/pdh").Body.String())
	if m == nil {
		e.t.Fatal("kein CSRF-Feld")
	}
	v.Set("csrf", m[1])
	return e.do(formReq(path, v))
}

func (e *adminEnv) setPDH(p config.PDHCfg) {
	e.t.Helper()
	c, _, err := e.store.Load()
	if err != nil {
		e.t.Fatal(err)
	}
	c.PDH = p
	if err := e.store.Save(c); err != nil {
		e.t.Fatal(err)
	}
}

func (e *adminEnv) deviceFileExists() bool {
	_, err := os.Stat(filepath.Join(e.dir, "pdh", "device.json"))
	return err == nil
}

func TestAdminPages(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())

	// Seiten und Menü
	r := e.get("/admin/pdh")
	if r.Code != http.StatusOK {
		t.Fatalf("/admin/pdh: %d", r.Code)
	}
	for _, s := range []string{"nicht gekoppelt", ">PDH</a>", ">Kartenleser</a>", ">Netzwerk</a>", ">System</a>", "PDH-Terminal"} {
		if !strings.Contains(r.Body.String(), s) {
			t.Errorf("/admin/pdh ohne %q", s)
		}
	}
	if r := e.get("/admin/card"); r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Kartenleser folgt in einer späteren Version") {
		t.Errorf("/admin/card: %d", r.Code)
	}
	if r := e.get("/admin/"); r.Header().Get("Location") != "/admin/pdh" {
		t.Errorf("Startseite: %q", r.Header().Get("Location"))
	}

	// Ungültige Adresse
	r = e.post("/admin/pdh/settings", url.Values{"url": {"https://pdh.firma.de/pfad"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "ohne Pfad") {
		t.Errorf("ungültige URL: %d %s", r.Code, r.Body.String())
	}

	// Speichern gültig
	r = e.post("/admin/pdh/settings", url.Values{
		"url": {p.hostURL()}, "deviceName": {"Kasse 1"}, "extraOrigins": {"https://cdn.firma.de:443\r\n\r\n https://img.firma.de:443 "},
	})
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), "Einstellungen gespeichert") {
		t.Fatalf("Speichern: %d %s", r.Code, r.Body.String())
	}
	c, _, _ := e.store.Load()
	if c.PDH.URL != p.hostURL() || c.PDH.DeviceName != "Kasse 1" || len(c.PDH.ExtraOrigins) != 2 || c.PDH.AllowHTTP {
		t.Errorf("Konfiguration: %+v", c.PDH)
	}

	// Koppeln: falscher Code, dann richtiger
	r = e.post("/admin/pdh/pair", url.Values{"code": {"FALSCH"}})
	if !strings.Contains(r.Body.String(), "Code ungültig oder abgelaufen") || e.deviceFileExists() {
		t.Errorf("falscher Code: %d", r.Code)
	}
	r = e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	body := r.Body.String()
	if r.Code != http.StatusOK || !strings.Contains(body, "Gekoppelt als") || !e.deviceFileExists() {
		t.Fatalf("Koppeln: %d %s", r.Code, body)
	}
	if p.gotName.Load() != "Kasse 1" {
		t.Errorf("Gerätename: %v", p.gotName.Load())
	}
	if !strings.Contains(body, "gekoppelt als „Anna“") || !strings.Contains(body, "verbunden") {
		t.Errorf("Zustand: %s", body)
	}
	for _, s := range []string{body, e.get("/admin/pdh").Body.String(), e.get("/admin/card").Body.String()} {
		if strings.Contains(s, "pdht_") || strings.Contains(s, "ABCD-1234") {
			t.Errorf("Geheimnis oder Code in der Seite")
		}
	}

	// Gesperrt
	p.revoked.Store(true)
	if s := e.get("/admin/pdh").Body.String(); !strings.Contains(s, "gesperrt") {
		t.Errorf("gesperrt: %s", s)
	}
	p.revoked.Store(false)

	// URL-Wechsel entfernt die Kopplung
	r = e.post("/admin/pdh/settings", url.Values{"url": {"https://anders.firma.de"}})
	if !strings.Contains(r.Body.String(), "Kopplung entfernt, weil die PDH-Adresse geändert wurde.") || e.deviceFileExists() {
		t.Errorf("URL-Wechsel: %d %s", r.Code, r.Body.String())
	}

	// Erneut koppeln, Einstellungen mit gleicher URL lassen die Kopplung stehen, Unpair löscht
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	e.post("/admin/pdh/settings", url.Values{"url": {p.hostURL()}, "deviceName": {"Neu"}})
	if !e.deviceFileExists() {
		t.Errorf("gleiche URL: Kopplung weg")
	}
	r = e.post("/admin/pdh/unpair", url.Values{})
	if e.deviceFileExists() || !strings.Contains(r.Body.String(), "Kopplung entfernt.") {
		t.Errorf("Unpair: %d", r.Code)
	}

	// Ohne CSRF abgelehnt
	if r := e.do(formReq("/admin/pdh/unpair", url.Values{})); r.Code != http.StatusForbidden {
		t.Errorf("ohne CSRF: %d", r.Code)
	}
}

func TestAdminForeignDeviceFileIsNotPaired(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	if err := SaveDevice(e.dir, Device{DeviceID: "x", Secret: testSecret, User: "Bob", URL: "https://andere.firma.de"}); err != nil {
		t.Fatal(err)
	}
	s := e.get("/admin/pdh").Body.String()
	if !strings.Contains(s, "nicht gekoppelt") || strings.Contains(s, "Bob") || strings.Contains(s, "pdht_") {
		t.Errorf("fremde Kopplung: %s", s)
	}
}

func TestAdminUntrustedShown(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.untrustedClient())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	r := e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	if !strings.Contains(r.Body.String(), "Zertifikat von PDH wird nicht vertraut") || e.deviceFileExists() {
		t.Errorf("Zertifikat: %d", r.Code)
	}
}

// hostURL nennt den Testserver unter einem Namen, den die Konfiguration zulässt
// (das Zertifikat von httptest gilt für example.com); der Client wählt die echte Adresse an.
func (p *fakePDH) hostURL() string {
	_, port, _ := net.SplitHostPort(p.srv.Listener.Addr().String())
	return "https://example.com:" + port
}

func (p *fakePDH) transport(trust bool) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	addr := p.srv.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	if trust {
		pool := x509.NewCertPool()
		pool.AddCert(p.srv.Certificate())
		tr.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	return tr
}

func (p *fakePDH) client() Client { return Client{HTTP: &http.Client{Transport: p.transport(true)}} }

// untrustedClient kennt die Zertifizierungsstelle des Testservers nicht.
func (p *fakePDH) untrustedClient() Client {
	return Client{HTTP: &http.Client{Transport: p.transport(false)}}
}

func TestNoRedirects(t *testing.T) {
	var hits atomic.Int32
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"device_id":"a1","secret":"pdht_x","user":"U"}`))
	}))
	defer second.Close()
	for _, code := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, second.URL+r.URL.Path, code)
		}))
		for name, c := range map[string]Client{"Standard": {}, "eigener Client": {HTTP: &http.Client{}}} {
			if _, err := c.Pair(context.Background(), first.URL, "ABCD", "x"); err == nil {
				t.Errorf("%s %d: Pair ohne Fehler", name, code)
			}
			if st, _ := c.Check(context.Background(), Device{DeviceID: "a1", Secret: testSecret, URL: first.URL}); st == StateOK {
				t.Errorf("%s %d: Check folgt der Weiterleitung", name, code)
			}
		}
		first.Close()
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("zweiter Server bekam %d Anfragen", n)
	}
}

func TestPairRejectsBadIdentity(t *testing.T) {
	p := newFakePDH(t)
	p.status.Store(200)
	for _, body := range []string{
		`{"device_id":"<b>","secret":"pdht_x","user":"U"}`,
		`{"device_id":"` + strings.Repeat("a", 65) + `","secret":"pdht_x","user":"U"}`,
		`{"device_id":"a1","secret":"pdht x","user":"U"}`,
	} {
		p.body.Store(body)
		if _, err := p.client().Pair(context.Background(), p.hostURL(), "x", "x"); err == nil || err.Error() != "Ungültige Antwort von PDH" {
			t.Errorf("%s: %v", body, err)
		}
	}
}

func TestAdminEscapesAndTruncatesPDHText(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	evil := "<script>alert(1)</script>" + strings.Repeat("a", 1000)
	p.user.Store(evil)
	r := e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	for _, s := range []string{r.Body.String(), e.get("/admin/pdh").Body.String()} {
		if strings.Contains(s, "<script>alert") || !strings.Contains(s, "&lt;script&gt;alert(1)&lt;/script&gt;") {
			t.Errorf("Benutzername nicht maskiert")
		}
		if strings.Contains(s, strings.Repeat("a", 200)) {
			t.Errorf("Benutzername nicht gekürzt")
		}
	}
	d, err := LoadDevice(e.dir)
	if err != nil || len([]rune(d.User)) > 200 {
		t.Errorf("gespeichert: %v %d", err, len([]rune(d.User)))
	}

	p.status.Store(422)
	p.body.Store(`{"error":"<img src=x onerror=alert(1)>` + strings.Repeat("b", 1000) + `"}`)
	r = e.post("/admin/pdh/pair", url.Values{"code": {"X"}})
	s := r.Body.String()
	if strings.Contains(s, "<img src") || !strings.Contains(s, "&lt;img") || strings.Contains(s, strings.Repeat("b", 200)) {
		t.Errorf("422-Text nicht maskiert oder gekürzt")
	}
}

func TestAdminSameURLKeepsPairing(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	r := e.post("/admin/pdh/settings", url.Values{"url": {p.hostURL()}})
	if !e.deviceFileExists() || !strings.Contains(r.Body.String(), "gekoppelt als") {
		t.Errorf("gleiche URL: Kopplung weg")
	}
	// Abweichende Schreibweisen lehnt die Prüfung ab; die Kopplung bleibt.
	for _, u := range []string{p.hostURL() + "/", strings.Replace(p.hostURL(), "example.com", "EXAMPLE.com", 1)} {
		r = e.post("/admin/pdh/settings", url.Values{"url": {u}})
		if r.Code != http.StatusBadRequest || !e.deviceFileExists() {
			t.Errorf("%s: %d, Datei %v", u, r.Code, e.deviceFileExists())
		}
	}
}

func TestAdminCorruptDeviceRemovedOnURLChange(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	path := filepath.Join(e.dir, "pdh", "device.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.post("/admin/pdh/settings", url.Values{"url": {"https://anders.firma.de"}})
	if e.deviceFileExists() {
		t.Errorf("beschädigte Datei bleibt")
	}
}

func TestAdminExtraOriginsNormalized(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	r := e.post("/admin/pdh/settings", url.Values{
		"url": {p.hostURL()}, "extraOrigins": {"https://cdn.firma.de\nhttp://img.firma.de:8080"},
	})
	if r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d %s", r.Code, r.Body.String())
	}
	c, _, _ := e.store.Load()
	if len(c.PDH.ExtraOrigins) != 2 || c.PDH.ExtraOrigins[0] != "https://cdn.firma.de:443" || c.PDH.ExtraOrigins[1] != "http://img.firma.de:8080" {
		t.Errorf("Ursprünge: %v", c.PDH.ExtraOrigins)
	}
	if body := e.get("/admin/pdh").Body.String(); !strings.Contains(body, "https://cdn.firma.de:443") {
		t.Errorf("Seite zeigt den Ursprung nicht: %s", body)
	}
	r = e.post("/admin/pdh/settings", url.Values{"url": {p.hostURL()}, "extraOrigins": {"kein url"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "Zusätzliche Adresse ungültig") {
		t.Errorf("Unsinn: %d %s", r.Code, r.Body.String())
	}
}
