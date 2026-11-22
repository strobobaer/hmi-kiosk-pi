package pdhterminal

import (
	"context"
	"encoding/json"
	"hmikiosk/internal/config"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	t0 = time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Minute)
	t2 = t0.Add(2 * time.Minute)
)

// funcPDH ist ein PDH, das die Funktionen-Schnittstelle nach dem Vertrag bedient.
type funcPDH struct {
	mu        sync.Mutex
	funcs     FuncSet
	theme     map[string]string
	noPut     bool // ältere Version: PUT endet mit 404
	down      bool // nicht erreichbar (503)
	putCalls  int
	getCalls  int
	srv       *httptest.Server
	lastToken string
}

func newFuncPDH(t *testing.T) *funcPDH {
	p := &funcPDH{funcs: FuncSet{}}
	p.srv = httptest.NewServer(http.HandlerFunc(p.serve))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *funcPDH) body() map[string]any {
	out := map[string]any{}
	for k, v := range p.funcs {
		out[k] = v
	}
	return out
}

func (p *funcPDH) serve(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		http.Error(w, "weg", http.StatusServiceUnavailable)
		return
	}
	p.lastToken = r.Header.Get("Authorization")
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/terminal/v1/device":
		p.getCalls++
		resp := map[string]any{"device_id": "dev-1", "user": "Kasse", "name": "T1", "functions": p.body()}
		if p.theme != nil {
			resp["theme"] = p.theme
		}
		_ = json.NewEncoder(w).Encode(resp)
	case r.Method == http.MethodPut && r.URL.Path == "/api/terminal/v1/functions":
		p.putCalls++
		if p.noPut {
			http.NotFound(w, r)
			return
		}
		var in struct {
			Functions map[string]wireFunc `json:"functions"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			http.Error(w, "kaputt", http.StatusBadRequest)
			return
		}
		for k, v := range parseFuncs(in.Functions) {
			if cur, ok := p.funcs[k]; !ok || v.UpdatedAt.After(cur.UpdatedAt) { // nur strikt jüngere gelten
				p.funcs[k] = v
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"functions": p.body()})
	default:
		http.NotFound(w, r)
	}
}

func newSyncEnv(t *testing.T, p *funcPDH) (*FuncSync, string) {
	t.Helper()
	dir := t.TempDir()
	if err := SaveDevice(dir, Device{DeviceID: "dev-1", Secret: "geheim", User: "Kasse", URL: p.srv.URL, PairedAt: t0}); err != nil {
		t.Fatal(err)
	}
	f := NewFuncSync(dir, Client{})
	f.now = func() time.Time { return t1 }
	return f, dir
}

func TestSyncLocalNewerWins(t *testing.T) {
	p := newFuncPDH(t)
	p.funcs[FuncTicket] = FuncValue{Enabled: true, UpdatedAt: t0}
	f, _ := newSyncEnv(t, p)
	if ch, err := f.Set(FuncTicket, false); err != nil || !ch {
		t.Fatalf("Set: %v %v", ch, err)
	}
	f.SyncOnce(context.Background())
	if v := p.funcs[FuncTicket]; v.Enabled || !v.UpdatedAt.Equal(t1) {
		t.Fatalf("PDH hat %+v, erwartet aus mit %v", v, t1)
	}
	if v := f.Get(FuncTicket); v.Enabled || f.Pending(FuncTicket) {
		t.Fatalf("lokal %+v pending=%v", v, f.Pending(FuncTicket))
	}
	if p.lastToken != "Bearer geheim" {
		t.Errorf("Authorization %q", p.lastToken)
	}
}

func TestSyncPDHNewerWins(t *testing.T) {
	p := newFuncPDH(t)
	p.funcs[FuncTicket] = FuncValue{Enabled: true, UpdatedAt: t2} // jünger als die lokale Änderung (t1)
	f, _ := newSyncEnv(t, p)
	if _, err := f.Set(FuncTicket, false); err != nil {
		t.Fatal(err)
	}
	f.SyncOnce(context.Background())
	if v := f.Get(FuncTicket); !v.Enabled || !v.UpdatedAt.Equal(t2) || f.Pending(FuncTicket) {
		t.Fatalf("lokal %+v pending=%v, erwartet PDH-Wert an mit %v", v, f.Pending(FuncTicket), t2)
	}
	if p.putCalls != 0 {
		t.Errorf("PDH war jünger: es darf nichts übertragen werden (%d PUT)", p.putCalls)
	}
}

func TestSyncEqualTimestampPDHWins(t *testing.T) {
	p := newFuncPDH(t)
	p.funcs[FuncHMI] = FuncValue{Enabled: true, UpdatedAt: t1}
	f, _ := newSyncEnv(t, p)
	if _, err := f.Set(FuncHMI, false); err != nil { // lokal mit t1, gleich dem von PDH
		t.Fatal(err)
	}
	f.SyncOnce(context.Background())
	if v := f.Get(FuncHMI); !v.Enabled || f.Pending(FuncHMI) {
		t.Fatalf("bei Gleichstand gilt PDH: %+v pending=%v", v, f.Pending(FuncHMI))
	}
	if !p.funcs[FuncHMI].Enabled {
		t.Errorf("PDH darf bei Gleichstand nicht überschrieben werden")
	}
}

func TestSyncLocalOlderStaysAndPDHKeepsNewer(t *testing.T) {
	// Lokal t1 (ausgeschaltet), PDH t0 (an): lokal ist jünger, PDH übernimmt.
	p := newFuncPDH(t)
	p.funcs[FuncMaint] = FuncValue{Enabled: true, UpdatedAt: t0}
	f, _ := newSyncEnv(t, p)
	f.Set(FuncMaint, false)
	f.SyncOnce(context.Background())
	if p.funcs[FuncMaint].Enabled {
		t.Fatal("PDH hat die jüngere lokale Änderung nicht übernommen")
	}
}

func TestSyncPDHValueAppliedWithoutLocalChange(t *testing.T) {
	p := newFuncPDH(t)
	p.funcs[FuncSaverSlide] = FuncValue{Enabled: true, UpdatedAt: t2}
	p.funcs["unbekannt"] = FuncValue{Enabled: true, UpdatedAt: t2}
	f, _ := newSyncEnv(t, p)
	f.SyncOnce(context.Background())
	if v := f.Get(FuncSaverSlide); !v.Enabled || !v.UpdatedAt.Equal(t2) {
		t.Fatalf("Folienschau %+v", v)
	}
	if v := f.Get(FuncFault); !v.Enabled || !v.UpdatedAt.IsZero() {
		t.Fatalf("Grundwert Störung %+v", v)
	}
	if p.putCalls != 0 {
		t.Error("ohne lokale Änderung wird nichts übertragen")
	}
}

func TestSyncPDH404MeansLocalOnlyWithoutRetryStorm(t *testing.T) {
	p := newFuncPDH(t)
	p.noPut = true
	f, _ := newSyncEnv(t, p)
	f.Set(FuncTicket, false)
	for i := 0; i < 5; i++ {
		f.SyncOnce(context.Background())
	}
	if v := f.Get(FuncTicket); v.Enabled {
		t.Fatalf("der lokale Wert muss bleiben: %+v", v)
	}
	if p.putCalls != 1 {
		t.Fatalf("nach 404 darf nicht erneut gesendet werden: %d PUT", p.putCalls)
	}
	if !f.Status().Unsupported {
		t.Error("Status soll „nicht unterstützt“ melden")
	}
	// Nach Ablauf der Wartezeit wird wieder geprüft (PDH wurde aktualisiert).
	p.mu.Lock()
	p.noPut = false
	p.mu.Unlock()
	f.now = func() time.Time { return t1.Add(funcUnsupportedBy + time.Second) }
	f.SyncOnce(context.Background())
	if p.putCalls != 2 || f.Pending(FuncTicket) {
		t.Fatalf("nach der Wartezeit: %d PUT, pending=%v", p.putCalls, f.Pending(FuncTicket))
	}
}

func TestSyncOfflineThenBack(t *testing.T) {
	p := newFuncPDH(t)
	f, dir := newSyncEnv(t, p)
	p.mu.Lock()
	p.down = true
	p.mu.Unlock()
	f.Set(FuncStillstandForTest, false)
	f.SyncOnce(context.Background())
	if v := f.Get(FuncDowntime); v.Enabled || !f.Pending(FuncDowntime) {
		t.Fatalf("offline: lokaler Wert bleibt, vorgemerkt: %+v pending=%v", v, f.Pending(FuncDowntime))
	}
	if f.Status().LastErr == "" {
		t.Error("Fehler soll sichtbar sein")
	}
	// Neustart des Geräts: die Vormerkung überlebt.
	f2 := NewFuncSync(dir, Client{})
	f2.now = func() time.Time { return t2 }
	if !f2.Pending(FuncDowntime) || f2.Get(FuncDowntime).Enabled {
		t.Fatal("Vormerkung nach Neustart verloren")
	}
	p.mu.Lock()
	p.down = false
	p.mu.Unlock()
	f2.SyncOnce(context.Background())
	if v := p.funcs[FuncDowntime]; v.Enabled || !v.UpdatedAt.Equal(t1) {
		t.Fatalf("PDH hat %+v nach dem Wiederkommen", v)
	}
	if f2.Pending(FuncDowntime) || f2.Status().LastErr != "" {
		t.Errorf("nach dem Wiederkommen ist alles bestätigt: pending=%v err=%q", f2.Pending(FuncDowntime), f2.Status().LastErr)
	}
}

// FuncStillstandForTest kürzt den Schlüssel im Test ab.
const FuncStillstandForTest = FuncDowntime

func TestSettingsEvent(t *testing.T) {
	p := newFuncPDH(t)
	f, dir := newSyncEnv(t, p)
	f.Set(FuncFault, false) // lokal t1
	// älteres Ereignis (t0): ignoriert
	f.HandleSettingsEvent([]byte(`{"functions":{"stoerung":{"enabled":true,"updated_at":"` + t0.Format(timeFormat) + `"}},"manual_login":"card_pin"}`))
	if f.Get(FuncFault).Enabled {
		t.Fatal("älteres Ereignis darf den lokalen Wert nicht überschreiben")
	}
	// jüngeres (t2): gilt
	f.HandleSettingsEvent([]byte(`{"functions":{"stoerung":{"enabled":true,"updated_at":"` + t2.Format(timeFormat) + `"}},"theme":{"scheme":"light"}}`))
	if v := f.Get(FuncFault); !v.Enabled || !v.UpdatedAt.Equal(t2) || f.Pending(FuncFault) {
		t.Fatalf("jüngeres Ereignis: %+v", v)
	}
	if th := LoadTheme(dir); th.Scheme != "light" || th.Bg != "#f3f3f3" {
		t.Errorf("Theme aus dem Ereignis: %+v", th)
	}
	// Ereignis ohne functions oder kaputt: nichts passiert
	f.HandleSettingsEvent([]byte(`{"idle":120}`))
	f.HandleSettingsEvent([]byte(`kein json`))
	if v := f.Get(FuncFault); !v.Enabled {
		t.Fatal("Ereignis ohne functions darf nichts ändern")
	}
}

func TestEventStreamParsing(t *testing.T) {
	p := newFuncPDH(t)
	f, _ := newSyncEnv(t, p)
	stream := ": ping\n\nevent: settings\ndata: {\"functions\":{\"ticket\":{\"enabled\":false,\"updated_at\":\"" + t2.Format(timeFormat) + "\"}}}\n\nevent: slides\ndata: {}\n\n"
	f.consumeEvents(strings.NewReader(stream))
	if v := f.Get(FuncTicket); v.Enabled || !v.UpdatedAt.Equal(t2) {
		t.Fatalf("Ticket %+v", v)
	}
}

func TestSetLocalRules(t *testing.T) {
	dir := t.TempDir()
	s := openFuncStore(dir)
	if _, err := s.SetLocal("gibtsnicht", true, t0); err == nil {
		t.Error("unbekannte Funktion muss abgelehnt werden")
	}
	if ch, _ := s.SetLocal(FuncTicket, true, t0); ch {
		t.Error("gleicher Wert ist keine Änderung")
	}
	s.SetLocal(FuncTicket, false, t1)
	s.SetLocal(FuncTicket, true, t0) // Uhr zurückgestellt: Zeitpunkt geht nicht rückwärts
	if v := s.Get(FuncTicket); !v.Enabled || !v.UpdatedAt.After(t1) {
		t.Fatalf("Zeitpunkt muss wachsen: %+v", v)
	}
	if got := openFuncStore(dir).Get(FuncTicket); !got.Enabled || !got.UpdatedAt.Equal(s.Get(FuncTicket).UpdatedAt) {
		t.Fatalf("nicht gespeichert: %+v", got)
	}
}

func TestWireFormat(t *testing.T) {
	b, _ := json.Marshal(FuncSet{FuncTicket: {Enabled: false, UpdatedAt: time.Date(2026, 11, 22, 10, 0, 0, 123_000_000, time.UTC)}})
	if string(b) != `{"ticket":{"enabled":false,"updated_at":"2026-11-22T10:00:00.123Z"}}` {
		t.Fatalf("Format: %s", b)
	}
}

func TestPollAppliesThemeAndOlderPDHWithoutFields(t *testing.T) {
	p := newFuncPDH(t)
	p.theme = map[string]string{"scheme": "dark", "bg": "#101010", "bg2": "javascript:evil"}
	f, dir := newSyncEnv(t, p)
	f.SyncOnce(context.Background())
	th := LoadTheme(dir)
	if th.Bg != "#101010" || th.Bg2 != DarkTheme().Bg2 {
		t.Fatalf("Theme %+v: gültige Farbe übernehmen, ungültige ignorieren", th)
	}
	// Ein PDH ohne die neuen Felder (altes /device): kein Fehler, nichts ändert sich.
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/terminal/v1/device" {
			_, _ = w.Write([]byte(`{"device_id":"d","user":"u","name":"n"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer old.Close()
	dir2 := t.TempDir()
	SaveDevice(dir2, Device{DeviceID: "d", Secret: "s", URL: old.URL, PairedAt: t0})
	f2 := NewFuncSync(dir2, Client{})
	f2.Set(FuncTicket, false)
	f2.SyncOnce(context.Background())
	if f2.Get(FuncTicket).Enabled || !f2.Status().Unsupported {
		t.Fatal("altes PDH: lokal bleibt, nur lokal")
	}
}

func TestThemeDefaultsMatchPDH(t *testing.T) {
	if DarkTheme().Bg != "#0f1117" || LightTheme().Bg != "#f3f3f3" {
		t.Fatal("Grundfarben weichen von PDH ab")
	}
	if !strings.Contains(DarkTheme().CSS(), "--bg:#0f1117") {
		t.Fatal(DarkTheme().CSS())
	}
	if !strings.Contains((Theme{Bg: "red;}"}).CSS(), "--bg:#0f1117") {
		t.Fatal("ungültige Farben dürfen nicht ins CSS")
	}
	if LoadTheme(t.TempDir()) != DarkTheme() {
		t.Fatal("ohne Datei gilt dunkel")
	}
}

func TestStartPageUsesPDHBackground(t *testing.T) {
	dir := t.TempDir()
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{} }, Client{}, nil, nil)
	get := func() string {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/start", nil)
		r.Host, r.RemoteAddr = "127.0.0.1:8080", "127.0.0.1:50000"
		h.ServeHTTP(w, r)
		return w.Body.String()
	}
	if b := get(); !strings.Contains(b, "--bg:#0f1117") || strings.Contains(b, "#111") {
		t.Fatalf("Startseite ohne Theme: %s", b)
	}
	if err := SaveTheme(dir, LightTheme()); err != nil {
		t.Fatal(err)
	}
	if b := get(); !strings.Contains(b, "--bg:#f3f3f3") {
		t.Fatalf("Startseite mit hellem Theme: %s", b)
	}
}
