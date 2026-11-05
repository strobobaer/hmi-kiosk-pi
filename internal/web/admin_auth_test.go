package web

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hmikiosk/internal/secrets"
)

func concrete(path string) string {
	return strings.NewReplacer("{id}", "x", "{$}", "").Replace(path)
}

func TestFirstStartForcesPasswordSetup(t *testing.T) {
	e := newEnvNoPassword(t)
	b := e.browser()
	for _, p := range []string{"/admin/", "/admin/login", "/admin/hmis", "/admin/system", "/admin/unbekannt"} {
		r := b.get(p)
		if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/setup" {
			t.Errorf("%s: %d %q", p, r.Code, r.Header().Get("Location"))
		}
	}
	// zu kurz: bleibt ohne Passwort
	r := b.postPage("/admin/setup", url.Values{"password": {"123456789"}, "password2": {"123456789"}})
	if r.Code == http.StatusSeeOther || e.cfg().Admin.PasswordHash != "" {
		t.Fatalf("zu kurzes Passwort angenommen: %d", r.Code)
	}
	wantContains(t, r.Body.String(), "mindestens 10 Zeichen")
	// verschieden
	r = b.postPage("/admin/setup", url.Values{"password": {"1234567890"}, "password2": {"0987654321"}})
	if r.Code == http.StatusSeeOther || e.cfg().Admin.PasswordHash != "" {
		t.Fatalf("abweichende Wiederholung angenommen: %d", r.Code)
	}
	// Umlaute zählen als ein Zeichen, nicht als zwei Bytes
	r = b.postPage("/admin/setup", url.Values{"password": {"äöüäöü"}, "password2": {"äöüäöü"}})
	if e.cfg().Admin.PasswordHash != "" {
		t.Fatalf("Länge in Bytes gezählt: %d", r.Code)
	}

	r = b.postPage("/admin/setup", url.Values{"password": {"1234567890"}, "password2": {"1234567890"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Einrichtung: %d %s", r.Code, r.Body.String())
	}
	if e.cfg().Admin.PasswordHash != "fast$1234567890" {
		t.Fatalf("Hash: %q", e.cfg().Admin.PasswordHash)
	}
	if e.appliedCount() != 1 {
		t.Errorf("Apply %d mal", e.appliedCount())
	}
	// das Setzen meldet an
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Errorf("nach Einrichtung nicht angemeldet: %d", r.Code)
	}
	// danach gibt es die Seite nicht mehr, auch nicht für andere
	for _, br := range []*browser{b, e.browser()} {
		if r := br.get("/admin/setup"); r.Code != http.StatusNotFound {
			t.Errorf("GET setup: %d", r.Code)
		}
		if r := br.postRaw("/admin/setup", url.Values{"password": {"anderes-passwort"}, "password2": {"anderes-passwort"}}); r.Code != http.StatusNotFound {
			t.Errorf("POST setup: %d", r.Code)
		}
	}
	if e.cfg().Admin.PasswordHash != "fast$1234567890" {
		t.Error("Passwort durch zweite Einrichtung geändert")
	}
}

func TestSetupRequiresPreSessionToken(t *testing.T) {
	e := newEnvNoPassword(t)
	b := e.browser()
	form := url.Values{"password": {"1234567890"}, "password2": {"1234567890"}}
	if r := b.postRaw("/admin/setup", form); r.Code != http.StatusForbidden {
		t.Errorf("ohne Wert: %d", r.Code)
	}
	tok := b.tokenFrom("/admin/setup")
	// Wert aus dem Formular, aber ohne das zugehörige Cookie
	form.Set("csrf", tok)
	if r := e.browser().postRaw("/admin/setup", form); r.Code != http.StatusForbidden {
		t.Errorf("ohne Cookie: %d", r.Code)
	}
	form.Set("csrf", "falsch")
	if r := b.postRaw("/admin/setup", form); r.Code != http.StatusForbidden {
		t.Errorf("falscher Wert: %d", r.Code)
	}
	if e.cfg().Admin.PasswordHash != "" {
		t.Error("Passwort gesetzt")
	}
}

