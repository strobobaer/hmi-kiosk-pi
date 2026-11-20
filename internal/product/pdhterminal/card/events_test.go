package card

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testOrigin = "https://pdh.example"

func newEventsHandler(h *Hub, origin string) http.Handler {
	return EventsHandler(h, &fakeState{state: StateOK}, func() string { return origin })
}

func TestEventsOriginRejected(t *testing.T) {
	cases := []struct{ name, reqOrigin, allowed string }{
		{"kein Origin", "", testOrigin},
		{"fremder Origin", "http://evil.example", testOrigin},
		{"anderer Port", "https://pdh.example:8443", testOrigin},
		{"nicht konfiguriert", testOrigin, ""},
	}
	for _, c := range cases {
		for _, method := range []string{http.MethodGet, http.MethodOptions} {
			t.Run(c.name+" "+method, func(t *testing.T) {
				h := newEventsHandler(NewHub(time.Now), c.allowed)
				r := httptest.NewRequest(method, "/api/card/events", nil)
				if c.reqOrigin != "" {
					r.Header.Set("Origin", c.reqOrigin)
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != http.StatusForbidden {
					t.Fatalf("Status %d", w.Code)
				}
				if v := w.Header().Get("Access-Control-Allow-Origin"); v != "" {
					t.Errorf("Allow-Origin = %q", v)
				}
			})
		}
	}
}

func TestEventsMethodNotAllowed(t *testing.T) {
	h := newEventsHandler(NewHub(time.Now), testOrigin)
	r := httptest.NewRequest(http.MethodPost, "/api/card/events", nil)
	r.Header.Set("Origin", testOrigin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("Status %d", w.Code)
	}
}

func TestEventsPreflight(t *testing.T) {
	h := newEventsHandler(NewHub(time.Now), testOrigin)
	r := httptest.NewRequest(http.MethodOptions, "/api/card/events", nil)
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Status %d", w.Code)
	}
	for k, v := range map[string]string{
		"Access-Control-Allow-Origin":          testOrigin,
		"Access-Control-Allow-Methods":         "GET",
		"Access-Control-Max-Age":               "600",
		"Vary":                                 "Origin",
		"Access-Control-Allow-Private-Network": "true",
	} {
		if got := w.Header().Get(k); got != v {
			t.Errorf("%s = %q, erwartet %q", k, got, v)
		}
	}
	// Ohne Private-Network-Anfrage keine Private-Network-Freigabe.
	r = httptest.NewRequest(http.MethodOptions, "/api/card/events", nil)
	r.Header.Set("Origin", testOrigin)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if v := w.Header().Get("Access-Control-Allow-Private-Network"); v != "" {
		t.Errorf("Allow-Private-Network = %q", v)
	}
}

func TestEventsTooMany(t *testing.T) {
	hub := NewHub(time.Now)
	for i := 0; i < MaxSubscribers; i++ {
		_, cancel, err := hub.Subscribe()
		if err != nil {
			t.Fatal(err)
		}
		defer cancel()
	}
	r := httptest.NewRequest(http.MethodGet, "/api/card/events", nil)
	r.Header.Set("Origin", testOrigin)
	w := httptest.NewRecorder()
	newEventsHandler(hub, testOrigin).ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("Status %d", w.Code)
	}
}

func openStream(t *testing.T, url string) (*http.Response, func()) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Origin", testOrigin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp, func() { resp.Body.Close() }
}

