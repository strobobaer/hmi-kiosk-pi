package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/secrets"
)

func TestPeerCheckErrorIsNotReportedAsWrongKey(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.Peers = []config.Peer{{Name: "Nord", Address: "10.0.0.2:8443"}, {Name: "Süd", Address: "10.0.0.3:8443"}}
	})
	e.checkPeer = func(_ context.Context, p config.Peer) coord.PeerState {
		if p.Name == "Nord" {
			return coord.PeerState{Reachable: true, Err: errors.New("Terminal antwortet mit Status 500")}
		}
		return coord.PeerState{Reachable: true} // echter Schlüsselfehler
	}
	body := b.get("/admin/system").Body.String()
	if !strings.Contains(body, "Nord: Prüfung fehlgeschlagen") || !strings.Contains(body, "Status 500") {
		t.Errorf("Fehler nicht als Prüfungsfehler gezeigt: %s", body)
	}
	if !strings.Contains(body, "Süd: Schlüssel falsch") || strings.Contains(body, "Nord: Schlüssel falsch") {
		t.Error("„Schlüssel falsch“ nur bei echtem Fehler")
	}
}

func TestRepairLinkOnSystemPageOnlyWhenRepairIsPossible(t *testing.T) {
	e, b := newLoggedIn(t) // keine verschlüsselten Werte
	e.a.d.Box = secrets.Unavailable()
	body := b.get("/admin/system").Body.String()
	if strings.Contains(body, `href="/admin/repair"`) {
		t.Error("Reparieren-Link ohne verschlüsselte Werte")
	}
	if !strings.Contains(body, "Schlüsseldatei nicht verfügbar") {
		t.Error("Zustand nicht angezeigt")
	}
}

func TestPeersAreCheckedOnlyForTheSystemPageGet(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) { c.Peers = []config.Peer{{Name: "Nord", Address: "10.0.0.2:8443"}} })
	var mu sync.Mutex
	calls := 0
	e.checkPeer = func(context.Context, config.Peer) coord.PeerState {
		mu.Lock()
		calls++
		mu.Unlock()
		return coord.PeerState{Reachable: true, KeyOK: true}
	}
	b.get("/admin/system")
	if calls != 1 {
		t.Fatalf("GET: %d Prüfungen", calls)
	}
	// Die Fehlerseite einer anderen System-Aktion fragt kein Terminal ab.
	csrf := b.tokenFrom("/admin/system") // dieser Aufruf ist wieder ein GET
	before := calls
	r := b.postRaw("/admin/system/maintenance", url.Values{"csrf": {csrf}})
	if r.Code != http.StatusUnprocessableEntity || calls != before {
		t.Fatalf("Status %d, %d Prüfungen", r.Code, calls)
	}
}

func manualMaintenanceEnv(t *testing.T) (*adminEnv, *browser) {
	t.Helper()
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) {
		c.Terminal.HardwareID = "10000000aaaa00001111"
		c.Terminal.MaintenanceManual = true
	})
	e.hw = "10000000aaaa00001111"
	return e, b
}

func TestManualMaintenancePageOffersEndAndFittingWording(t *testing.T) {
	_, b := manualMaintenanceEnv(t)
	body := b.get("/admin/maintenance").Body.String()
	for _, want := range []string{"Wartungsmodus beenden", `action="/admin/maintenance/cancel"`, "von Hand gestartet"} {
		if !strings.Contains(body, want) {
			t.Errorf("Wartungsseite ohne %q", want)
		}
	}
	if strings.Contains(body, "Das alte Gerät") {
		t.Error("Wortlaut des Gerätewechsels bei Hand-Start")
	}
	_, hw := maintenanceEnv(t)
	hwBody := hw.get("/admin/maintenance").Body.String()
	if strings.Contains(hwBody, "Wartungsmodus beenden") {
		t.Error("Beenden-Knopf trotz Gerätewechsel")
	}
}

func TestEndingManualMaintenanceKeepsMaintenanceWhenHardwareDiffers(t *testing.T) {
	e, b := manualMaintenanceEnv(t)
	e.hw = "10000000bbbb99998888"
	if strings.Contains(b.get("/admin/maintenance").Body.String(), "Wartungsmodus beenden") {
		t.Error("Beenden-Knopf trotz Gerätewechsel")
	}
	wantRedirect(t, b.postVia("/admin/maintenance", "/admin/maintenance/cancel", nil), "/admin/maintenance")
	if !e.cfg().Terminal.MaintenanceManual {
		t.Fatal("Kennzeichen trotz Gerätewechsel entfernt")
	}
}

func TestEndingManualMaintenance(t *testing.T) {
	e, b := manualMaintenanceEnv(t)
	before := e.appliedCount()
	if r := b.postRaw("/admin/maintenance/cancel", nil); r.Code != http.StatusForbidden {
		t.Fatalf("ohne CSRF: %d", r.Code)
	}
	wantRedirect(t, e.browser().postRaw("/admin/maintenance/cancel", nil), "/admin/login")
	wantRedirect(t, b.postVia("/admin/maintenance", "/admin/maintenance/cancel", nil), "/admin/system")
	if e.cfg().Terminal.MaintenanceManual || e.appliedCount() != before+1 {
		t.Fatal("Wartungsmodus nicht beendet oder nicht übernommen")
	}
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("Seiten nach dem Beenden: %d", r.Code)
	}
}