// Parallele Einrichtung: Nur die erste darf gelten, sonst könnte eine zweite
// Person das soeben gesetzte Passwort überschreiben.
func TestConcurrentSetupOnlyOneWins(t *testing.T) {
	e := newEnvNoPassword(t)
	var wg sync.WaitGroup
	var wins atomic.Int32
	// Erst alle Formulare holen: Sobald jemand gewinnt, gibt es die Seite nicht mehr.
	browsers := make([]*browser, 8)
	tokens := make([]string, 8)
	for i := range browsers {
		browsers[i] = e.browser()
		tokens[i] = browsers[i].tokenFrom("/admin/setup")
	}
	for i, b := range browsers {
		pw := "passwort-nummer-" + string(rune('a'+i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := b.postRaw("/admin/setup", url.Values{"csrf": {tokens[i]}, "password": {pw}, "password2": {pw}})
			if r.Code == http.StatusSeeOther {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 || e.appliedCount() != 1 {
		t.Errorf("Gewinner %d, Apply %d", wins.Load(), e.appliedCount())
	}
}

func TestLoginRequiresPreSessionToken(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	check := checkPassword
	checkPassword = func(h, p string) bool { calls.Add(1); return check(h, p) }
	b := e.browser()
	if r := b.postRaw("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusForbidden {
		t.Errorf("Status %d", r.Code)
	}
	if calls.Load() != 0 {
		t.Error("Passwort ohne CSRF-Wert geprüft")
	}
}

func TestLoginWithRealArgon2(t *testing.T) {
	e := newEnv(t)
	hashPassword, checkPassword = secrets.HashPassword, secrets.CheckPassword
	h, err := secrets.HashPassword(testAdminPassword)
	if err != nil {
		t.Fatal(err)
	}
	c := e.cfg()
	c.Admin.PasswordHash = h
	if err := e.store.Save(c); err != nil {
		t.Fatal(err)
	}
	b := e.browser()
	if r := b.postPage("/admin/login", url.Values{"password": {"falsches-passwort"}}); r.Code == http.StatusSeeOther {
		t.Fatal("falsches Passwort angenommen")
	}
	if r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusSeeOther {
		t.Fatalf("richtiges Passwort abgelehnt: %d", r.Code)
	}
}

func TestLockout5FailuresFor5Minutes(t *testing.T) {
	e := newEnv(t)
	var calls atomic.Int32
	check := checkPassword
	checkPassword = func(h, p string) bool { calls.Add(1); return check(h, p) }

	b := e.browser()
	bad := url.Values{"password": {"falsch-falsch"}}
	for i := 0; i < 5; i++ {
		if r := b.postPage("/admin/login", bad); r.Code == http.StatusSeeOther {
			t.Fatalf("Versuch %d angenommen", i)
		}
	}
	if calls.Load() != 5 {
		t.Fatalf("Prüfungen: %d", calls.Load())
	}
	// gesperrt, auch mit richtigem Passwort und aus einem anderen Browser; keine Prüfung
	for _, br := range []*browser{b, e.browser()} {
		r := br.postPage("/admin/login", url.Values{"password": {testAdminPassword}})
		if r.Code != http.StatusTooManyRequests {
			t.Fatalf("Sperre: %d", r.Code)
		}
		wantContains(t, r.Body.String(), "gesperrt")
	}
	if calls.Load() != 5 {
		t.Fatalf("während der Sperre geprüft: %d", calls.Load())
	}
	e.clock.advance(5*time.Minute - time.Second)
	if r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusTooManyRequests {
		t.Fatalf("Sperre zu kurz: %d", r.Code)
	}
	e.clock.advance(time.Second)
	if r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusSeeOther {
		t.Fatalf("nach Ablauf: %d", r.Code)
	}
}

func TestLockoutCounterResetsOnSuccess(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	bad := url.Values{"password": {"falsch-falsch"}}
	for i := 0; i < 4; i++ {
		b.postPage("/admin/login", bad)
	}
	if r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d", r.Code)
	}
	c := e.browser()
	for i := 0; i < 4; i++ {
		c.postPage("/admin/login", bad)
	}
	if r := c.postPage("/admin/login", url.Values{"password": {testAdminPassword}}); r.Code != http.StatusSeeOther {
		t.Fatalf("Zähler nicht zurückgesetzt: %d", r.Code)
	}
}

// Gleichzeitige Anfragen dürfen die Sperre nicht umgehen und nicht jede 64 MB belegen.
func TestConcurrentLoginsCannotBypassLockout(t *testing.T) {
	e := newEnv(t)
	var calls, running, maxRunning atomic.Int32
	check := checkPassword
	checkPassword = func(h, p string) bool {
		calls.Add(1)
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		running.Add(-1)
		return check(h, p)
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		b := e.browser()
		tok := b.tokenFrom("/admin/login")
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.postRaw("/admin/login", url.Values{"csrf": {tok}, "password": {"falsch-falsch"}})
		}()
	}
	wg.Wait()
	if calls.Load() != 5 {
		t.Errorf("Prüfungen: %d, erwartet 5", calls.Load())
	}
	if maxRunning.Load() != 1 {
		t.Errorf("gleichzeitig: %d", maxRunning.Load())
	}
}

func TestSessionCookieSecureHttpOnlySameSite(t *testing.T) {
	e := newEnv(t)
	b := e.browser()
	r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}})
	var found bool
	for _, c := range r.Result().Cookies() {
		if c.Name != "hmi_admin" {
			continue
		}
		found = true
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/admin" {
			t.Errorf("Cookie: %+v", c)
		}
		if len(c.Value) < 43 { // 256 Bit in Base64url
			t.Errorf("Wert zu kurz: %d", len(c.Value))
		}
	}
	if !found {
		t.Fatal("kein Cookie")
	}
	pre := e.browser().get("/admin/login")
	cookies := pre.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("kein Vor-Cookie")
	}
	for _, c := range cookies {
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/admin" {
			t.Errorf("Vor-Cookie: %+v", c)
		}
	}
}