func TestEventsStream(t *testing.T) {
	hub := NewHub(time.Now)
	srv := httptest.NewServer(newEventsHandler(hub, testOrigin))
	defer srv.Close()
	resp, closeBody := openStream(t, srv.URL)
	defer closeBody()
	if resp.StatusCode != 200 {
		t.Fatalf("Status %d", resp.StatusCode)
	}
	for k, v := range map[string]string{
		"Content-Type":                "text/event-stream",
		"Cache-Control":               "no-store",
		"Access-Control-Allow-Origin": testOrigin,
		"Vary":                        "Origin",
	} {
		if got := resp.Header.Get(k); got != v {
			t.Errorf("%s = %q", k, got)
		}
	}
	// Abo ist registriert, sobald die Kopfzeilen da sind (Handler flusht sie zuerst).
	hub.Publish("DEADBEEF")
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		var sb strings.Builder
		for sc.Scan() {
			sb.WriteString(sc.Text() + "\n")
			if strings.Contains(sb.String(), `"uid":"DEADBEEF"`) {
				break
			}
		}
		got <- sb.String()
	}()
	select {
	case body := <-got:
		if !strings.Contains(body, "event: card") || !strings.Contains(body, `"uid":"DEADBEEF"`) {
			t.Errorf("Body: %q", body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("kein Ereignis")
	}
}

func TestEventsPing(t *testing.T) {
	old := pingInterval
	pingInterval = 20 * time.Millisecond
	defer func() { pingInterval = old }()
	srv := httptest.NewServer(newEventsHandler(NewHub(time.Now), testOrigin))
	defer srv.Close()
	resp, closeBody := openStream(t, srv.URL)
	defer closeBody()
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), ": ping") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case <-got:
	case <-time.After(3 * time.Second):
		t.Fatal("kein Ping")
	}
}

func TestEventsDropsStale(t *testing.T) {
	var offset atomic.Int64
	base := time.Unix(1000, 0)
	hub := NewHub(func() time.Time { return base.Add(time.Duration(offset.Load())) })
	hub.Publish("DEADBEEF")
	offset.Store(int64(6 * time.Second))

	srv := httptest.NewServer(newEventsHandler(hub, testOrigin))
	defer srv.Close()
	resp, closeBody := openStream(t, srv.URL)
	defer closeBody()
	got := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: card") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case l := <-got:
		t.Fatalf("veraltetes Ereignis geliefert: %q", l)
	case <-time.After(300 * time.Millisecond):
	}
}

type fakeState struct {
	mu      sync.Mutex
	state   string
	subs    []chan struct{}
	stopped int // Zahl der Abmeldungen von WatchState
}

func (f *fakeState) stops() int { f.mu.Lock(); defer f.mu.Unlock(); return f.stopped }

func (f *fakeState) ReaderState() string { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *fakeState) WatchState() (<-chan struct{}, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{}, 1)
	f.subs = append(f.subs, ch)
	return ch, func() { f.mu.Lock(); f.stopped++; f.mu.Unlock() }
}
func (f *fakeState) set(s string, notify bool) {
	f.mu.Lock()
	f.state = s
	subs := append([]chan struct{}(nil), f.subs...)
	f.mu.Unlock()
	if notify {
		for _, c := range subs {
			select {
			case c <- struct{}{}:
			default:
			}
		}
	}
}

func readLines(resp *http.Response) <-chan string {
	out := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			out <- sc.Text()
		}
		close(out)
	}()
	return out
}

// nextEvent liefert die nächste data-Zeile samt vorangehendem event-Namen.
func nextEvent(t *testing.T, lines <-chan string, wait time.Duration) (string, string, bool) {
	t.Helper()
	var ev string
	timeout := time.After(wait)
	for {
		select {
		case l, ok := <-lines:
			if !ok {
				return "", "", false
			}
			if strings.HasPrefix(l, "event: ") {
				ev = strings.TrimPrefix(l, "event: ")
			}
			if strings.HasPrefix(l, "data: ") {
				return ev, strings.TrimPrefix(l, "data: "), true
			}
		case <-timeout:
			return "", "", false
		}
	}
}

