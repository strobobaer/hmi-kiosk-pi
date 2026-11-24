package pdhterminal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errNet = &net.OpError{Op: "dial", Err: errors.New("connection refused")}

// doReq führt eine echte Anfrage mit c aus und liefert den Client-Fehler (*url.Error).
func doReq(t *testing.T, ctx context.Context, c *http.Client, url string) error {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("Fehler erwartet")
	}
	return err
}

func TestIsNetworkErr(t *testing.T) {
	// Verbindung abgelehnt: Listener öffnen und wieder schließen.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedAddr := ln.Addr().String()
	ln.Close()
	refused := doReq(t, context.Background(), &http.Client{}, "http://"+closedAddr)

	// Server, dessen Zertifikat dem Client unbekannt ist.
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer tlsSrv.Close()
	untrusted := doReq(t, context.Background(), &http.Client{}, tlsSrv.URL)

	// HTTP-Antwort auf einem HTTPS-Port: tls.RecordHeaderError.
	plain := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer plain.Close()
	recHdr := doReq(t, context.Background(), &http.Client{}, "https://"+plain.Listener.Addr().String())

	// Gegenseite schließt direkt nach accept: EOF im Handshake. Gilt als Netzfehler
	// (Verbindung abgebrochen, z. B. Proxy oder Absturz), solange kein TLS-Typ erkannt wird.
	eofLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer eofLn.Close()
	go func() {
		for {
			c, err := eofLn.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	eof := doReq(t, context.Background(), &http.Client{}, "https://"+eofLn.Addr().String())

	// Bereits abgebrochener Kontext.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := doReq(t, cctx, &http.Client{}, tlsSrv.URL)

	// Client.Timeout gegen hängenden Server.
	release := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	defer hang.Close()
	defer close(release)
	timeout := doReq(t, context.Background(), &http.Client{Timeout: 50 * time.Millisecond}, hang.URL)

	// TLS-Alert der Gegenseite.
	alert := &net.OpError{Op: "remote error", Err: tls.AlertError(42)}

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"refused", refused, true},
		{"untrusted", untrusted, false},
		{"recordheader", recHdr, false},
		{"eof-handshake", eof, true},
		{"canceled", canceled, false},
		{"canceled-wrapped", fmt.Errorf("x: %w", canceled), false},
		{"timeout", timeout, true},
		{"os-deadline", os.ErrDeadlineExceeded, true},
		{"deadline", context.DeadlineExceeded, true},
		{"dns", &net.DNSError{IsNotFound: true}, true},
		{"alert", alert, false},
		{"x509", x509.UnknownAuthorityError{}, false},
		{"tls", &tls.CertificateVerificationError{}, false},
		{"other", errors.New("x"), false},
		{"nil", nil, false},
	}
	for _, c := range cases {
		if got := IsNetworkErr(c.err); got != c.want {
			t.Errorf("%s: %v -> %v, want %v", c.name, c.err, got, c.want)
		}
	}
}

func TestRouteSwitchOnNetworkErr(t *testing.T) {
	r := NewRoute("https://a/", "https://b")
	if !r.Fail("https://a", errNet) {
		t.Fatal("kein Wechsel")
	}
	if r.Base() != "https://b" || r.Name() != "tailnet" {
		t.Fatalf("%s %s", r.Base(), r.Name())
	}
	if st := r.Status(); st.Name != "tailnet" || st.LastErr == "" || st.Since.IsZero() {
		t.Fatalf("Status %+v", st)
	}
}

func TestRouteNoSwitchOnTLSAndCancel(t *testing.T) {
	for _, e := range []error{
		x509.UnknownAuthorityError{},
		context.Canceled,
		fmt.Errorf("get: %w", context.Canceled),
	} {
		r := NewRoute("https://a", "https://b")
		if r.Fail("https://a", e) || r.Base() != "https://a" {
			t.Fatalf("Wechsel bei %v", e)
		}
	}
}

func TestRouteNoFallbackConfigured(t *testing.T) {
	r := NewRoute("https://a", "")
	if r.Fail("https://a", errNet) || r.Base() != "https://a" {
		t.Fatal("Wechsel ohne Ausweichadresse")
	}
}

