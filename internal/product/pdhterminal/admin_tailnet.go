package pdhterminal

import (
	"context"
	_ "embed"
	"net/http"
	"time"

	"hmikiosk/internal/tailnet"
	"hmikiosk/internal/web"
)

//go:embed admin_tailnet.html
var tailnetPage string

const (
	tailnetStatusTimeout = 10 * time.Second
	tailnetActionTimeout = 60 * time.Second
)

// tailnetView ist, was die Seite „Tailnet“ anzeigt. Der Auth-Key kommt darin nie vor.
type tailnetView struct {
	Status   tailnet.Status
	StatusOK bool   // Status ließ sich abfragen
	Text     string // Zustand als Satz
	Class    string // ok / warn / err
	Host     string // Hostname, mit dem angemeldet würde
	Notice   string
}

func (p *adminProduct) tailnetRoutes() []web.ProductRoute {
	return []web.ProductRoute{
		{Method: http.MethodGet, Path: "/admin/tailnet", Handle: func(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
			p.renderTailnet(f, w, r, http.StatusOK, "", "")
		}},
		{Method: http.MethodPost, Path: "/admin/tailnet/up", Handle: p.tailnetUp},
		{Method: http.MethodPost, Path: "/admin/tailnet/down", Handle: p.tailnetDown},
	}
}

// tailnetHost ist der Hostname im Tailnet: aus dem Gerätenamen (PDH-Einstellung, sonst Rechnername).
func (p *adminProduct) tailnetHost(f web.AdminFrame) string {
	name := ""
	if cfg, err := f.Load(); err == nil {
		name = cfg.PDH.DeviceName
	}
	if name == "" {
		name = defaultName()
	}
	return tailnet.HostnameFrom(name)
}

func (p *adminProduct) renderTailnet(f web.AdminFrame, w http.ResponseWriter, r *http.Request, status int, errMsg, notice string) {
	v := tailnetView{Notice: notice, Host: p.tailnetHost(f)}
	ctx, cancel := context.WithTimeout(r.Context(), tailnetStatusTimeout)
	st, err := p.tailnet.Status(ctx)
	cancel()
	switch {
	case err != nil:
		v.Text, v.Class = "Zustand nicht abfragbar: "+err.Error(), "warn"
	case !st.Installed:
		v.StatusOK, v.Text, v.Class = true, "Tailscale ist auf diesem Gerät nicht installiert.", "warn"
	case !st.LoggedIn:
		v.Status, v.StatusOK, v.Text, v.Class = st, true, "nicht angemeldet", "warn"
	default:
		v.Status, v.StatusOK, v.Class = st, true, "ok"
		v.Text = "angemeldet als " + st.Name + " · " + st.DNSName + " · " + st.IP
	}
	f.Render(w, status, "tailnet", "tailnet", "Tailnet", errMsg, v)
}

// tailnetUp meldet mit dem Auth-Key an. Der Key wird nur gelesen, geprüft und weitergereicht: Er steht weder im
// Protokoll noch in einer Meldung noch in der Antwortseite.
func (p *adminProduct) tailnetUp(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.renderTailnet(f, w, r, http.StatusBadRequest, "Formular unlesbar", "")
		return
	}
	key := r.PostFormValue("authKey")
	// Eingefügte Schlüssel tragen oft Leerraum an den Rändern; innen ist keiner erlaubt.
	key = trimSpaceASCII(key)
	if err := tailnet.ValidateKey(key); err != nil {
		p.renderTailnet(f, w, r, http.StatusBadRequest, err.Error(), "")
		return
	}
	host := p.tailnetHost(f)
	p.tailnetMu.Lock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), tailnetActionTimeout)
	err := p.tailnet.Up(ctx, key, host)
	cancel()
	p.tailnetMu.Unlock()
	key = ""
	if err != nil {
		f.Logf("tailnet: Anmeldung fehlgeschlagen")
		p.renderTailnet(f, w, r, http.StatusBadGateway, err.Error(), "")
		return
	}
	name := host
	sctx, scancel := context.WithTimeout(r.Context(), tailnetStatusTimeout)
	if st, serr := p.tailnet.Status(sctx); serr == nil && st.Name != "" {
		name = st.Name
	}
	scancel()
	f.Logf("tailnet: angemeldet als %s", name)
	p.renderTailnet(f, w, r, http.StatusOK, "", "Angemeldet.")
}

func (p *adminProduct) tailnetDown(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	p.tailnetMu.Lock()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), tailnetActionTimeout)
	err := p.tailnet.Down(ctx)
	cancel()
	p.tailnetMu.Unlock()
	if err != nil {
		f.Logf("tailnet: Abmeldung fehlgeschlagen")
		p.renderTailnet(f, w, r, http.StatusBadGateway, err.Error(), "")
		return
	}
	f.Logf("tailnet: abgemeldet")
	p.renderTailnet(f, w, r, http.StatusOK, "", "Abgemeldet.")
}

// trimSpaceASCII schneidet Leerzeichen, Tabulatoren und Zeilenenden an den Rändern ab.
func trimSpaceASCII(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t' || s[start] == '\r' || s[start] == '\n') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == '\n') {
		end--
	}
	return s[start:end]
}
