package web

import (
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type fakeSoak struct {
	mu      sync.Mutex
	started []time.Duration
	stops   int
	err     error
	status  SoakStatus
}

func (f *fakeSoak) Start(d time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.started = append(f.started, d)
	return nil
}
func (f *fakeSoak) Stop() { f.mu.Lock(); f.stops++; f.mu.Unlock() }
func (f *fakeSoak) Status() SoakStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

func TestSoakStartNeedsConfirmationAndKnownDuration(t *testing.T) {
	e, b := newLoggedIn(t)
	for _, d := range []string{"1", "8", "24", "72"} {
		r := b.post("/admin/system/soak/start", url.Values{"hours": {d}, "confirm": {"ja"}})
		if r.Code != http.StatusSeeOther {
			t.Fatalf("%s h: %d %s", d, r.Code, r.Body.String())
		}
	}
	want := []time.Duration{time.Hour, 8 * time.Hour, 24 * time.Hour, 72 * time.Hour}
	if len(e.soak.started) != 4 {
		t.Fatalf("gestartet: %v", e.soak.started)
	}
	for i, w := range want {
		if e.soak.started[i] != w {
			t.Errorf("Start %d: %v", i, e.soak.started[i])
		}
	}
	n := len(e.soak.started)
	for name, v := range map[string]url.Values{
		"ohne Bestätigung": {"hours": {"8"}},
		"Dauer 5":          {"hours": {"5"}, "confirm": {"ja"}},
		"Dauer leer":       {"confirm": {"ja"}},
		"Dauer Text":       {"hours": {"abc"}, "confirm": {"ja"}},
		"Dauer negativ":    {"hours": {"-8"}, "confirm": {"ja"}},
	} {
		if r := b.post("/admin/system/soak/start", v); r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, r.Code)
		}
	}
	if len(e.soak.started) != n {
		t.Error("ungültige Anfragen haben gestartet")
	}
}

func TestSoakStartRefusalIsShown(t *testing.T) {
	e, b := newLoggedIn(t)
	e.soak.err = errors.New("Es läuft eine Sitzung (Presse 7).")
	r := b.post("/admin/system/soak/start", url.Values{"hours": {"1"}, "confirm": {"ja"}})
	if r.Code != http.StatusConflict {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Es läuft eine Sitzung")
}

func TestSoakStop(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.post("/admin/system/soak/stop", nil); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	if e.soak.stops != 1 {
		t.Fatalf("Stopps: %d", e.soak.stops)
	}
}

func TestSoakRequiresCSRF(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.postRaw("/admin/system/soak/start", url.Values{"hours": {"1"}, "confirm": {"ja"}})
	if r.Code == http.StatusSeeOther || len(e.soak.started) != 0 {
		t.Fatalf("ohne CSRF-Token: %d", r.Code)
	}
}

func TestSoakStatusOnSystemPage(t *testing.T) {
	e, b := newLoggedIn(t)
	wantContains(t, b.get("/admin/system").Body.String(), "Dauerprüfung", "Nicht gestartet")
	e.soak.status = SoakStatus{
		Running: true, Started: time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC), Duration: 8 * time.Hour,
		Cycles: 42, Failures: 3, RSSBytes: 150 << 20, Leak: true, SlopeBytesPerHour: 3 << 20,
	}
	body := b.get("/admin/system").Body.String()
	wantContains(t, body, "läuft seit", "42", "Fehler", "150", "Leck", "ja", "Dauerprüfung stoppen")
	e.soak.status = SoakStatus{Started: e.soak.status.Started, Ended: e.soak.status.Started.Add(time.Hour), Cycles: 10}
	body = b.get("/admin/system").Body.String()
	wantContains(t, body, "beendet", "nein")
}

func TestSoakDownloads(t *testing.T) {
	e, b := newLoggedIn(t)
	if r := b.get("/admin/system/soak.jsonl"); r.Code != http.StatusNotFound {
		t.Errorf("jsonl vorher: %d", r.Code)
	}
	if r := b.get("/admin/system/soak.txt"); r.Code != http.StatusNotFound {
		t.Errorf("txt vorher: %d", r.Code)
	}
	if err := os.WriteFile(filepath.Join(e.logDir, "soak.jsonl"), []byte(`{"cycle":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := b.get("/admin/system/soak.jsonl")
	if r.Code != http.StatusOK || r.Body.String() != `{"cycle":1}`+"\n" {
		t.Fatalf("jsonl: %d %q", r.Code, r.Body.String())
	}
	wantContains(t, r.Header().Get("Content-Disposition"), "attachment", "soak.jsonl")

	e.soak.status = SoakStatus{Started: time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC), Ended: time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC),
		Cycles: 10, Failures: 1, RSSBytes: 100 << 20, Leak: true, SlopeBytesPerHour: 2 << 20}
	r = b.get("/admin/system/soak.txt")
	if r.Code != http.StatusOK {
		t.Fatalf("txt: %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Dauerprüfung", "Durchgänge: 10", "Fehler: 1", "Leck: ja")
}
