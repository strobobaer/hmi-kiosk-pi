package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

// terminal ist ein Browser am Terminal: Chromium ruft http://127.0.0.1:8081 auf.
func (e *adminEnv) terminal() *browser {
	b := e.browser()
	b.onTerminal = true
	return b
}

func (e *adminEnv) terminalLogin() *browser {
	e.t.Helper()
	b := e.terminal()
	r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}})
	if r.Code != http.StatusSeeOther {
		e.t.Fatalf("Anmeldung am Terminal: %d %s", r.Code, r.Body.String())
	}
	return b
}

func localReq(method, host, path string, hdr map[string]string) *http.Request {
	req := httptest.NewRequest(method, "http://"+host+path, strings.NewReader(""))
	req.Host = host
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

func serveLocal(e *adminEnv, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.a.localHandler().ServeHTTP(w, req)
	return w
}

func TestLocalAdminChecksHost(t *testing.T) {
	e := newEnv(t)
	for _, host := range []string{"127.0.0.1:8081", "localhost:8081"} {
		if w := serveLocal(e, localReq(http.MethodGet, host, "/admin/login", nil)); w.Code != http.StatusOK {
			t.Errorf("Host %s: %d", host, w.Code)
		}
	}
	for _, host := range []string{"evil.example:8081", "192.168.1.20:8081", "127.0.0.1:8443", "127.0.0.1", "localhost.evil:8081", ""} {
		if w := serveLocal(e, localReq(http.MethodGet, host, "/admin/login", nil)); w.Code != http.StatusForbidden {
			t.Errorf("Host %q: %d, erwartet 403", host, w.Code)
		}
	}
}

func TestLocalAdminPostNeedsLocalOrigin(t *testing.T) {
	e := newEnv(t)
	b := e.terminal()
	tok := b.tokenFrom("/admin/login")
	form := url.Values{"password": {testAdminPassword}, "csrf": {tok}}
	for _, hdr := range []map[string]string{
		{},
		{"Origin": "http://evil.example"},
		{"Origin": "https://127.0.0.1:8081"},
		{"Origin": "http://127.0.0.1:8080"},
		{"Referer": "http://evil.example/admin/login"},
	} {
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/admin/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		for k, v := range b.jar {
			req.AddCookie(&http.Cookie{Name: k, Value: v})
		}
		if w := serveLocal(e, req); w.Code != http.StatusForbidden {
			t.Errorf("Kopf %v: %d, erwartet 403", hdr, w.Code)
		}
	}
	// Mit dem eigenen Ursprung (Referer reicht) gelingt die Anmeldung.
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/admin/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", "http://localhost:8081/admin/login")
	req.Host = "localhost:8081"
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	if w := serveLocal(e, req); w.Code != http.StatusSeeOther {
		t.Fatalf("eigener Ursprung: %d %s", w.Code, w.Body.String())
	}
}

func TestLocalAdminCookiesWithoutSecure(t *testing.T) {
	e := newEnv(t)
	b := e.terminal()
	tok := b.tokenFrom("/admin/login")
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8081/admin/login", strings.NewReader(url.Values{"password": {testAdminPassword}, "csrf": {tok}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://127.0.0.1:8081")
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := serveLocal(e, req)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("Anmeldung: %d", w.Code)
	}
	var found bool
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			t.Fatal("Terminal nutzt das Cookie der HTTPS-Seite")
		}
		if c.Name == localSessionCookie && c.MaxAge >= 0 {
			found = true
			if c.Secure {
				t.Fatal("Cookie über HTTP mit Secure")
			}
			if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
				t.Fatal("Cookie ohne HttpOnly/SameSite")
			}
		}
	}
	if !found {
		t.Fatal("kein Sitzungs-Cookie")
	}
	// Die HTTPS-Seite setzt weiter Secure.
	r := e.browser().get("/admin/login")
	for _, c := range r.Result().Cookies() {
		if !c.Secure {
			t.Fatalf("HTTPS-Cookie %s ohne Secure", c.Name)
		}
	}
}

