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
	return StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: url} }, c, nil, nil), url
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
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client(), nil, nil)
	w = startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Gerät in PDH gesperrt.") || !strings.Contains(w.Body.String(), `content="60"`) || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("gesperrt: %s", w.Body)
	}
	// Zertifikat
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.untrustedClient(), nil, nil)
	w = startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Zertifikat von PDH wird nicht vertraut. Neuer Versuch in 30 Sekunden.") ||
		!strings.Contains(w.Body.String(), `http-equiv="refresh" content="30"`) || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("Zertifikat: %s", w.Body)
	}
	// nicht erreichbar
	p.srv.Close()
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client(), nil, nil)
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
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client(), nil, nil)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "Gerät nicht gekoppelt") || strings.Contains(w.Body.String(), testSecret) {
		t.Errorf("%s", w.Body)
	}
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{} }, p.client(), nil, nil)
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
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: url, AllowHTTP: true} }, client, nil, nil)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	body := w.Body.String()
	if !strings.Contains(body, `<form method="post" action="`+url+`/terminal/geraet">`) {
		t.Errorf("Formular mit http-Adresse fehlt: %s", body)
	}
	if csp := w.Header().Get("Content-Security-Policy"); !strings.HasSuffix(csp, "form-action "+url) {
		t.Errorf("CSP = %q", csp)
	}
	// Ohne den Haken bleibt die Startseite ohne Formular.
	h = StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: url} }, client, nil, nil)
	if w := startReq(t, h, "GET", "/start", "127.0.0.1:8080"); strings.Contains(w.Body.String(), "<form") {
		t.Errorf("http ohne Erlaubnis zeigt Formular")
	}
}

func TestStartCardRoute(t *testing.T) {
	p := newFakePDH(t)
	var gotMethod string
	cards := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusNoContent)
	})
	h := StartHandler(t.TempDir(), func() config.PDHCfg { return config.PDHCfg{URL: p.hostURL()} }, p.client(), cards, nil)
	for _, c := range [][2]string{{"GET", "/api/card/events"}, {"OPTIONS", "/api/card/events"},
		{"POST", "/api/card/result"}, {"OPTIONS", "/api/card/result"}} {
		gotMethod = ""
		w := startReq(t, h, c[0], c[1], "127.0.0.1:8080")
		if w.Code != http.StatusNoContent || gotMethod != c[0] {
			t.Errorf("%s %s: Status %d, Methode %q", c[0], c[1], w.Code, gotMethod)
		}
	}
	if w := startReq(t, h, "POST", "/api/card/result", "evil:8080"); w.Code != http.StatusForbidden {
		t.Errorf("result fremder Host: Status %d", w.Code)
	}
	if w := startReq(t, h, "GET", "/api/card/events", "evil:8080"); w.Code != http.StatusForbidden {
		t.Errorf("fremder Host: Status %d", w.Code)
	}
	// Ohne Kartenroute (nil) bleibt der Pfad unbekannt bzw. POST gesperrt.
	h = StartHandler(t.TempDir(), func() config.PDHCfg { return config.PDHCfg{} }, p.client(), nil, nil)
	if w := startReq(t, h, "GET", "/api/card/events", "127.0.0.1:8080"); w.Code != http.StatusNotFound {
		t.Errorf("nil: Status %d", w.Code)
	}
}

func TestCardOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://pdh.example":      "https://pdh.example",
		"https://PDH.example:443":  "https://pdh.example",
		"https://pdh.example:8443": "https://pdh.example:8443",
		"http://pdh.lan":           "http://pdh.lan",
		"http://pdh.lan:8080":      "http://pdh.lan:8080",
		"":                         "",
	} {
		if got := cardOrigin(config.PDHCfg{URL: in, ExtraOrigins: []string{"https://cdn.example:443"}}, in); got != want {
			t.Errorf("cardOrigin(%q) = %q, erwartet %q", in, got, want)
		}
	}
}

// namedURL nennt einen Testserver unter einem Namen, den die Konfiguration zulässt (nur Namen, keine IP-Adressen).
func namedURL(s *routeSrv, host string) string {
	_, port, _ := net.SplitHostPort(s.Listener.Addr().String())
	return "https://" + host + ":" + port
}

