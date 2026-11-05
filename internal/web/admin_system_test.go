package web

import (
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestWifiOffRefusedWhenRemoteViaWifi(t *testing.T) {
	e, b := newLoggedIn(t)
	e.wifi = true
	r := b.post("/admin/network/wifi-off", nil)
	if r.Code == http.StatusSeeOther {
		t.Fatal("nicht verweigert")
	}
	wantContains(t, r.Body.String(), "WLAN")
	if len(e.helper.got()) != 0 || e.cfg().Network.WifiOff {
		t.Error("Helfer gerufen oder gespeichert")
	}
}

func TestWifiOffRefusedWithoutEthernetLink(t *testing.T) {
	e, b := newLoggedIn(t)
	e.eth = false
	r := b.post("/admin/network/wifi-off", nil)
	if r.Code == http.StatusSeeOther {
		t.Fatal("nicht verweigert")
	}
	wantContains(t, r.Body.String(), "Ethernet")
	if len(e.helper.got()) != 0 || e.cfg().Network.WifiOff {
		t.Error("Helfer gerufen oder gespeichert")
	}
}

func TestWifiOffCallsHelperAndMarksRebootForOn(t *testing.T) {
	e, b := newLoggedIn(t)
	body := b.follow(b.post("/admin/network/wifi-off", nil))
	wantContains(t, body, "wirkt dauerhaft nach dem Neustart")
	if got := e.helper.got(); len(got) != 1 || got[0].cmd != "wifi-off" {
		t.Fatalf("Helfer: %+v", got)
	}
	if !e.cfg().Network.WifiOff || e.appliedCount() != 1 {
		t.Error("nicht gespeichert oder Apply fehlt")
	}
	body = b.follow(b.post("/admin/network/wifi-on", nil))
	wantContains(t, body, "Neustart nötig")
	if got := e.helper.got(); len(got) != 2 || got[1].cmd != "wifi-on" {
		t.Fatalf("Helfer: %+v", got)
	}
	if e.cfg().Network.WifiOff {
		t.Error("nicht zurückgesetzt")
	}
}

func TestWifiHelperFailureNotPersisted(t *testing.T) {
	e, b := newLoggedIn(t)
	e.helper.err = errors.New("kaputt")
	r := b.post("/admin/network/wifi-off", nil)
	if r.Code == http.StatusSeeOther || e.cfg().Network.WifiOff || e.appliedCount() != 0 {
		t.Errorf("Status %d, gespeichert trotz Helferfehler", r.Code)
	}
}

// Nach erfolgreichem Helfer gilt die Änderung bereits; die Meldung darf nicht
// behaupten, es bleibe alles beim Alten.
func TestWifiSaveFailureAfterHelperSaysSo(t *testing.T) {
	e, b := newLoggedIn(t)
	breakSave(e)
	r := b.post("/admin/network/wifi-off", nil)
	if r.Code == http.StatusSeeOther {
		t.Fatal("Erfolg gemeldet")
	}
	wantContains(t, r.Body.String(), "nicht gespeichert")
	wantNotContains(t, r.Body.String(), "bisherigen Einstellungen")
}

func TestBluetoothHasNoWifiGuard(t *testing.T) {
	e, b := newLoggedIn(t)
	e.wifi, e.eth = true, false
	wantContains(t, b.follow(b.post("/admin/network/bt-off", nil)), "Neustart")
	if !e.cfg().Network.BluetoothOff {
		t.Error("nicht gespeichert")
	}
	wantContains(t, b.follow(b.post("/admin/network/bt-on", nil)), "Neustart nötig")
	if e.cfg().Network.BluetoothOff {
		t.Error("nicht zurückgesetzt")
	}
	got := e.helper.got()
	if len(got) != 2 || got[0].cmd != "bt-off" || got[1].cmd != "bt-on" {
		t.Errorf("Helfer: %+v", got)
	}
}

func TestNTPValidatedBeforeHelper(t *testing.T) {
	e, b := newLoggedIn(t)
	for _, bad := range []string{"", "-rf", "a b", "ntp.local;reboot", "$(reboot)", "a/b", strings.Repeat("a", 254)} {
		if r := b.post("/admin/network/ntp", url.Values{"ntp": {bad}}); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d", bad, r.Code)
		}
	}
	if len(e.helper.got()) != 0 {
		t.Fatalf("Helfer: %+v", e.helper.got())
	}
	for _, good := range []string{"ntp.werk.local", "10.0.0.1", "fd00::1"} {
		if r := b.post("/admin/network/ntp", url.Values{"ntp": {good}}); r.Code != http.StatusSeeOther {
			t.Errorf("%q: %d", good, r.Code)
		}
		if got := e.helper.got(); got[len(got)-1] != (helperCall{"set-ntp", good}) {
			t.Errorf("Helfer: %+v", got)
		}
		if e.cfg().Network.NTPServer != good {
			t.Errorf("gespeichert: %q", e.cfg().Network.NTPServer)
		}
	}
}

