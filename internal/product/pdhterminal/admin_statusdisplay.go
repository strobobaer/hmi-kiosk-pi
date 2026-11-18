package pdhterminal

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"hmikiosk/internal/config"
	"hmikiosk/internal/web"
)

// statusDisplayView ist, was die Seite Statusdisplay anzeigt.
type statusDisplayView struct {
	Type, Bus, Addr string
	Detail          string
	StateClass      string // ok, warn oder err
	Unsupported     string
	Notice          string
	TestMsg         string
	TestOK          bool
}

// renderStatusDisplay zeigt die Seite. form (nicht nil) ersetzt die gespeicherten Werte im Formular.
func (p *adminProduct) renderStatusDisplay(f web.AdminFrame, w http.ResponseWriter, status int, errMsg, notice string, form *config.StatusDisplayCfg) {
	p.renderStatusDisplayTest(f, w, status, errMsg, notice, form, "", false)
}

// renderStatusDisplayTest zeigt zusätzlich das Ergebnis des Prüfknopfs (testMsg leer: keine Prüfung).
func (p *adminProduct) renderStatusDisplayTest(f web.AdminFrame, w http.ResponseWriter, status int, errMsg, notice string, form *config.StatusDisplayCfg, testMsg string, testOK bool) {
	cfg, err := f.Load()
	if err != nil {
		f.Render(w, http.StatusInternalServerError, "statusdisplay", "statusdisplay", "Statusdisplay", err.Error(), statusDisplayView{})
		return
	}
	shown := cfg.StatusDisplay
	if form != nil {
		shown = *form
	}
	bus, addr := shown.Settings()
	ok, reason := shown.Known()
	v := statusDisplayView{
		Type: shown.Type, Bus: bus, Addr: fmt.Sprintf("%#x", addr), Unsupported: reason,
		Notice: notice, TestMsg: testMsg, TestOK: testOK,
	}
	if !ok {
		v.Type = "" // zeigt „Aus“, ohne die Konfiguration zu ändern
	}
	state, detail := p.display.Status()
	v.Detail = detail
	switch state {
	case "ok":
		v.StateClass = "ok"
	case "error":
		v.StateClass = "err"
	default:
		v.StateClass = "warn"
	}
	f.Render(w, status, "statusdisplay", "statusdisplay", "Statusdisplay", errMsg, v)
}

func (p *adminProduct) statusDisplaySave(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.renderStatusDisplay(f, w, http.StatusBadRequest, "Formular unlesbar", "", nil)
		return
	}
	in := config.StatusDisplayCfg{
		Type:   strings.TrimSpace(r.PostFormValue("type")),
		I2CBus: strings.TrimSpace(r.PostFormValue("i2cBus")),
	}
	if s := strings.TrimSpace(r.PostFormValue("i2cAddr")); s != "" {
		n, err := strconv.ParseInt(s, 0, 0)
		if err != nil {
			p.renderStatusDisplay(f, w, http.StatusBadRequest, fmt.Sprintf("Adresse ungültig: %q (erwartet z. B. 0x3C)", s), "", &in)
			return
		}
		in.I2CAddr = int(n)
	}
	if err := config.ValidateStatusDisplayStrict(in); err != nil {
		p.renderStatusDisplay(f, w, http.StatusBadRequest, err.Error(), "", &in)
		return
	}
	if err := f.Update(func(c *config.Config) error { c.StatusDisplay = in; return nil }); err != nil {
		p.renderStatusDisplay(f, w, http.StatusBadRequest, err.Error(), "", &in)
		return
	}
	p.renderStatusDisplay(f, w, http.StatusOK, "", "Einstellungen gespeichert.", nil)
}

// statusDisplayTest fragt das Display unter dem gespeicherten Bus und der gespeicherten Adresse an.
func (p *adminProduct) statusDisplayTest(f web.AdminFrame, w http.ResponseWriter, _ *http.Request) {
	cfg, err := f.Load()
	if err != nil {
		p.renderStatusDisplay(f, w, http.StatusInternalServerError, err.Error(), "", nil)
		return
	}
	if cfg.StatusDisplay.Type == "" {
		p.renderStatusDisplayTest(f, w, http.StatusOK, "", "", nil, "Statusdisplay aus", true)
		return
	}
	if ok, reason := cfg.StatusDisplay.Known(); !ok {
		p.renderStatusDisplayTest(f, w, http.StatusOK, "", "", nil, "Statusdisplay: "+reason, false)
		return
	}
	bus, addr := cfg.StatusDisplay.Settings()
	if err := p.probe(bus, addr); err != nil {
		p.renderStatusDisplayTest(f, w, http.StatusOK, "", "", nil, fmt.Sprintf("Statusdisplay antwortet nicht (%s, %#x): %v", bus, addr, err), false)
		return
	}
	p.renderStatusDisplayTest(f, w, http.StatusOK, "", "", nil, fmt.Sprintf("Statusdisplay antwortet (%s, %#x)", bus, addr), true)
}
