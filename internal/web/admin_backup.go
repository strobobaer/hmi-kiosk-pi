package web

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"hmikiosk/internal/helperapi"
)

// BackupAPI ist die SD-Sicherung im root-Helfer (erfüllt *helperapi.Client). Der Dienst
// liest die Karte nie selbst; er reicht den Strom des Helfers an den Browser weiter.
type BackupAPI interface {
	BackupStream(ctx context.Context) (size int64, body io.ReadCloser, err error)
	BackupUSB(ctx context.Context, terminalName string) error
	BackupCancel(ctx context.Context) error
	BackupStatus(ctx context.Context) (helperapi.BackupStatus, error)
}

const (
	backupCallTimeout = 10 * time.Second
	backupRefreshSec  = 3
)

type backupView struct {
	Running   bool
	Download  bool
	Target    string
	Progress  string
	Percent   int
	Remaining string
	Finished  bool
	File      string
	LastError string
	Unknown   bool // Stand nicht abrufbar
}

func (a *admin) backupStatus() (helperapi.BackupStatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), backupCallTimeout)
	defer cancel()
	return a.d.Backup.BackupStatus(ctx)
}

func newBackupView(st helperapi.BackupStatus) backupView {
	v := backupView{
		Running: st.Running, Download: st.Mode == "download", Target: st.Target,
		Finished: st.Finished, File: st.File, LastError: st.Error,
	}
	if st.Running && st.TotalBytes > 0 {
		v.Progress = formatBytes(st.ReadBytes) + " von " + formatBytes(st.TotalBytes)
		v.Percent = int(min(st.ReadBytes*100/st.TotalBytes, 100))
		if st.ReadBytes > 0 && st.ElapsedSec > 0 {
			rest := st.ElapsedSec * float64(st.TotalBytes-st.ReadBytes) / float64(st.ReadBytes)
			v.Remaining = formatRemaining(rest)
		}
	}
	return v
}

func formatRemaining(sec float64) string {
	min := int(sec/60 + 0.999)
	switch {
	case min <= 1:
		return "etwa 1 Minute"
	case min < 60:
		return fmt.Sprintf("etwa %d Minuten", min)
	}
	return fmt.Sprintf("etwa %d Std. %d Min.", min/60, min%60)
}

func (a *admin) renderBackup(w http.ResponseWriter, s sessionRef, status int, errMsg string) {
	v := backupView{}
	refresh := 0
	st, err := a.backupStatus()
	if err != nil {
		v.Unknown = true
		if errMsg == "" {
			errMsg = err.Error()
		}
	} else {
		v = newBackupView(st)
		if v.Running {
			refresh = backupRefreshSec
		}
	}
	a.pageRefresh(w, s, status, "backup.html", "system", "SD-Sicherung", errMsg, refresh, v)
}

func (a *admin) handleBackupPage(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	if a.d.Backup == nil {
		notFound(w)
		return
	}
	a.renderBackup(w, s, http.StatusOK, "")
}

func (a *admin) terminalName() string {
	if cfg, _, err := a.d.Store.Load(); err == nil {
		return cfg.Terminal.Name
	}
	return ""
}

// handleBackupDownload streamt das gzip-Abbild als Datei. Die Antwort dauert länger als
// die Fristen des Servers; sie werden für diese Verbindung aufgehoben.
func (a *admin) handleBackupDownload(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Backup == nil {
		notFound(w)
		return
	}
	if r.PostForm.Get("confirm") != "ja" {
		a.renderBackup(w, s, http.StatusUnprocessableEntity, "Bitte die Sicherheitsabfrage bestätigen.")
		return
	}
	_, body, err := a.d.Backup.BackupStream(r.Context())
	if err != nil {
		a.renderBackup(w, s, http.StatusConflict, err.Error())
		return
	}
	defer body.Close()

	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.SetReadDeadline(time.Time{})

	name := helperapi.BackupFileName(a.terminalName(), a.d.Clock.Now().Local())
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	if _, err := io.Copy(w, body); err != nil {
		return // der Browser ist weg; der Helfer sieht es am geschlossenen Strom
	}
	// Das Ende des Stroms beweist nicht, dass das Abbild vollständig ist (Abbruch im
	// Helfer endet ebenfalls mit EOF). Nur der Stand des Helfers sagt es. Sonst wird
	// die Verbindung abgebrochen, damit der Browser den Download als fehlgeschlagen zeigt.
	st, err := a.backupStatus()
	if err != nil || !st.Finished || st.Error != "" || st.Running {
		panic(http.ErrAbortHandler)
	}
}

func (a *admin) handleBackupUSB(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Backup == nil {
		notFound(w)
		return
	}
	if r.PostForm.Get("confirm") != "ja" {
		a.renderBackup(w, s, http.StatusUnprocessableEntity, "Bitte die Sicherheitsabfrage bestätigen.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), backupCallTimeout+30*time.Second)
	defer cancel()
	if err := a.d.Backup.BackupUSB(ctx, helperapi.SanitizeBackupName(a.terminalName())); err != nil {
		a.renderBackup(w, s, http.StatusConflict, err.Error())
		return
	}
	a.done(w, r, s, "/admin/system/backup", "Die Sicherung auf den USB-Stick wurde gestartet.")
}

func (a *admin) handleBackupCancel(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Backup == nil {
		notFound(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), backupCallTimeout)
	defer cancel()
	if err := a.d.Backup.BackupCancel(ctx); err != nil {
		a.renderBackup(w, s, http.StatusConflict, err.Error())
		return
	}
	a.done(w, r, s, "/admin/system/backup", "Die Sicherung wird abgebrochen.")
}
