package pdhterminal

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

func startReq(t *testing.T, h http.Handler, method, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	r.Host = host
	r.RemoteAddr = "127.0.0.1:50000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func startSetup(t *testing.T, c Client, pair bool) (http.Handler, string) {
	t.Helper()
	p := newFakePDH(t)
	dir := t.TempDir()
	url := p.hostURL()
	if pair {
		if err := SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, User: "Anna", URL: url}); err != nil {
			t.Fatal(err)
		}
	}
	if c.HTTP == nil {
		c = p.client()
	}
	return StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: url} }, c), url
}

func TestStartOK(t *testing.T) {
	h, url := startSetup(t, Client{}, true)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if w.Code != 200 {
		t.Fatalf("Status %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `<form method="post" action="`+url+`/terminal/geraet">`) {
		t.Errorf("Formular fehlt: %s", body)
	}
	if !strings.Contains(body, `name="secret" value="`+testSecret+`"`) || !strings.Contains(body, "Anmelden") {
		t.Errorf("Feld/Knopf fehlt: %s", body)
	}
	for k, v := range map[string]string{
		"Content-Security-Policy": "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action " + url,
		"Cache-Control":           "no-store",
		"Referrer-Policy":         "no-referrer",
		"X-Frame-Options":         "DENY",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q", k, got)
		}
	}
	for k, vs := range w.Header() {
		for _, v := range vs {
			if strings.Contains(v, testSecret) {
				t.Errorf("Geheimnis im Header %s", k)
			}
		}
	}
}

func TestStartRedirectNoSecret(t *testing.T) {
	h, _ := startSetup(t, Client{}, true)
	w := startReq(t, h, "GET", "/", "localhost:8080")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/start" {
		t.Errorf("%d %q", w.Code, w.Header().Get("Location"))
	}
}

func TestStartStates(t *testing.T) {
	// nicht gekoppelt
	h, _ := startSetup(t, Client{}, false)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Gerät nicht gekoppelt. Admin-Seite: 5 Sekunden oben rechts auf den Bildschirm drücken.") ||
		!strings.Contains(w.Body.String(), `http-equiv="refresh" content="60"`) || strings.Contains(w.Body.String(), "<form") {
		t.Errorf("nicht gekoppelt: %s", w.Body)
	}
	// gesperrt
	p := newFakePDH(t)
	p.revoked.Store(true)
	dir := t.TempDir()
	_ = SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, URL: p.hostURL()})
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client())
	w = startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Gerät in PDH gesperrt.") || !strings.Contains(w.Body.String(), `content="60"`) || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("gesperrt: %s", w.Body)
	}
	// Zertifikat
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.untrustedClient())
	w = startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Zertifikat von PDH wird nicht vertraut. Neuer Versuch in 30 Sekunden.") ||
		!strings.Contains(w.Body.String(), `http-equiv="refresh" content="30"`) || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("Zertifikat: %s", w.Body)
	}
	// nicht erreichbar
	p.srv.Close()
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client())
	w = startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "PDH nicht erreichbar. Neuer Versuch in 30 Sekunden.") ||
		!strings.Contains(w.Body.String(), `http-equiv="refresh" content="30"`) || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("nicht erreichbar: %d %s", w.Code, w.Body)
	}
}

func TestStartOtherURLCountsUnpaired(t *testing.T) {
	p := newFakePDH(t)
	dir := t.TempDir()
	_ = SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, URL: "https://andere.firma.de"})
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client())
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Gerät nicht gekoppelt") || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("%s", w.Body)
	}
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{} }, p.client())
	if w := startReq(t, h, "GET", "/start", "127.0.0.1:8080"); !strings.Contains(w.Body.String(), "Gerät nicht gekoppelt") {
		t.Errorf("ohne URL: %s", w.Body)
	}
}

func TestStartHostAndMethod(t *testing.T) {
	h, _ := startSetup(t, Client{}, true)
	w := startReq(t, h, "GET", "/start", "evil.example:8080")
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("fremder Host: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/start", nil)
	r.Host = "127.0.0.1:8080"
	r.RemoteAddr = "192.0.2.7:1"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusForbidden {
		t.Errorf("fremde Gegenstelle: %d", rec.Code)
	}
	if w := startReq(t, h, "POST", "/start", "127.0.0.1:8080"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
	if w := startReq(t, h, "PUT", "/start", "127.0.0.1:8080"); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("PUT: %d", w.Code)
	}
	if w := startReq(t, h, "HEAD", "/start", "127.0.0.1:8080"); w.Code != 200 {
		t.Errorf("HEAD: %d", w.Code)
	}
}

// Mit „unverschlüsselt erlauben“ trägt die Startseite die http-Adresse in Formular und CSP.
func TestStartHTTPAllowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testSecret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"device_id":"a1-1","user":"Anna","name":"x"}`))
	}))
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	_, port, _ := net.SplitHostPort(addr)
	url := "http://pdh.werk:" + port
	dir := t.TempDir()
	if err := SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, User: "Anna", URL: url}); err != nil {
		t.Fatal(err)
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	client := Client{HTTP: &http.Client{Transport: tr}}
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: url, AllowHTTP: true} }, client)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	body := w.Body.String()
	if !strings.Contains(body, `<form method="post" action="`+url+`/terminal/geraet">`) {
		t.Errorf("Formular mit http-Adresse fehlt: %s", body)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.HasSuffix(csp, "form-action "+url) {
		t.Errorf("CSP = %q", csp)
	}
	// Ohne den Haken bleibt die Startseite ohne Formular.
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: url} }, client)
	if w := startReq(t, h, "GET", "/start", "127.0.0.1:8080"); strings.Contains(w.Body.String(), "<form") {
		t.Errorf("http ohne Erlaubnis zeigt Formular")
	}
}
