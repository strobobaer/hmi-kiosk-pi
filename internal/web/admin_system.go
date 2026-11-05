package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"hmikiosk/internal/config"
)

const (
	logViewLines    = 200
	logViewBytes    = 1 << 20
	logDownloadSize = 5 << 20
	selftestLimit   = 2 * time.Minute
	uploadDeadline  = 15 * time.Minute
)

// systemExtras sind Werte, die nur eine bestimmte Antwort zeigt.
type systemExtras struct {
	Selftest []SelftestStep
	Display  *displayFormData
	Time     string
}

type displayFormData struct {
	OverrideW, OverrideH, Graphics string
}

type systemData struct {
	Status   SystemStatus
	LogLines string
	HasLog   bool
	Now      string
	Time     string
	Display  displayFormData
	Selftest []SelftestStep
}

func (a *admin) renderSystem(w http.ResponseWriter, s sessionRef, status int, errMsg string, ex systemExtras) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	now := a.d.Clock.Now().Local()
	d := systemData{
		Status: a.d.Status(), Selftest: ex.Selftest, Time: ex.Time,
		Now:     now.Format("02.01.2006 15:04:05"),
		Display: displayFormData{OverrideW: intOrEmpty(cfg.Display.OverrideW), OverrideH: intOrEmpty(cfg.Display.OverrideH), Graphics: cfg.Display.GraphicsProfile},
	}
	if ex.Time == "" {
		d.Time = now.Format("2006-01-02T15:04")
	}
	if ex.Display != nil {
		d.Display = *ex.Display
	}
	d.LogLines, d.HasLog = logTailLines(a.d.LogDir)
	a.page(w, s, status, "system.html", "system", "System", errMsg, d)
}

func intOrEmpty(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

func (a *admin) handleSystemPage(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderSystem(w, s, http.StatusOK, "", systemExtras{})
}

// formatBytes zeigt GB mit Komma, wie in der Oberfläche üblich.
func formatBytes(n int64) string {
	const gb = 1 << 30
	const mb = 1 << 20
	if n >= gb {
		return strings.Replace(strconv.FormatFloat(float64(n)/gb, 'f', 1, 64), ".", ",", 1) + " GB"
	}
	return strconv.FormatInt(n/mb, 10) + " MB"
}

// --- Protokoll ---

// newestLog liefert die jüngste reguläre Datei. Verweise werden nicht verfolgt,
// damit über das Protokoll keine anderen Dateien lesbar werden.
func newestLog(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var best string
	var bestTime time.Time
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = filepath.Join(dir, e.Name()), info.ModTime()
		}
	}
	return best, best != ""
}

// readTail liest höchstens limit Bytes vom Dateiende.
func readTail(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	offset := max(info.Size()-limit, 0)
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, false, err
	}
	b, err := io.ReadAll(io.LimitReader(f, limit))
	return b, offset > 0, err
}

func logTailLines(dir string) (string, bool) {
	path, ok := newestLog(dir)
	if !ok {
		return "", false
	}
	b, truncated, err := readTail(path, logViewBytes)
	if err != nil {
		return "", false
	}
	lines := strings.Split(strings.TrimRight(strings.ToValidUTF8(string(b), "�"), "\n"), "\n")
	if truncated && len(lines) > 1 {
		lines = lines[1:] // die erste Zeile ist angeschnitten
	}
	if len(lines) > logViewLines {
		lines = lines[len(lines)-logViewLines:]
	}
	return strings.Join(lines, "\n"), true
}

func (a *admin) handleLogDownload(w http.ResponseWriter, r *http.Request, _ sessionRef) {
	path, ok := newestLog(a.d.LogDir)
	if !ok {
		http.Error(w, "Kein Protokoll vorhanden", http.StatusNotFound)
		return
	}
	b, _, err := readTail(path, logDownloadSize)
	if err != nil {
		http.Error(w, "Protokoll nicht lesbar", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "hmi-kiosk.log"}))
	_, _ = w.Write(b)
}

// --- Anzeige, Uhrzeit, Neustart ---

