package coord

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

var testKey = []byte("0123456789abcdef0123456789abcdef")

// lockedClock: Der Server liest die Uhr in eigenen Goroutinen.
type lockedClock struct {
	mu sync.Mutex
	t  time.Time
}

func (l *lockedClock) Now() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.t
}

func (l *lockedClock) Advance(d time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.t = l.t.Add(d)
}

func newAPI(t *testing.T, key []byte) (*Coordinator, *lockedClock, *httptest.Server) {
	t.Helper()
	clk := &lockedClock{t: time.Unix(1000, 0)}
	c := New(settings("a", nor, "b"), &fakeTransport{}, clk)
	srv := httptest.NewServer(NewHandler(c, key))
	t.Cleanup(srv.Close)
	return c, clk, srv
}

func fetchNonce(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := srv.Client().Post(srv.URL+pathNonce, "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var out nonceResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Nonce
}

func post(t *testing.T, srv *httptest.Server, path, nonce, sig string, body []byte) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(headerNonce, nonce)
	req.Header.Set(headerSignature, sig)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

func takeoverBody(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(takeoverReq{HMI: "h1", By: Holder{Terminal: "b", Name: "B"}, Warn: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandlerRejectsBadSignature(t *testing.T) {
	c, _, srv := newAPI(t, testKey)
	body := takeoverBody(t)
	nonce := fetchNonce(t, srv)

	if got := post(t, srv, pathTakeover, nonce, sign([]byte("wrong key"), pathTakeover, nonce, body), body); got != 401 {
		t.Fatalf("wrong key: %d", got)
	}
	if got := post(t, srv, pathTakeover, nonce, "kein-hex", body); got != 401 {
		t.Fatalf("garbage: %d", got)
	}
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathHeartbeat, nonce, body), body); got != 401 {
		t.Fatalf("other path: %d", got)
	}
	tampered := append(append([]byte(nil), body...), ' ')
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathTakeover, nonce, body), tampered); got != 401 {
		t.Fatalf("tampered body: %d", got)
	}
	noEvent(t, c)

	// Ungültige Versuche verbrauchen die Zufallszahl nicht.
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathTakeover, nonce, body), body); got != 200 {
		t.Fatalf("valid after failures: %d", got)
	}
}

func TestHandlerRejectsReusedNonce(t *testing.T) {
	c, _, srv := newAPI(t, testKey)
	body := takeoverBody(t)
	nonce := fetchNonce(t, srv)
	sig := sign(testKey, pathTakeover, nonce, body)
	if got := post(t, srv, pathTakeover, nonce, sig, body); got != 200 {
		t.Fatalf("first: %d", got)
	}
	<-c.Events()
	if got := post(t, srv, pathTakeover, nonce, sig, body); got != 401 {
		t.Fatalf("second: %d", got)
	}
	noEvent(t, c)
}

func TestHandlerRejectsUnknownNonce(t *testing.T) {
	_, _, srv := newAPI(t, testKey)
	body := takeoverBody(t)
	nonce := strings.Repeat("ab", 32)
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathTakeover, nonce, body), body); got != 401 {
		t.Fatalf("got %d", got)
	}
}

func TestHandlerRejectsEverythingWithEmptyKey(t *testing.T) {
	_, _, srv := newAPI(t, nil)
	body := takeoverBody(t)
	nonce := fetchNonce(t, srv)
	if got := post(t, srv, pathTakeover, nonce, sign(nil, pathTakeover, nonce, body), body); got != 401 {
		t.Fatalf("got %d", got)
	}
}

func TestNonceIs32RandomBytes(t *testing.T) {
	_, _, srv := newAPI(t, testKey)
	n1, n2 := fetchNonce(t, srv), fetchNonce(t, srv)
	raw, err := hex.DecodeString(n1)
	if err != nil || len(raw) != 32 {
		t.Fatalf("nonce %q: %v", n1, err)
	}
	if n1 == n2 {
		t.Fatal("nonce repeated")
	}
}

func TestNonceExpiresAfter30s(t *testing.T) {
	_, clk, srv := newAPI(t, testKey)
	body := takeoverBody(t)
	nonce := fetchNonce(t, srv)
	clk.Advance(30 * time.Second)
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathTakeover, nonce, body), body); got != 401 {
		t.Fatalf("got %d", got)
	}
	nonce = fetchNonce(t, srv)
	clk.Advance(29 * time.Second)
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathTakeover, nonce, body), body); got != 200 {
		t.Fatalf("got %d", got)
	}
}

