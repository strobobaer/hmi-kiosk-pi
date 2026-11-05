package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/browser"
	"hmikiosk/internal/config"
	"hmikiosk/internal/selftest"
	"hmikiosk/internal/session"
)

// localJSON spricht die lokale Seite mit JSON-Körper an.
func (f *fixture) localJSON(method, path, body string) (int, string) {
	f.t.Helper()
	req, err := http.NewRequest(method, "http://"+f.bound.Local+path, strings.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}
	req.Host = "127.0.0.1:8080"
	req.Header.Set("Origin", "http://127.0.0.1:8080")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestSelftestStatusReportsFreeAndBusy(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	code, body := f.local("GET", "/api/selftest/status")
	var st SelftestStatus
	if err := json.Unmarshal([]byte(body), &st); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s %v", code, body, err)
	}
	if st.Busy != "" || st.PID == 0 {
		t.Fatalf("frei erwartet: %+v", st)
	}

	f.stop()
	g := newFixture(t)
	g.saveConfig(func(c *config.Config) { oneHMI(c); c.Kiosk.PermanentHMI = "presse" })
	g.start()
	_, body = g.local("GET", "/api/selftest/status")
	json.Unmarshal([]byte(body), &st)
	if !strings.Contains(st.Busy, "Daueranzeige") {
		t.Fatalf("Daueranzeige muss die Prüfung sperren: %+v", st)
	}
}

func TestSelftestRefusesWhileSessionIsActive(t *testing.T) {
	port := listenVNC(t, "")
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) {
		oneHMI(c)
		c.HMIs[0].Port = port
	})
	f.start()
	if code, _ := f.local("POST", "/api/hmi/presse/open"); code != http.StatusNoContent {
		t.Fatalf("Open %d", code)
	}
	eventually(t, "Sitzung", func() bool { return f.bound.Snapshot().State == session.StateStarting })
	started := len(f.runner.started())

	code, body := f.local("POST", "/api/selftest/run")
	var res []selftest.StepResult
	if err := json.Unmarshal([]byte(body), &res); err != nil || code != http.StatusOK {
		t.Fatalf("%d %s %v", code, body, err)
	}
	if len(res) != 1 || res[0].OK || !strings.Contains(res[0].Detail, "Presse 7") || !strings.Contains(res[0].Detail, "Sitzung") {
		t.Fatalf("%+v", res)
	}
	if n := len(f.runner.started()); n != started {
		t.Fatalf("die Prüfung startete %d Client(s) während einer Sitzung", n-started)
	}
	steps := f.s.adminSelftest(context.Background())
	if len(steps) != 1 || steps[0].OK {
		t.Fatalf("Admin-Seite: %+v", steps)
	}
}

func TestSelftestRunsOnlyOnce(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	f.s.selftestMu.Lock()
	defer f.s.selftestMu.Unlock()
	_, body := f.local("POST", "/api/selftest/run")
	if !strings.Contains(body, "läuft bereits") {
		t.Fatalf("%s", body)
	}
}

func TestSelftestRunNeedsOriginAndHost(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	req, _ := http.NewRequest("POST", "http://"+f.bound.Local+"/api/selftest/run", nil)
	req.Host = "127.0.0.1:8080" // ohne Origin
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("ohne Origin: %d", resp.StatusCode)
	}
}

func TestTouchHitsReachTheBoardAndPageSwitchesBrowser(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(oneHMI)
	f.start()
	if code, _ := f.localJSON("POST", "/api/selftest/touch", `{"target":2}`); code != http.StatusNoContent {
		t.Fatalf("Treffer: %d", code)
	}
	if f.s.touch.Count() != 1 {
		t.Fatalf("Brett: %d", f.s.touch.Count())
	}
	snap := session.Snapshot{State: session.StateTiles}
	if got := f.s.browserTarget(snap); got != browser.TargetTiles {
		t.Fatalf("normal: %v", got)
	}
	f.s.showSelftestPage("touch")
	if got := f.s.browserTarget(snap); got != browser.TargetTouchTest {
		t.Fatalf("touch: %v", got)
	}
	f.s.showSelftestPage("bar")
	if got := f.s.browserTarget(snap); got != browser.TargetBar {
		t.Fatalf("bar: %v", got)
	}
	f.s.showSelftestPage("")
	if got := f.s.browserTarget(snap); got != browser.TargetTiles {
		t.Fatalf("zurück: %v", got)
	}
}

func stubService(t *testing.T, busy string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/selftest/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, SelftestStatus{Busy: busy, PID: 1})
	})
	mux.HandleFunc("POST /api/selftest/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") != "http://127.0.0.1:8080" {
			http.Error(w, "Origin", http.StatusForbidden)
			return
		}
		writeJSON(w, []selftest.StepResult{{Name: "terminals", OK: true, Detail: "ok"}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRunSoakRefusesWhenServiceReportsASession(t *testing.T) {
	srv := stubService(t, "Es läuft eine Sitzung (Presse 7).")
	f := newFixture(t)
	f.saveConfig(oneHMI)
	var out []string
	_, err := RunSoak(context.Background(), SoakOptions{
		DataDir: f.data, RunDir: f.run, Duration: time.Hour, Runner: f.runner, Base: srv.URL,
		Out: func(s string) { out = append(out, s) }, Warn: func(string) {},
	})
	if err == nil || !strings.Contains(err.Error(), "Sitzung") {
		t.Fatalf("err=%v", err)
	}
	if len(out) != 0 || len(f.runner.started()) != 0 {
		t.Fatalf("es darf nichts laufen: %v", out)
	}
}

func TestRunSelftestRemoteSendsOriginAndDecodes(t *testing.T) {
	srv := stubService(t, "")
	res, err := RunSelftestRemote(context.Background(), srv.URL)
	if err != nil || len(res) != 1 || res[0].Name != "terminals" || !res[0].OK {
		t.Fatalf("%+v %v", res, err)
	}
	srv.Close()
	if _, err := RunSelftestRemote(context.Background(), srv.URL); err == nil {
		t.Fatal("ohne Dienst muss ein Fehler kommen")
	}
}
