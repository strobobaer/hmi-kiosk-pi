package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

// hashFreeFallback legt einen guten Stand ohne Passwort an und ruft dann damage.
func hashFreeFallback(t *testing.T, damage func(mainPath string)) *adminEnv {
	t.Helper()
	e := newEnvNoPassword(t)
	if err := e.store.Save(config.Default()); err != nil {
		t.Fatal(err)
	}
	if err := e.store.MarkGood(); err != nil {
		t.Fatal(err)
	}
	damage(filepath.Join(e.dir, "config.json"))
	return e
}

func snapshotDir(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, en := range entries {
		b, err := os.ReadFile(filepath.Join(dir, en.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[en.Name()] = string(b)
	}
	return out
}

func TestHashFreeFallbackNeverOpensSetup(t *testing.T) {
	for name, damage := range map[string]func(string){
		"defekt": func(p string) {
			if err := os.WriteFile(p, []byte("{kaputt"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"fehlt": func(p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := hashFreeFallback(t, damage)
			before := snapshotDir(t, e.dir)
			for _, rt := range e.a.routes() {
				if rt.kind == routeAsset {
					continue
				}
				for _, m := range []string{http.MethodGet, http.MethodPost} {
					if rt.method != "" && rt.method != m {
						continue
					}
					b := e.browser()
					// Passender Vor-Cookie und Wert: Am CSRF darf es nicht scheitern.
					b.jar[preSessionCookie] = "wert"
					req := formRequest(concrete(rt.path), url.Values{"csrf": {"wert"}, "password": {"1234567890"}, "password2": {"1234567890"}})
					req.Method = m
					r := b.do(req)
					if r.Code != http.StatusInternalServerError {
						t.Errorf("%s %s: Status %d nach %q", m, rt.path, r.Code, r.Header().Get("Location"))
					}
					wantNotContains(t, r.Body.String(), e.dir, "config.json", "password")
				}
			}
			after := snapshotDir(t, e.dir)
			if len(after) != len(before) {
				t.Errorf("Dateien geändert: %d statt %d", len(after), len(before))
			}
			for k, v := range before {
				if after[k] != v {
					t.Errorf("%s geändert", k)
				}
			}
			if e.appliedCount() != 0 {
				t.Error("Apply gerufen")
			}
			if r := e.browser().get("/admin/admin.css"); r.Code != http.StatusOK {
				t.Errorf("Stil: %d", r.Code)
			}
		})
	}
}

// Ein Rückfall mit Passwort bleibt bedienbar, damit man die Lage beheben kann.
func TestFallbackWithPasswordStillAllowsLogin(t *testing.T) {
	e := newEnv(t)
	if err := e.store.MarkGood(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	if r := e.browser().get("/admin/setup"); r.Code != http.StatusNotFound {
		t.Errorf("Einrichtung: %d", r.Code)
	}
	b := e.login()
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Errorf("Status %d", r.Code)
	}
}

func TestConfigReadFailureMessagesAreGeneric(t *testing.T) {
	e := newEnv(t)
	if err := os.WriteFile(filepath.Join(e.dir, "config.json"), []byte("{kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.browser().get("/admin/login")
	if r.Code != http.StatusInternalServerError {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Konfiguration nicht lesbar")
	wantNotContains(t, r.Body.String(), e.dir, "config.json")

	// Die Prüfung im Handler sieht denselben Fehler, falls die Datei erst nach
	// dem Durchlauf von wrap defekt wird.
	for name, err := range map[string]error{
		"Anmeldung": e.a.verifyHeld("egal"),
		"Speichern": e.a.update(func(*config.Config) error { return nil }),
	} {
		if !errors.Is(err, errConfigUnreadable) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		wantNotContains(t, loginMessage(err), e.dir, "config.json")
		wantNotContains(t, publicMessage(err), e.dir, "config.json")
	}
	if authStatus(errConfigUnreadable) != http.StatusInternalServerError || formStatus(errConfigUnreadable) != http.StatusInternalServerError {
		t.Error("Status")
	}
}

func TestPublicMessageHidesSaveDetails(t *testing.T) {
	err := saveFailed{errors.New("open /data/config.json: read-only file system")}
	msg := publicMessage(err)
	wantContains(t, msg, "bisherigen Einstellungen")
	wantNotContains(t, msg, "/data", "read-only")
	if got := publicMessage(errors.New("Eingabe falsch")); got != "Eingabe falsch" {
		t.Errorf("%q", got)
	}
}

func TestNotFoundIsGerman(t *testing.T) {
	e, b := newLoggedIn(t)
	for name, r := range map[string]*httptest.ResponseRecorder{
		"Einrichtung": e.browser().get("/admin/setup"),
		"HMI":         b.get("/admin/hmis/nix"),
		"Auffang":     b.get("/admin/unbekannt"),
	} {
		body := r.Body.String()
		if r.Code != http.StatusNotFound || !strings.Contains(body, "Seite nicht gefunden") || strings.Contains(body, "page not found") {
			t.Errorf("%s: %d %q", name, r.Code, body)
		}
	}
}

func TestImportRejectsReservedHMIID(t *testing.T) {
	dst, db := newLoggedIn(t)
	base := config.Default()
	base.Client = config.ClientCfg{Profile: "remmina", Quality: 2, ColorDepth: 32}
	base.HMIs = []config.HMI{{ID: "new", Name: "A", Host: "10.0.0.1", Port: 5900}}
	body := importBlob(t, db, sealedExport(t, exportFile{Format: exportFormat, Config: base}), exportPW)
	wantContains(t, body, "Kennung ungültig")
	if len(dst.cfg().HMIs) != 0 || dst.appliedCount() != 0 {
		t.Error("übernommen")
	}
}

func TestSelftestRunsOnlyOnceAtATime(t *testing.T) {
	e, first := newLoggedIn(t)
	second := e.login()
	started, release := make(chan struct{}), make(chan struct{})
	e.selftest = func(context.Context) []SelftestStep {
		close(started)
		<-release
		return []SelftestStep{{Name: "Fertig", OK: true}}
	}
	tok, tok2 := first.tokenFrom("/admin/system"), second.tokenFrom("/admin/system")
	done := make(chan string)
	go func() {
		done <- first.postRaw("/admin/system/selftest", url.Values{"csrf": {tok}}).Body.String()
	}()
	<-started
	r := second.postRaw("/admin/system/selftest", url.Values{"csrf": {tok2}})
	if r.Code != http.StatusConflict {
		t.Errorf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Selbstprüfung läuft bereits")
	close(release)
	wantContains(t, <-done, "Fertig")

	e.selftest = nil
	if r := second.postRaw("/admin/system/selftest", url.Values{"csrf": {tok2}}); r.Code != http.StatusOK {
		t.Errorf("danach gesperrt: %d", r.Code)
	}
}
