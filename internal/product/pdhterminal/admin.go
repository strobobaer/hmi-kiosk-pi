package pdhterminal

import (
	"context"
	_ "embed"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/localport"
	"hmikiosk/internal/product/pdhterminal/card"
	"hmikiosk/internal/product/pdhterminal/oled"
	"hmikiosk/internal/web"
)

//go:embed admin_pdh.html
var pdhPage string

//go:embed admin_card.html
var cardPage string

//go:embed admin_statusdisplay.html
var statusDisplayPage string

//go:embed admin_kiosk.html
var kioskPage string

const checkTimeout = 5 * time.Second

// pdhView ist, was die Seite PDH anzeigt. Weder Code noch Geheimnis kommen darin vor.
type pdhView struct {
	URL, DeviceName, Extra, DefaultName string
	AllowHTTP                           bool
	Paired                              bool
	User, Since                         string
	State, StateClass                   string
	Notice                              string

	// Lokale Startseite: LocalPort ist der Wert im Feld, Port/AdminPort der tatsächlich benutzte Port der
	// Startseite und der lokalen Admin-Seite, Pending der gespeicherte, aber erst nach dem Neustart gültige.
	LocalPort       string
	Port, AdminPort int
	Pending         int
	Failed          int // eingestellter Port, der sich nicht binden ließ; es läuft der Grundwert
	PortProblem     string
	DefaultPort     int
}

type adminProduct struct {
	dataDir string
	client  Client
	funcs   *FuncSync
	cards   *card.Service
	display *oled.Service
	// probe prüft das Statusdisplay (Variable für Tests).
	probe func(bus string, addr int) error
	// local meldet den tatsächlich benutzten Port der Startseite; nil: der eingestellte Port (localport).
	local func() localState
	// adminPort liefert den Port der Admin-Seite über HTTPS (für die Kollisionsprüfung); nil: 8443.
	adminPort func() int
	// bindProbe versucht, addr kurz zu binden (nil: wirklich binden); für Tests ersetzbar.
	bindProbe func(addr string) error
	// kiosk: Windows-Kiosk, die Seite hat den Abschnitt „Kiosk“; logoff meldet den Benutzer ab (Windows:
	// ExitWindowsEx(EWX_LOGOFF), Tests ersetzen es).
	kiosk  bool
	logoff func() error

	learnMu    sync.Mutex
	learnCache *learnResult // Antwort der letzten erfolgreichen Anlernanfrage, für ein zweites Absenden
}

func newAdminProduct(dataDir string, client Client, cards *card.Service, display *oled.Service) *adminProduct {
	return &adminProduct{dataDir: dataDir, client: client, funcs: NewFuncSync(dataDir, client), cards: cards, display: display, probe: oled.Probe}
}

// Admin beschreibt das PDH-Terminal für den Admin-Rahmen.
func Admin(dataDir string, client Client, cards *card.Service, display *oled.Service) *web.AdminProduct {
	return newAdminProduct(dataDir, client, cards, display).product()
}

func (p *adminProduct) product() *web.AdminProduct {
	ap := p.baseProduct()
	if p.kiosk {
		ap.Nav = append(ap.Nav, web.NavItem{Section: "kiosk", Label: "Kiosk", Path: "/admin/kiosk"})
		ap.Pages["winkiosk"] = kioskPage
		ap.Routes = append(ap.Routes,
			web.ProductRoute{Method: http.MethodGet, Path: "/admin/kiosk", Handle: func(f web.AdminFrame, w http.ResponseWriter, _ *http.Request) {
				f.Render(w, http.StatusOK, "winkiosk", "kiosk", "Kiosk", "", kioskView{})
			}},
			web.ProductRoute{Method: http.MethodPost, Path: "/admin/kiosk/logoff", Handle: p.kioskLogoff},
		)
	}
	return ap
}

// kioskView ist, was die Seite „Kiosk“ anzeigt.
type kioskView struct{ Notice string }

// kioskLogoff meldet den Kiosk-Benutzer ab, aber nur nach der Bestätigung im Formular.
func (p *adminProduct) kioskLogoff(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostFormValue("confirm") != "1" {
		f.Render(w, http.StatusBadRequest, "winkiosk", "kiosk", "Kiosk", "Bitte die Abmeldung bestätigen", kioskView{})
		return
	}
	if p.logoff == nil {
		f.Render(w, http.StatusInternalServerError, "winkiosk", "kiosk", "Kiosk", "Abmelden ist hier nicht möglich", kioskView{})
		return
	}
	if err := p.logoff(); err != nil {
		f.Render(w, http.StatusInternalServerError, "winkiosk", "kiosk", "Kiosk", "Abmeldung fehlgeschlagen: "+err.Error(), kioskView{})
		return
	}
	f.Render(w, http.StatusOK, "winkiosk", "kiosk", "Kiosk", "", kioskView{Notice: "Der Kiosk-Benutzer wird abgemeldet."})
}