func TestNonceStoreBoundedOldestEvicted(t *testing.T) {
	s := newNonceStore(newClock())
	first, err := s.issue()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.issue()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxNonces-1; i++ {
		if _, err := s.issue(); err != nil {
			t.Fatal(err)
		}
	}
	if n := s.size(); n != maxNonces {
		t.Fatalf("size %d", n)
	}
	if s.consume(first) {
		t.Fatal("oldest must be evicted")
	}
	if !s.consume(second) {
		t.Fatal("second must survive")
	}
	if s.consume(second) {
		t.Fatal("single use")
	}
}

func TestHandlerBodyLimit(t *testing.T) {
	_, _, srv := newAPI(t, testKey)
	body := bytes.Repeat([]byte("x"), maxBodyBytes+1)
	nonce := fetchNonce(t, srv)
	if got := post(t, srv, pathTakeover, nonce, sign(testKey, pathTakeover, nonce, body), body); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", got)
	}
}

func TestHandlerRejectsInvalidJSONAndMethod(t *testing.T) {
	_, _, srv := newAPI(t, testKey)
	body := []byte("{")
	nonce := fetchNonce(t, srv)
	if got := post(t, srv, pathClaim, nonce, sign(testKey, pathClaim, nonce, body), body); got != 400 {
		t.Fatalf("got %d", got)
	}
	resp, err := srv.Client().Get(srv.URL + pathNonce)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", resp.StatusCode)
	}
}

// tlsPeer startet den Handler hinter selbst erzeugtem Zertifikat.
func tlsPeer(t *testing.T, c *Coordinator, key []byte) config.Peer {
	t.Helper()
	return tlsPeerHandler(t, NewHandler(c, key))
}

func tlsPeerHandler(t *testing.T, h http.Handler) config.Peer {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	return config.Peer{Name: "remote", Address: strings.TrimPrefix(srv.URL, "https://")}
}

