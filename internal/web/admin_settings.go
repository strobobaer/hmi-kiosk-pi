package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"hmikiosk/internal/config"
	"hmikiosk/internal/session"
)

const (
	helperWifiOff        = "wifi-off"
	helperWifiOn         = "wifi-on"
	helperBtOff          = "bt-off"
	helperBtOn           = "bt-on"
	helperReboot         = "reboot"
	helperRestartService = "restart-service"
	helperSetTime        = "set-time"
	helperSetNTP         = "set-ntp"

	helperLimit      = 45 * time.Second
	minSharedKeyLen  = 16
	maxInactivityMin = 24 * 60
)

// checkClient, checkKiosk und checkDisplay prüfen, was config.Validate nicht kennt.
// Sie gelten für die Formulare und für den Import.
func checkClient(c config.ClientCfg) error {
	if c.Profile != "remmina" && c.Profile != "tigervnc" {
		return fmt.Errorf("Unbekanntes Client-Profil: %q", c.Profile)
	}
	if c.Quality != 0 && c.Quality != 1 && c.Quality != 2 && c.Quality != 9 {
		return fmt.Errorf("Unbekannte Qualitätsstufe: %d", c.Quality)
	}
	if c.ColorDepth != 8 && c.ColorDepth != 16 && c.ColorDepth != 32 {
		return fmt.Errorf("Unbekannte Farbtiefe: %d", c.ColorDepth)
	}
	return nil
}

func checkKiosk(k config.KioskCfg) error {
	if k.InactivityMinutes < 0 || k.InactivityMinutes > maxInactivityMin {
		return fmt.Errorf("Rückkehr bei Inaktivität muss zwischen 0 und %d Minuten liegen.", maxInactivityMin)
	}
	if k.HideBar && k.PermanentHMI == "" {
		return errors.New(config.ErrHideBarWithoutPermanent)
	}
	if k.InputMode != config.InputTouch && k.InputMode != config.InputMouse {
		return fmt.Errorf("Unbekannte Bedienart: %q", k.InputMode)
	}
	switch k.Layout {
	case "", "auto", "2", "3", "4":
		return nil
	}
	return fmt.Errorf("Unbekanntes Kachel-Layout: %q", k.Layout)
}

func checkDisplay(d config.DisplayCfg) error {
	if d.GraphicsProfile != "labwc" && d.GraphicsProfile != "x11" {
		return fmt.Errorf("Unbekanntes Grafikprofil: %q", d.GraphicsProfile)
	}
	if d.OverrideW == 0 && d.OverrideH == 0 {
		return nil
	}
	if d.OverrideW < 320 || d.OverrideW > 7680 || d.OverrideH < 200 || d.OverrideH > 4320 {
		return errors.New("Die Auflösung muss aus Breite (320 bis 7680) und Höhe (200 bis 4320) bestehen oder leer sein.")
	}
	return nil
}

// --- Client ---

type clientFormData struct {
	Profile, Quality, ColorDepth string
	Scale                        bool
}

func (a *admin) renderClient(w http.ResponseWriter, s sessionRef, status int, errMsg string, f clientFormData) {
	a.page(w, s, status, "client.html", "client", "Client", errMsg, f)
}

func (a *admin) handleClientForm(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	a.renderClient(w, s, http.StatusOK, "", clientFormData{
		Profile: cfg.Client.Profile, Quality: strconv.Itoa(cfg.Client.Quality),
		ColorDepth: strconv.Itoa(cfg.Client.ColorDepth), Scale: cfg.Client.Scale,
	})
}

func (a *admin) handleClientSave(w http.ResponseWriter, r *http.Request, s sessionRef) {
	v := r.PostForm
	f := clientFormData{Profile: v.Get("profile"), Quality: v.Get("quality"), ColorDepth: v.Get("color_depth"), Scale: v.Get("scale") != ""}
	err := a.update(func(c *config.Config) error {
		q, err1 := strconv.Atoi(f.Quality)
		d, err2 := strconv.Atoi(f.ColorDepth)
		if err1 != nil || err2 != nil {
			return errors.New("Qualität und Farbtiefe müssen Zahlen sein.")
		}
		next := config.ClientCfg{Profile: f.Profile, Quality: q, ColorDepth: d, Scale: f.Scale}
		if err := checkClient(next); err != nil {
			return err
		}
		c.Client = next
		return nil
	})
	if err != nil {
		a.renderClient(w, s, formStatus(err), err.Error(), f)
		return
	}
	a.done(w, r, s, "/admin/client", "Gespeichert.")
}

