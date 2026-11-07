package web

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/secrets"
)

// sealForeign versiegelt mit einem anderen Schlüssel als dem des Geräts.
func (e *adminEnv) sealForeign(s string) string {
	e.t.Helper()
	key, _ := secrets.GenerateKey()
	b, err := secrets.NewBox(key)
	if err != nil {
		e.t.Fatal(err)
	}
	enc, err := b.Seal(s)
	if err != nil {
		e.t.Fatal(err)
	}
	return enc
}

// loseDeviceKey stellt den Zustand nach dem Start ohne Schlüsseldatei her.
func (e *adminEnv) loseDeviceKey() {
	e.t.Helper()
	if err := os.Remove(e.dir + "/secrets.key"); err != nil {
		e.t.Fatal(err)
	}
	e.a.d.Box = secrets.Unavailable()
}

func (e *adminEnv) deviceBox() *secrets.Box { return e.a.d.Box }

// keyTrouble: HMI „Presse“ (lesbar), „Ofen“ (falscher Schlüssel), gemeinsamer Schlüssel falsch.
func keyTrouble(t *testing.T) (*adminEnv, *browser) {
	t.Helper()
	e, b := newLoggedIn(t)
	e.addHMI("p", "Presse", "pw-p")
	e.mutate(func(c *config.Config) {
		c.HMIs = append(c.HMIs, config.HMI{ID: "o", Name: "Ofen", Host: "10.0.0.6", Port: 5900, PasswordEnc: e.sealForeign("pw-o"), Icon: "press", Color: "blue"})
		c.SharedKeyEnc = e.sealForeign("gemeinsam")
	})
	return e, b
}

func TestMissingDeviceKeyRedirectsEveryPageToRepair(t *testing.T) {
	e, b := keyTrouble(t)
	e.loseDeviceKey()
	for _, path := range []string{"/admin/", "/admin/hmis", "/admin/hmis/p", "/admin/terminals", "/admin/network", "/admin/system", "/admin/system/backup", "/admin/maintenance", "/admin/gibtsnicht"} {
		wantRedirect(t, b.get(path), "/admin/repair")
	}
	csrf := b.tokenFrom("/admin/repair")
	for _, path := range []string{"/admin/terminals", "/admin/system/reboot", "/admin/hmis/new", "/admin/system/maintenance"} {
		wantRedirect(t, b.postRaw(path, url.Values{"csrf": {csrf}}), "/admin/repair")
	}
	wantRedirect(t, b.upload("/admin/system/import", [][2]string{{"csrf", csrf}}, []byte("x")), "/admin/repair")
}

func TestRepairModeKeepsRepairLogoutAssetsAndLogReachable(t *testing.T) {
	e, b := keyTrouble(t)
	e.loseDeviceKey()
	for _, path := range []string{"/admin/admin.css", "/admin/admin.js", "/admin/repair"} {
		if r := b.get(path); r.Code != http.StatusOK {
			t.Errorf("%s: %d", path, r.Code)
		}
	}
	if r := b.get("/admin/system/log"); r.Code == http.StatusSeeOther {
		t.Errorf("Protokoll umgeleitet nach %s", r.Header().Get("Location"))
	}
	wantRedirect(t, b.postVia("/admin/repair", "/admin/logout", nil), "/admin/login")
}

func TestLoginDoesNotNeedTheDeviceKey(t *testing.T) {
	e, _ := keyTrouble(t)
	e.loseDeviceKey()
	b := e.browser()
	r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}})
	wantRedirect(t, r, "/admin/hmis")
	wantRedirect(t, b.get("/admin/"), "/admin/repair")
}

func TestRepairNeedsLoginAndCSRF(t *testing.T) {
	e, b := keyTrouble(t)
	e.loseDeviceKey()
	anon := e.browser()
	wantRedirect(t, anon.get("/admin/repair"), "/admin/login")
	wantRedirect(t, anon.postRaw("/admin/repair", url.Values{"confirm": {"ja"}}), "/admin/login")
	if r := b.postRaw("/admin/repair", url.Values{"confirm": {"ja"}}); r.Code != http.StatusForbidden {
		t.Fatalf("ohne CSRF: %d", r.Code)
	}
	if _, err := os.Stat(e.dir + "/secrets.key"); err == nil {
		t.Fatal("Schlüssel trotz abgelehnter Anfrage erzeugt")
	}
}

func TestRepairPageListsAffectedEntries(t *testing.T) {
	e, b := keyTrouble(t)
	e.loseDeviceKey()
	body := b.get("/admin/repair").Body.String()
	for _, want := range []string{"Presse", "Ofen", "Terminal-Schlüssel", "Neuen Geräteschlüssel erzeugen und betroffene Passwörter löschen"} {
		if !strings.Contains(body, want) {
			t.Errorf("Seite ohne %q", want)
		}
	}
}

