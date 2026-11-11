package pdhterminal

import (
	"context"
	_ "embed"
	"net/http"
	"os"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/web"
)

//go:embed admin_pdh.html
var pdhPage string

//go:embed admin_card.html
var cardPage string

const checkTimeout = 5 * time.Second

// pdhView ist, was die Seite PDH anzeigt. Weder Code noch Geheimnis kommen darin vor.
type pdhView struct {
	URL, DeviceName, Extra, DefaultName string
	AllowHTTP                           bool
	Paired                              bool
	User, Since                         string
	State, StateClass                   string
	Notice                              string
}

type adminProduct struct {
	dataDir string
	client  Client
}

// Admin beschreibt das PDH-Terminal für den Admin-Rahmen.
func Admin(dataDir string, client Client) *web.AdminProduct {
	p := &adminProduct{dataDir: dataDir, client: client}
	return &web.AdminProduct{
		ID: "pdh-terminal", Name: "PDH-Terminal", Home: "/admin/pdh", HMI: false, IdleSeconds: 120,
		Nav: []web.NavItem{
			{Section: "pdh", Label: "PDH", Path: "/admin/pdh"},
			{Section: "card", Label: "Kartenleser", Path: "/admin/card"},
			{Section: "network", Label: "Netzwerk", Path: "/admin/network"},
			{Section: "system", Label: "System", Path: "/admin/system"},
		},
		Pages: map[string]string{"pdh": pdhPage, "card": cardPage},
		Routes: []web.ProductRoute{
			{Method: http.MethodGet, Path: "/admin/pdh", Handle: func(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
				p.render(f, w, r, http.StatusOK, "", "", nil)
			}},
			{Method: http.MethodPost, Path: "/admin/pdh/settings", Handle: p.settings},
			{Method: http.MethodPost, Path: "/admin/pdh/pair", Handle: p.pair},
			{Method: http.MethodPost, Path: "/admin/pdh/unpair", Handle: p.unpair},
			{Method: http.MethodGet, Path: "/admin/card", Handle: func(f web.AdminFrame, w http.ResponseWriter, _ *http.Request) {
				f.Render(w, http.StatusOK, "card", "card", "Kartenleser", "", nil)
			}},
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
		if s := strings.TrimSpace(line); s != "" {
			in.ExtraOrigins = append(in.ExtraOrigins, s)
		}
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
	dev, err := p.client.Pair(context.WithoutCancel(r.Context()), cfg.PDH.URL, code, name)
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
	p.render(f, w, r, http.StatusOK, "", "Gekoppelt als „"+dev.User+"“.", nil)
}

func (p *adminProduct) unpair(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := RemoveDevice(p.dataDir); err != nil {
		p.render(f, w, r, http.StatusInternalServerError, err.Error(), "", nil)
		return
	}
	f.Logf("PDH-Kopplung entfernt")
	p.render(f, w, r, http.StatusOK, "", "Kopplung entfernt.", nil)
}