func TestSetTimeValidatedAndSentAsRFC3339(t *testing.T) {
	e, b := newLoggedIn(t)
	for _, bad := range []string{"", "2026; rm -rf /", "2026-13-01T10:00", "gestern", "1999-01-01T10:00"} {
		if r := b.post("/admin/system/time", url.Values{"time": {bad}}); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%q: %d", bad, r.Code)
		}
	}
	if len(e.helper.got()) != 0 {
		t.Fatalf("Helfer: %+v", e.helper.got())
	}
	if r := b.post("/admin/system/time", url.Values{"time": {"2026-11-05T14:30"}}); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	got := e.helper.got()
	if len(got) != 1 || got[0].cmd != "set-time" {
		t.Fatalf("Helfer: %+v", got)
	}
	if ts, err := time.Parse(time.RFC3339, got[0].arg); err != nil || !ts.Equal(time.Date(2026, 11, 5, 14, 30, 0, 0, time.Local)) {
		t.Errorf("Wert %q %v", got[0].arg, err)
	}
}

func TestRebootAndRestartRequireConfirmation(t *testing.T) {
	e, b := newLoggedIn(t)
	for path, cmd := range map[string]string{"/admin/system/reboot": "reboot", "/admin/system/restart-service": "restart-service"} {
		before := len(e.helper.got())
		if r := b.post(path, nil); r.Code == http.StatusSeeOther {
			t.Errorf("%s ohne Bestätigung: %d", path, r.Code)
		}
		if len(e.helper.got()) != before {
			t.Errorf("%s: Helfer ohne Bestätigung", path)
		}
		r := b.post(path, url.Values{"confirm": {"ja"}})
		if r.Code != http.StatusSeeOther {
			t.Errorf("%s: %d", path, r.Code)
		}
		got := e.helper.got()
		if len(got) != before+1 || got[before].cmd != cmd {
			t.Errorf("%s: %+v", path, got)
		}
	}
}

func TestSystemPageShowsStatusAndSelftest(t *testing.T) {
	_, b := newLoggedIn(t)
	wantContains(t, b.get("/admin/system").Body.String(), "0.9.1", "Testwarnung", "1920x1080", "3,0 GB", "8,0 GB", "aktiv")
	body := b.post("/admin/system/selftest", nil).Body.String()
	wantContains(t, body, "Remmina", "gefunden", "Touch", "fehlt")
}

func TestDisplaySettings(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/system/display", url.Values{"override_w": {"1280"}, "override_h": {"800"}, "graphics": {"x11"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d %s", r.Code, r.Body.String())
	}
	if d := e.cfg().Display; d.OverrideW != 1280 || d.OverrideH != 800 || d.GraphicsProfile != "x11" {
		t.Errorf("%+v", d)
	}
	for _, f := range []url.Values{
		{"override_w": {"1280"}, "override_h": {""}, "graphics": {"x11"}},
		{"override_w": {"-5"}, "override_h": {"800"}, "graphics": {"x11"}},
		{"override_w": {""}, "override_h": {""}, "graphics": {"wayland"}},
	} {
		if r := b.post("/admin/system/display", f); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", f, r.Code)
		}
	}
	r = b.post("/admin/system/display", url.Values{"override_w": {""}, "override_h": {""}, "graphics": {"labwc"}})
	if d := e.cfg().Display; r.Code != http.StatusSeeOther || d.OverrideW != 0 || d.OverrideH != 0 {
		t.Errorf("%d %+v", r.Code, d)
	}
}

func TestLogViewShowsLast200LinesOfNewestFile(t *testing.T) {
	e, b := newLoggedIn(t)
	var sb strings.Builder
	for i := 0; i < 300; i++ {
		sb.WriteString("zeile-")
		sb.WriteString(strings.Repeat("0", 3-len(itoa(i))))
		sb.WriteString(itoa(i))
		sb.WriteString(" <tag>\n")
	}
	oldf := filepath.Join(e.logDir, "alt.log")
	newf := filepath.Join(e.logDir, "neu.log")
	os.WriteFile(oldf, []byte("uralt\n"), 0o600)
	os.WriteFile(newf, []byte(sb.String()), 0o600)
	past := time.Now().Add(-time.Hour)
	os.Chtimes(oldf, past, past)
	os.MkdirAll(filepath.Join(e.logDir, "verzeichnis"), 0o700)

	body := b.get("/admin/system").Body.String()
	wantContains(t, body, "zeile-100", "zeile-299", "&lt;tag&gt;")
	wantNotContains(t, body, "zeile-099", "uralt", "<tag>")
}