// --- Kiosk ---

type kioskFormData struct {
	Inactivity, Layout, Permanent, InputMode string
	HideBar, NetworkTiles                    bool
	WebLogins                                bool // Knopf „Gespeicherte Web-Anmeldungen löschen“
	HMIs                                     []option
}

func (a *admin) renderKiosk(w http.ResponseWriter, s sessionRef, status int, errMsg string, f kioskFormData) {
	if cfg, _, err := a.d.Store.Load(); err == nil {
		for _, h := range cfg.HMIs {
			f.HMIs = append(f.HMIs, option{h.ID, h.Name})
		}
	}
	f.WebLogins = a.d.ClearWebLogins != nil
	a.page(w, s, status, "kiosk.html", "kiosk", "Kiosk", errMsg, f)
}

func (a *admin) handleKioskForm(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderKioskStored(w, s, http.StatusOK, "")
}

// renderKioskStored zeigt das Kiosk-Formular mit den gespeicherten Werten.
func (a *admin) renderKioskStored(w http.ResponseWriter, s sessionRef, status int, errMsg string) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	k := cfg.Kiosk
	mode := k.InputMode
	if mode == "" {
		mode = config.InputTouch
	}
	a.renderKiosk(w, s, status, errMsg, kioskFormData{
		Inactivity: strconv.Itoa(k.InactivityMinutes), Layout: k.Layout, Permanent: k.PermanentHMI, HideBar: k.HideBar, InputMode: mode, NetworkTiles: k.NetworkTiles,
	})
}

func (a *admin) handleKioskSave(w http.ResponseWriter, r *http.Request, s sessionRef) {
	v := r.PostForm
	f := kioskFormData{Inactivity: strings.TrimSpace(v.Get("inactivity")), Layout: v.Get("layout"), Permanent: v.Get("permanent"), HideBar: v.Get("hide_bar") != "", InputMode: v.Get("input_mode"), NetworkTiles: v.Get("network_tiles") != ""}
	err := a.update(func(c *config.Config) error {
		n, err := strconv.Atoi(f.Inactivity)
		if err != nil {
			return fmt.Errorf("Rückkehr bei Inaktivität muss eine Zahl sein: %q", f.Inactivity)
		}
		mode := f.InputMode
		if mode == "" { // ältere Formulare ohne das Feld: bisherige Bedienart behalten
			mode = c.Kiosk.InputMode
		}
		if mode == "" {
			mode = config.InputTouch
		}
		next := config.KioskCfg{InactivityMinutes: n, Layout: f.Layout, PermanentHMI: f.Permanent, HideBar: f.HideBar, InputMode: mode, NetworkTiles: f.NetworkTiles}
		if err := checkKiosk(next); err != nil {
			return err
		}
		c.Kiosk = next
		return nil
	})
	if err != nil {
		a.renderKiosk(w, s, formStatus(err), err.Error(), f)
		return
	}
	msg := "Gespeichert."
	if f.NetworkTiles {
		msg = "Gespeichert. Die Kachelseite ist ohne Neustart im Netz freigegeben (nur für Tests), die Adresse steht oben."
	}
	a.done(w, r, s, "/admin/kiosk", msg)
}

// --- Terminals ---

type terminalsFormData struct {
	ID, Name, Role, Mode string
	Peers                []config.Peer
	KeySet               bool
	GeneratedKey         string // nur direkt nach dem Erzeugen, nie aus der Konfiguration
}

// sparePeerRows sind leere Zeilen für weitere Terminals (ohne Skript).
const sparePeerRows = 3

func (a *admin) renderTerminals(w http.ResponseWriter, s sessionRef, status int, errMsg string, f *terminalsFormData) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	if f == nil {
		f = &terminalsFormData{Name: cfg.Terminal.Name, Role: string(cfg.Terminal.Role), Mode: string(cfg.DefaultMode), Peers: cfg.Peers}
	}
	f.ID = cfg.Terminal.ID
	f.KeySet = cfg.SharedKeyEnc != ""
	f.Peers = append(append([]config.Peer(nil), f.Peers...), make([]config.Peer, sparePeerRows)...)
	a.page(w, s, status, "terminals.html", "terminals", "Terminals", errMsg, *f)
}

func (a *admin) handleTerminalsForm(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderTerminals(w, s, http.StatusOK, "", nil)
}

