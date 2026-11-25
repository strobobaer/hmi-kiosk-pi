package pdhterminal

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/inputmode"
)

// sessionState legt input und input-setting der laufenden Sitzung ab; leer lässt die Datei weg.
func sessionState(t *testing.T, input, setting string) {
	t.Helper()
	dir := t.TempDir()
	for name, v := range map[string]string{"input": input, "input-setting": setting} {
		if v == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(inputmode.SetDir(dir))
}

func setInputMode(t *testing.T, e *adminEnv, mode string) {
	t.Helper()
	c, _, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	c.Kiosk.InputMode = mode
	if err := e.store.Save(c); err != nil {
		t.Fatal(err)
	}
}

func savedInputMode(t *testing.T, e *adminEnv) string {
	t.Helper()
	c, _, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return c.Kiosk.InputMode
}

func linuxEnv(t *testing.T) *adminEnv {
	return newAdminEnvWith(t, Client{}, func(p *adminProduct) { p.windows = false })
}

func TestAdminInputSectionShowsChoiceAndDefaultAuto(t *testing.T) {
	sessionState(t, "", "")
	e := linuxEnv(t)
	body := e.get("/admin/pdh").Body.String()
	for _, want := range []string{"<h2>Bedienung</h2>", `action="/admin/pdh/input"`, `name="input_mode"`,
		`value="touch"`, `value="mouse"`, `value="auto" selected`} {
		if !strings.Contains(body, want) {
			t.Errorf("Seite ohne %q", want)
		}
	}
}

func TestAdminInputExistingDeviceWithoutValueShowsTouch(t *testing.T) {
	sessionState(t, "", "")
	e := linuxEnv(t)
	setInputMode(t, e, "")
	if body := e.get("/admin/pdh").Body.String(); !strings.Contains(body, `value="touch" selected`) {
		t.Fatal("leere Bedienart wird nicht als Touch gezeigt")
	}
}

func TestAdminInputShowsCurrentState(t *testing.T) {
	cases := []struct {
		input, setting, want string
	}{
		{"mouse", "auto", "zurzeit: Maus erkannt – Zeiger sichtbar"},
		{"touch", "auto", "zurzeit: keine Maus erkannt – Zeiger ausgeblendet"},
		{"mouse", "mouse", "zurzeit: Maus – Zeiger sichtbar"},
		{"touch", "touch", "zurzeit: Touch – Zeiger ausgeblendet"},
		{"", "", "zurzeit: nicht ermittelt"},
	}
	for _, c := range cases {
		sessionState(t, c.input, c.setting)
		e := linuxEnv(t)
		setInputMode(t, e, c.setting)
		if c.setting == "" {
			setInputMode(t, e, "auto")
		}
		if body := e.get("/admin/pdh").Body.String(); !strings.Contains(body, c.want) {
			t.Errorf("%s/%s: %q fehlt in %s", c.input, c.setting, c.want, body)
		}
	}
}

func TestAdminInputSaveWritesConfigAndOffersRebootWhenSessionDiffers(t *testing.T) {
	sessionState(t, "touch", "touch")
	e := linuxEnv(t)
	setInputMode(t, e, "touch")
	r := e.post("/admin/pdh/input", url.Values{"input_mode": {"auto"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d %s", r.Code, r.Body.String())
	}
	if got := savedInputMode(t, e); got != config.InputAuto {
		t.Fatalf("gespeichert %q", got)
	}
	body := r.Body.String()
	for _, want := range []string{"nach dem Neustart des Terminals", `action="/admin/system/reboot"`, "Terminal neu starten"} {
		if !strings.Contains(body, want) {
			t.Errorf("Antwort ohne %q", want)
		}
	}
}

func TestAdminInputSaveUnchangedNeedsNoReboot(t *testing.T) {
	sessionState(t, "mouse", "mouse")
	e := linuxEnv(t)
	setInputMode(t, e, "mouse")
	r := e.post("/admin/pdh/input", url.Values{"input_mode": {"mouse"}})
	if r.Code != http.StatusOK {
		t.Fatalf("Speichern: %d", r.Code)
	}
	if body := r.Body.String(); strings.Contains(body, "/admin/system/reboot") {
		t.Fatal("Neustart angeboten, obwohl die Sitzung schon so läuft")
	}
}

func TestAdminInputSaveRejectsUnknown(t *testing.T) {
	sessionState(t, "", "")
	e := linuxEnv(t)
	setInputMode(t, e, "touch")
	r := e.post("/admin/pdh/input", url.Values{"input_mode": {"stift"}})
	if r.Code != http.StatusBadRequest || !strings.Contains(r.Body.String(), "Unbekannte Bedienart") {
		t.Fatalf("unbekannt: %d", r.Code)
	}
	if got := savedInputMode(t, e); got != config.InputTouch {
		t.Fatalf("Wert wurde trotzdem gespeichert: %q", got)
	}
	if r := e.post("/admin/pdh/input", url.Values{}); r.Code != http.StatusBadRequest {
		t.Fatalf("ohne Wert: %d", r.Code)
	}
}

func TestAdminInputWindowsShowsOnlyNote(t *testing.T) {
	sessionState(t, "", "")
	e := newAdminEnvWith(t, Client{}, func(p *adminProduct) { p.windows = true })
	body := e.get("/admin/pdh").Body.String()
	if !strings.Contains(body, "Unter Windows ist der Mauszeiger immer sichtbar") {
		t.Fatal("Hinweis fehlt")
	}
	if strings.Contains(body, `name="input_mode"`) || strings.Contains(body, "/admin/pdh/input") {
		t.Fatal("unter Windows darf es keine Auswahl geben")
	}
	before := savedInputMode(t, e)
	if r := e.post("/admin/pdh/input", url.Values{"input_mode": {"mouse"}}); r.Code != http.StatusBadRequest {
		t.Fatalf("Speichern unter Windows: %d", r.Code)
	}
	if savedInputMode(t, e) != before {
		t.Fatal("Konfiguration unter Windows geändert")
	}
}
