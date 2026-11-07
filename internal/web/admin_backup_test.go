package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"hmikiosk/internal/helperapi"
)

type fakeBackup struct {
	mu        sync.Mutex
	payload   []byte
	size      int64
	streamErr error
	startErr  error
	cancelErr error
	statusErr error
	status    helperapi.BackupStatus
	streams   int
	starts    []string
	cancels   int
	closed    int
}

func (f *fakeBackup) BackupStream(context.Context) (int64, io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.streamErr != nil {
		return 0, nil, f.streamErr
	}
	f.streams++
	return f.size, &fakeBody{Reader: bytes.NewReader(f.payload), f: f}, nil
}

type fakeBody struct {
	io.Reader
	f *fakeBackup
}

func (b *fakeBody) Close() error {
	b.f.mu.Lock()
	b.f.closed++
	b.f.mu.Unlock()
	return nil
}

func (f *fakeBackup) BackupUSB(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return f.startErr
	}
	f.starts = append(f.starts, name)
	return nil
}

func (f *fakeBackup) BackupCancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cancels++
	return f.cancelErr
}

func (f *fakeBackup) BackupStatus(context.Context) (helperapi.BackupStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, f.statusErr
}

func (f *fakeBackup) set(st helperapi.BackupStatus) {
	f.mu.Lock()
	f.status = st
	f.mu.Unlock()
}

func TestBackupDownloadStreamsFileWithName(t *testing.T) {
	e, b := newLoggedIn(t)
	e.backup.payload = []byte("gzip-bytes-hier")
	e.backup.size = 1 << 30
	e.backup.set(helperapi.BackupStatus{Mode: "download", Finished: true})
	r := b.post("/admin/system/backup/download", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if ct := r.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type %q", ct)
	}
	name := helperapi.BackupFileName("Terminal 1", e.clock.Now().Local())
	if cd := r.Header().Get("Content-Disposition"); cd != `attachment; filename=`+name {
		t.Errorf("Content-Disposition %q, erwartet Dateiname %s", cd, name)
	}
	if !strings.HasPrefix(name, "hmikiosk-Terminal-1-2026-11-05-") || !strings.HasSuffix(name, ".img.gz") {
		t.Errorf("Name %q", name)
	}
	if r.Body.String() != "gzip-bytes-hier" {
		t.Errorf("Rumpf %q", r.Body.String())
	}
	if e.backup.closed != 1 {
		t.Errorf("Strom nicht geschlossen: %d", e.backup.closed)
	}
}

func TestBackupDownloadNeedsConfirmation(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/system/backup/download", url.Values{})
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Bitte die Sicherheitsabfrage bestätigen.")
	if e.backup.streams != 0 {
		t.Error("Strom ohne Bestätigung geöffnet")
	}
}