func (a *admin) handleDisplay(w http.ResponseWriter, r *http.Request, s sessionRef) {
	v := r.PostForm
	f := displayFormData{OverrideW: strings.TrimSpace(v.Get("override_w")), OverrideH: strings.TrimSpace(v.Get("override_h")), Graphics: v.Get("graphics")}
	err := a.update(func(c *config.Config) error {
		ow, err1 := atoiOrZero(f.OverrideW)
		oh, err2 := atoiOrZero(f.OverrideH)
		if err1 != nil || err2 != nil {
			return errors.New("Die Auflösung muss aus Zahlen bestehen.")
		}
		next := config.DisplayCfg{OverrideW: ow, OverrideH: oh, GraphicsProfile: f.Graphics}
		if err := checkDisplay(next); err != nil {
			return err
		}
		c.Display = next
		return nil
	})
	if err != nil {
		a.renderSystem(w, s, formStatus(err), err.Error(), systemExtras{Display: &f})
		return
	}
	a.done(w, r, s, "/admin/system", "Gespeichert.")
}

func atoiOrZero(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	return strconv.Atoi(s)
}

// parseLocalTime akzeptiert die Werte eines datetime-local-Felds. Dem Helfer geht
// nur der neu formatierte Wert zu, nie die Eingabe.
func parseLocalTime(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05"} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err != nil {
			continue
		}
		if t.Year() < 2020 || t.Year() > 2100 {
			return time.Time{}, errors.New("Jahr außerhalb des gültigen Bereichs")
		}
		return t, nil
	}
	return time.Time{}, errors.New("Format ungültig")
}

func (a *admin) handleSetTime(w http.ResponseWriter, r *http.Request, s sessionRef) {
	raw := r.PostForm.Get("time")
	t, err := parseLocalTime(raw)
	if err != nil {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Bitte Datum und Uhrzeit gültig angeben.", systemExtras{Time: raw})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), helperLimit)
	defer cancel()
	if err := a.d.Helper.Call(ctx, helperSetTime, t.Format(time.RFC3339)); err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, fmt.Sprintf("Die Uhrzeit konnte nicht gesetzt werden (%v).", err), systemExtras{Time: raw})
		return
	}
	a.done(w, r, s, "/admin/system", "Uhrzeit gesetzt.")
}

func (a *admin) confirmedHelper(w http.ResponseWriter, r *http.Request, s sessionRef, cmd, okMsg string) {
	if r.PostForm.Get("confirm") != "ja" {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Bitte die Sicherheitsabfrage bestätigen.", systemExtras{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), helperLimit)
	defer cancel()
	if err := a.d.Helper.Call(ctx, cmd, ""); err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, fmt.Sprintf("Der Befehl ist fehlgeschlagen (%v).", err), systemExtras{})
		return
	}
	a.done(w, r, s, "/admin/system", okMsg)
}

func (a *admin) handleReboot(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.confirmedHelper(w, r, s, helperReboot, "Das Terminal startet neu.")
}

func (a *admin) handleRestartService(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.confirmedHelper(w, r, s, helperRestartService, "Der Dienst startet neu.")
}

// --- Selbstprüfung ---

func (a *admin) handleSelftest(w http.ResponseWriter, r *http.Request, s sessionRef) {
	ctx, cancel := context.WithTimeout(r.Context(), selftestLimit)
	defer cancel()
	a.renderSystem(w, s, http.StatusOK, "", systemExtras{Selftest: a.d.Selftest(ctx)})
}

// --- Update ---

func (a *admin) handleUpdate(w http.ResponseWriter, r *http.Request, s sessionRef, mr *multipart.Reader) {
	// Es gibt nur einen Speicherplatz für ein Paket von bis zu 200 MB.
	if !a.updateMu.TryLock() {
		a.renderSystem(w, s, http.StatusConflict, "Es läuft bereits ein Update.", systemExtras{})
		return
	}
	defer a.updateMu.Unlock()
	// Die Server-Fristen sind für kurze Anfragen gedacht, nicht für diesen Upload.
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Now().Add(uploadDeadline))
	_ = rc.SetWriteDeadline(time.Now().Add(uploadDeadline))

	_, blob, err := readUpload(mr)
	if err != nil {
		uploadError(w, err)
		return
	}
	if len(blob) == 0 {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Bitte eine Update-Datei auswählen.", systemExtras{})
		return
	}
	version, err := a.d.Update.Install(r.Context(), blob)
	if err != nil {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Update fehlgeschlagen: "+err.Error(), systemExtras{})
		return
	}
	a.done(w, r, s, "/admin/system", "Update installiert: Version "+version+".")
}

func uploadError(w http.ResponseWriter, err error) {
	if isTooLarge(err) {
		http.Error(w, "Datei zu groß", http.StatusRequestEntityTooLarge)
		return
	}
	http.Error(w, "Upload fehlgeschlagen", http.StatusBadRequest)
}
