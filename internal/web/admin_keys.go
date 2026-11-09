package web

import (
	"context"
	"net/http"
	"sync"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
)

const pathRepair = "/admin/repair"

// repairPaths bleiben im Reparaturmodus erreichbar; alles andere führt zur Reparaturseite.
var repairPaths = map[string]bool{
	pathRepair:          true,
	"/admin/logout":     true,
	pathLocalBack:       true,
	"/admin/system/log": true,
}

// repairForced: Die Schlüsseldatei fehlt oder ist unlesbar, obwohl verschlüsselte Werte
// gespeichert sind. Ohne Reparatur ist nichts davon nutzbar.
func (a *admin) repairForced(c config.Config) bool {
	return !a.d.Box.Available() && c.HasSealedValues()
}

// --- System-Seite: Abschnitt „Schlüssel“ ---

type peerKeyView struct {
	Name, Address, Label, Class, Detail string
}

type keysView struct {
	DeviceMissing bool // Schlüsseldatei nicht verfügbar
	Affected      []string
	Peers         []peerKeyView
	Fingerprint   string // leer: kein Update-Schlüssel eingebaut
	NoPeers       bool   // keine Terminals eingetragen
	PeersChecked  bool   // die Abfrage lief (nur beim Aufruf der Seite)
}

func (a *admin) keysView(cfg config.Config, withPeers bool) keysView {
	v := keysView{
		DeviceMissing: !a.d.Box.Available(),
		Affected:      scanKeys(cfg, a.d.Box).Names(),
		Fingerprint:   a.d.UpdateKeyFingerprint,
		NoPeers:       len(cfg.Peers) == 0,
		PeersChecked:  withPeers,
	}
	if withPeers {
		v.Peers = a.checkPeers(cfg.Peers)
	}
	return v
}

// checkPeers fragt alle eingetragenen Terminals gleichzeitig, je mit kurzem Zeitlimit.
func (a *admin) checkPeers(peers []config.Peer) []peerKeyView {
	out := make([]peerKeyView, len(peers))
	var wg sync.WaitGroup
	for i, p := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = a.checkPeer(p)
		}()
	}
	wg.Wait()
	return out
}

func (a *admin) checkPeer(p config.Peer) peerKeyView {
	v := peerKeyView{Name: p.Name, Address: p.Address}
	st := coord.PeerState{}
	if a.d.CheckPeer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), peerCheckLimit)
		defer cancel()
		st = a.d.CheckPeer(ctx, p)
	}
	switch {
	case !st.Reachable:
		v.Label, v.Class = "nicht erreichbar", "check"
	case st.KeyOK:
		v.Label, v.Class = "Schlüssel stimmt", "pass"
	case st.Err != nil: // erreichbar, aber die Prüfung selbst ging schief: kein Beweis für einen falschen Schlüssel
		v.Label, v.Class, v.Detail = "Prüfung fehlgeschlagen", "check", st.Err.Error()
	default:
		v.Label, v.Class = "Schlüssel falsch", "fail"
	}
	return v
}

// --- Reparaturseite ---

type lostHMI struct{ ID, Name string }

type repairData struct {
	Forced   bool
	Affected []string
	Done     bool
	// Nach der Reparatur: was neu einzugeben ist.
	LostHMIs   []lostHMI
	SharedLost bool
}

func (a *admin) renderRepair(w http.ResponseWriter, s sessionRef, status int, errMsg string, d repairData) {
	a.page(w, s, status, "repair.html", "repair", "Schlüssel reparieren", errMsg, d)
}

func lostPasswords(c config.Config) []lostHMI {
	var out []lostHMI
	for _, h := range c.HMIs {
		if h.PasswordLost && h.PasswordEnc == "" {
			out = append(out, lostHMI{h.ID, h.Name})
		}
	}
	return out
}

func (a *admin) handleRepairPage(w http.ResponseWriter, r *http.Request, s sessionRef) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	affected := scanKeys(cfg, a.d.Box).Names()
	forced := a.repairForced(cfg)
	if !forced && len(affected) == 0 {
		redirect(w, r, "/admin/system")
		return
	}
	a.renderRepair(w, s, http.StatusOK, "", repairData{Forced: forced, Affected: affected})
}

func (a *admin) handleRepairRun(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	cfg, info, err := a.d.Store.Load()
	if err != nil || setupClosedByFallback(cfg, info) {
		a.renderRepair(w, s, http.StatusInternalServerError, errConfigUnreadable.Error(), repairData{})
		return
	}
	pending := repairData{Forced: a.repairForced(cfg), Affected: scanKeys(cfg, a.d.Box).Names()}
	if r.PostForm.Get("confirm") != "ja" {
		a.renderRepair(w, s, http.StatusUnprocessableEntity, "Bitte bestätigen, dass ein neuer Geräteschlüssel erzeugt und die betroffenen Passwörter gelöscht werden sollen.", pending)
		return
	}
	if a.d.KeyFile == "" {
		a.renderRepair(w, s, http.StatusInternalServerError, "Der Ort der Schlüsseldatei ist nicht bekannt.", pending)
		return
	}
	out, res, err := repairKeys(a.d.KeyFile, a.d.Box, cfg, a.d.Store.Save)
	if err != nil {
		a.renderRepair(w, s, http.StatusInternalServerError, err.Error(), pending)
		return
	}
	a.d.Apply(out)
	done := repairData{Done: true, LostHMIs: lostPasswords(out)}
	for _, name := range res.Cleared {
		if name == sharedKeyLabel {
			done.SharedLost = true
		}
	}
	a.renderRepair(w, s, http.StatusOK, "", done)
}

// --- Wartungsmodus von Hand ---

func (a *admin) handleMaintenanceStart(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if r.PostForm.Get("confirm") != "ja" {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Bitte bestätigen, dass der Wartungsmodus gestartet werden soll.", systemExtras{})
		return
	}
	err := a.update(func(c *config.Config) error {
		c.Terminal.MaintenanceManual = true
		return nil
	})
	if err != nil {
		a.renderSystem(w, s, formStatus(err), err.Error(), systemExtras{})
		return
	}
	redirect(w, r, pathMaintenance)
}