func TestBackupDownloadRefusalIsShown(t *testing.T) {
	e, b := newLoggedIn(t)
	e.backup.streamErr = errors.New("Eine SD-Sicherung läuft gerade.")
	r := b.post("/admin/system/backup/download", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusConflict {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Eine SD-Sicherung läuft gerade.")
	if ct := r.Header().Get("Content-Type"); strings.Contains(ct, "gzip") {
		t.Errorf("Content-Type %q", ct)
	}
}

// Bricht der Helfer ab, endet der Strom sauber (EOF). Der Browser darf das nicht für eine
// fertige Datei halten: die Verbindung wird abgebrochen.
func TestBackupDownloadIncompleteAbortsConnection(t *testing.T) {
	e, b := newLoggedIn(t)
	e.backup.payload = []byte("halb")
	e.backup.set(helperapi.BackupStatus{Mode: "download", Error: "Die Sicherung wurde abgebrochen."})
	defer func() {
		if rec := recover(); rec != http.ErrAbortHandler {
			t.Fatalf("recover: %v", rec)
		}
	}()
	b.post("/admin/system/backup/download", url.Values{"confirm": {"ja"}})
	t.Fatal("Antwort wurde regulär beendet")
}

func TestBackupUSBStartPassesCleanName(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/system/backup/usb", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/system/backup" {
		t.Fatalf("Status %d nach %q", r.Code, r.Header().Get("Location"))
	}
	if len(e.backup.starts) != 1 || e.backup.starts[0] != "Terminal-1" {
		t.Fatalf("Start: %v", e.backup.starts)
	}
	wantContains(t, b.follow(r), "wurde gestartet")
}

func TestBackupUSBNeedsConfirmationAndShowsRefusal(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.post("/admin/system/backup/usb", url.Values{}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ohne Bestätigung: %d", r.Code)
	}
	if len(e.backup.starts) != 0 {
		t.Fatal("ohne Bestätigung gestartet")
	}
	e.backup.startErr = errors.New("FAT32 kann keine Dateien über 4 GB speichern. Bitte den Stick mit exFAT formatieren.")
	r := b.post("/admin/system/backup/usb", url.Values{"confirm": {"ja"}})
	if r.Code != http.StatusConflict {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "FAT32 kann keine Dateien")
}

func TestBackupCancel(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.post("/admin/system/backup/cancel", url.Values{})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if e.backup.cancels != 1 {
		t.Fatalf("Abbrüche: %d", e.backup.cancels)
	}
	e.backup.cancelErr = errors.New("Es läuft keine SD-Sicherung.")
	r = b.post("/admin/system/backup/cancel", url.Values{})
	if r.Code != http.StatusConflict {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Es läuft keine SD-Sicherung.")
}

func TestBackupPageIdle(t *testing.T) {
	_, b := newLoggedIn(t)
	r := b.get("/admin/system/backup")
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	body := r.Body.String()
	wantContains(t, body, "SD-Sicherung", "Herunterladen", "Auf USB-Stick schreiben",
		"Kopie im laufenden Betrieb", "Passwörter und Schlüssel", "Konfigurations-Export",
		`action="/admin/system/backup/download"`, `action="/admin/system/backup/usb"`)
	wantNotContains(t, body, `http-equiv="refresh"`, "Abbrechen", "<script>")
}

func TestBackupPageRunningShowsProgress(t *testing.T) {
	e, b := newLoggedIn(t)
	e.backup.set(helperapi.BackupStatus{
		Running: true, Mode: "usb", Target: "USB-Stick (exFAT)",
		ReadBytes: 8 << 30, TotalBytes: 32 << 30, ElapsedSec: 600,
	})
	body := b.get("/admin/system/backup").Body.String()
	wantContains(t, body, "8,0 GB von 32,0 GB", "25 %", "30 Minuten", `http-equiv="refresh"`, "USB-Stick (exFAT)",
		`action="/admin/system/backup/cancel"`)
	wantNotContains(t, body, `action="/admin/system/backup/download"`)
}

func TestBackupPageFinishedAndFailed(t *testing.T) {
	e, b := newLoggedIn(t)
	e.backup.set(helperapi.BackupStatus{Mode: "usb", Finished: true, File: "hmikiosk-X-2026-11-05-1200.img.gz", ReadBytes: 1 << 30, TotalBytes: 1 << 30})
	body := b.get("/admin/system/backup").Body.String()
	wantContains(t, body, "hmikiosk-X-2026-11-05-1200.img.gz", "Stick kann entfernt werden")
	wantNotContains(t, body, `http-equiv="refresh"`)

	e.backup.set(helperapi.BackupStatus{Mode: "usb", Error: "Der USB-Stick ist voll."})
	body = b.get("/admin/system/backup").Body.String()
	wantContains(t, body, "Der USB-Stick ist voll.")
	wantNotContains(t, body, "Stick kann entfernt werden")
}

func TestBackupPageHelperUnreachable(t *testing.T) {
	e, b := newLoggedIn(t)
	e.backup.statusErr = errors.New("Der Helfer ist nicht erreichbar (x)")
	r := b.get("/admin/system/backup")
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Der Helfer ist nicht erreichbar")
}

func TestSystemPageLinksToBackupAndShowsRunning(t *testing.T) {
	e, b := newLoggedIn(t)
	body := b.get("/admin/system").Body.String()
	wantContains(t, body, `href="/admin/system/backup"`)
	wantNotContains(t, body, "SD-Sicherung läuft")
	e.backup.set(helperapi.BackupStatus{Running: true, Mode: "download", ReadBytes: 1, TotalBytes: 2})
	wantContains(t, b.get("/admin/system").Body.String(), "SD-Sicherung läuft")
}

func TestBackupRoutesNeedLogin(t *testing.T) {
	e := newEnv(t)
	br := e.browser()
	for _, p := range []string{"/admin/system/backup"} {
		if r := br.get(p); r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s: %d %q", p, r.Code, r.Header().Get("Location"))
		}
	}
	for _, p := range []string{"download", "usb", "cancel"} {
		r := br.postRaw("/admin/system/backup/"+p, url.Values{"confirm": {"ja"}})
		if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/login" {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
	if e.backup.streams != 0 || len(e.backup.starts) != 0 || e.backup.cancels != 0 {
		t.Error("Aktion ohne Anmeldung")
	}
}