func TestEventsReaderEvent(t *testing.T) {
	hub := NewHub(time.Now)
	st := &fakeState{state: StateNone}
	srv := httptest.NewServer(EventsHandler(hub, st, func() string { return testOrigin }))
	defer srv.Close()
	resp, closeBody := openStream(t, srv.URL)
	defer closeBody()
	lines := readLines(resp)

	// Erstes Ereignis: reader mit aktuellem Zustand.
	ev, data, ok := nextEvent(t, lines, 3*time.Second)
	if !ok || ev != "reader" || data != `{"state":"none"}` {
		t.Fatalf("erstes Ereignis: %q %q %v", ev, data, ok)
	}
	// Signal ohne Wechsel -> kein doppeltes Ereignis.
	st.set(StateNone, true)
	if ev, data, ok := nextEvent(t, lines, 200*time.Millisecond); ok {
		t.Fatalf("doppeltes Ereignis: %q %q", ev, data)
	}
	// Wechsel -> neues reader.
	st.set(StateOK, true)
	ev, data, ok = nextEvent(t, lines, 3*time.Second)
	if !ok || ev != "reader" || data != `{"state":"ok"}` {
		t.Fatalf("Wechsel: %q %q %v", ev, data, ok)
	}
	// card-Ereignisse unverändert.
	hub.Publish("DEADBEEF")
	ev, data, ok = nextEvent(t, lines, 3*time.Second)
	if !ok || ev != "card" || !strings.Contains(data, `"uid":"DEADBEEF"`) {
		t.Fatalf("card: %q %q %v", ev, data, ok)
	}
}

func TestEventsReaderOriginUnchanged(t *testing.T) {
	st := &fakeState{state: StateOK}
	h := EventsHandler(NewHub(time.Now), st, func() string { return testOrigin })
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Origin", "http://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "reader") {
		t.Fatalf("Status %d Body %q", w.Code, w.Body.String())
	}
}

// Beim Ende des Stroms (Client trennt) wird WatchState abgemeldet.
func TestEventsReaderUnsubscribeOnEnd(t *testing.T) {
	st := &fakeState{state: StateOK}
	srv := httptest.NewServer(EventsHandler(NewHub(time.Now), st, func() string { return testOrigin }))
	defer srv.Close()
	resp, closeBody := openStream(t, srv.URL)
	lines := readLines(resp)
	if ev, _, ok := nextEvent(t, lines, 3*time.Second); !ok || ev != "reader" {
		t.Fatalf("erstes Ereignis: %q %v", ev, ok)
	}
	if n := st.stops(); n != 0 {
		t.Fatalf("zu früh abgemeldet: %d", n)
	}
	closeBody()
	deadline := time.Now().Add(3 * time.Second)
	for st.stops() != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := st.stops(); n != 1 {
		t.Fatalf("Abmeldungen nach Stromende: %d, erwartet 1", n)
	}
}

type fakeShower struct {
	calls []showCall
}

type showCall struct {
	kinds      []string
	text, tone string
}

func (f *fakeShower) Show(kinds []string, text, tone string) {
	f.calls = append(f.calls, showCall{kinds, text, tone})
}

