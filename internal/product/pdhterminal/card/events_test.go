package card

import (
	"bufio"
	"net/http"
	"net/http/httptest"
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
	mu    sync.Mutex
	state string
	subs  []chan struct{}
}

func (f *fakeState) ReaderState() string { f.mu.Lock(); defer f.mu.Unlock(); return f.state }
func (f *fakeState) WatchState() (<-chan struct{}, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan struct{}, 1)
	f.subs = append(f.subs, ch)
	return ch, func() {}
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

// nextData liefert die nächste data-Zeile samt vorangehendem event-Namen.
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
