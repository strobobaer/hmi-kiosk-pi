package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/selftest"
	"hmikiosk/internal/session"
)

func TestSoakStartIsRefusedWhileBusy(t *testing.T) {
	t.Run("Daueranzeige", func(t *testing.T) {
		f := newFixture(t)
		f.saveConfig(func(c *config.Config) { oneHMI(c); c.Kiosk.PermanentHMI = "presse" })
		f.start()
		if err := f.s.soak.Start(time.Hour); err == nil || !strings.Contains(err.Error(), "Daueranzeige") {
			t.Fatalf("err=%v", err)
		}
		if f.s.soak.Status().Running {
			t.Fatal("darf nicht laufen")
		}
	})
	t.Run("Sitzung", func(t *testing.T) {
		port := listenVNC(t, "")
		f := newFixture(t)
		f.saveConfig(func(c *config.Config) { oneHMI(c); c.HMIs[0].Port = port })
		f.start()
		f.local("POST", "/api/hmi/presse/open")
		eventually(t, "Sitzung", func() bool { return f.bound.Snapshot().State == session.StateStarting })
		if err := f.s.soak.Start(time.Hour); err == nil || !strings.Contains(err.Error(), "Sitzung") {
			t.Fatalf("err=%v", err)
		}
		if f.s.soak.Status().Running {
			t.Fatal("darf nicht laufen")
		}
		// Nach der Weigerung ist die Sperre wieder frei.
		if !f.s.selftestMu.TryLock() {
			t.Fatal("Sperre blieb hängen")
		}
		f.s.selftestMu.Unlock()
	})
	t.Run("Selbstprüfung läuft", func(t *testing.T) {
		f := newFixture(t)
		f.saveConfig(oneHMI)
		f.start()
		f.s.selftestMu.Lock()
		defer f.s.selftestMu.Unlock()
		if err := f.s.soak.Start(time.Hour); err == nil || !strings.Contains(err.Error(), "läuft bereits") {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestSoakBlocksTilesAndSelftestUntilStopped(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	if err := f.s.soak.Start(time.Hour); err != nil {
		t.Fatal(err)
	}
	st := f.s.soak.Status()
	if !st.Running || st.Started.IsZero() || st.Duration != time.Hour {
		t.Fatalf("%+v", st)
	}
	code, body := f.local("POST", "/api/hmi/presse/open")
	if code != http.StatusConflict || !strings.Contains(body, "Dauerprüfung läuft") {
		t.Fatalf("Kachel: %d %s", code, body)
	}
	if n := len(f.runner.started()); n > 1 {
		t.Fatalf("die Kachel startete einen Client: %d", n)
	}
	if got := f.bound.Snapshot().Notice; got != "Dauerprüfung läuft" {
		t.Fatalf("Hinweis %q", got)
	}
	if err := f.s.soak.Start(time.Hour); err == nil {
		t.Fatal("zweiter Start muss scheitern")
	}
	res := f.s.runSelftest(context.Background())
	if len(res) != 1 || res[0].OK || !strings.Contains(res[0].Detail, "Dauerprüfung") {
		t.Fatalf("Selbstprüfung: %+v", res)
	}
	_, status := f.local("GET", "/api/selftest/status")
	if !strings.Contains(status, "Dauerprüfung") {
		t.Fatalf("Status für den Befehl: %s", status)
	}

	f.s.soak.Stop()
	eventually(t, "Ende der Dauerprüfung", func() bool { return !f.s.soak.Status().Running })
	if end := f.s.soak.Status().Ended; end.IsZero() {
		t.Fatal("Ende fehlt")
	}
	if !f.s.selftestMu.TryLock() {
		t.Fatal("Sperre blieb hängen")
	}
	f.s.selftestMu.Unlock()
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Kachel danach: %d", code)
	}
}

func TestSoakEndsWithServiceShutdown(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	if err := f.s.soak.Start(24 * time.Hour); err != nil {
		t.Fatal(err)
	}
	f.stop()
	if f.s.soak.Status().Running {
		t.Fatal("läuft nach dem Ende des Dienstes weiter")
	}
}

func TestSoakSamplesArePersistedAndKept(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	j := f.s.soak
	j.begin(time.Hour)
	j.record(selftest.SoakSample{Cycle: 1, RSSBytes: 100 << 20})
	j.record(selftest.SoakSample{Cycle: 2, RSSBytes: 110 << 20, Failures: 1, Error: "kaputt", Leak: true, SlopeBytesPerHour: 3 << 20})
	j.record(selftest.SoakSample{Cycle: 3, RSSBytes: 120 << 20, Failures: 1})
	st := j.Status()
	if st.Cycles != 3 || st.Failures != 1 || st.RSSBytes != 120<<20 || !st.Leak || st.LastError != "kaputt" {
		t.Fatalf("%+v", st)
	}
	b, err := os.ReadFile(filepath.Join(f.data, logDirName, soakLogName))
	if err != nil || strings.Count(string(b), "\n") != 3 {
		t.Fatalf("soak.jsonl: %q %v", b, err)
	}
	last, err := os.ReadFile(filepath.Join(f.run, soakLastName))
	if err != nil || !strings.Contains(string(last), `"Cycles":3`) {
		t.Fatalf("Ergebnisdatei: %q %v", last, err)
	}
	j.finish(nil)
	if j.Status().Running {
		t.Fatal("Ende nicht gebucht")
	}
}
