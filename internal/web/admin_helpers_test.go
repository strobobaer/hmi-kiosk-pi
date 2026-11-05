package web

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
	"hmikiosk/internal/secrets"
)

const testAdminPassword = "richtig-und-lang"

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type helperCall struct{ cmd, arg string }

type fakeHelper struct {
	mu    sync.Mutex
	calls []helperCall
	err   error
}

func (f *fakeHelper) Call(_ context.Context, cmd, arg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, helperCall{cmd, arg})
	return f.err
}

func (f *fakeHelper) got() []helperCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]helperCall(nil), f.calls...)
}

type fakeUpdate struct {
	mu    sync.Mutex
	blobs [][]byte
	err   error
}

func (f *fakeUpdate) Install(_ context.Context, blob []byte) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blobs = append(f.blobs, blob)
	return "1.2.3", f.err
}

// adminEnv ist ein Admin-Handler mit Fälschungen für alle Abhängigkeiten.
type adminEnv struct {
	t        *testing.T
	dir      string
	store    *config.Store
	box      *secrets.Box
	helper   *fakeHelper
	update   *fakeUpdate
	clock    *fakeClock
	logDir   string
	wifi     bool
	eth      bool
	probe    func(context.Context, config.HMI) rfb.ProbeResult
	selftest func(context.Context) []SelftestStep
	soak     *fakeSoak
	mu       sync.Mutex
	applied  []config.Config
	a        *admin
}

// useFastHash ersetzt Argon2 durch einen Vergleich, sonst dauert jeder Test Sekunden.
func useFastHash(t *testing.T) {
	t.Helper()
	oldHash, oldCheck := hashPassword, checkPassword
	hashPassword = func(pw string) (string, error) { return "fast$" + pw, nil }
	checkPassword = func(hash, pw string) bool { return hash == "fast$"+pw }
	t.Cleanup(func() { hashPassword, checkPassword = oldHash, oldCheck })
}

func newEnvNoPassword(t *testing.T) *adminEnv {
	t.Helper()
	useFastHash(t)
	dir := t.TempDir()
	box, err := secrets.OpenBox(dir + "/secrets.key")
	if err != nil {
		t.Fatal(err)
	}
	e := &adminEnv{
		t: t, dir: dir, store: config.Open(dir), box: box,
		helper: &fakeHelper{}, update: &fakeUpdate{}, soak: &fakeSoak{},
		clock: &fakeClock{t: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)},
		eth:   true, logDir: t.TempDir(),
	}
	e.a = newAdmin(AdminDeps{
		Store: e.store, Box: e.box,
		Apply: func(c config.Config) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.applied = append(e.applied, c)
		},
		Probe: func(ctx context.Context, h config.HMI) rfb.ProbeResult {
			if e.probe != nil {
				return e.probe(ctx, h)
			}
			return rfb.ProbeResult{Reachable: true, AuthOK: true, Width: 1280, Height: 800, Name: "Testserver"}
		},
		Helper: e.helper,
		Status: func() SystemStatus {
			return SystemStatus{Version: "0.9.1", OverlayActive: true, DataFreeBytes: 3 << 30, DataTotalBytes: 8 << 30, Display: "1920x1080", Warnings: []string{"Testwarnung"}}
		},
		LogDir:       e.logDir,
		RemoteIsWifi: func(*http.Request) bool { return e.wifi },
		EthernetLink: func() bool { return e.eth },
		Update:       e.update,
		Soak:         e.soak,
		Selftest: func(ctx context.Context) []SelftestStep {
			if e.selftest != nil {
				return e.selftest(ctx)
			}
			return []SelftestStep{{Name: "Remmina", OK: true, Detail: "gefunden"}, {Name: "Touch", OK: false, Detail: "fehlt"}}
		},
		Clock: e.clock,
	})
	return e
}

func newEnv(t *testing.T) *adminEnv {
	t.Helper()
	e := newEnvNoPassword(t)
	e.mutate(func(c *config.Config) {
		c.Admin.PasswordHash = "fast$" + testAdminPassword
		c.Terminal.Name = "Terminal 1"
	})
	return e
}

// newLoggedIn liefert einen angemeldeten Browser.
func newLoggedIn(t *testing.T) (*adminEnv, *browser) {
	t.Helper()
	e := newEnv(t)
	return e, e.login()
}

func (e *adminEnv) mutate(fn func(*config.Config)) {
	e.t.Helper()
	c := e.cfg()
	fn(&c)
	if err := e.store.Save(c); err != nil {
		e.t.Fatal(err)
	}
}

func (e *adminEnv) cfg() config.Config {
	e.t.Helper()
	c, _, err := e.store.Load()
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *adminEnv) appliedCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.applied)
}

func (e *adminEnv) hmi(id string) config.HMI {
	e.t.Helper()
	for _, h := range e.cfg().HMIs {
		if h.ID == id {
			return h
		}
	}
	e.t.Fatalf("HMI %q fehlt", id)
	return config.HMI{}
}