func TestTransportRoundTripOverSelfSignedTLS(t *testing.T) {
	remote := New(settings("r", nor, "x"), &fakeTransport{}, newClock())
	peer := tlsPeer(t, remote, testKey)
	tr := NewHTTPTransport(testKey, nil)
	ctx := context.Background()

	remote.Hold("h1", "s1")
	resp, err := tr.Claim(ctx, peer, ClaimReq{HMI: "h1", Claim: Claim{Terminal: "a", Role: nor, Nonce: "n"}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Holder == nil || resp.Holder.Terminal != "r" || resp.Holder.Session != "s1" || resp.ContestedBy != nil {
		t.Fatalf("got %+v", resp)
	}

	remote.Request(ctx, "h2")
	resp, err = tr.Claim(ctx, peer, ClaimReq{HMI: "h2", Claim: Claim{Terminal: "z", Role: nor}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContestedBy == nil || resp.ContestedBy.Terminal != "r" || resp.Holder != nil {
		t.Fatalf("got %+v", resp)
	}

	hb := Holder{Terminal: "a", Name: "A", Role: pri, Session: "s9", Permanent: true}
	if err := tr.Heartbeat(ctx, peer, "h3", hb); err != nil {
		t.Fatal(err)
	}
	if got := remote.Occupancy()["h3"]; got != hb {
		t.Fatalf("got %+v", got)
	}

	if err := tr.Takeover(ctx, peer, "h1", hb, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-remote.Events():
		if e.HMI != "h1" || e.By != hb || e.After != 5*time.Second {
			t.Fatalf("got %+v", e)
		}
	default:
		t.Fatal("no event")
	}
}

func TestTransportMaps401ToErrBadKey(t *testing.T) {
	remote := New(settings("r", nor, "x"), &fakeTransport{}, newClock())
	peer := tlsPeer(t, remote, testKey)
	tr := NewHTTPTransport([]byte("anderer Schlüssel"), nil)
	ctx := context.Background()

	if _, err := tr.Claim(ctx, peer, ClaimReq{HMI: "h1", Claim: Claim{Terminal: "a"}}); !errors.Is(err, ErrBadKey) {
		t.Fatalf("claim: %v", err)
	}
	if err := tr.Heartbeat(ctx, peer, "h1", Holder{Terminal: "a"}); !errors.Is(err, ErrBadKey) {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := tr.Takeover(ctx, peer, "h1", Holder{Terminal: "a"}, 0); !errors.Is(err, ErrBadKey) {
		t.Fatalf("takeover: %v", err)
	}
	noEvent(t, remote)
}

func TestTransportMaps5xxAndNetworkToPlainError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	peer := config.Peer{Name: "x", Address: strings.TrimPrefix(srv.URL, "https://")}
	tr := NewHTTPTransport(testKey, nil)
	err := tr.Heartbeat(context.Background(), peer, "h1", Holder{})
	if err == nil || errors.Is(err, ErrBadKey) {
		t.Fatalf("5xx: %v", err)
	}
	srv.Close()
	err = tr.Heartbeat(context.Background(), peer, "h1", Holder{})
	if err == nil || errors.Is(err, ErrBadKey) {
		t.Fatalf("network: %v", err)
	}
}

func TestPeerBaseURL(t *testing.T) {
	for addr, want := range map[string]string{
		"10.0.0.5":        "https://10.0.0.5:8443",
		"10.0.0.5:9000":   "https://10.0.0.5:9000",
		"terminal-2":      "https://terminal-2:8443",
		"terminal-2:9000": "https://terminal-2:9000",
		"fe80::1":         "https://[fe80::1]:8443",
		"[fe80::1]":       "https://[fe80::1]:8443",
		"[fe80::1]:9000":  "https://[fe80::1]:9000",
	} {
		if got := peerBaseURL(addr); got != want {
			t.Errorf("%s: got %s want %s", addr, got, want)
		}
	}
}

func TestCoordinatorRequestOverHTTP(t *testing.T) {
	remote := New(settings("r", nor, "x"), &fakeTransport{}, newClock())
	good := tlsPeer(t, remote, testKey)
	wrong := tlsPeer(t, New(settings("w", nor, "x"), &fakeTransport{}, newClock()), []byte("anderer"))
	good.Name, wrong.Name = "gut", "falsch"
	s := settings("a", pri)
	s.Peers = []config.Peer{good, wrong, {Name: "weg", Address: "127.0.0.1:1"}}
	c := New(s, NewHTTPTransport(testKey, nil), newClock())

	remote.Hold("h1", "s1")
	r := c.Request(context.Background(), "h1")
	if r.Decision.Outcome != TakeOver || r.Decision.Holder == nil || r.Decision.Holder.Terminal != "r" {
		t.Fatalf("got %+v", r)
	}
	if len(r.BadKey) != 1 || r.BadKey[0] != "falsch" || len(r.Unreachable) != 1 || r.Unreachable[0] != "weg" {
		t.Fatalf("got %+v", r)
	}
	if err := c.Takeover(context.Background(), "h1", r.Decision); err != nil {
		t.Fatal(err)
	}
	if e := <-remote.Events(); e.By.Terminal != "a" || e.After != TakeoverWarn {
		t.Fatalf("got %+v", e)
	}
}

func TestTransportReusesConnections(t *testing.T) {
	remote := New(settings("r", nor, "x"), &fakeTransport{}, newClock())
	var mu sync.Mutex
	opened := 0
	srv := httptest.NewUnstartedServer(NewHandler(remote, testKey))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			mu.Lock()
			opened++
			mu.Unlock()
		}
	}
	srv.StartTLS()
	defer srv.Close()
	peer := config.Peer{Name: "r", Address: strings.TrimPrefix(srv.URL, "https://")}
	tr := NewHTTPTransport(testKey, nil)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := tr.Heartbeat(ctx, peer, "h1", Holder{Terminal: "a"}); err != nil {
			t.Fatal(err)
		}
		if _, err := tr.Claim(ctx, peer, ClaimReq{HMI: "h1", Claim: Claim{Terminal: "a"}}); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if opened != 1 {
		t.Fatalf("opened %d connections", opened)
	}
}

func TestDefaultClientDropsIdleConnectionsAfter30s(t *testing.T) {
	ht := NewHTTPTransport(testKey, nil).(*httpTransport).client.Transport.(*http.Transport)
	if ht.IdleConnTimeout != 30*time.Second {
		t.Fatalf("got %v", ht.IdleConnTimeout)
	}
}

func TestClaimNameTravelsOverHTTP(t *testing.T) {
	s := settings("r", nor, "x")
	s.Self.Name = "Halle"
	remote := New(s, &fakeTransport{}, newClock())
	peer := tlsPeer(t, remote, testKey)
	ctx := context.Background()
	remote.Request(ctx, "h1")
	resp, err := NewHTTPTransport(testKey, nil).Claim(ctx, peer, ClaimReq{HMI: "h1", Claim: Claim{Terminal: "z", Role: nor}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.ContestedBy == nil || resp.ContestedBy.Name != "Halle" {
		t.Fatalf("got %+v", resp)
	}
}