func peersFromForm(names, addrs []string) []config.Peer {
	var peers []config.Peer
	for i := 0; i < max(len(names), len(addrs)); i++ {
		var p config.Peer
		if i < len(names) {
			p.Name = strings.TrimSpace(names[i])
		}
		if i < len(addrs) {
			p.Address = strings.TrimSpace(addrs[i])
		}
		if p.Name != "" || p.Address != "" {
			peers = append(peers, p)
		}
	}
	return peers
}

func (a *admin) handleTerminalsSave(w http.ResponseWriter, r *http.Request, s sessionRef) {
	v := r.PostForm
	f := terminalsFormData{
		Name: strings.TrimSpace(v.Get("name")), Role: v.Get("role"), Mode: v.Get("mode"),
		Peers: peersFromForm(v["peer_name"], v["peer_address"]),
	}
	err := a.update(func(c *config.Config) error {
		c.Terminal.Name = f.Name
		c.Terminal.Role = config.Role(f.Role)
		c.DefaultMode = config.Mode(f.Mode)
		c.Peers = f.Peers
		return nil
	})
	if err != nil {
		a.renderTerminals(w, s, formStatus(err), err.Error(), &f)
		return
	}
	a.done(w, r, s, "/admin/terminals", "Gespeichert.")
}

func (a *admin) storeSharedKey(key string) error {
	enc, err := a.d.Box.Seal(key)
	if err != nil {
		return fmt.Errorf("Schlüssel konnte nicht verschlüsselt werden: %w", err)
	}
	return a.update(func(c *config.Config) error {
		c.SharedKeyEnc = enc
		return nil
	})
}

// handleKeyGenerate zeigt den Schlüssel einmalig in der Antwort. Das geschieht
// ohne Weiterleitung, damit er nirgends zwischengespeichert werden muss.
func (a *admin) handleKeyGenerate(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		a.renderTerminals(w, s, http.StatusInternalServerError, "Es konnte kein Schlüssel erzeugt werden.", nil)
		return
	}
	key := hex.EncodeToString(raw)
	if err := a.storeSharedKey(key); err != nil {
		a.renderTerminals(w, s, formStatus(err), err.Error(), nil)
		return
	}
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	a.renderTerminals(w, s, http.StatusOK, "", &terminalsFormData{
		Name: cfg.Terminal.Name, Role: string(cfg.Terminal.Role), Mode: string(cfg.DefaultMode),
		Peers: cfg.Peers, GeneratedKey: key,
	})
}

func (a *admin) handleKeySet(w http.ResponseWriter, r *http.Request, s sessionRef) {
	key := strings.TrimSpace(r.PostForm.Get("shared_key"))
	if utf8.RuneCountInString(key) < minSharedKeyLen {
		a.renderTerminals(w, s, http.StatusUnprocessableEntity, fmt.Sprintf("Der Schlüssel muss mindestens %d Zeichen lang sein.", minSharedKeyLen), nil)
		return
	}
	if err := a.storeSharedKey(key); err != nil {
		a.renderTerminals(w, s, formStatus(err), err.Error(), nil)
		return
	}
	a.done(w, r, s, "/admin/terminals", "Schlüssel gespeichert.")
}

// --- Netzwerk ---

type networkData struct {
	WifiOff, BluetoothOff bool
	NTP                   string
	Eth                   ethView
}

func (a *admin) renderNetwork(w http.ResponseWriter, s sessionRef, status int, errMsg string, ntpOverride *string) {
	a.renderNetworkEth(w, s, status, errMsg, ntpOverride, nil)
}

// renderNetworkEth zeigt die Netzwerkseite; ethOverride hält eine abgelehnte Ethernet-Eingabe.
func (a *admin) renderNetworkEth(w http.ResponseWriter, s sessionRef, status int, errMsg string, ntpOverride *string, ethOverride *ethForm) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	d := networkData{WifiOff: cfg.Network.WifiOff, BluetoothOff: cfg.Network.BluetoothOff, NTP: cfg.Network.NTPServer}
	if ntpOverride != nil {
		d.NTP = *ntpOverride
	}
	d.Eth = a.ethernetView(cfg.Network.Ethernet, ethOverride)
	refresh := 0
	if d.Eth.Pending {
		refresh = ethRefreshSec
	}
	a.pageRefresh(w, s, status, "network.html", "network", "Netzwerk", errMsg, refresh, d)
}

func (a *admin) handleNetworkPage(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderNetwork(w, s, http.StatusOK, "", nil)
}

