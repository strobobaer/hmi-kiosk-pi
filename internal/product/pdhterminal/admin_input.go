package pdhterminal

import (
	"fmt"
	"net/http"

	"hmikiosk/internal/config"
	"hmikiosk/internal/inputmode"
	"hmikiosk/internal/web"
)

// inputView ist der Abschnitt „Bedienung“ der Seite PDH.
type inputView struct {
	Windows bool   // Windows: nur ein Hinweis, keine Auswahl
	Setting string // gespeicherte Einstellung (touch, mouse, auto)
	Current string // „zurzeit: …“
	Pending bool   // gespeichert, aber die laufende Sitzung wurde anders gestartet
	// SavedLabel nennt die gespeicherte Einstellung in Worten (für den Hinweis bei Pending).
	SavedLabel string
}

var inputLabels = map[string]string{
	config.InputTouch: "Touch",
	config.InputMouse: "Maus",
	config.InputAuto:  "Automatisch",
}

// inputView bildet die gespeicherte Einstellung und den Zustand der laufenden Sitzung ab. Die Sitzung
// (hmi-kiosk-session prepare) entscheidet, ob der Zeiger sichtbar ist; hier wird nur gelesen (inputmode).
func (p *adminProduct) inputView(saved string) inputView {
	if p.windows {
		return inputView{Windows: true}
	}
	if saved == "" { // Dateien vor „Automatisch“: leer gilt wie Touch
		saved = config.InputTouch
	}
	v := inputView{Setting: saved, SavedLabel: inputLabels[saved]}
	mode, started, ok := inputmode.Running()
	if !ok {
		v.Current = "zurzeit: nicht ermittelt (die Bildschirm-Sitzung hat noch keine Bedienart gemeldet)"
		return v
	}
	if started == "" { // Sitzung ohne Vermerk der Einstellung: sie lief mit dem festen Wert
		started = mode
	}
	switch {
	case started == config.InputAuto && mode == config.InputMouse:
		v.Current = "zurzeit: Maus erkannt – Zeiger sichtbar"
	case started == config.InputAuto:
		v.Current = "zurzeit: keine Maus erkannt – Zeiger ausgeblendet"
	case mode == config.InputMouse:
		v.Current = "zurzeit: Maus – Zeiger sichtbar"
	default:
		v.Current = "zurzeit: Touch – Zeiger ausgeblendet"
	}
	v.Pending = saved != started
	return v
}

// inputMode ist die wirksame Bedienart (touch oder mouse) für die Bildschirmtastatur.
func (s *service) inputMode() string { return inputmode.Effective(s.config().Kiosk.InputMode) }

// inputSave speichert die Bedienart. Die Sitzung liest sie beim Start (hmi-kiosk-session prepare); es gibt keinen
// sicheren Weg, die Sitzung ohne Neustart des Terminals zu wechseln, deshalb bietet die Seite den Neustart an.
func (p *adminProduct) inputSave(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if p.windows {
		p.render(f, w, r, http.StatusBadRequest, "Unter Windows gibt es keine Bedienart zu wählen.", "", nil)
		return
	}
	if err := r.ParseForm(); err != nil {
		p.render(f, w, r, http.StatusBadRequest, "Formular unlesbar", "", nil)
		return
	}
	mode := r.PostFormValue("input_mode")
	switch mode {
	case config.InputTouch, config.InputMouse, config.InputAuto:
	default:
		p.render(f, w, r, http.StatusBadRequest, fmt.Sprintf("Unbekannte Bedienart: %q (erlaubt: Touch, Maus oder Automatisch)", mode), "", nil)
		return
	}
	if err := f.Update(func(c *config.Config) error { c.Kiosk.InputMode = mode; return nil }); err != nil {
		p.render(f, w, r, http.StatusBadRequest, err.Error(), "", nil)
		return
	}
	f.Logf("Bedienart auf %s gestellt", mode)
	notice := "Gespeichert."
	if p.inputView(mode).Pending {
		notice = "Gespeichert. Die Bedienart gilt nach dem Neustart des Terminals."
	}
	p.render(f, w, r, http.StatusOK, "", notice, nil)
}