func postResult(h http.Handler, origin, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/card/result", strings.NewReader(body))
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const resultBody = `{"ok":true,"name":"M. Muster","notify":["overlay"]}`

func TestResultEndpoint(t *testing.T) {
	fg := false
	sh := &fakeShower{}
	h := ResultHandler(func() string { return testOrigin }, func() bool { return fg }, sh)

	if w := postResult(h, "http://evil.example", resultBody); w.Code != http.StatusForbidden {
		t.Errorf("falscher Ursprung: %d", w.Code)
	}
	if w := postResult(h, "", resultBody); w.Code != http.StatusForbidden {
		t.Errorf("ohne Ursprung: %d", w.Code)
	}
	if len(sh.calls) != 0 {
		t.Fatalf("nichts darf erscheinen: %v", sh.calls)
	}

	w := postResult(h, testOrigin, resultBody)
	if w.Code != http.StatusNoContent {
		t.Fatalf("Status %d", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != testOrigin {
		t.Errorf("Allow-Origin fehlt")
	}
	want := []showCall{{[]string{"overlay"}, "Angemeldet: M. Muster", "ok"}}
	if !reflect.DeepEqual(sh.calls, want) {
		t.Fatalf("Show = %v", sh.calls)
	}

	// Fenster vorn: nichts.
	sh.calls, fg = nil, true
	if w := postResult(h, testOrigin, resultBody); w.Code != http.StatusNoContent || len(sh.calls) != 0 {
		t.Errorf("Vordergrund: %d, %v", w.Code, sh.calls)
	}
	fg = false

	// Alter PDH-Stand ohne notify, off: nichts.
	for _, b := range []string{`{"ok":true,"name":"A"}`, `{"ok":true,"name":"A","notify":["off","toast"]}`} {
		if w := postResult(h, testOrigin, b); w.Code != http.StatusNoContent || len(sh.calls) != 0 {
			t.Errorf("%s: %d, %v", b, w.Code, sh.calls)
		}
	}

	// Bereinigt und gekürzt.
	long := strings.Repeat("x", 300)
	postResult(h, testOrigin, `{"ok":false,"message":"Kar\u0000te `+long+`","notify":["banner","toast"]}`)
	if len(sh.calls) != 1 || sh.calls[0].tone != "error" || !strings.HasPrefix(sh.calls[0].text, "Karte xxx") ||
		len([]rune(sh.calls[0].text)) != 120 || !reflect.DeepEqual(sh.calls[0].kinds, []string{"toast", "banner"}) {
		t.Errorf("Show = %+v", sh.calls)
	}

	// Zu groß und kaputt.
	big := `{"ok":true,"name":"` + strings.Repeat("a", 2100) + `","notify":["overlay"]}`
	sh.calls = nil
	if w := postResult(h, testOrigin, big); w.Code != http.StatusRequestEntityTooLarge || len(sh.calls) != 0 {
		t.Errorf("zu groß: %d", w.Code)
	}
	if w := postResult(h, testOrigin, `{`); w.Code != http.StatusBadRequest {
		t.Errorf("kaputt: %d", w.Code)
	}
	// Auch ohne Content-Length (chunked) bleibt die Grenze.
	r := httptest.NewRequest(http.MethodPost, "/api/card/result", io.NopCloser(strings.NewReader(big)))
	r.ContentLength = -1
	r.Header.Set("Origin", testOrigin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("chunked: %d", rec.Code)
	}

	r = httptest.NewRequest(http.MethodGet, "/api/card/result", nil)
	r.Header.Set("Origin", testOrigin)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", rec.Code)
	}
}

func TestResultPreflight(t *testing.T) {
	h := ResultHandler(func() string { return testOrigin }, func() bool { return false }, &fakeShower{})
	r := httptest.NewRequest(http.MethodOptions, "/api/card/result", nil)
	r.Header.Set("Origin", testOrigin)
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Private-Network") != "true" ||
		w.Header().Get("Access-Control-Allow-Origin") != testOrigin || w.Header().Get("Access-Control-Allow-Methods") != "POST" {
		t.Errorf("Vorabanfrage: %d %v", w.Code, w.Header())
	}
	r = httptest.NewRequest(http.MethodOptions, "/api/card/result", nil)
	r.Header.Set("Origin", "http://evil.example")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("fremder Ursprung: %d", w.Code)
	}
}

// Am Raspberry Pi liegt das Fenster immer vorn und der Platzhalter zeigt nichts: nur 204.
func TestResultEndpointLinux(t *testing.T) {
	sh := &fakeShower{}
	h := ResultHandler(func() string { return testOrigin }, func() bool { return true }, sh)
	if w := postResult(h, testOrigin, resultBody); w.Code != http.StatusNoContent || len(sh.calls) != 0 {
		t.Errorf("Status %d, Show %v", w.Code, sh.calls)
	}
}
