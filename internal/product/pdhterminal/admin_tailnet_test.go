package pdhterminal

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/tailnet"
)

const tailnetTestKey = "tskey-auth-kGEHEIM123CNTRL-zzzzyyyyxxxx"

type fakeTailnet struct {
	st tailnet.Status
	statusErr,
	upErr, downErr error
	upKey, upHost string
	ups, downs    int
}

func (f *fakeTailnet) Status(context.Context) (tailnet.Status, error) { return f.st, f.statusErr }
func (f *fakeTailnet) Up(_ context.Context, key, host string) error {
	f.ups++
	f.upKey, f.upHost = key, host
	if f.upErr == nil {
		f.st = tailnet.Status{Installed: true, LoggedIn: true, Name: host, DNSName: host + ".tail1.ts.net", IP: "100.64.0.5", State: "Running"}
	}
	return f.upErr
}
func (f *fakeTailnet) Down(context.Context) error { f.downs++; return f.downErr }

func tailnetEnv(t *testing.T, ft *fakeTailnet) *adminEnv {
	t.Helper()
	return newAdminEnvWith(t, newFakePDH(t).client(), func(p *adminProduct) { p.tailnet = ft })
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })
	return &buf
}

func TestAdminTailnetNoKeyInLogOrPage(t *testing.T) {
	logs := captureLog(t)
	ft := &fakeTailnet{st: tailnet.Status{Installed: true, State: "NeedsLogin"}}
	e := tailnetEnv(t, ft)
	e.setPDH(config.PDHCfg{URL: "https://pdh.firma.de", DeviceName: "Halle 3"})

	r := e.post("/admin/tailnet/up", url.Values{"authKey": {"  " + tailnetTestKey + "\n"}})
	if r.Code != http.StatusOK {
		t.Fatalf("POST: %d\n%s", r.Code, r.Body.String())
	}
	if ft.ups != 1 || ft.upKey != tailnetTestKey || ft.upHost != "halle-3" {
		t.Fatalf("Up: %d %q %q", ft.ups, ft.upKey, ft.upHost)
	}
	if strings.Contains(r.Body.String(), tailnetTestKey) || strings.Contains(r.Body.String(), "GEHEIM") {
		t.Fatal("Key in der Antwortseite")
	}
	if strings.Contains(logs.String(), "GEHEIM") {
		t.Fatalf("Key im Protokoll: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "tailnet: angemeldet als halle-3") {
		t.Errorf("Audit-Zeile fehlt: %s", logs.String())
	}
	body := r.Body.String()
	for _, want := range []string{"angemeldet als halle-3", "halle-3.tail1.ts.net", "100.64.0.5"} {
		if !strings.Contains(body, want) {
			t.Errorf("%q fehlt auf der Seite", want)
		}
	}
}

// Auch ein Fehler des Steuerprogramms und ein ungültiger Key dürfen den Key nicht zurückgeben.
func TestAdminTailnetErrorsKeepKeyOut(t *testing.T) {
	logs := captureLog(t)
	ft := &fakeTailnet{st: tailnet.Status{Installed: true, State: "NeedsLogin"}, upErr: errors.New("Anmelden fehlgeschlagen: key invalid")}
	e := tailnetEnv(t, ft)
	r := e.post("/admin/tailnet/up", url.Values{"authKey": {tailnetTestKey}})
	if r.Code == http.StatusOK || !strings.Contains(r.Body.String(), "Anmelden fehlgeschlagen") {
		t.Fatalf("Fehler nicht angezeigt: %d", r.Code)
	}
	if strings.Contains(r.Body.String(), "GEHEIM") || strings.Contains(logs.String(), "GEHEIM") {
		t.Fatal("Key in Seite oder Protokoll")
	}
	for _, bad := range []string{"", "   ", "ab\ncd", "a b", strings.Repeat("k", 513)} {
		ft.ups = 0
		r := e.post("/admin/tailnet/up", url.Values{"authKey": {bad}})
		if r.Code != http.StatusBadRequest || ft.ups != 0 {
			t.Errorf("Key %q: %d, Up %d", bad, r.Code, ft.ups)
		}
		if bad != "" && strings.TrimSpace(bad) != "" && strings.Contains(r.Body.String(), bad) {
			t.Errorf("Key %q in der Antwort", bad)
		}
	}
}

func TestAdminTailnetPageStates(t *testing.T) {
	ft := &fakeTailnet{}
	e := tailnetEnv(t, ft)
	get := func() string {
		r := e.get("/admin/tailnet")
		if r.Code != http.StatusOK {
			t.Fatalf("GET: %d", r.Code)
		}
		return r.Body.String()
	}
	const upForm, downForm = `action="/admin/tailnet/up"`, `action="/admin/tailnet/down"`
	body := get()
	if !strings.Contains(body, "nicht installiert") || strings.Contains(body, `name="authKey"`) ||
		strings.Contains(body, upForm) || strings.Contains(body, downForm) {
		t.Errorf("nicht installiert:\n%s", body)
	}
	// Nicht angemeldet: nur „Anmelden“.
	ft.st = tailnet.Status{Installed: true, State: "NeedsLogin"}
	body = get()
	if !strings.Contains(body, "nicht angemeldet") || !strings.Contains(body, `type="password"`) ||
		!strings.Contains(body, `name="authKey"`) || !strings.Contains(body, `autocomplete="off"`) ||
		!strings.Contains(body, upForm) || !strings.Contains(body, ">Anmelden</button>") {
		t.Errorf("nicht angemeldet:\n%s", body)
	}
	if strings.Contains(body, downForm) || strings.Contains(body, `class="btn danger">Abmelden</button>`) {
		t.Errorf("nicht angemeldet mit Knopf „Abmelden“:\n%s", body)
	}
	// Angemeldet: nur „Abmelden“.
	ft.st = tailnet.Status{Installed: true, LoggedIn: true, Name: "kasse", DNSName: "kasse.t.ts.net", IP: "100.64.0.9", State: "Running"}
	body = get()
	if !strings.Contains(body, "angemeldet als kasse · kasse.t.ts.net · 100.64.0.9") ||
		!strings.Contains(body, downForm) || !strings.Contains(body, `class="btn danger">Abmelden</button>`) {
		t.Errorf("angemeldet:\n%s", body)
	}
	if strings.Contains(body, upForm) || strings.Contains(body, `name="authKey"`) || strings.Contains(body, ">Anmelden</button>") {
		t.Errorf("angemeldet mit Formular „Anmelden“:\n%s", body)
	}
	// Zustand unbekannt: beide, damit der Techniker handeln kann.
	ft.statusErr = errors.New("Helfer nicht erreichbar")
	if body = get(); !strings.Contains(body, "nicht abfragbar") || !strings.Contains(body, upForm) || !strings.Contains(body, downForm) {
		t.Errorf("Statusfehler:\n%s", body)
	}
	if !strings.Contains(e.get("/admin/pdh").Body.String(), `href="/admin/tailnet"`) {
		t.Error("Menüpunkt Tailnet fehlt")
	}
}

func TestAdminTailnetDown(t *testing.T) {
	logs := captureLog(t)
	ft := &fakeTailnet{st: tailnet.Status{Installed: true, LoggedIn: true, Name: "kasse"}}
	e := tailnetEnv(t, ft)
	r := e.post("/admin/tailnet/down", url.Values{})
	if r.Code != http.StatusOK || ft.downs != 1 || !strings.Contains(r.Body.String(), "Abgemeldet") {
		t.Fatalf("Abmelden: %d %d", r.Code, ft.downs)
	}
	if !strings.Contains(logs.String(), "tailnet: abgemeldet") {
		t.Errorf("Audit: %s", logs.String())
	}
	ft.downErr = errors.New("Abmelden fehlgeschlagen: kaputt")
	if r := e.post("/admin/tailnet/down", url.Values{}); r.Code == http.StatusOK || !strings.Contains(r.Body.String(), "kaputt") {
		t.Errorf("Fehler nicht angezeigt: %d", r.Code)
	}
}

func TestAdminTailnetAbsentWithoutCtl(t *testing.T) {
	e := newAdminEnv(t, newFakePDH(t).client())
	if r := e.get("/admin/tailnet"); r.Code != http.StatusNotFound {
		t.Errorf("GET ohne Steuerung: %d", r.Code)
	}
	if strings.Contains(e.get("/admin/pdh").Body.String(), "/admin/tailnet") {
		t.Error("Menüpunkt ohne Steuerung")
	}
}
