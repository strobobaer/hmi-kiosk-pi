package app

import (
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
	"hmikiosk/internal/session"
)

const (
	wantPasswordMissingNotice = "VNC-Passwort fehlt – bitte in der Admin-Seite neu eingeben."
	adminTestPassword         = "richtig-und-lang"
)

// foreignSeal versiegelt mit einem Schlüssel, der nicht der des Geräts ist.
func foreignSeal(t *testing.T, plain string) string {
	t.Helper()
	key, _ := secrets.GenerateKey()
	b, err := secrets.NewBox(key)
	if err != nil {
		t.Fatal(err)
	}
	enc, err := b.Seal(plain)
	if err != nil {
		t.Fatal(err)
	}
	return enc
}

// withLostKeyFile richtet verschlüsselte Werte ein und löscht danach die Schlüsseldatei.
func withLostKeyFile(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	enc := f.seal("geheim")
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].PasswordEnc = enc
		c.SharedKeyEnc = f.seal("gemeinsam")
	})
	if err := os.Remove(filepath.Join(f.data, "secrets.key")); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestMissingKeyFileWithSealedValuesDoesNotRecreateKeyAndKeepsAdmin(t *testing.T) {
	f := withLostKeyFile(t).start()
	if f.s.box == nil || f.s.box.Available() {
		t.Fatal("Box muss vorhanden, aber ohne Schlüssel sein")
	}
	if _, err := os.Stat(filepath.Join(f.data, "secrets.key")); err == nil {
		t.Fatal("Schlüsseldatei stillschweigend neu angelegt")
	}
	if f.bound.Admin == "" {
		t.Fatal("Admin-Seite läuft nicht")
	}
	resp, err := insecureClient().Get("https://" + f.bound.Admin + "/admin/hmis")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable {
		t.Fatal("Admin-Seite meldet 503 statt Reparatur")
	}
	if !hasWarning(f.bound.Status().Warnings, "Schlüsseldatei") {
		t.Fatalf("Warnungen %v", f.bound.Status().Warnings)
	}
	// Die Kacheln laufen weiter, das HMI zeigt den Hinweis und startet nichts.
	if code, body := f.local("GET", "/"); code != 200 || !strings.Contains(body, "Presse 7") {
		t.Fatalf("Kachelseite %d", code)
	}
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Hinweis", func() bool {
		s := f.bound.Snapshot()
		return s.State == session.StateTiles && s.Notice == wantPasswordMissingNotice
	})
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Clients gestartet", n)
	}
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

type adminSession struct {
	t    *testing.T
	c    *http.Client
	base string
}

func (f *fixture) adminLogin() *adminSession {
	f.t.Helper()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{
		Jar:           jar,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	a := &adminSession{t: f.t, c: c, base: "https://" + f.bound.Admin}
	_, page := a.get("/admin/login")
	if _, loc := a.post("/admin/login", page, url.Values{"password": {adminTestPassword}}); loc == "/admin/login" {
		f.t.Fatal("Anmeldung abgelehnt")
	}
	return a
}

func (a *adminSession) get(path string) (int, string) {
	a.t.Helper()
	resp, err := a.c.Get(a.base + path)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if loc := resp.Header.Get("Location"); loc != "" {
		return resp.StatusCode, "→" + loc
	}
	return resp.StatusCode, string(b)
}

// post liest das CSRF-Feld aus page und liefert Status und Weiterleitungsziel (oder Rumpf).
func (a *adminSession) post(path, page string, v url.Values) (int, string) {
	a.t.Helper()
	m := csrfField.FindStringSubmatch(page)
	if m == nil {
		a.t.Fatalf("kein CSRF-Feld in %q", page)
	}
	v.Set("csrf", m[1])
	resp, err := a.c.PostForm(a.base+path, v)
	if err != nil {
		a.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if loc := resp.Header.Get("Location"); loc != "" {
		return resp.StatusCode, loc
	}
	return resp.StatusCode, string(b)
}

func TestRepairInRunningServiceRestoresKeyAndTilesNotice(t *testing.T) {
	hash, err := secrets.HashPassword(adminTestPassword)
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	enc := f.seal("geheim")
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].PasswordEnc = enc
		c.SharedKeyEnc = f.seal("gemeinsam")
		c.Admin.PasswordHash = hash
	})
	if err := os.Remove(filepath.Join(f.data, "secrets.key")); err != nil {
		t.Fatal(err)
	}
	f.start()

	a := f.adminLogin()
	if code, loc := a.get("/admin/hmis"); code != http.StatusSeeOther || loc != "→/admin/repair" {
		t.Fatalf("Umleitung: %d %s", code, loc)
	}
	_, page := a.get("/admin/repair")
	if !strings.Contains(page, "Presse 7") {
		t.Fatalf("Reparaturseite ohne betroffenes HMI: %s", page)
	}
	if code, body := a.post("/admin/repair", page, url.Values{"confirm": {"ja"}}); code != http.StatusOK {
		t.Fatalf("Reparatur: %d %s", code, body)
	}

	if !f.s.box.Available() {
		t.Fatal("Box ohne neuen Schlüssel")
	}
	raw, err := os.ReadFile(filepath.Join(f.data, "secrets.key"))
	if err != nil || len(raw) != 32 {
		t.Fatalf("Schlüsseldatei: %d Byte, %v", len(raw), err)
	}
	saved, _, _ := config.Open(f.data).Load()
	if saved.HMIs[0].PasswordEnc != "" || !saved.HMIs[0].PasswordLost || saved.SharedKeyEnc != "" {
		t.Fatalf("Konfiguration: %+v", saved.HMIs[0])
	}
	if code, _ := a.get("/admin/hmis"); code != http.StatusOK {
		t.Fatalf("Liste nach der Reparatur: %d", code)
	}
	// Das HMI verlangt weiter ein neues Passwort, auch nach der Reparatur.
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantPasswordMissingNotice })
	// Ein neues Passwort lässt sich nun speichern und wieder entschlüsseln.
	cur := f.s.config()
	h := cur.HMIs[0]
	h.PasswordEnc, _ = f.s.box.Seal("neu")
	h.PasswordLost = false
	if pw, err := f.s.password(h); err != nil || pw != "neu" {
		t.Fatalf("Passwort: %q %v", pw, err)
	}
}