func TestSessionExpiresAfter30MinIdle(t *testing.T) {
	e, b := newLoggedIn(t)
	e.clock.advance(29 * time.Minute)
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("zu früh abgelaufen: %d", r.Code)
	}
	e.clock.advance(29 * time.Minute) // Nutzung hat die Frist verlängert
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatalf("Frist nicht verlängert: %d", r.Code)
	}
	e.clock.advance(30 * time.Minute)
	r := b.get("/admin/hmis")
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/login" {
		t.Fatalf("nicht abgelaufen: %d", r.Code)
	}
}

func TestLoginIssuesFreshTokenEachTime(t *testing.T) {
	e := newEnv(t)
	a, c := e.login(), e.login()
	if a.jar[sessionCookie] == c.jar[sessionCookie] {
		t.Error("gleicher Wert")
	}
	if a.tokenFrom("/admin/hmis") == c.tokenFrom("/admin/hmis") {
		t.Error("gleicher CSRF-Wert")
	}
}

func TestChangePassword(t *testing.T) {
	e, b := newLoggedIn(t)
	other := e.login()
	r := b.post("/admin/system/password", url.Values{"current": {"falsch-falsch"}, "password": {"neues-passwort-1"}, "password2": {"neues-passwort-1"}})
	if r.Code == http.StatusSeeOther || e.cfg().Admin.PasswordHash != "fast$"+testAdminPassword {
		t.Fatalf("falsches aktuelles Passwort angenommen: %d", r.Code)
	}
	r = b.post("/admin/system/password", url.Values{"current": {testAdminPassword}, "password": {"kurz"}, "password2": {"kurz"}})
	if r.Code == http.StatusSeeOther || e.cfg().Admin.PasswordHash != "fast$"+testAdminPassword {
		t.Fatalf("kurzes Passwort angenommen: %d", r.Code)
	}
	r = b.post("/admin/system/password", url.Values{"current": {testAdminPassword}, "password": {"neues-passwort-1"}, "password2": {"neues-passwort-1"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if e.cfg().Admin.PasswordHash != "fast$neues-passwort-1" {
		t.Fatal("nicht gespeichert")
	}
	if r := other.get("/admin/hmis"); r.Code != http.StatusSeeOther {
		t.Errorf("andere Sitzung lebt: %d", r.Code)
	}
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Errorf("eigene Sitzung beendet: %d", r.Code)
	}
}

func TestChangePasswordCountsTowardsLockout(t *testing.T) {
	_, b := newLoggedIn(t)
	var calls atomic.Int32
	check := checkPassword
	checkPassword = func(h, p string) bool { calls.Add(1); return check(h, p) }
	form := func(cur string) url.Values {
		return url.Values{"current": {cur}, "password": {"neues-passwort-1"}, "password2": {"neues-passwort-1"}}
	}
	for i := 0; i < 5; i++ {
		b.post("/admin/system/password", form("falsch-falsch"))
	}
	r := b.post("/admin/system/password", form(testAdminPassword))
	if r.Code != http.StatusTooManyRequests || calls.Load() != 5 {
		t.Fatalf("Status %d, Prüfungen %d", r.Code, calls.Load())
	}
}

func TestChangePasswordSaveFailureKeepsSessions(t *testing.T) {
	e, b := newLoggedIn(t)
	other := e.login()
	breakSave(e)
	r := b.post("/admin/system/password", url.Values{"current": {testAdminPassword}, "password": {"neues-passwort-1"}, "password2": {"neues-passwort-1"}})
	if r.Code == http.StatusSeeOther {
		t.Fatal("Erfolg gemeldet")
	}
	wantContains(t, r.Body.String(), "bisherigen Einstellungen")
	if r := other.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Errorf("andere Sitzung beendet: %d", r.Code)
	}
}

func TestLogout(t *testing.T) {
	_, b := newLoggedIn(t)
	if r := b.postRaw("/admin/logout", url.Values{}); r.Code != http.StatusForbidden {
		t.Fatalf("ohne CSRF: %d", r.Code)
	}
	if r := b.get("/admin/hmis"); r.Code != http.StatusOK {
		t.Fatal("abgemeldet ohne CSRF")
	}
	cookie := b.jar[sessionCookie]
	r := b.post("/admin/logout", nil)
	if r.Code != http.StatusSeeOther || r.Header().Get("Location") != "/admin/login" {
		t.Fatalf("Status %d", r.Code)
	}
	b.jar[sessionCookie] = cookie // Serverseitig ungültig, nicht nur im Browser gelöscht
	if r := b.get("/admin/hmis"); r.Code != http.StatusSeeOther {
		t.Errorf("Sitzung lebt: %d", r.Code)
	}
}