func TestRepairRequiresConfirmation(t *testing.T) {
	e, b := keyTrouble(t)
	e.loseDeviceKey()
	r := b.postPage("/admin/repair", nil)
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ohne Bestätigung: %d", r.Code)
	}
	if e.deviceBox().Available() {
		t.Fatal("Schlüssel trotzdem erzeugt")
	}
}

func TestRepairCreatesKeyClearsUnreadableAndShowsNextSteps(t *testing.T) {
	e, b := keyTrouble(t)
	before := e.appliedCount()
	e.loseDeviceKey()
	r := b.postPage("/admin/repair", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	body := r.Body.String()
	for _, want := range []string{`href="/admin/hmis/p"`, `href="/admin/hmis/o"`, `href="/admin/terminals"`, "neu eingeben"} {
		if !strings.Contains(body, want) {
			t.Errorf("Ergebnis ohne %q", want)
		}
	}
	if !e.deviceBox().Available() {
		t.Fatal("Box ohne neuen Schlüssel")
	}
	if e.appliedCount() != before+1 {
		t.Fatal("Apply nicht gerufen")
	}
	c := e.cfg()
	for _, h := range c.HMIs {
		if h.PasswordEnc != "" || !h.PasswordLost {
			t.Errorf("%s: %+v", h.Name, h)
		}
	}
	if c.SharedKeyEnc != "" {
		t.Error("Terminal-Schlüssel nicht geleert")
	}
	// Danach ist der Zwang weg.
	list := b.get("/admin/hmis")
	if list.Code != http.StatusOK {
		t.Fatalf("Liste nach Reparatur: %d", list.Code)
	}
	if !strings.Contains(list.Body.String(), "Passwort fehlt") {
		t.Error("Liste ohne Hinweis „Passwort fehlt“")
	}
	// Die Reparatur ist wiederholbar und lässt dann nichts mehr zu tun.
	wantRedirect(t, b.get("/admin/repair"), "/admin/system")
}

func TestRepairKeepsReadablePasswords(t *testing.T) {
	e, b := keyTrouble(t) // Schlüssel vorhanden, nur ein Teil unlesbar
	r := b.postPage("/admin/repair", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	p := e.hmi("p")
	if pw, err := e.deviceBox().Open(p.PasswordEnc); err != nil || pw != "pw-p" || p.PasswordLost {
		t.Fatalf("Presse: %q %v %+v", pw, err, p)
	}
	if o := e.hmi("o"); o.PasswordEnc != "" || !o.PasswordLost {
		t.Fatalf("Ofen: %+v", o)
	}
}

func TestRepairSaveFailureKeepsOldKeyAndConfig(t *testing.T) {
	e, b := keyTrouble(t)
	breakSave(e)
	old, _ := os.ReadFile(e.dir + "/secrets.key")
	r := b.postPage("/admin/repair", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("Status %d", r.Code)
	}
	if now, _ := os.ReadFile(e.dir + "/secrets.key"); string(now) != string(old) {
		t.Fatal("Schlüssel trotz Fehler geändert")
	}
	if pw, err := e.deviceBox().Open(e.hmi("p").PasswordEnc); err != nil || pw != "pw-p" {
		t.Fatalf("Presse unlesbar: %v", err)
	}
}

func TestMismatchedKeyWarnsOnPagesWithRepairButton(t *testing.T) {
	_, b := keyTrouble(t)
	list := b.get("/admin/hmis")
	if list.Code != http.StatusOK {
		t.Fatalf("Liste: %d (kein Zwang bei lesbarer Schlüsseldatei)", list.Code)
	}
	for _, want := range []string{"Passwort unlesbar", `href="/admin/repair"`, "Reparieren"} {
		if !strings.Contains(list.Body.String(), want) {
			t.Errorf("Liste ohne %q", want)
		}
	}
	edit := b.get("/admin/hmis/o").Body.String()
	if !strings.Contains(edit, "nicht lesbar") || !strings.Contains(edit, `href="/admin/repair"`) {
		t.Errorf("Bearbeitungsseite ohne Hinweis: %s", edit)
	}
	if strings.Contains(b.get("/admin/hmis/p").Body.String(), `href="/admin/repair"`) {
		t.Error("Hinweis bei lesbarem Passwort")
	}
}

func TestSystemPageShowsDeviceKeyResult(t *testing.T) {
	_, b := keyTrouble(t)
	body := b.get("/admin/system").Body.String()
	for _, want := range []string{"Geräteschlüssel", "Ofen", "Terminal-Schlüssel", `href="/admin/repair"`} {
		if !strings.Contains(body, want) {
			t.Errorf("System-Seite ohne %q", want)
		}
	}
	e, b2 := newLoggedIn(t)
	e.addHMI("p", "Presse", "pw")
	if !strings.Contains(b2.get("/admin/system").Body.String(), "passt") {
		t.Error("System-Seite ohne „passt“")
	}
}

func TestSystemPageShowsPeerKeyStates(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.Peers = []config.Peer{{Name: "Nord", Address: "10.0.0.2:8443"}, {Name: "Süd", Address: "10.0.0.3:8443"}, {Name: "Ost", Address: "10.0.0.4:8443"}}
	})
	var started sync.WaitGroup
	started.Add(3)
	e.checkPeer = func(ctx context.Context, p config.Peer) coord.PeerState {
		started.Done()
		started.Wait() // alle drei müssen gleichzeitig laufen, sonst hängt der Test
		switch p.Name {
		case "Nord":
			return coord.PeerState{Reachable: true, KeyOK: true}
		case "Süd":
			return coord.PeerState{Reachable: true}
		}
		return coord.PeerState{Err: context.DeadlineExceeded}
	}
	done := make(chan string, 1)
	go func() { done <- b.get("/admin/system").Body.String() }()
	var body string
	select {
	case body = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Terminal-Prüfung läuft nicht parallel")
	}
	for _, want := range []string{"Nord", "Schlüssel stimmt", "Süd", "Schlüssel falsch", "Ost", "nicht erreichbar"} {
		if !strings.Contains(body, want) {
			t.Errorf("System-Seite ohne %q", want)
		}
	}
}