func (p *adminProduct) baseProduct() *web.AdminProduct {
	return &web.AdminProduct{
		ID: "pdh-terminal", Name: "PDH-Terminal", Home: "/admin/pdh", HMI: false, IdleSeconds: 120,
		Nav: []web.NavItem{
			{Section: "pdh", Label: "PDH", Path: "/admin/pdh"},
			{Section: "functions", Label: "Funktionen", Path: "/admin/functions"},
			{Section: "card", Label: "Kartenleser", Path: "/admin/card"},
			{Section: "statusdisplay", Label: "Statusdisplay", Path: "/admin/statusdisplay"},
			{Section: "network", Label: "Netzwerk", Path: "/admin/network"},
			{Section: "system", Label: "System", Path: "/admin/system"},
		},
		Pages: map[string]string{"pdh": pdhPage, "functions": functionsPage, "card": cardPage, "statusdisplay": statusDisplayPage},
		Theme: func() string { return LoadTheme(p.dataDir).CSS() },
		Routes: []web.ProductRoute{
			{Method: http.MethodGet, Path: "/admin/pdh", Handle: func(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
				p.render(f, w, r, http.StatusOK, "", "", nil)
			}},
			{Method: http.MethodGet, Path: "/admin/functions", Handle: func(f web.AdminFrame, w http.ResponseWriter, _ *http.Request) {
				p.renderFunctions(f, w, http.StatusOK, "", "")
			}},
			{Method: http.MethodPost, Path: "/admin/functions", Handle: p.functionsSave},
			{Method: http.MethodPost, Path: "/admin/pdh/settings", Handle: p.settings},
			{Method: http.MethodPost, Path: "/admin/pdh/localport", Handle: p.localPortSave},
			{Method: http.MethodPost, Path: "/admin/pdh/pair", Handle: p.pair},
			{Method: http.MethodPost, Path: "/admin/pdh/unpair", Handle: p.unpair},
			{Method: http.MethodGet, Path: "/admin/card", Handle: func(f web.AdminFrame, w http.ResponseWriter, _ *http.Request) {
				p.renderCard(f, w, http.StatusOK, "", "", nil)
			}},
			{Method: http.MethodPost, Path: "/admin/card/settings", Handle: p.cardSave},
			{Method: http.MethodPost, Path: "/admin/card/learn", Handle: p.cardLearn},
			{Method: http.MethodGet, Path: "/admin/statusdisplay", Handle: func(f web.AdminFrame, w http.ResponseWriter, _ *http.Request) {
				p.renderStatusDisplay(f, w, http.StatusOK, "", "", nil)
			}},
			{Method: http.MethodPost, Path: "/admin/statusdisplay", Handle: p.statusDisplaySave},
			{Method: http.MethodPost, Path: "/admin/statusdisplay/test", Handle: p.statusDisplayTest},
		},
	}
}

func defaultName() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "pdh-terminal"
}

// render zeigt die Seite PDH. form (nicht nil) ersetzt die gespeicherten Werte im Formular.
func (p *adminProduct) render(f web.AdminFrame, w http.ResponseWriter, r *http.Request, status int, errMsg, notice string, form *config.PDHCfg) {
	p.renderPort(f, w, r, status, errMsg, notice, form, nil)
}

// renderPort wie render; port (nicht nil) ersetzt den gespeicherten Port im Feld der lokalen Startseite.
func (p *adminProduct) renderPort(f web.AdminFrame, w http.ResponseWriter, r *http.Request, status int, errMsg, notice string, form *config.PDHCfg, port *string) {
	cfg, err := f.Load()
	if err != nil {
		f.Render(w, http.StatusInternalServerError, "pdh", "pdh", "PDH", err.Error(), pdhView{})
		return
	}
	shown := cfg.PDH
	if form != nil {
		shown = *form
	}
	v := pdhView{
		URL: shown.URL, AllowHTTP: shown.AllowHTTP, DeviceName: shown.DeviceName,
		Extra: strings.Join(shown.ExtraOrigins, "\n"), DefaultName: defaultName(), Notice: notice,
		DefaultPort: localport.Default,
	}
	v.LocalPort = strconv.Itoa(cfg.LocalPort())
	if port != nil {
		v.LocalPort = *port
	}
	ls := localState{Port: localport.Get()}
	if p.local != nil {
		ls = p.local()
	}
	v.Port, v.AdminPort, v.PortProblem = ls.Port, ls.Port+1, ls.Problem
	if want := cfg.LocalPort(); want != ls.Port {
		if want == ls.Failed && want != 0 {
			v.Failed = want
		} else {
			v.Pending = want
		}
	}
	dev, derr := LoadDevice(p.dataDir)
	switch {
	case derr != nil:
		if errMsg == "" {
			errMsg = derr.Error()
		}
	case dev != nil && cfg.PDH.URL != "" && dev.URL == cfg.PDH.URL:
		v.Paired, v.User = true, dev.User
		if !dev.PairedAt.IsZero() {
			v.Since = dev.PairedAt.Local().Format("02.01.2006 15:04")
		}
		ctx, cancel := context.WithTimeout(r.Context(), checkTimeout)
		st, _ := p.client.Check(ctx, *dev)
		cancel()
		switch st {
		case StateOK:
			v.State, v.StateClass = "verbunden", "ok"
		case StateRevoked:
			v.State, v.StateClass = "gesperrt – bitte neu koppeln", "err"
		case StateUntrusted:
			v.State, v.StateClass = "Zertifikat von PDH wird nicht vertraut", "err"
		default:
			v.State, v.StateClass = "nicht erreichbar", "warn"
		}
	}
	f.Render(w, status, "pdh", "pdh", "PDH", errMsg, v)
}