func TestPasswordLookupDistinguishesNoneLostAndUnreadable(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	s, err := newService(f.options())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	good, _ := s.box.Seal("geheim")

	if pw, err := s.password(config.HMI{}); pw != "" || err != nil {
		t.Fatalf("ohne Passwort: %q %v", pw, err)
	}
	if pw, err := s.password(config.HMI{PasswordEnc: good}); pw != "geheim" || err != nil {
		t.Fatalf("lesbar: %q %v", pw, err)
	}
	for name, h := range map[string]config.HMI{
		"verloren":  {PasswordLost: true},
		"unlesbar":  {PasswordEnc: foreignSeal(t, "x")},
		"kein Text": {PasswordEnc: "kaputt"},
	} {
		if _, err := s.password(h); !errors.Is(err, session.ErrPasswordMissing) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMismatchedKeyFileShowsNoticeInsteadOfStartingClient(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].PasswordEnc = foreignSeal(t, "geheim")
	})
	f.seal("x") // legt die Schlüsseldatei des Geräts an; der Wert im HMI stammt von einem anderen
	f.start()
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantPasswordMissingNotice })
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Clients gestartet", n)
	}
}

func TestLostPasswordMarkerBlocksSessionWithoutProbe(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].PasswordLost = true
	})
	f.start()
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantPasswordMissingNotice })
	if n := len(f.runner.started()); n != 0 {
		t.Fatalf("%d Clients gestartet", n)
	}
}

// --- Wartungsmodus von Hand ---

const wantManualMaintenanceNotice = "Wartungsmodus: Von Hand gestartet. Einrichtung in der Admin-Seite."

func TestManualMaintenanceBlocksTilesAndGoesQuiet(t *testing.T) {
	f := newFixture(t)
	f.hw = "aaaa"
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.Terminal.HardwareID = "aaaa"
		c.Terminal.MaintenanceManual = true
	})
	f.start()
	if !f.s.maintenance() || !f.s.coord.Standby() {
		t.Fatal("kein Wartungsmodus oder Koordinator nicht still")
	}
	if code := f.peerNonceStatus(); code != http.StatusServiceUnavailable {
		t.Fatalf("Peer-API %d", code)
	}
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantManualMaintenanceNotice })
	code, body := f.local("POST", "/api/hmi/presse/open")
	if code != http.StatusConflict || !strings.Contains(body, wantManualMaintenanceNotice) {
		t.Fatalf("Kachel: %d %s", code, body)
	}
	if !hasWarning(f.bound.Status().Warnings, wantManualMaintenanceNotice) {
		t.Fatalf("Warnungen %v", f.bound.Status().Warnings)
	}
}

func TestApplyEndsManualMaintenance(t *testing.T) {
	f := newFixture(t)
	f.hw = "aaaa"
	cfg := f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.Terminal.HardwareID = "aaaa"
		c.Terminal.MaintenanceManual = true
	})
	f.start()
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantManualMaintenanceNotice })
	cfg.Terminal.MaintenanceManual = false
	f.s.Apply(cfg)
	if f.s.maintenance() || f.s.coord.Standby() {
		t.Fatal("Wartungsmodus dauert an")
	}
	eventually(t, "Hinweis weg", func() bool { return f.bound.Snapshot().Notice == "" })
}

func TestApplyStartsManualMaintenanceInRunningService(t *testing.T) {
	f := newFixture(t)
	f.hw = "aaaa"
	cfg := f.saveConfig(func(c *config.Config) { oneHMI(c); c.Terminal.HardwareID = "aaaa" })
	f.start()
	cfg.Terminal.MaintenanceManual = true
	f.s.Apply(cfg)
	if !f.s.maintenance() || !f.s.coord.Standby() {
		t.Fatal("Wartungsmodus nicht gestartet")
	}
	eventually(t, "Hinweis", func() bool { return f.bound.Snapshot().Notice == wantManualMaintenanceNotice })
}