func TestLocalAdminRequiresLogin(t *testing.T) {
	e := newEnv(t)
	b := e.terminal()
	r := b.get("/admin/hmis")
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/login" {
		t.Fatalf("ohne Anmeldung: %d %s", r.Code, r.Header().Get("Location"))
	}
	b = e.terminalLogin()
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("angemeldet: %d", r.Code)
	}
}

func TestLocalAndRemoteSessionsAreSeparate(t *testing.T) {
	e := newEnv(t)
	remote := e.login()
	term := e.terminalLogin()
	// Ein HTTPS-Wert gilt am Terminal nicht, auch nicht unter dem Namen der lokalen Sitzung.
	cross := e.terminal()
	cross.jar[sessionCookie] = remote.jar[sessionCookie]
	cross.jar[localSessionCookie] = remote.jar[sessionCookie]
	if r := cross.get("/admin/hmis"); r.Code != http.StatusSeeOther {
		t.Fatalf("HTTPS-Sitzung gilt am Terminal: %d", r.Code)
	}
	back := e.browser()
	back.jar[sessionCookie] = term.jar[localSessionCookie]
	back.jar[localSessionCookie] = term.jar[localSessionCookie]
	if r := back.get("/admin/hmis"); r.Code != http.StatusSeeOther {
		t.Fatalf("Terminal-Sitzung gilt über HTTPS: %d", r.Code)
	}
	// Abmelden am Terminal beendet die HTTPS-Sitzung nicht und umgekehrt.
	if r := term.post("/admin/logout", nil); r.Code != http.StatusSeeOther {
		t.Fatalf("Abmelden: %d", r.Code)
	}
	if r := remote.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("HTTPS-Sitzung nach Abmelden am Terminal: %d", r.Code)
	}
}

func TestLocalAdminSharesLockout(t *testing.T) {
	e := newEnv(t)
	b := e.terminal()
	for i := 0; i < maxFailures; i++ {
		b.postPage("/admin/login", url.Values{"password": {"falsch"}})
	}
	r := e.browser().postPage("/admin/login", url.Values{"password": {testAdminPassword}})
	if r.Code != http.StatusTooManyRequests {
		t.Fatalf("Sperre gilt nicht für HTTPS: %d", r.Code)
	}
}

func TestBackToKioskOnlyOnTerminal(t *testing.T) {
	e := newEnv(t)
	term := e.terminalLogin()
	body := term.get("/admin/hmis").Body.String()
	wantContains(t, body, "Zurück zum Kiosk", `action="/admin/local/back"`, `src="/admin/local.js"`, `src="/keyboard.js"`, `data-idle="300"`)
	login := e.terminal().get("/admin/login").Body.String()
	wantContains(t, login, "Zurück zum Kiosk", `action="/admin/local/back"`, `data-osk="auto"`)

	remote := e.login().get("/admin/hmis").Body.String()
	wantNotContains(t, remote, "Zurück zum Kiosk", "/admin/local", "keyboard.js", "data-idle")
}

func TestLocalKeyboardModeFollowsInputMode(t *testing.T) {
	e := newEnv(t)
	e.mutate(func(c *config.Config) { c.Kiosk.InputMode = config.InputMouse })
	body := e.terminal().get("/admin/login").Body.String()
	wantContains(t, body, `data-osk="manual"`)
}

func TestLocalBackEndsSession(t *testing.T) {
	e := newEnv(t)
	var left atomic.Int32
	e.a.d.LocalLeft = func() { left.Add(1) }
	b := e.terminalLogin()
	r := b.post("/admin/local/back", nil)
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != KioskURL {
		t.Fatalf("Zurück: %d %q", r.Code, r.Header().Get("Location"))
	}
	if left.Load() != 1 {
		t.Fatal("Rückkehr nicht gemeldet")
	}
	if r := b.get("/admin/hmis"); r.Code != http.StatusSeeOther {
		t.Fatalf("Sitzung nach Zurück noch gültig: %d", r.Code)
	}
	// Über HTTPS gibt es die Route nicht.
	if r := e.login().post("/admin/local/back", nil); r.Code != http.StatusNotFound {
		t.Fatalf("HTTPS: %d", r.Code)
	}
}