func (p *adminProduct) settings(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.render(f, w, r, http.StatusBadRequest, "Formular unlesbar", "", nil)
		return
	}
	in := config.PDHCfg{
		URL:        strings.TrimSpace(r.PostFormValue("url")),
		AllowHTTP:  r.PostFormValue("allowHttp") != "",
		DeviceName: strings.TrimSpace(r.PostFormValue("deviceName")),
	}
	for _, line := range strings.Split(r.PostFormValue("extraOrigins"), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		o, err := config.WebOrigin(s)
		if err != nil {
			in.ExtraOrigins = append(in.ExtraOrigins, s)
			p.render(f, w, r, http.StatusBadRequest, fmt.Sprintf("Zusätzliche Adresse ungültig: %q (%v; erwartet z. B. https://cdn.firma.de)", s, err), "", &in)
			return
		}
		in.ExtraOrigins = append(in.ExtraOrigins, o)
	}
	oldURL := ""
	if c, err := f.Load(); err == nil {
		oldURL = c.PDH.URL
	}
	if err := f.Update(func(c *config.Config) error { c.PDH = in; return nil }); err != nil {
		p.render(f, w, r, http.StatusBadRequest, err.Error(), "", &in)
		return
	}
	notice := "Einstellungen gespeichert."
	dev, lerr := LoadDevice(p.dataDir)
	// Eine beschädigte Datei hat keine Adresse: Ändert sich die Adresse, geht sie mit weg.
	if (lerr == nil && dev != nil && dev.URL != in.URL) || (lerr != nil && oldURL != in.URL) {
		if err := RemoveDevice(p.dataDir); err != nil {
			p.render(f, w, r, http.StatusInternalServerError, err.Error(), "", nil)
			return
		}
		notice = "Kopplung entfernt, weil die PDH-Adresse geändert wurde."
	}
	p.render(f, w, r, http.StatusOK, "", notice, nil)
}