// namedClient wählt jeden Namen auf 127.0.0.1 an und vertraut den Testzertifikaten (gültig für example.com).
func namedClient(srvs ...*routeSrv) *http.Client {
	tr := trustClient(srvs...).Transport.(*http.Transport)
	tr.TLSClientConfig.ServerName = "example.com"
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		_, port, _ := net.SplitHostPort(addr)
		return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+port)
	}
	return &http.Client{Transport: tr}
}

// fallbackStart richtet eine Startseite mit nicht erreichbarer Hauptadresse und laufender Ausweichadresse ein.
func fallbackStart(t *testing.T) (h http.Handler, mainURL, fbURL string, rt *Route) {
	t.Helper()
	main, fb := newRouteSrv(200), newRouteSrv(200)
	t.Cleanup(fb.Close)
	mainURL, fbURL = namedURL(main, "pdh.lan"), namedURL(fb, "pdh.tail1234.ts.net")
	main.Close()
	dir := t.TempDir()
	if err := SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, User: "Anna", URL: mainURL}); err != nil {
		t.Fatal(err)
	}
	rt = NewRoute(mainURL, fbURL)
	c := Client{HTTP: namedClient(main, fb), Route: rt}
	cfg := func() config.PDHCfg { return config.PDHCfg{URL: mainURL, FallbackURL: fbURL} }
	return StartHandler(dir, cfg, c, nil, nil), mainURL, fbURL, rt
}

func TestStartUsesFallbackAction(t *testing.T) {
	h, mainURL, fbURL, rt := fallbackStart(t)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if rt.Name() != "tailnet" {
		t.Fatalf("Route %q, erwartet tailnet: %s", rt.Name(), w.Body)
	}
	body := w.Body.String()
	if !strings.Contains(body, `action="`+fbURL+`/terminal/geraet"`) || strings.Contains(body, `action="`+mainURL) {
		t.Errorf("Action nicht auf Ausweichadresse: %s", body)
	}
	want := "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; form-action " + mainURL + " " + fbURL
	if got := w.Header().Get("Content-Security-Policy"); got != want {
		t.Errorf("CSP %q, erwartet %q", got, want)
	}
}

// Das Tor bleibt: Ein Gerät, das auf der Ausweichadresse gekoppelt ist, gilt als nicht gekoppelt.
func TestStartGateUnchanged(t *testing.T) {
	dir := t.TempDir()
	main, fb := newRouteSrv(200), newRouteSrv(200)
	defer main.Close()
	defer fb.Close()
	mainURL, fbURL := namedURL(main, "pdh.lan"), namedURL(fb, "pdh.tail1234.ts.net")
	if err := SaveDevice(dir, Device{DeviceID: "a1-1", Secret: testSecret, User: "Anna", URL: fbURL}); err != nil {
		t.Fatal(err)
	}
	c := Client{HTTP: namedClient(main, fb), Route: NewRoute(mainURL, fbURL)}
	h := StartHandler(dir, func() config.PDHCfg { return config.PDHCfg{URL: mainURL, FallbackURL: fbURL} }, c, nil, nil)
	w := startReq(t, h, "GET", "/start", "127.0.0.1:8080")
	if !strings.Contains(w.Body.String(), "nicht gekoppelt") || strings.Contains(w.Body.String(), "<form") {
		t.Errorf("erwartet nicht gekoppelt: %s", w.Body)
	}
}

func TestCardOriginFollowsRoute(t *testing.T) {
	p := config.PDHCfg{URL: "https://pdh.lan", FallbackURL: "https://pdh.tail1234.ts.net"}
	for base, want := range map[string]string{
		"https://pdh.lan":                 "https://pdh.lan",
		"https://pdh.tail1234.ts.net":     "https://pdh.tail1234.ts.net",
		"https://pdh.tail1234.ts.net:443": "https://pdh.tail1234.ts.net",
		"https://pdh.ts.net:8443":         "https://pdh.ts.net:8443",
	} {
		if got := cardOrigin(p, base); got != want {
			t.Errorf("cardOrigin(%q) = %q, erwartet %q", base, got, want)
		}
	}
	if got := cardOrigin(config.PDHCfg{}, ""); got != "" {
		t.Errorf("leer: %q", got)
	}
}