func TestLogDownloadLimitedTo5MB(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.get("/admin/system/log"); r.Code != http.StatusNotFound {
		t.Errorf("leer: %d", r.Code)
	}
	big := strings.Repeat("x", 6<<20) + "ENDE\n"
	if err := os.WriteFile(filepath.Join(e.logDir, "dienst.log"), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	r := b.get("/admin/system/log")
	if r.Code != http.StatusOK || r.Body.Len() != 5<<20 {
		t.Fatalf("%d, %d Bytes", r.Code, r.Body.Len())
	}
	if !strings.HasSuffix(r.Body.String(), "ENDE\n") {
		t.Error("nicht das Ende der Datei")
	}
	wantContains(t, r.Header().Get("Content-Disposition"), "attachment")
	wantContains(t, r.Header().Get("Content-Type"), "text/plain")
}

func TestLogIgnoresSymlinks(t *testing.T) {
	e, b := newLoggedIn(t)
	secret := filepath.Join(e.dir, "secrets.key")
	if err := os.Symlink(secret, filepath.Join(e.logDir, "zzz.log")); err != nil {
		t.Skip(err)
	}
	if r := b.get("/admin/system/log"); r.Code != http.StatusNotFound {
		t.Errorf("Status %d", r.Code)
	}
}

func TestUpdateInstalls(t *testing.T) {
	e, b := newLoggedIn(t)
	tok := b.tokenFrom("/admin/system")
	body := b.follow(b.upload("/admin/system/update", [][2]string{{"csrf", tok}}, []byte("paket")))
	wantContains(t, body, "1.2.3")
	if len(e.update.blobs) != 1 || string(e.update.blobs[0]) != "paket" {
		t.Errorf("Blobs: %q", e.update.blobs)
	}
	e.update.err = errors.New("Signatur ungültig")
	r := b.upload("/admin/system/update", [][2]string{{"csrf", tok}}, []byte("paket"))
	wantContains(t, r.Body.String(), "Signatur ungültig")
	if r := b.upload("/admin/system/update", [][2]string{{"csrf", tok}}, nil); r.Code == http.StatusSeeOther {
		t.Error("leere Datei angenommen")
	}
}

func TestRequestBodyLimitedOnUpdateAndImport(t *testing.T) {
	if updateBodyLimit != 200<<20 {
		t.Errorf("Update-Grenze %d", updateBodyLimit)
	}
	if importBodyLimit != 1<<20 {
		t.Errorf("Import-Grenze %d", importBodyLimit)
	}
	old := updateBodyLimit
	updateBodyLimit = 1 << 16
	defer func() { updateBodyLimit = old }()

	e, b := newLoggedIn(t)
	tok := b.tokenFrom("/admin/system")
	r := b.upload("/admin/system/update", [][2]string{{"csrf", tok}}, make([]byte, 1<<16+1))
	if r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Update: %d", r.Code)
	}
	if len(e.update.blobs) != 0 {
		t.Error("Install gerufen")
	}
	before := e.cfg()
	r = b.upload("/admin/system/import", [][2]string{{"csrf", tok}, {"password", "x"}}, make([]byte, 1<<20+1))
	if r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Import: %d", r.Code)
	}
	if after := e.cfg(); after.Terminal != before.Terminal || e.appliedCount() != 0 {
		t.Error("Konfiguration geändert")
	}
}

func TestLayoutHasNavigationAndLogout(t *testing.T) {
	_, b := newLoggedIn(t)
	body := b.get("/admin/hmis").Body.String()
	wantContains(t, body, "HMIs", "Client", "Kiosk", "Terminals", "Netzwerk", "System", `action="/admin/logout"`, `name="viewport"`, `/admin/admin.css`, `/admin/admin.js`)
}

func TestAdminAssetsHaveNoInlineScriptOrStyle(t *testing.T) {
	n := 0
	err := fs.WalkDir(assets, "assets/admin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		n++
		b, _ := assets.ReadFile(p)
		s := string(b)
		if regexp.MustCompile(`<script[^>]*>[^<]`).MatchString(s) || strings.Contains(s, " style=") ||
			strings.Contains(s, "<style") || regexp.MustCompile(` on[a-z]+=`).MatchString(s) {
			t.Errorf("%s enthält Inline-Skript oder -Stil", p)
		}
		return nil
	})
	if err != nil || n < 8 {
		t.Fatalf("%d Seiten, %v", n, err)
	}
}

// Jedes Formular mit POST trägt das CSRF-Feld, sonst wäre es im Browser tot.
func TestEveryPostFormCarriesCSRFField(t *testing.T) {
	err := fs.WalkDir(assets, "assets/admin", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, _ := assets.ReadFile(p)
		forms := regexp.MustCompile(`(?s)<form[^>]*>.*?</form>`).FindAllString(string(b), -1)
		for _, f := range forms {
			if !strings.Contains(f, `name="csrf"`) {
				t.Errorf("%s: Formular ohne CSRF-Feld: %.80s", p, f)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	s := ""
	for ; i > 0; i /= 10 {
		s = string(rune('0'+i%10)) + s
	}
	return s
}
