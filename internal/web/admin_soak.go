package web

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// SoakAPI ist die Dauerprüfung im Dienst. Start liefert einen Fehler mit deutscher
// Begründung, wenn sie nicht starten darf.
type SoakAPI interface {
	Start(d time.Duration) error
	Stop()
	Status() SoakStatus
}

// SoakStatus ist der Stand der Dauerprüfung. Started ist null, wenn noch keine lief;
// Ended ist null, solange sie läuft.
type SoakStatus struct {
	Running           bool
	Started, Ended    time.Time
	Duration          time.Duration
	Cycles, Failures  int
	RSSBytes          int64
	Leak              bool
	SlopeBytesPerHour float64
	LastError         string
}

// soakHours sind die wählbaren Dauern.
var soakHours = map[string]time.Duration{
	"1": time.Hour, "8": 8 * time.Hour, "24": 24 * time.Hour, "72": 72 * time.Hour,
}

type soakView struct {
	Running   bool
	Never     bool
	State     string
	Since     string
	Planned   string
	Cycles    int
	Failures  int
	RSSBytes  int64
	Leak      string
	LastError string
}

func yesNo(b bool) string {
	if b {
		return "ja"
	}
	return "nein"
}

func (a *admin) soakView() (soakView, bool) {
	if a.d.Soak == nil {
		return soakView{}, false
	}
	st := a.d.Soak.Status()
	v := soakView{
		Running: st.Running, Never: st.Started.IsZero(), Cycles: st.Cycles, Failures: st.Failures,
		RSSBytes: st.RSSBytes, LastError: st.LastError,
		Leak: yesNo(st.Leak), Planned: st.Duration.String(),
	}
	if st.Leak {
		v.Leak = fmt.Sprintf("ja (%.1f MB je Stunde)", st.SlopeBytesPerHour/(1<<20))
	}
	const layout = "02.01.2006 15:04:05"
	switch {
	case v.Never:
		v.State = "Nicht gestartet"
	case st.Running:
		v.State = "läuft seit"
		v.Since = st.Started.Local().Format(layout)
	default:
		v.State = "beendet"
		v.Since = st.Started.Local().Format(layout) + " bis " + st.Ended.Local().Format(layout)
	}
	return v, true
}

func (a *admin) handleSoakStart(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Soak == nil {
		notFound(w)
		return
	}
	if r.PostForm.Get("confirm") != "ja" {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Bitte die Sicherheitsabfrage bestätigen.", systemExtras{})
		return
	}
	d, ok := soakHours[r.PostForm.Get("hours")]
	if !ok {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Bitte eine der angebotenen Dauern wählen.", systemExtras{})
		return
	}
	if err := a.d.Soak.Start(d); err != nil {
		a.renderSystem(w, s, http.StatusConflict, err.Error(), systemExtras{})
		return
	}
	a.done(w, r, s, "/admin/system", "Die Dauerprüfung läuft.")
}

func (a *admin) handleSoakStop(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Soak == nil {
		notFound(w)
		return
	}
	a.d.Soak.Stop()
	a.done(w, r, s, "/admin/system", "Die Dauerprüfung wird beendet.")
}

func (a *admin) handleSoakLog(w http.ResponseWriter, _ *http.Request, _ sessionRef) {
	b, _, err := readTail(filepath.Join(a.d.LogDir, "soak.jsonl"), logDownloadSize)
	if err != nil {
		http.Error(w, "Noch keine Dauerprüfung gelaufen", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "soak.jsonl"}))
	_, _ = w.Write(b)
}

func (a *admin) handleSoakSummary(w http.ResponseWriter, _ *http.Request, _ sessionRef) {
	v, ok := a.soakView()
	if !ok || v.Never {
		http.Error(w, "Noch keine Dauerprüfung gelaufen", http.StatusNotFound)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Dauerprüfung (%s %s)\n", v.State, v.Since)
	fmt.Fprintf(&b, "Vorgesehene Dauer: %s\nDurchgänge: %d\nFehler: %d\nSpeicher (Dienst und Client): %s\nLeck: %s\n",
		v.Planned, v.Cycles, v.Failures, formatBytes(v.RSSBytes), v.Leak)
	if v.LastError != "" {
		fmt.Fprintf(&b, "Letzter Fehler: %s\n", v.LastError)
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "dauerpruefung.txt"}))
	_, _ = io.WriteString(w, b.String())
}