// helperThenPersist ruft zuerst den Helfer und speichert nur bei Erfolg. Scheitert
// danach das Speichern, ist die Änderung am Gerät schon wirksam; darum eine eigene
// Meldung statt des üblichen Texts "es bleibt beim Alten".
func (a *admin) helperThenPersist(ctx context.Context, cmd, arg string, change func(*config.Config)) error {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, helperLimit)
	defer cancel()
	if err := a.d.Helper.Call(ctx, cmd, arg); err != nil {
		return fmt.Errorf("Der Befehl ist fehlgeschlagen: %v", err)
	}
	err := a.updateLocked(func(c *config.Config) error {
		change(c)
		return nil
	})
	if err != nil {
		var sf saveFailed
		if errors.As(err, &sf) {
			err = sf.err
		} else if errors.Is(err, errConfigUnreadable) {
			err = errors.New("Konfiguration nicht lesbar")
		}
		return fmt.Errorf("Der Befehl wurde ausgeführt, die Einstellung wurde aber nicht gespeichert. Diese Seite zeigt deshalb den alten Stand; bitte erneut versuchen (%v).", err)
	}
	return nil
}

func (a *admin) networkAction(w http.ResponseWriter, r *http.Request, s sessionRef, cmd string, change func(*config.Config), okMsg string) {
	if err := a.helperThenPersist(r.Context(), cmd, "", change); err != nil {
		a.renderNetwork(w, s, http.StatusInternalServerError, err.Error(), nil)
		return
	}
	a.done(w, r, s, "/admin/network", okMsg)
}

func (a *admin) handleWifiOff(w http.ResponseWriter, r *http.Request, s sessionRef) {
	switch {
	case a.d.RemoteIsWifi(r):
		a.renderNetwork(w, s, http.StatusConflict, "WLAN kann nicht abgeschaltet werden: Diese Sitzung läuft über WLAN und würde abbrechen.", nil)
		return
	case !a.d.EthernetLink():
		a.renderNetwork(w, s, http.StatusConflict, "WLAN kann nicht abgeschaltet werden: Die Ethernet-Verbindung hat keinen Link, das Terminal wäre nicht mehr erreichbar.", nil)
		return
	}
	a.networkAction(w, r, s, helperWifiOff, func(c *config.Config) { c.Network.WifiOff = true },
		"WLAN ist abgeschaltet; das wirkt dauerhaft nach dem Neustart.")
}

func (a *admin) handleWifiOn(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.networkAction(w, r, s, helperWifiOn, func(c *config.Config) { c.Network.WifiOff = false },
		"WLAN wird wieder eingeschaltet: Neustart nötig.")
}

func (a *admin) handleBtOff(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.networkAction(w, r, s, helperBtOff, func(c *config.Config) { c.Network.BluetoothOff = true },
		"Bluetooth ist abgeschaltet; das wirkt dauerhaft nach dem Neustart.")
}

func (a *admin) handleBtOn(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.networkAction(w, r, s, helperBtOn, func(c *config.Config) { c.Network.BluetoothOff = false },
		"Bluetooth wird wieder eingeschaltet: Neustart nötig.")
}

func (a *admin) handleNTP(w http.ResponseWriter, r *http.Request, s sessionRef) {
	server := strings.TrimSpace(r.PostForm.Get("ntp"))
	if !config.ValidNTPServer(server) {
		a.renderNetwork(w, s, http.StatusUnprocessableEntity, "Bitte einen gültigen Rechnernamen oder eine IP-Adresse als NTP-Server angeben.", &server)
		return
	}
	err := a.helperThenPersist(r.Context(), helperSetNTP, server, func(c *config.Config) { c.Network.NTPServer = server })
	if err != nil {
		a.renderNetwork(w, s, http.StatusInternalServerError, err.Error(), &server)
		return
	}
	a.done(w, r, s, "/admin/network", "NTP-Server gesetzt.")
}

// handleWebLoginsDelete löscht das Profil der Web-Kacheln (Cookies, also gespeicherte
// Anmeldungen). Bei offener Web-Kachel lehnt der Dienst ab.
func (a *admin) handleWebLoginsDelete(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.ClearWebLogins == nil {
		notFound(w)
		return
	}
	if err := a.d.ClearWebLogins(); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, session.ErrWebOpen) {
			status = http.StatusConflict
		}
		a.renderKioskStored(w, s, status, err.Error())
		return
	}
	a.done(w, r, s, "/admin/kiosk", "Gespeicherte Web-Anmeldungen gelöscht.")
}