func (e *adminEnv) addHMI(id, name, password string) {
	e.t.Helper()
	enc := ""
	if password != "" {
		var err error
		if enc, err = e.box.Seal(password); err != nil {
			e.t.Fatal(err)
		}
	}
	e.mutate(func(c *config.Config) {
		c.HMIs = append(c.HMIs, config.HMI{ID: id, Name: name, Host: "10.0.0.5", Port: 5900, PasswordEnc: enc, Icon: "press", Color: "blue"})
	})
}

// breakSave lässt das Speichern scheitern, das Lesen aber gelingen: config.json
// wird zum Verzeichnis (als root greifen Dateirechte nicht), geladen wird der
// letzte gute Stand.
func breakSave(e *adminEnv) {
	e.t.Helper()
	if err := e.store.MarkGood(); err != nil {
		e.t.Fatal(err)
	}
	p := filepath.Join(e.dir, "config.json")
	if err := os.Remove(p); err != nil {
		e.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(p, "belegt"), 0o700); err != nil {
		e.t.Fatal(err)
	}
}

// browser hält die Cookies einer Besucherin. Jeder Browser hat eigene Cookies.
type browser struct {
	e   *adminEnv
	jar map[string]string
}

func (e *adminEnv) browser() *browser { return &browser{e: e, jar: map[string]string{}} }

func (e *adminEnv) login() *browser {
	e.t.Helper()
	b := e.browser()
	r := b.postPage("/admin/login", url.Values{"password": {testAdminPassword}})
	if r.Code != http.StatusSeeOther {
		e.t.Fatalf("Anmeldung: %d %s", r.Code, r.Body.String())
	}
	return b
}

func (b *browser) do(req *http.Request) *httptest.ResponseRecorder {
	for k, v := range b.jar {
		req.AddCookie(&http.Cookie{Name: k, Value: v})
	}
	w := httptest.NewRecorder()
	b.e.a.handler().ServeHTTP(w, req)
	for _, c := range w.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.jar, c.Name)
		} else {
			b.jar[c.Name] = c.Value
		}
	}
	return w
}

func (b *browser) get(path string) *httptest.ResponseRecorder {
	return b.do(httptest.NewRequest(http.MethodGet, "https://admin.test"+path, nil))
}

func formRequest(path string, v url.Values) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "https://admin.test"+path, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

// postRaw sendet ohne CSRF-Feld, außer der Aufrufer hat es gesetzt.
func (b *browser) postRaw(path string, v url.Values) *httptest.ResponseRecorder {
	return b.do(formRequest(path, v))
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (b *browser) tokenFrom(page string) string {
	b.e.t.Helper()
	r := b.get(page)
	m := csrfRe.FindStringSubmatch(r.Body.String())
	if m == nil {
		b.e.t.Fatalf("%s: kein CSRF-Feld (%d)", page, r.Code)
	}
	return m[1]
}

// postPage holt den Wert aus page und sendet an page.
func (b *browser) postPage(page string, v url.Values) *httptest.ResponseRecorder {
	b.e.t.Helper()
	return b.postVia(page, page, v)
}

func (b *browser) postVia(page, path string, v url.Values) *httptest.ResponseRecorder {
	b.e.t.Helper()
	if v == nil {
		v = url.Values{}
	}
	v.Set("csrf", b.tokenFrom(page))
	return b.postRaw(path, v)
}

// post sendet als angemeldete Besucherin mit gültigem CSRF-Wert.
func (b *browser) post(path string, v url.Values) *httptest.ResponseRecorder {
	b.e.t.Helper()
	return b.postVia("/admin/client", path, v)
}

// follow liest nach einer Weiterleitung die Zielseite.
func (b *browser) follow(r *httptest.ResponseRecorder) string {
	b.e.t.Helper()
	if r.Code != http.StatusSeeOther {
		b.e.t.Fatalf("Weiterleitung erwartet, Status %d: %s", r.Code, r.Body.String())
	}
	return b.get(r.Header().Get("Location")).Body.String()
}

// upload sendet multipart in der Reihenfolge der Felder, die Datei zuletzt.
func (b *browser) upload(path string, fields [][2]string, file []byte) *httptest.ResponseRecorder {
	b.e.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range fields {
		if err := mw.WriteField(f[0], f[1]); err != nil {
			b.e.t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", "datei.bin")
	if err != nil {
		b.e.t.Fatal(err)
	}
	if _, err := fw.Write(file); err != nil {
		b.e.t.Fatal(err)
	}
	mw.Close()
	req := httptest.NewRequest(http.MethodPost, "https://admin.test"+path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return b.do(req)
}

func wantContains(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if !strings.Contains(body, p) {
			t.Errorf("%q fehlt in:\n%s", p, body)
		}
	}
}

func wantNotContains(t *testing.T, body string, parts ...string) {
	t.Helper()
	for _, p := range parts {
		if strings.Contains(body, p) {
			t.Errorf("%q darf nicht vorkommen in:\n%s", p, body)
		}
	}
}
