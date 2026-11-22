package pdhterminal

import (
	_ "embed"
	"net/http"
	"time"

	"hmikiosk/internal/web"
)

//go:embed admin_functions.html
var functionsPage string

type funcRow struct {
	Key, Label, State string
	On                bool
}

// functionsView ist, was die Seite Funktionen anzeigt.
type functionsView struct {
	Rows      []funcRow
	Notice    string
	Sync      string
	SyncClass string
	ClockWarn bool // Geräteuhr unplausibel (vor 2025)
}

// funcRows beschreibt jeden Schalter mit seinem Stand (Zeitpunkt der letzten Änderung, ob PDH ihn schon kennt).
func (p *adminProduct) funcRows() []funcRow {
	rows := make([]funcRow, 0, len(FuncDefs))
	for _, d := range FuncDefs {
		v := p.funcs.Get(d.Key)
		r := funcRow{Key: d.Key, Label: d.Label, On: v.Enabled}
		switch {
		case p.funcs.Pending(d.Key):
			r.State = "geändert " + v.UpdatedAt.Local().Format("02.01.2006 15:04") + ", noch nicht an PDH übertragen"
		case !v.UpdatedAt.IsZero() && v.UpdatedAt.Year() > 1970:
			r.State = "zuletzt geändert " + v.UpdatedAt.Local().Format("02.01.2006 15:04")
		}
		rows = append(rows, r)
	}
	return rows
}

func (p *adminProduct) syncText() (text, class string) {
	st := p.funcs.Status()
	switch {
	case st.Unsupported:
		return "Diese PDH-Version kennt die Funktionen nicht, die Werte gelten nur an diesem Terminal", "warn"
	case st.LastErr != "":
		return st.LastErr + " – die Werte bleiben lokal erhalten und werden später übertragen", "warn"
	case st.LastOK.IsZero():
		return "noch nicht abgeglichen", "warn"
	}
	return "abgeglichen " + st.LastOK.Local().Format("15:04:05"), "ok"
}

func (p *adminProduct) renderFunctions(f web.AdminFrame, w http.ResponseWriter, status int, errMsg, notice string) {
	v := functionsView{Rows: p.funcRows(), Notice: notice, ClockWarn: time.Now().Year() < 2025}
	v.Sync, v.SyncClass = p.syncText()
	f.Render(w, status, "functions", "functions", "Funktionen", errMsg, v)
}

func (p *adminProduct) functionsSave(f web.AdminFrame, w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		p.renderFunctions(f, w, http.StatusBadRequest, "Formular unlesbar", "")
		return
	}
	changed := 0
	for _, d := range FuncDefs {
		on := r.PostFormValue("f_"+d.Key) != ""
		// Nur Schalter, die die Person gegenüber der angezeigten Seite geändert hat, sind lokale Änderungen
		// (sonst überschriebe das Speichern Änderungen aus PDH, die nach dem Öffnen der Seite kamen).
		was := r.PostFormValue("was_" + d.Key)
		if was == "" || (was == "1") == on {
			continue
		}
		ch, err := p.funcs.Set(d.Key, on)
		if err != nil {
			p.renderFunctions(f, w, http.StatusInternalServerError, err.Error(), "")
			return
		}
		if ch {
			changed++
		}
	}
	notice := "Keine Änderung."
	if changed > 0 {
		f.Logf("Funktionen geändert (%d)", changed)
		notice = "Gespeichert. Die Änderung geht jetzt an PDH."
	}
	p.renderFunctions(f, w, http.StatusOK, "", notice)
}