func TestPeerCheckHasShortTimeout(t *testing.T) {
	old := peerCheckLimit
	peerCheckLimit = 50 * time.Millisecond
	t.Cleanup(func() { peerCheckLimit = old })
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) { c.Peers = []config.Peer{{Name: "Nord", Address: "10.0.0.2:8443"}} })
	e.checkPeer = func(ctx context.Context, p config.Peer) coord.PeerState {
		<-ctx.Done()
		return coord.PeerState{Err: ctx.Err()}
	}
	start := time.Now()
	body := b.get("/admin/system").Body.String()
	if time.Since(start) > 3*time.Second || !strings.Contains(body, "nicht erreichbar") {
		t.Fatalf("Zeitlimit greift nicht (%v)", time.Since(start))
	}
}

func TestSystemPageWithoutPeers(t *testing.T) {
	_, b := newLoggedIn(t)
	if !strings.Contains(b.get("/admin/system").Body.String(), "keine Terminals eingetragen") {
		t.Fatal("kein Hinweis ohne Terminals")
	}
}

func TestSystemPageShowsUpdateKeyFingerprint(t *testing.T) {
	e, b := newLoggedIn(t)
	if body := b.get("/admin/system").Body.String(); !strings.Contains(body, "0123456789abcdef") {
		t.Fatal("Fingerabdruck fehlt")
	}
	e.a.d.UpdateKeyFingerprint = ""
	body := b.get("/admin/system").Body.String()
	if !strings.Contains(body, "kein Schlüssel eingebaut") || !strings.Contains(body, "gesperrt") {
		t.Fatal("Hinweis auf gesperrte Updates fehlt")
	}
}

func TestHMIListDistinguishesLostFromNoPassword(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("ohne", "Ohne", "")
	e.mutate(func(c *config.Config) {
		c.HMIs = append(c.HMIs, config.HMI{ID: "weg", Name: "Weg", Host: "10.0.0.7", Port: 5900, PasswordLost: true, Icon: "press", Color: "blue"})
	})
	body := b.get("/admin/hmis").Body.String()
	if strings.Count(body, "Passwort fehlt") != 1 || !strings.Contains(body, "Ohne Passwort") {
		t.Fatalf("Kennzeichnung: %s", body)
	}
}

func TestSettingOrRemovingPasswordClearsLostMarker(t *testing.T) {
	for name, form := range map[string]url.Values{
		"neu":      {"password": {"neu"}},
		"entfernt": {"remove_password": {"on"}},
	} {
		t.Run(name, func(t *testing.T) {
			e, b := newLoggedIn(t)
			e.mutate(func(c *config.Config) {
				c.HMIs = []config.HMI{{ID: "weg", Name: "Weg", Host: "10.0.0.7", Port: 5900, PasswordLost: true, Icon: "press", Color: "blue", Takeover: config.StyleConfirm, AllowWatch: true}}
			})
			form.Set("name", "Weg")
			form.Set("host", "10.0.0.7")
			form.Set("port", "5900")
			form.Set("icon", "press")
			form.Set("color", "blue")
			form.Set("order", "0")
			form.Set("takeover", string(config.StyleConfirm))
			form.Set("allow_watch", "on")
			r := b.postVia("/admin/hmis/weg", "/admin/hmis/weg", form)
			if r.Code != http.StatusSeeOther {
				t.Fatalf("Speichern: %d %s", r.Code, r.Body.String())
			}
			if e.hmi("weg").PasswordLost {
				t.Fatal("Marker bleibt")
			}
		})
	}
}

