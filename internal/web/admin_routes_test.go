package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestAllAdminRoutesRequireLogin(t *testing.T) {
	e := newEnv(t)
	first := newEnvNoPassword(t)
	n := 0
	for _, rt := range e.a.routes() {
		if rt.kind == routeAsset || rt.kind == routeLogin || rt.kind == routeSetup {
			continue
		}
		n++
		methods := []string{rt.method}
		if rt.method == "" {
			methods = []string{http.MethodGet, http.MethodPost}
		}
		for _, m := range methods {
			check := func(env *adminEnv, want string) {
				t.Helper()
				req := formRequest(concrete(rt.path), url.Values{})
				req.Method = m
				r := env.browser().do(req)
				if r.Code != http.StatusSeeOther || r.Header().Get("Location") != want {
					t.Errorf("%s %s: %d nach %q, erwartet %s", m, rt.path, r.Code, r.Header().Get("Location"), want)
				}
			}
			check(e, "/admin/login")
			check(first, "/admin/setup")
		}
	}
	if n < 25 {
		t.Fatalf("nur %d geschützte Routen gefunden", n)
	}
	if len(e.helper.got()) != 0 || e.appliedCount() != 0 {
		t.Error("Aktion ohne Anmeldung ausgeführt")
	}
}

func TestPostWithoutCSRFRejected(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "geheim")
	before := e.cfg()
	n := 0
	for _, rt := range e.a.routes() {
		if rt.kind != routeSession && rt.kind != routeUpload {
			continue
		}
		if rt.method != http.MethodPost && rt.method != "" {
			continue
		}
		n++
		path := concrete(rt.path)
		for name, form := range map[string]url.Values{"fehlt": {}, "falsch": {"csrf": {"AAAA"}}} {
			if r := b.postRaw(path, form); r.Code != http.StatusForbidden {
				t.Errorf("%s (%s): Status %d", rt.path, name, r.Code)
			}
		}
		// Wert nur in der URL gilt nicht
		if r := b.postRaw(path+"?csrf="+url.QueryEscape(b.tokenFrom("/admin/system")), url.Values{}); r.Code != http.StatusForbidden {
			t.Errorf("%s: Wert aus der URL angenommen: %d", rt.path, r.Code)
		}
	}
	if n < 18 {
		t.Fatalf("nur %d POST-Routen", n)
	}
	if len(e.helper.got()) != 0 || e.appliedCount() != 0 || len(e.update.blobs) != 0 {
		t.Error("Aktion ohne CSRF-Wert ausgeführt")
	}
	if after := e.cfg(); len(after.HMIs) != len(before.HMIs) {
		t.Error("Konfiguration geändert")
	}
}

func TestUploadRequiresCSRFBeforeFile(t *testing.T) {
	e, b := newLoggedIn(t)
	tok := b.tokenFrom("/admin/system")
	for _, path := range []string{"/admin/system/update", "/admin/system/import"} {
		if r := b.upload(path, nil, []byte("x")); r.Code != http.StatusForbidden {
			t.Errorf("%s ohne Wert: %d", path, r.Code)
		}
		if r := b.upload(path, [][2]string{{"csrf", "falsch"}}, []byte("x")); r.Code != http.StatusForbidden {
			t.Errorf("%s falscher Wert: %d", path, r.Code)
		}
		if r := b.upload(path, [][2]string{{"password", "x"}, {"csrf", tok}}, []byte("x")); r.Code != http.StatusForbidden {
			t.Errorf("%s Wert nicht zuerst: %d", path, r.Code)
		}
	}
	if len(e.update.blobs) != 0 {
		t.Error("Update ohne CSRF-Wert")
	}
}

func TestForeignOriginRejected(t *testing.T) {
	e, b := newLoggedIn(t)
	tok := b.tokenFrom("/admin/system")
	valid := url.Values{"csrf": {tok}, "profile": {"remmina"}, "quality": {"2"}, "color_depth": {"32"}}
	for _, origin := range []string{"https://evil.example", "null", "https://admin.test.evil.example", "not a url"} {
		req := formRequest("/admin/client", valid)
		req.Header.Set("Origin", origin)
		if r := b.do(req); r.Code != http.StatusForbidden {
			t.Errorf("Origin %q: %d", origin, r.Code)
		}
		lg := e.browser()
		ltok := lg.tokenFrom("/admin/login")
		req = formRequest("/admin/login", url.Values{"csrf": {ltok}, "password": {testAdminPassword}})
		req.Header.Set("Origin", origin)
		if r := lg.do(req); r.Code != http.StatusForbidden {
			t.Errorf("Anmeldung mit Origin %q: %d", origin, r.Code)
		}
	}
	req := formRequest("/admin/client", valid)
	req.Header.Set("Origin", "https://admin.test")
	if r := b.do(req); r.Code != http.StatusSeeOther {
		t.Errorf("eigener Origin: %d %s", r.Code, r.Body.String())
	}
}

func TestOrdinaryFormBodyLimited(t *testing.T) {
	e, b := newLoggedIn(t)
	tok := b.tokenFrom("/admin/system")
	big := strings.Repeat("a", 64<<10)
	r := b.postRaw("/admin/client", url.Values{"csrf": {tok}, "profile": {"remmina"}, "x": {big}})
	if r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Status %d", r.Code)
	}
	lg := e.browser()
	r = lg.postVia("/admin/login", "/admin/login", url.Values{"password": {big}})
	if r.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Anmeldung: %d", r.Code)
	}
}

func TestSecurityHeadersAllowFormsButNoInline(t *testing.T) {
	e := newEnv(t)
	r := e.browser().get("/admin/login")
	h := r.Header()
	csp := h.Get("Content-Security-Policy")
	wantContains(t, csp, "default-src 'none'", "script-src 'self'", "style-src 'self'", "form-action 'self'", "frame-ancestors 'none'", "base-uri 'none'")
	wantNotContains(t, csp, "unsafe-inline")
	if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Cache-Control") != "no-store" {
		t.Errorf("Kopfzeilen: %v", h)
	}
	if h.Get("Strict-Transport-Security") != "" {
		t.Error("HSTS gesetzt")
	}
	for _, p := range []string{"/admin/admin.css", "/admin/admin.js"} {
		if r := e.browser().get(p); r.Code != http.StatusOK || r.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
}