func TestRouteFailOnFallbackNoFlip(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	r.Fail("https://a", errNet)
	if r.Fail("https://b", errNet) || r.Base() != "https://b" {
		t.Fatal("Schleife")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("Zeitüberschreitung: " + what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestRouteRunReturns(t *testing.T) {
	old := routeProbeEvery
	routeProbeEvery = 5 * time.Millisecond
	defer func() { routeProbeEvery = old }()
	r := NewRoute("https://a", "https://b")
	var calls atomic.Int32
	probe := func(ctx context.Context, base string) error {
		if base != "https://a" {
			t.Errorf("probe base %s", base)
		}
		if calls.Add(1) == 1 {
			return errNet
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, probe); close(done) }()

	// Solange die Hauptadresse aktiv ist, darf nie geprüft werden: mehrere Takte abwarten.
	time.Sleep(40 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("probe bei aktiver Hauptadresse")
	}
	r.Fail("https://a", errNet)
	waitFor(t, "Rückkehr", func() bool { return r.Base() == "https://a" })
	if calls.Load() < 2 {
		t.Fatalf("probe-Aufrufe %d", calls.Load())
	}
	if st := r.Status(); st.Name != "lan" || st.LastErr != "" {
		t.Fatalf("Status nach Rückkehr %+v", st)
	}
	cancel()
	<-done
}

func TestRouteRunZeroIntervalAndProbeTimeout(t *testing.T) {
	oldE, oldT := routeProbeEvery, routeProbeTimeout
	routeProbeEvery, routeProbeTimeout = 0, 20*time.Millisecond
	defer func() { routeProbeEvery, routeProbeTimeout = oldE, oldT }()
	r := NewRoute("https://a", "https://b")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, func(ctx context.Context, _ string) error { <-ctx.Done(); return ctx.Err() })
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run kehrt nicht zurück")
	}

	// Probe ohne Eigenfrist: das Zeitlimit von Run muss greifen.
	routeProbeEvery = 5 * time.Millisecond
	r.Fail("https://a", errNet)
	got := make(chan error, 1)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go r.Run(ctx2, func(ctx context.Context, _ string) error {
		<-ctx.Done()
		select {
		case got <- ctx.Err():
		default:
		}
		return ctx.Err()
	})
	select {
	case e := <-got:
		if !errors.Is(e, context.DeadlineExceeded) {
			t.Fatalf("%v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("kein Zeitlimit für probe")
	}
}

func TestRouteRunNoReturnAfterSetURLs(t *testing.T) {
	old := routeProbeEvery
	routeProbeEvery = 5 * time.Millisecond
	defer func() { routeProbeEvery = old }()
	r := NewRoute("https://a", "https://b")
	r.Fail("https://a", errNet)
	started, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var calls atomic.Int32
	go func() {
		r.Run(ctx, func(context.Context, string) error {
			if calls.Add(1) > 1 {
				return errNet
			}
			close(started)
			<-release
			return nil
		})
		close(done)
	}()
	<-started
	r.SetURLs("https://a2", "https://b")
	r.Fail("https://a2", errNet)
	close(release)
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	// Die zu Beginn laufende Probe galt der alten Lage und darf nicht zurücksetzen.
	if r.Base() != "https://b" {
		t.Fatal("alte Probe hat zurückgesetzt")
	}
}

// Ausweichadresse tot, Hauptadresse wieder da: Ein Netzfehler auf der Ausweichadresse stößt die Prüfung sofort an,
// die Rückkehr wartet nicht auf den (hier stundenlangen) Takt.
func TestRouteKickReturnsWithoutTick(t *testing.T) {
	oldE, oldK := routeProbeEvery, routeKickMin
	routeProbeEvery, routeKickMin = time.Hour, 0
	defer func() { routeProbeEvery, routeKickMin = oldE, oldK }()
	r := NewRoute("https://a", "https://b")
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, func(_ context.Context, base string) error {
			if base != "https://a" {
				t.Errorf("probe base %s", base)
			}
			calls.Add(1)
			return nil
		})
		close(done)
	}()
	defer func() { cancel(); <-done }()

	// Fehler auf der Hauptadresse schaltet um; das allein stößt keine Prüfung an.
	if !r.Fail("https://a", errNet) {
		t.Fatal("kein Wechsel")
	}
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 0 || r.Base() != "https://b" {
		t.Fatalf("probe ohne Anstoß: %d, %s", calls.Load(), r.Base())
	}
	// TLS-Fehler auf der Ausweichadresse ist kein Netzfehler: kein Anstoß.
	r.Fail("https://b", x509.UnknownAuthorityError{})
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("Anstoß bei TLS-Fehler")
	}
	// Netzfehler auf der Ausweichadresse: Anstoß, kein Wechsel zurück durch Fail selbst.
	if r.Fail("https://b", errNet) {
		t.Fatal("Fail auf Ausweichadresse meldet Wechsel")
	}
	waitFor(t, "Rückkehr nach Anstoß", func() bool { return r.Base() == "https://a" })
	if calls.Load() != 1 {
		t.Fatalf("probe-Aufrufe %d", calls.Load())
	}
}

