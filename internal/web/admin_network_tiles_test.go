package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

func TestKioskNetworkTilesToggle(t *testing.T) {
	e, b := newLoggedIn(t)
	page := b.get("/admin/kiosk").Body.String()
	wantContains(t, page, "Kachelseite im Netz freigeben (nur für Tests)")
	wantContains(t, page, "Jeder im Netz kann dann HMI-Sitzungen auf diesem Terminal starten.")
	wantContains(t, page, "Bildschirm des Terminals")
	if strings.Contains(page, `name="network_tiles" checked`) {
		t.Fatal("ab Werk angehakt")
	}

	r := b.post("/admin/kiosk", url.Values{"inactivity": {"15"}, "layout": {"auto"}, "permanent": {""}, "input_mode": {"touch"}, "network_tiles": {"on"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Speichern: %d %s", r.Code, r.Body.String())
	}
	if !e.cfg().Kiosk.NetworkTiles {
		t.Fatal("nicht gespeichert")
	}
	if !e.lastApplied().Kiosk.NetworkTiles {
		t.Fatal("nicht übernommen")
	}
	page = b.get("/admin/kiosk").Body.String()
	wantContains(t, page, "im Netz freigegeben")
	wantContains(t, page, `name="network_tiles" checked`)

	// Andere Felder speichern lässt die Freigabe nur fallen, wenn der Haken fehlt.
	r = b.post("/admin/kiosk", url.Values{"inactivity": {"15"}, "layout": {"auto"}, "permanent": {""}, "input_mode": {"touch"}})
	if r.Code != http.StatusSeeOther || e.cfg().Kiosk.NetworkTiles {
		t.Fatalf("Ausschalten: %d %+v", r.Code, e.cfg().Kiosk)
	}
}

func TestNetworkTilesBannerWithAddress(t *testing.T) {
	e, b := newLoggedIn(t)
	e.tileURLs = []string{"http://192.168.1.20:8080/"}
	wantNotContains(t, b.get("/admin/hmis").Body.String(), "192.168.1.20:8080")
	e.mutate(func(c *config.Config) { c.Kiosk.NetworkTiles = true })
	for _, p := range []string{"/admin/hmis", "/admin/system", "/admin/client"} {
		body := b.get(p).Body.String()
		wantContains(t, body, "http://192.168.1.20:8080/")
		wantContains(t, body, "ohne Anmeldung")
	}
	// Ohne bekannte Adresse steht ein Platzhalter da.
	e.tileURLs = nil
	wantContains(t, b.get("/admin/hmis").Body.String(), "http://&lt;IP des Terminals&gt;:8080/")
}

func TestImportKeepsLocalNetworkTiles(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.mutate(func(c *config.Config) { c.Kiosk.NetworkTiles = true; c.Kiosk.Layout = "4" })
	blob := exportBlob(t, sb)
	dst, db := newLoggedIn(t)
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if k := dst.cfg().Kiosk; k.NetworkTiles || k.Layout != "4" {
		t.Errorf("Import: %+v", k)
	}

	// Umgekehrt bleibt eine lokale Freigabe stehen.
	src.mutate(func(c *config.Config) { c.Kiosk.NetworkTiles = false })
	blob = exportBlob(t, sb)
	dst.mutate(func(c *config.Config) { c.Kiosk.NetworkTiles = true })
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if !dst.cfg().Kiosk.NetworkTiles {
		t.Error("lokale Freigabe überschrieben")
	}
}
