package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/session"
	"hmikiosk/internal/web"
)

const adminProbeLimit = 10 * time.Second

func cloneConfig(c config.Config) config.Config {
	c.Peers = slices.Clone(c.Peers)
	c.HMIs = slices.Clone(c.HMIs)
	return c
}

// Apply übernimmt eine gespeicherte Konfiguration. Der Sitzungsautomat liest sie
// über config, also beim nächsten Takt.
func (s *service) Apply(c config.Config) {
	c = cloneConfig(c)
	key, keyWarn := s.openKey(c)
	s.mu.Lock()
	s.cfg, s.key, s.keyWarn = c, key, keyWarn
	s.cfgWarn = "" // Der Aufrufer hat gespeichert, config.json ist also wieder gültig.
	s.mu.Unlock()
	s.coord.UpdateIdentity(selfOf(c), peersFor(c, key))
	s.origins.Set(config.WebOrigins(c))
	s.syncTileListener(c)
	s.syncMaintenance()
	s.reach.wake()
	select {
	case s.browser <- struct{}{}:
	default:
	}
}

func (s *service) config() config.Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cfg
}

// checkPeer prüft den Schlüssel eines anderen Terminals ohne Seiteneffekt.
func (s *service) checkPeer(ctx context.Context, p config.Peer) coord.PeerState {
	st := coord.CheckPeer(ctx, s.peerKey, nil, p)
	if st.Reachable && len(s.peerKey()) == 0 {
		st.KeyOK, st.Err = false, errors.New("auf diesem Terminal ist kein gemeinsamer Schlüssel gesetzt")
	}
	return st
}

func (s *service) peerKey() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key
}

// openKey liefert den gemeinsamen Schlüssel. Fehlt er, wird nicht mit leerem
// Schlüssel gesendet: Jede Anfrage liefe in eine Ablehnung, und die Kacheln zeigten
// einen falschen Schlüssel, wo gar keiner eingerichtet ist.
func (s *service) openKey(c config.Config) (key []byte, warning string) {
	if c.SharedKeyEnc == "" {
		if len(c.Peers) > 0 {
			warning = "Kein gemeinsamer Schlüssel gesetzt: Die Abstimmung mit anderen Terminals ist aus."
		}
		return nil, warning
	}
	if !s.box.Available() {
		return nil, "Ein gemeinsamer Schlüssel ist gesetzt, aber nicht lesbar: Die Abstimmung mit anderen Terminals ist aus."
	}
	plain, err := s.box.Open(c.SharedKeyEnc)
	if err != nil || plain == "" {
		log.Printf("Gemeinsamer Schlüssel nicht lesbar: %v", err)
		return nil, "Ein gemeinsamer Schlüssel ist gesetzt, aber nicht lesbar: Die Abstimmung mit anderen Terminals ist aus."
	}
	return []byte(plain), ""
}

func selfOf(c config.Config) coord.Holder {
	return coord.Holder{Terminal: c.Terminal.ID, Name: c.Terminal.Name, Role: c.Terminal.Role}
}

func peersFor(c config.Config, key []byte) []config.Peer {
	if len(key) == 0 {
		return nil
	}
	return c.Peers
}

func (s *service) rulesFor(hmi string) coord.Rules {
	c := s.config()
	r := coord.Rules{Mode: c.DefaultMode}
	for _, h := range c.HMIs {
		if h.ID != hmi {
			continue
		}
		r.Style, r.AllowWatch = h.Takeover, h.AllowWatch
		if h.Mode != nil {
			r.Mode = *h.Mode
		}
		break
	}
	return r
}

func (s *service) isPermanent(hmi string) bool { return s.config().Kiosk.PermanentHMI == hmi }

func (s *service) profile() client.Profile {
	name := s.config().Client.Profile
	p, err := client.ByName(name)
	if err != nil {
		log.Printf("Client-Profil %q unbekannt, nutze remmina", name)
		p, _ = client.ByName("remmina")
	}
	return p
}