// localPortSave speichert den Port der lokalen Startseite. Er wirkt nach dem Neustart des Dienstes, den der
// Dienst nach dem Speichern selbst auslöst (service.portChanged).
func (p *adminProduct) localPortSave(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.render(f, w, r, http.StatusBadRequest, "Formular unlesbar", "", nil)
		return
	}
	raw := strings.TrimSpace(r.PostFormValue("localPort"))
	fail := func(msg string) {
		p.renderPort(f, w, r, http.StatusBadRequest, msg, "", nil, &raw)
	}
	port := 0
	if raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			fail(fmt.Sprintf("Port der lokalen Startseite muss eine Zahl sein: %q", raw))
			return
		}
		port = n
	}
	adminPort := config.DefaultAdminPort
	if p.adminPort != nil {
		adminPort = p.adminPort()
	}
	if err := config.ValidateLocalPort(port, adminPort); err != nil {
		fail(err.Error())
		return
	}
	if port == localport.Default {
		port = 0 // der Grundwert steht nicht in der Datei
	}
	eff := portOrDefault(port)
	cfg, lerr := f.Load()
	if lerr != nil {
		p.render(f, w, r, http.StatusInternalServerError, lerr.Error(), "", nil)
		return
	}
	changed := eff != cfg.LocalPort() // gegen den gespeicherten Port: Die Richtlinie folgt ihm, nicht dem laufenden
	if changed {
		// Ein nicht bindbarer Port würde den Neustart ohne Startseite zurücklassen: vorher prüfen. Die Ports dieses
		// Laufs gelten als frei (sie sind belegt, weil das Terminal sie selbst hält).
		for _, bp := range []int{eff, eff + 1} {
			if bp == localport.Get() || bp == localport.Admin() {
				continue
			}
			if err := p.tryBind(bp); err != nil {
				fail(fmt.Sprintf("Port %d lässt sich auf diesem Rechner nicht öffnen (%v). Bitte einen anderen Port wählen.", bp, err))
				return
			}
		}
		// Erst die Richtlinie des Browsers, dann die Konfiguration: Steht der neue Port in der Datei, ohne dass der
		// Browser ihn öffnen darf, bliebe der Bildschirm nach dem nächsten Start gesperrt.
		if h := f.Helper(); h != nil {
			ctx, cancel := context.WithTimeout(r.Context(), policyPortTimeout)
			err := h.Call(ctx, helperSetLocalPort, strconv.Itoa(eff))
			cancel()
			if err != nil {
				f.Logf("Richtlinie für Port %d nicht gesetzt: %v", eff, err)
				fail("Browser-Richtlinie konnte nicht angepasst werden – Port nicht geändert (Systemteile aktualisieren)")
				return
			}
			setPolicyPortMarker(p.dataDir, eff)
		}
	}
	if err := f.Update(func(c *config.Config) error { c.Kiosk.LocalPort = port; return nil }); err != nil {
		if h := f.Helper(); changed && h != nil { // Richtlinie zurück auf den bisherigen Port
			ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), policyPortTimeout)
			if h.Call(ctx, helperSetLocalPort, strconv.Itoa(cfg.LocalPort())) == nil {
				setPolicyPortMarker(p.dataDir, cfg.LocalPort())
			}
			cancel()
		}
		fail(err.Error())
		return
	}
	notice := "Einstellung gespeichert."
	if want := localport.Default; port != 0 || localport.Get() != want {
		if eff := portOrDefault(port); eff != localport.Get() {
			f.Logf("Port der lokalen Startseite auf %d gestellt", eff)
			notice = fmt.Sprintf("Gespeichert. Das Terminal wird neu gestartet; danach ist die Startseite unter Port %d und die lokale Admin-Seite unter Port %d erreichbar.", eff, eff+1)
		}
	}
	p.render(f, w, r, http.StatusOK, "", notice, nil)
}

// tryBind öffnet 127.0.0.1:port kurz und schließt es sofort wieder.
func (p *adminProduct) tryBind(port int) error {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	if p.bindProbe != nil {
		return p.bindProbe(addr)
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return l.Close()
}

func portOrDefault(p int) int {
	if p == 0 {
		return localport.Default
	}
	return p
}

func (p *adminProduct) pair(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.render(f, w, r, http.StatusBadRequest, "Formular unlesbar", "", nil)
		return
	}
	code := strings.TrimSpace(r.PostFormValue("code"))
	if code == "" {
		p.render(f, w, r, http.StatusBadRequest, "Bitte den Kopplungscode eingeben.", "", nil)
		return
	}
	cfg, err := f.Load()
	if err != nil {
		p.render(f, w, r, http.StatusInternalServerError, err.Error(), "", nil)
		return
	}
	if cfg.PDH.URL == "" {
		p.render(f, w, r, http.StatusBadRequest, "Zuerst die PDH-Adresse speichern.", "", nil)
		return
	}
	name := cfg.PDH.DeviceName
	if name == "" {
		name = defaultName()
	}
	// Die bisherige Kopplung (auch eine widerrufene) geht mit, damit PDH das Terminal übernimmt.
	// Unlesbar oder beschädigt: ohne "previous" koppeln.
	prev, _ := LoadDevice(p.dataDir)
	dev, moved, err := p.client.PairMove(context.WithoutCancel(r.Context()), cfg.PDH.URL, code, name, prev)
	if err != nil {
		f.Logf("PDH-Kopplung fehlgeschlagen")
		p.render(f, w, r, http.StatusBadRequest, err.Error(), "", nil)
		return
	}
	if err := SaveDevice(p.dataDir, dev); err != nil {
		f.Logf("PDH-Kopplung konnte nicht gespeichert werden")
		p.render(f, w, r, http.StatusInternalServerError, err.Error(), "", nil)
		return
	}
	f.Logf("PDH-Kopplung hergestellt")
	msg := "Gekoppelt als „" + dev.User + "“."
	if moved {
		msg += " Terminal aus altem Benutzer übernommen."
	}
	p.render(f, w, r, http.StatusOK, "", msg, nil)
}

func (p *adminProduct) unpair(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := RemoveDevice(p.dataDir); err != nil {
		p.render(f, w, r, http.StatusInternalServerError, err.Error(), "", nil)
		return
	}
	f.Logf("PDH-Kopplung entfernt")
	p.render(f, w, r, http.StatusOK, "", "Kopplung entfernt.", nil)
}
