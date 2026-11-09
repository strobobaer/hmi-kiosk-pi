package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
)

var remoteKeyRe = regexp.MustCompile(`hmik_[A-Za-z0-9_-]{43}`)

func TestNetworkPageRemoteSectionOff(t *testing.T) {
	e, b := newLoggedIn(t)
	e.a.d.TLSFingerprint = func() string { return "ab:cd" }
	page := b.get("/admin/network").Body.String()
	wantContains(t, page, "Fernsteuerung (PDH)", "Status: aus", "ab:cd", `action="/admin/network/remote/generate"`)
	wantNotContains(t, page, `action="/admin/network/remote/off"`)
	if remoteKeyRe.MatchString(page) {
		t.Fatal("Schlüssel ohne Erzeugen angezeigt")
	}
}

func TestRemoteKeyGeneratedShownOnceStoredAsHash(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/network/remote/generate", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	key := remoteKeyRe.FindString(r.Body.String())
	if key == "" {
		t.Fatalf("kein Schlüssel angezeigt: %s", r.Body.String())
	}
	wantContains(t, r.Body.String(), "nur dieses eine Mal")
	if r.Header().Get("Cache-Control") != "no-store" {
		t.Error("Cache-Control")
	}
	cfg := e.cfg()
	if cfg.Admin.RemoteKeyHash != RemoteKeyHash(key) {
		t.Fatalf("Hash %q", cfg.Admin.RemoteKeyHash)
	}
	if e.lastApplied().Admin.RemoteKeyHash != cfg.Admin.RemoteKeyHash {
		t.Fatal("nicht übernommen")
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), key) {
		t.Fatal("Schlüssel im Klartext gespeichert")
	}
	page := b.get("/admin/network").Body.String()
	wantContains(t, page, "Status: an", `action="/admin/network/remote/off"`)
	if strings.Contains(page, key) || remoteKeyRe.MatchString(page) {
		t.Fatal("Schlüssel ein zweites Mal angezeigt")
	}

	// Neu erzeugen macht den alten ungültig.
	r = b.post("/admin/network/remote/generate", nil)
	key2 := remoteKeyRe.FindString(r.Body.String())
	if key2 == "" || key2 == key || e.cfg().Admin.RemoteKeyHash != RemoteKeyHash(key2) {
		t.Fatalf("neu erzeugt: %q", key2)
	}
}

func TestRemoteKeyOffDeletesHash(t *testing.T) {
	e, b := newLoggedIn(t)
	b.post("/admin/network/remote/generate", nil)
	r := b.post("/admin/network/remote/off", nil)
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if e.cfg().Admin.RemoteKeyHash != "" || e.lastApplied().Admin.RemoteKeyHash != "" {
		t.Fatal("nicht gelöscht")
	}
	wantContains(t, b.follow(r), "Fernsteuerung ist aus", "Status: aus")
}

func TestRemoteKeyFormsNeedCSRF(t *testing.T) {
	e, b := newLoggedIn(t)
	for _, p := range []string{"/admin/network/remote/generate", "/admin/network/remote/off"} {
		if r := b.postRaw(p, url.Values{"csrf": {"falsch"}}); r.Code != http.StatusForbidden {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
	if e.cfg().Admin.RemoteKeyHash != "" {
		t.Fatal("ohne CSRF erzeugt")
	}
	// Ohne Anmeldung nur die Weiterleitung.
	if r := e.browser().postRaw("/admin/network/remote/generate", nil); r.Code != http.StatusSeeOther {
		t.Fatalf("ohne Anmeldung: %d", r.Code)
	}
}

// Der Schlüssel gehört zu diesem Terminal: nicht exportieren, beim Import den lokalen behalten.
func TestRemoteKeyNotExportedNotImported(t *testing.T) {
	src, sb := newLoggedIn(t)
	sb.post("/admin/network/remote/generate", nil)
	srcHash := src.cfg().Admin.RemoteKeyHash
	blob := exportBlob(t, sb)
	plain, err := secrets.OpenExport(blob, exportPW)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), srcHash) || strings.Contains(string(plain), "remoteKeyHash") {
		t.Fatal("Schlüssel-Hash im Export")
	}

	dst, db := newLoggedIn(t)
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if dst.cfg().Admin.RemoteKeyHash != "" {
		t.Fatal("Import hat einen Schlüssel gesetzt")
	}
	db.post("/admin/network/remote/generate", nil)
	dstHash := dst.cfg().Admin.RemoteKeyHash
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if dst.cfg().Admin.RemoteKeyHash != dstHash {
		t.Fatal("Import hat den lokalen Schlüssel überschrieben")
	}
}

// Als neues Terminal eingerichtet: Der Schlüssel des alten gilt nicht weiter, der TLS-Schlüssel ist neu.
// Als Ersatz: beides bleibt, PDH muss nichts ändern.
func TestMaintenanceRemoteKeyAndTLSKey(t *testing.T) {
	for _, c := range []struct {
		name, path string
		form       url.Values
		keep       bool
	}{
		{"Ersatz", "/admin/maintenance/replace", url.Values{"confirm": {"ja"}}, true},
		{"neu", "/admin/maintenance/new", url.Values{"name": {"Presse Nord"}, "role": {"normal"}}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, b := maintenanceEnv(t)
			_, hash, _ := NewRemoteKey()
			e.mutate(func(cfg *config.Config) { cfg.Admin.RemoteKeyHash = hash })
			before, err := LoadOrCreateCert(e.tlsDir)
			if err != nil {
				t.Fatal(err)
			}
			oldCert := readCert(t, e)
			if r := b.postVia("/admin/maintenance", c.path, c.form); r.Code != http.StatusSeeOther {
				t.Fatalf("Status %d: %s", r.Code, r.Body.String())
			}
			if readCert(t, e) == oldCert {
				t.Fatal("Zertifikat nicht erneuert")
			}
			after, err := LoadOrCreateCert(e.tlsDir)
			if err != nil {
				t.Fatal(err)
			}
			sameTLS := PublicKeyFingerprint(before) == PublicKeyFingerprint(after)
			sameKey := e.cfg().Admin.RemoteKeyHash == hash
			if sameTLS != c.keep || sameKey != c.keep {
				t.Fatalf("TLS-Schlüssel gleich %v, PDH-Schlüssel gleich %v, erwartet %v", sameTLS, sameKey, c.keep)
			}
		})
	}
}