func TestLocalLogoutReportsLeftToo(t *testing.T) {
	e := newEnv(t)
	var left atomic.Int32
	e.a.d.LocalLeft = func() { left.Add(1) }
	b := e.terminalLogin()
	b.post("/admin/logout", nil)
	if left.Load() != 0 {
		t.Fatal("Abmelden ohne Rückkehr zählt als Rückkehr")
	}
}

func TestLocalSessionEndsAfterFiveMinutesIdle(t *testing.T) {
	e := newEnv(t)
	term := e.terminalLogin()
	remote := e.login()
	e.clock.advance(4 * time.Minute)
	if r := term.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("nach 4 min: %d", r.Code)
	}
	e.clock.advance(localSessionIdle)
	if r := term.get("/admin/hmis"); r.Code != http.StatusSeeOther {
		t.Fatalf("nach 5 min ohne Eingabe noch angemeldet: %d", r.Code)
	}
	if r := remote.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("HTTPS-Sitzung nach 9 min: %d", r.Code)
	}
}

func TestLocalAdminReportsActivity(t *testing.T) {
	e := newEnv(t)
	var active atomic.Int32
	e.a.d.LocalActive = func() { active.Add(1) }
	count := func() int32 { return active.Load() }

	// Ohne Sitzung zählen weder Unterseiten (CSS, Bilder) noch Abrufe fremder Seiten.
	anon := e.terminal()
	anon.get("/admin/admin.css")
	anon.get("/admin/login")
	for _, site := range []string{"cross-site", "same-site"} {
		for _, mode := range []string{"no-cors", "cors"} {
			serveLocal(e, localReq(http.MethodGet, "127.0.0.1:8081", "/admin/login", map[string]string{"Sec-Fetch-Site": site, "Sec-Fetch-Mode": mode}))
		}
	}
	serveLocal(e, localReq(http.MethodGet, "127.0.0.1:8081", "/admin/login", map[string]string{"Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "navigate"}))
	if n := count(); n != 0 {
		t.Fatalf("%d Aufrufe ohne Sitzung gezählt", n)
	}
	// Das Zahnrad (Navigation von der Kachelseite) zählt, ebenso eine Sitzung am Terminal.
	serveLocal(e, localReq(http.MethodGet, "127.0.0.1:8081", "/admin/", map[string]string{"Sec-Fetch-Site": "same-site", "Sec-Fetch-Mode": "navigate"}))
	if count() != 1 {
		t.Fatal("Navigation über das Zahnrad nicht gezählt")
	}
	b := e.terminalLogin()
	n := count()
	b.get("/admin/hmis")
	if count() <= n {
		t.Fatal("Aufruf mit Sitzung nicht gezählt")
	}
	// Mit Sitzung zählt ein Abruf fremder Seiten trotzdem nicht.
	n = count()
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8081/admin/hmis", nil)
	req.Host = "127.0.0.1:8081"
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	serveLocal(e, req)
	if count() != n {
		t.Fatal("fremder Abruf mit Sitzung gezählt")
	}
	e.browser().get("/admin/login")
	e.login().get("/admin/hmis")
	serveLocal(e, localReq(http.MethodGet, "evil.example:8081", "/admin/hmis", nil))
	if count() != n {
		t.Fatal("HTTPS oder abgelehnter Host zählt als Terminal")
	}
}

func TestLocalBackWorksWithoutValidSession(t *testing.T) {
	e := newEnv(t)
	var left atomic.Int32
	e.a.d.LocalLeft = func() { left.Add(1) }
	b := e.terminalLogin()
	tok := csrfRe.FindStringSubmatch(b.get("/admin/hmis").Body.String())[1]
	e.clock.advance(localSessionIdle + time.Minute)
	r := b.postRaw("/admin/local/back", url.Values{"csrf": {tok}})
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != KioskURL {
		t.Fatalf("Zurück mit abgelaufener Sitzung: %d %q", r.Code, r.Header().Get("Location"))
	}
	if left.Load() != 1 {
		t.Fatal("Rückkehr nicht gemeldet")
	}
	// Auch ganz ohne Cookie (Anmeldeseite).
	r = e.terminal().postRaw("/admin/local/back", nil)
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != KioskURL {
		t.Fatalf("Zurück ohne Sitzung: %d %q", r.Code, r.Header().Get("Location"))
	}
	// Fremder Ursprung bleibt abgelehnt.
	w := serveLocal(e, localReq(http.MethodPost, "127.0.0.1:8081", "/admin/local/back", map[string]string{"Origin": "http://evil.example"}))
	if w.Code != http.StatusForbidden {
		t.Fatalf("fremder Ursprung: %d", w.Code)
	}
}

func TestLoginPageBackIsFormOnTerminal(t *testing.T) {
	e := newEnv(t)
	body := e.terminal().get("/admin/login").Body.String()
	wantContains(t, body, `id="kiosk-back"`, `action="/admin/local/back"`)
}

func TestLocalAdminHasNoPeerAndRedirectsRoot(t *testing.T) {
	e := newEnv(t)
	if w := serveLocal(e, localReq(http.MethodGet, "127.0.0.1:8081", "/peer/occupancy", nil)); w.Code != http.StatusNotFound {
		t.Fatalf("/peer/: %d", w.Code)
	}
	w := serveLocal(e, localReq(http.MethodGet, "127.0.0.1:8081", "/", nil))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/admin/" {
		t.Fatalf("/: %d %q", w.Code, w.Header().Get("Location"))
	}
	if w := serveLocal(e, localReq(http.MethodGet, "127.0.0.1:8081", "/keyboard.js", nil)); w.Code != http.StatusOK {
		t.Fatalf("/keyboard.js: %d", w.Code)
	}
}

func TestLocalAdminKeyboardAPI(t *testing.T) {
	e := newEnv(t)
	kb := &fakeKeyboard{}
	e.a.d.Keyboard = kb
	ok := map[string]string{"Origin": "http://127.0.0.1:8081"}
	if w := serveLocal(e, localReq(http.MethodPost, "127.0.0.1:8081", "/api/keyboard/show", ok)); w.Code != http.StatusNoContent {
		t.Fatalf("show: %d", w.Code)
	}
	if w := serveLocal(e, localReq(http.MethodPost, "127.0.0.1:8081", "/api/keyboard/hide", map[string]string{"Origin": "http://evil.example"})); w.Code != http.StatusForbidden {
		t.Fatalf("fremder Ursprung: %d", w.Code)
	}
	if w := serveLocal(e, localReq(http.MethodPost, "127.0.0.1:8081", "/api/keyboard/hide", nil)); w.Code != http.StatusForbidden {
		t.Fatalf("ohne Ursprung: %d", w.Code)
	}
	if got := strings.Join(kb.got(), ","); got != "show" {
		t.Fatalf("Aufrufe %q", got)
	}
	// Über HTTPS gibt es die Tastatur nicht.
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "https://admin.test/api/keyboard/show", nil)
	e.a.handler().ServeHTTP(w, req)
	if w.Code == http.StatusNoContent || len(kb.got()) != 1 {
		t.Fatalf("HTTPS steuert die Tastatur: %d", w.Code)
	}
}

func TestLocalPagesAreTouchFriendly(t *testing.T) {
	e := newEnv(t)
	body := e.terminal().get("/admin/login").Body.String()
	wantContains(t, body, `class="on-terminal"`)
	css := e.terminal().get("/admin/admin.css").Body.String()
	wantContains(t, css, ".on-terminal")
}