// Angestoßene Prüfungen sind gedrosselt: höchstens eine je routeKickMin, auch bei vielen Fehlern.
func TestRouteKickRateLimited(t *testing.T) {
	oldE, oldK := routeProbeEvery, routeKickMin
	routeProbeEvery, routeKickMin = time.Hour, time.Hour
	defer func() { routeProbeEvery, routeKickMin = oldE, oldK }()
	r := NewRoute("https://a", "https://b")
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, func(context.Context, string) error { calls.Add(1); return errNet })
		close(done)
	}()
	defer func() { cancel(); <-done }()
	r.Fail("https://a", errNet)
	r.Fail("https://b", errNet)
	waitFor(t, "erste Prüfung", func() bool { return calls.Load() == 1 })
	for i := 0; i < 50; i++ {
		r.Fail("https://b", errNet)
		time.Sleep(time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("probe-Aufrufe %d, erwartet 1", n)
	}
	if r.Base() != "https://b" {
		t.Fatal("Rückkehr trotz gescheiterter Prüfung")
	}
}

// Nach Ablauf von routeKickMin darf der nächste Anstoß wieder prüfen.
func TestRouteKickAfterMinInterval(t *testing.T) {
	oldE, oldK := routeProbeEvery, routeKickMin
	routeProbeEvery, routeKickMin = time.Hour, 40*time.Millisecond
	defer func() { routeProbeEvery, routeKickMin = oldE, oldK }()
	r := NewRoute("https://a", "https://b")
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx, func(context.Context, string) error {
			if calls.Add(1) == 1 {
				return errNet
			}
			return nil
		})
		close(done)
	}()
	defer func() { cancel(); <-done }()
	r.Fail("https://a", errNet)
	r.Fail("https://b", errNet)
	waitFor(t, "erste Prüfung", func() bool { return calls.Load() == 1 })
	time.Sleep(60 * time.Millisecond)
	r.Fail("https://b", errNet)
	waitFor(t, "Rückkehr nach zweitem Anstoß", func() bool { return r.Base() == "https://a" })
}

// Bei aktiver Hauptadresse stößt ein Fehler auf der (inaktiven) Ausweichadresse nichts an.
func TestRouteNoKickOnPrimary(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	r.Fail("https://b", errNet)
	select {
	case <-r.kick:
		t.Fatal("Anstoß bei aktiver Hauptadresse")
	default:
	}
}

func TestRouteSetURLs(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	r.Fail("https://a", errNet)
	r.SetURLs("https://a", "")
	if r.Base() != "https://a" {
		t.Fatal(r.Base())
	}
	if st := r.Status(); st.LastErr != "" {
		t.Fatalf("Status %+v", st)
	}
	r.SetURLs("https://a", "https://b")
	r.Fail("https://a", errNet)
	r.SetURLs("https://a2", "https://b")
	if r.Base() != "https://a2" {
		t.Fatal(r.Base())
	}
}

func TestRouteConcurrent(t *testing.T) {
	r := NewRoute("https://a", "https://b")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				switch (i + j) % 4 {
				case 0:
					r.Fail("https://a", errNet)
				case 1:
					_ = r.Base()
					_ = r.Name()
				case 2:
					_ = r.Status()
				case 3:
					r.SetURLs("https://a", "https://b")
				}
			}
		}(i)
	}
	wg.Wait()
}