// ramDir legt je Sitzung einen Ordner im RAM an; das Profil darin enthält das Passwort.
func (s *service) ramDir() (string, func(), error) {
	base := filepath.Join(s.opts.RunDir, sessionsDir)
	if err := os.MkdirAll(base, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(base, "s-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() {
		if err := os.RemoveAll(dir); err != nil {
			log.Printf("Sitzungsordner nicht entfernt: %v", err)
		}
	}, nil
}

func (s *service) screen() (int, int) {
	c := s.config()
	w, h, found := platform.DisplaySize(sysRoot)
	return platform.Resolve(c.Display.OverrideW, c.Display.OverrideH, w, h, found)
}

// password liest das VNC-Passwort. Kein Geheimtext heißt: kein Passwort gesetzt, außer
// die Reparatur hat es gelöscht. Ein verlorenes oder unlesbares Passwort meldet
// session.ErrPasswordMissing.
func (s *service) password(h config.HMI) (string, error) {
	if h.PasswordEnc == "" {
		if h.PasswordLost {
			return "", session.ErrPasswordMissing
		}
		return "", nil
	}
	pw, err := s.box.Open(h.PasswordEnc)
	if err != nil {
		return "", fmt.Errorf("%w: %v", session.ErrPasswordMissing, err)
	}
	return pw, nil
}

// sessionProbe läuft zu Beginn jedes Versuchs außerhalb der Zustands-Goroutine.
// Hier wird der Name aufgelöst, den TCP danach alle 500 ms ohne DNS braucht.
func (s *service) sessionProbe(ctx context.Context, h config.HMI, password string) rfb.ProbeResult {
	if err := s.hosts.resolve(ctx, h.Host); err != nil {
		log.Printf("Sitzung: Name von HMI %q nicht auflösbar: %v", h.ID, err)
		return rfb.ProbeResult{Err: fmt.Sprintf("Der Name „%s“ lässt sich nicht auflösen", h.Host)}
	}
	return rfb.Probe(ctx, h.Host, h.Port, password)
}

func (s *service) adminProbe(ctx context.Context, h config.HMI) rfb.ProbeResult {
	if h.IsWeb() {
		// Eine Webseite hat keine VNC-Anmeldung: nur ob der Server auf dem Port der Adresse antwortet.
		host, port, ok := h.Endpoint()
		if !ok {
			return rfb.ProbeResult{Err: "Die Adresse ist ungültig"}
		}
		ctx, cancel := context.WithTimeout(ctx, adminProbeLimit)
		defer cancel()
		return rfb.ProbeResult{Reachable: tcpReachable(ctx, host, port)}
	}
	pw, err := s.password(h)
	if err != nil {
		if h.PasswordLost {
			return rfb.ProbeResult{Err: "Das Passwort fehlt, bitte neu eingeben"}
		}
		return rfb.ProbeResult{Err: "Das gespeicherte Passwort lässt sich nicht entschlüsseln"}
	}
	ctx, cancel := context.WithTimeout(ctx, adminProbeLimit)
	defer cancel()
	return rfb.Probe(ctx, h.Host, h.Port, pw)
}

func (s *service) hmiInSession() string {
	snap := s.mgr.Snapshot()
	if snap.State == session.StateTiles || snap.State == session.StateReconnecting {
		return ""
	}
	return snap.HMI
}

func (s *service) tiles() []web.TileView {
	hmis := slices.Clone(s.config().HMIs)
	slices.SortStableFunc(hmis, func(a, b config.HMI) int { return a.Order - b.Order })
	occupied := s.coord.Occupancy()
	tiles := make([]web.TileView, 0, len(hmis))
	for _, h := range hmis {
		tile := web.TileView{ID: h.ID, Name: h.Name, Icon: h.Icon, Color: h.Color, Reachable: s.reach.reachable(h.ID), AuthFailed: h.AuthFailed, Web: h.IsWeb()}
		// Web-Kacheln sind lokal und frei: Eine Belegung gibt es nur bei VNC.
		if holder, ok := occupied[h.ID]; ok && !h.IsWeb() {
			tile.BusyBy = holder.Name
		}
		tiles = append(tiles, tile)
	}
	return tiles
}

func (s *service) onSessionEnd(e session.Ended) {
	mode := "Bedienen"
	switch {
	case e.Remote && (e.ViewOnly || e.Watch):
		mode = "PDH, nur ansehen"
	case e.Remote:
		mode = "PDH, bedienen"
	case e.Permanent && e.Web:
		mode = "Daueranzeige Webseite"
	case e.Permanent:
		mode = "Daueranzeige"
	case e.Web:
		mode = "Webseite"
	case e.Watch:
		mode = "Mitschauen"
	case e.ViewOnly:
		mode = "Nur ansehen"
	}
	s.journal.Session(appcore.SessionRecord{
		Terminal: s.config().Terminal.Name,
		HMI:      e.HMIName,
		Mode:     mode,
		Duration: e.Duration,
		Reason:   e.Reason,
	})
}

// remoteIsWifi: Die Antwort geht über die Schnittstelle, auf der die Anfrage ankam.
func remoteIsWifi(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr)
	if !ok {
		return false
	}
	_, wifi := platform.InterfaceForIP(addr.IP)
	return wifi
}

// markAuthFailed vermerkt ein abgelehntes VNC-Passwort in der Konfiguration. Es
// läuft nie in der Zustands-Goroutine des Automaten: Apply ruft ihn selbst.
// Eine unlesbare oder durch den Rückfall ersetzte Konfiguration bleibt unberührt,
// ebenso ein HMI, dessen Passwort sich seit der Prüfung (passwordEnc) geändert hat.
func (s *service) markAuthFailed(id, passwordEnc string) {
	s.cfgMu.Lock()
	defer s.cfgMu.Unlock()
	cfg, info, err := s.store.Load()
	if err != nil || info.UsedFallback {
		log.Printf("Markierung „Passwort abgelehnt“ für HMI %q nicht gespeichert: Konfiguration nicht lesbar", id)
		return
	}
	i := slices.IndexFunc(cfg.HMIs, func(h config.HMI) bool { return h.ID == id })
	if i < 0 || cfg.HMIs[i].AuthFailed {
		return
	}
	if cfg.HMIs[i].PasswordEnc != passwordEnc {
		log.Printf("Markierung „Passwort abgelehnt“ für HMI %q verworfen: Passwort wurde inzwischen geändert", id)
		return
	}
	cfg.HMIs[i].AuthFailed = true
	if err := s.store.Save(cfg); err != nil {
		log.Printf("Markierung „Passwort abgelehnt“ für HMI %q nicht gespeichert: %v", id, err)
		return
	}
	s.Apply(cfg)
}
