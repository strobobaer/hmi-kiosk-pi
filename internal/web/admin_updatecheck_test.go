package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/updatecheck"
)

// releaseServer spielt GitHub: Release-Liste und Paketdatei über HTTPS.
type releaseServer struct {
	srv     *httptest.Server
	pkg     []byte
	entered chan struct{} // Download hat begonnen
	release chan struct{} // nil: Download sofort
}

func newReleaseServer(t *testing.T) *releaseServer {
	t.Helper()
	rs := &releaseServer{pkg: []byte("paket-0.10.0"), entered: make(chan struct{}, 4)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/releases", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `[{"tag_name":"v0.10.0","html_url":"https://github.com/o/r/releases/tag/v0.10.0","body":"Neu: <b>fett</b>",
			"assets":[{"name":"hmi-kiosk-0.10.0.pkg","size":%d,"browser_download_url":"%s/dl/hmi-kiosk-0.10.0.pkg"}]},
			{"tag_name":"v0.9.2","assets":[]}]`, len(rs.pkg), rs.srv.URL)
	})
	mux.HandleFunc("GET /dl/hmi-kiosk-0.10.0.pkg", func(w http.ResponseWriter, _ *http.Request) {
		rs.entered <- struct{}{}
		if rs.release != nil {
			<-rs.release
		}
		_, _ = w.Write(rs.pkg)
	})
	rs.srv = httptest.NewTLSServer(mux)
	t.Cleanup(rs.srv.Close)
	return rs
}

// withUpdateCheck verdrahtet die echte Prüfung gegen den Testserver; die Quelle kommt
// wie im Dienst aus der gespeicherten Konfiguration.
func withUpdateCheck(t *testing.T, e *adminEnv, rs *releaseServer) {
	t.Helper()
	e.mutate(func(c *config.Config) { c.Update = config.UpdateCfg{Repo: "o/r"} })
	e.a.d.UpdateCheck = &updatecheck.Service{
		Checker:  &updatecheck.Checker{BaseURL: rs.srv.URL, Client: rs.srv.Client()},
		Settings: func() config.UpdateCfg { return e.cfg().Update },
		Current:  "0.9.1",
		Now:      func() time.Time { return time.Date(2026, 11, 9, 7, 30, 0, 0, time.UTC) },
		Logf:     func(string, ...any) {},
	}
}

func TestUpdateSectionCheckAndOffer(t *testing.T) {
	e, b := newLoggedIn(t)
	rs := newReleaseServer(t)
	withUpdateCheck(t, e, rs)

	body := b.get("/admin/system").Body.String()
	wantContains(t, body, "Updates", "o/r", "noch keine", `action="/admin/system/update/check"`)
	wantNotContains(t, body, `action="/admin/system/update/install"`)

	if r := b.postRaw("/admin/system/update/check", nil); r.Code != http.StatusForbidden {
		t.Errorf("ohne CSRF: %d", r.Code)
	}
	body = b.follow(b.postVia("/admin/system", "/admin/system/update/check", nil))
	wantContains(t, body, "Version 0.10.0 verfügbar", "Neu: &lt;b&gt;fett&lt;/b&gt;", "https://github.com/o/r/releases/tag/v0.10.0",
		`action="/admin/system/update/install"`, `name="version" value="0.10.0"`, "Herunterladen und installieren", "data-confirm=")
	wantNotContains(t, body, "<b>fett</b>")
}

func TestUpdateInstallByClickUsesInstallPath(t *testing.T) {
	e, b := newLoggedIn(t)
	rs := newReleaseServer(t)
	withUpdateCheck(t, e, rs)

	// Ohne vorherige Prüfung gibt es nichts zu laden.
	r := b.postVia("/admin/system", "/admin/system/update/install", url.Values{"version": {"0.10.0"}})
	if r.Code != http.StatusUnprocessableEntity || len(e.update.blobs) != 0 {
		t.Fatalf("ohne Prüfung: %d %d", r.Code, len(e.update.blobs))
	}
	b.postVia("/admin/system", "/admin/system/update/check", nil)

	for _, v := range []string{"0.9.2", "1.0.0", "", "../x"} {
		r := b.postVia("/admin/system", "/admin/system/update/install", url.Values{"version": {v}})
		if r.Code != http.StatusUnprocessableEntity {
			t.Errorf("Version %q: %d", v, r.Code)
		}
	}
	if len(e.update.blobs) != 0 {
		t.Fatalf("falsche Version installiert: %q", e.update.blobs)
	}

	body := b.follow(b.postVia("/admin/system", "/admin/system/update/install", url.Values{"version": {"0.10.0"}}))
	wantContains(t, body, "Update installiert: Version 1.2.3")
	if len(e.update.blobs) != 1 || string(e.update.blobs[0]) != "paket-0.10.0" {
		t.Errorf("Blobs: %q", e.update.blobs)
	}

	e.update.err = errors.New("Version 0.10.0 braucht neuere Systemteile (Stand 2, dieses Gerät hat Stand 1).")
	r = b.postVia("/admin/system", "/admin/system/update/install", url.Values{"version": {"0.10.0"}})
	if r.Code != http.StatusUnprocessableEntity {
		t.Errorf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Update fehlgeschlagen: Version 0.10.0 braucht neuere Systemteile")
}

func TestUpdateInstallAndUploadShareLock(t *testing.T) {
	e, b := newLoggedIn(t)
	rs := newReleaseServer(t)
	rs.release = make(chan struct{})
	withUpdateCheck(t, e, rs)
	b.postVia("/admin/system", "/admin/system/update/check", nil)
	tok := b.tokenFrom("/admin/system")
	other := e.login()
	otherTok := other.tokenFrom("/admin/system")

	done := make(chan int)
	go func() {
		done <- b.postRaw("/admin/system/update/install", url.Values{"csrf": {tok}, "version": {"0.10.0"}}).Code
	}()
	<-rs.entered
	r := other.upload("/admin/system/update", [][2]string{{"csrf", otherTok}}, []byte("paket"))
	if r.Code != http.StatusConflict {
		t.Errorf("Upload während des Downloads: %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Es läuft bereits ein Update.")
	r = other.postRaw("/admin/system/update/install", url.Values{"csrf": {otherTok}, "version": {"0.10.0"}})
	if r.Code != http.StatusConflict {
		t.Errorf("zweiter Klick: %d", r.Code)
	}
	close(rs.release)
	if code := <-done; code != http.StatusSeeOther {
		t.Errorf("Installation: %d", code)
	}
	if len(e.update.blobs) != 1 {
		t.Errorf("%d Installationen", len(e.update.blobs))
	}
}

func TestUpdateSettingsSavedAndValidated(t *testing.T) {
	e, b := newLoggedIn(t)
	withUpdateCheck(t, e, newReleaseServer(t))

	r := b.postVia("/admin/system", "/admin/system/update/settings", url.Values{"repo": {"a/b/c"}, "prerelease": {"ja"}})
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ungültig: %d", r.Code)
	}
	wantContains(t, r.Body.String(), `value="a/b/c"`)
	if e.cfg().Update.Repo != "o/r" {
		t.Error("ungültige Quelle gespeichert")
	}
	b.follow(b.postVia("/admin/system", "/admin/system/update/settings", url.Values{"repo": {" neu/repo "}, "prerelease": {"ja"}}))
	if u := e.cfg().Update; u.Repo != "neu/repo" || !u.Prerelease {
		t.Errorf("%+v", u)
	}
	body := b.follow(b.postVia("/admin/system", "/admin/system/update/settings", url.Values{"repo": {""}}))
	wantContains(t, body, "aus")
	if u := e.cfg().Update; u.Repo != "" || u.Prerelease {
		t.Errorf("%+v", u)
	}
}

func TestUpdateSectionWithoutCheckerIsHidden(t *testing.T) {
	_, b := newLoggedIn(t)
	wantNotContains(t, b.get("/admin/system").Body.String(), `action="/admin/system/update/check"`)
	for _, p := range []string{"/admin/system/update/check", "/admin/system/update/install", "/admin/system/update/settings"} {
		if r := b.postVia("/admin/system", p, nil); r.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
}

func TestUpdateCheckErrorShown(t *testing.T) {
	e, b := newLoggedIn(t)
	rs := newReleaseServer(t)
	withUpdateCheck(t, e, rs)
	rs.srv.Close()
	r := b.postVia("/admin/system", "/admin/system/update/check", nil)
	if r.Code == http.StatusSeeOther {
		r.Body.WriteString(b.follow(r))
	}
	wantContains(t, r.Body.String(), "GitHub nicht erreichbar")
	if !strings.Contains(b.get("/admin/system").Body.String(), "GitHub nicht erreichbar") {
		t.Error("Fehler der letzten Prüfung fehlt auf der Seite")
	}
}

// Die Quelle reist mit der Exportdatei; ältere Dateien ohne Feld bekommen die Standardquelle.
func TestImportTakesUpdateSource(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.mutate(func(c *config.Config) { c.Update = config.UpdateCfg{Repo: "firma/kiosk", Prerelease: true} })
	blob := exportBlob(t, sb)

	dst, db := newLoggedIn(t)
	dst.mutate(func(c *config.Config) { c.Update = config.UpdateCfg{} })
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if u := dst.cfg().Update; u.Repo != "firma/kiosk" || !u.Prerelease {
		t.Errorf("%+v", u)
	}
}