// --- Wartungsmodus von Hand ---

func TestSystemPageOffersMaintenanceStart(t *testing.T) {
	_, b := newLoggedIn(t)
	body := b.get("/admin/system").Body.String()
	if !strings.Contains(body, `action="/admin/system/maintenance"`) || !strings.Contains(body, "Wartungsmodus starten") {
		t.Fatal("Knopf fehlt")
	}
}

func TestMaintenanceStartNeedsConfirmCSRFAndLogin(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.postRaw("/admin/system/maintenance", url.Values{"confirm": {"ja"}}); r.Code != http.StatusForbidden {
		t.Fatalf("ohne CSRF: %d", r.Code)
	}
	wantRedirect(t, e.browser().postRaw("/admin/system/maintenance", url.Values{"confirm": {"ja"}}), "/admin/login")
	if r := b.postVia("/admin/system", "/admin/system/maintenance", nil); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ohne Bestätigung: %d", r.Code)
	}
	if e.cfg().Terminal.MaintenanceManual {
		t.Fatal("Wartungsmodus trotz abgelehnter Anfragen")
	}
}

func TestMaintenanceStartOpensTheMaintenanceFlow(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) { c.Terminal.HardwareID = "10000000aaaa00001111" })
	e.hw = "10000000aaaa00001111" // dasselbe Gerät
	before := e.appliedCount()
	r := b.postVia("/admin/system", "/admin/system/maintenance", url.Values{"confirm": {"ja"}})
	wantRedirect(t, r, "/admin/maintenance")
	if !e.cfg().Terminal.MaintenanceManual || e.appliedCount() != before+1 {
		t.Fatal("Wartungsmodus nicht gesetzt oder nicht übernommen")
	}
	wantRedirect(t, b.get("/admin/hmis"), "/admin/maintenance")
	if r := b.get("/admin/maintenance"); r.Code != http.StatusOK {
		t.Fatalf("Wartungsseite: %d", r.Code)
	}
}

func TestManualMaintenanceEndsWithReplaceAndTakesHardwareID(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.Terminal.HardwareID = "10000000aaaa00001111"
		c.Terminal.MaintenanceManual = true
	})
	e.hw = "10000000bbbb99998888"
	if _, err := LoadOrCreateCert(e.tlsDir); err != nil {
		t.Fatal(err)
	}
	r := b.postVia("/admin/maintenance", "/admin/maintenance/replace", url.Values{"confirm": {"ja"}})
	wantRedirect(t, r, "/admin/hmis")
	c := e.cfg()
	if c.Terminal.MaintenanceManual || c.Terminal.HardwareID != e.hw {
		t.Fatalf("Zustand: %+v", c.Terminal)
	}
}

func TestManualMaintenanceWithUnreadableHardwareIDKeepsStoredID(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.Terminal.HardwareID = "10000000aaaa00001111"
		c.Terminal.MaintenanceManual = true
	})
	e.hw = ""
	if _, err := LoadOrCreateCert(e.tlsDir); err != nil {
		t.Fatal(err)
	}
	wantRedirect(t, b.postVia("/admin/maintenance", "/admin/maintenance/replace", url.Values{"confirm": {"ja"}}), "/admin/hmis")
	c := e.cfg()
	if c.Terminal.MaintenanceManual || c.Terminal.HardwareID != "10000000aaaa00001111" {
		t.Fatalf("Zustand: %+v", c.Terminal)
	}
}

func TestManualMaintenanceAsNewTerminalGetsNewID(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.Terminal.HardwareID = "10000000aaaa00001111"
		c.Terminal.MaintenanceManual = true
	})
	e.hw = "10000000aaaa00001111"
	if _, err := LoadOrCreateCert(e.tlsDir); err != nil {
		t.Fatal(err)
	}
	oldID := e.cfg().Terminal.ID
	r := b.postVia("/admin/maintenance", "/admin/maintenance/new", url.Values{"name": {"Terminal 2"}, "role": {"normal"}})
	wantRedirect(t, r, "/admin/hmis")
	c := e.cfg()
	if c.Terminal.MaintenanceManual || c.Terminal.ID == oldID || c.Terminal.Name != "Terminal 2" {
		t.Fatalf("Zustand: %+v", c.Terminal)
	}
}
