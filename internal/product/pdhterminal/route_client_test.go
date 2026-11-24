package pdhterminal

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"hmikiosk/internal/config"
)

// routeSrv ist ein TLS-Testserver, der Anfragen, die Kopfzeile X-Terminal-Route und den Rumpf mitschreibt.
type routeSrv struct {
	*httptest.Server
	hits  atomic.Int32
	route atomic.Value // letzte Kopfzeile X-Terminal-Route
	body  atomic.Value // letzter Rumpf
}

func newRouteSrv(status int) *routeSrv {
	rs := &routeSrv{}
	rs.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rs.hits.Add(1)
		rs.route.Store(r.Header.Get("X-Terminal-Route"))
		b, _ := io.ReadAll(r.Body)
		rs.body.Store(string(b))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"functions":{}}`))
	}))
	return rs
}

func (rs *routeSrv) gotRoute() string {
	v, _ := rs.route.Load().(string)
	return v
}

// trustClient vertraut den Zertifikaten der Testserver (nur im Test).
func trustClient(srvs ...*routeSrv) *http.Client {
	pool := x509.NewCertPool()
	for _, s := range srvs {
		pool.AddCert(s.Certificate())
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
}

func TestCheckFallsBack(t *testing.T) {
	main, fb := newRouteSrv(200), newRouteSrv(200)
	defer fb.Close()
	main.Close() // Hauptadresse nicht erreichbar
	rt := NewRoute(main.URL, fb.URL)
	c := Client{HTTP: trustClient(main, fb), Route: rt}
	st, err := c.Check(context.Background(), Device{URL: main.URL, Secret: "s"})
	if st != StateOK || err != nil {
		t.Fatalf("Check: %v %v", st, err)
	}
	if fb.gotRoute() != "tailnet" || rt.Name() != "tailnet" {
		t.Fatalf("Weg %q, Route %q", fb.gotRoute(), rt.Name())
	}
}

func TestPushFunctionsRetriesBodyOnFallback(t *testing.T) {
	main, fb := newRouteSrv(200), newRouteSrv(200)
	defer fb.Close()
	main.Close()
	c := Client{HTTP: trustClient(main, fb), Route: NewRoute(main.URL, fb.URL)}
	st, _, err := c.PushFunctions(context.Background(), Device{URL: main.URL, Secret: "s"}, FuncSet{})
	if st != StateOK || err != nil {
		t.Fatalf("Push: %v %v", st, err)
	}
	if s, _ := fb.body.Load().(string); s == "" {
		t.Fatal("Rumpf kam bei der Wiederholung nicht an")
	}
}

func TestCheckNoFallbackOnUntrusted(t *testing.T) {
	main, fb := newRouteSrv(200), newRouteSrv(200)
	defer main.Close()
	defer fb.Close()
	c := Client{HTTP: trustClient(), Route: NewRoute(main.URL, fb.URL)} // leerer Pool: Zertifikat unbekannt
	st, err := c.Check(context.Background(), Device{URL: main.URL, Secret: "s"})
	if st != StateUntrusted || err == nil {
		t.Fatalf("Check: %v %v", st, err)
	}
	if fb.hits.Load() != 0 || c.Route.Name() != "lan" {
		t.Fatalf("Ausweichadresse angefragt: %d, Route %s", fb.hits.Load(), c.Route.Name())
	}
}

func TestCheckNoFallbackOn401(t *testing.T) {
	main, fb := newRouteSrv(401), newRouteSrv(200)
	defer main.Close()
	defer fb.Close()
	c := Client{HTTP: trustClient(main, fb), Route: NewRoute(main.URL, fb.URL)}
	st, err := c.Check(context.Background(), Device{URL: main.URL, Secret: "s"})
	if st != StateRevoked || err != nil {
		t.Fatalf("Check: %v %v", st, err)
	}
	if fb.hits.Load() != 0 || main.gotRoute() != "lan" {
		t.Fatalf("Ausweichadresse angefragt: %d, Weg %q", fb.hits.Load(), main.gotRoute())
	}
}

func TestNoRouteUnchanged(t *testing.T) {
	main := newRouteSrv(200)
	defer main.Close()
	st, err := (Client{HTTP: trustClient(main)}).Check(context.Background(), Device{URL: main.URL, Secret: "s"})
	if st != StateOK || err != nil {
		t.Fatalf("Check: %v %v", st, err)
	}
	if main.hits.Load() != 1 || main.gotRoute() != "lan" {
		t.Fatalf("Treffer %d, Weg %q", main.hits.Load(), main.gotRoute())
	}
}

func TestProbe(t *testing.T) {
	for status, want := range map[int]bool{200: true, 401: true, 500: false, 403: false} {
		s := newRouteSrv(status)
		err := (Client{HTTP: trustClient(s)}).Probe(context.Background(), Device{URL: s.URL, Secret: "s"}, s.URL)
		s.Close()
		if (err == nil) != want {
			t.Errorf("Status %d: %v", status, err)
		}
	}
}

func TestRunSetURLsOnConfigChange(t *testing.T) {
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	c := config.Default()
	c.PDH.URL, c.PDH.FallbackURL = "https://pdh.example", "https://pdh.tail.ts.net"
	s.apply(c)
	if !s.route.Fail("https://pdh.example", errNet) || s.route.Base() != "https://pdh.tail.ts.net" {
		t.Fatalf("Umschalten: %q", s.route.Base())
	}
	c.PDH.FallbackURL = ""
	s.apply(c)
	if s.route.Base() != "https://pdh.example" {
		t.Fatalf("Basis nach Leeren: %q", s.route.Base())
	}
}

func TestProbeNoSecretToOtherURL(t *testing.T) {
	a, b := newRouteSrv(200), newRouteSrv(200)
	defer a.Close()
	defer b.Close()
	// Gerät ist an A gekoppelt, die Route hat B als Hauptadresse: B bekommt weder Anfrage noch Geheimnis.
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HardwareID: func() string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if err := SaveDevice(s.o.DataDir, Device{DeviceID: "1", Secret: "geheim", URL: a.URL}); err != nil {
		t.Fatal(err)
	}
	s.client.HTTP = trustClient(a, b)
	if err := s.probePrimary(context.Background(), b.URL); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if a.hits.Load() != 0 || b.hits.Load() != 0 {
		t.Fatalf("Anfragen: A %d, B %d", a.hits.Load(), b.hits.Load())
	}
}

func TestTargetDirectDevice(t *testing.T) {
	c := Client{Route: NewRoute("https://p.example", "https://f.example")}
	other := Device{URL: "https://other.example"}
	if b, n := c.target(other); b != "https://other.example" || n != "lan" {
		t.Fatalf("anderes Gerät: %q %q", b, n)
	}
	if b, n := c.target(Device{URL: "https://p.example"}); b != "https://p.example" || n != "lan" {
		t.Fatalf("Hauptadresse: %q %q", b, n)
	}
	c.Route.Fail("https://p.example", errNet)
	if b, n := c.target(Device{URL: "https://p.example"}); b != "https://f.example" || n != "tailnet" {
		t.Fatalf("Ausweichadresse: %q %q", b, n)
	}
	if b, n := c.target(other); b != "https://other.example" || n != "lan" {
		t.Fatalf("anderes Gerät bei aktiver Ausweichadresse: %q %q", b, n)
	}
}

// Ziel und Wegangabe kommen aus einem Schnappschuss: Während die Hauptadresse wechselt, geht eine Anfrage für das an
// A gekoppelte Gerät nie an die neue Hauptadresse B, und "tailnet" steht genau dann, wenn das Ziel die
// Ausweichadresse ist. Mit -race laufen lassen.
func TestTargetConsistentSnapshot(t *testing.T) {
	r := NewRoute("https://a", "https://f")
	c := Client{Route: r}
	d := Device{URL: "https://a"}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			r.SetURLs("https://b", "https://f")
			r.SetURLs("https://a", "https://f")
			r.Fail("https://a", errNet)
		}
	}()
	for i := 0; i < 20000; i++ {
		base, route := c.target(d)
		if base != "https://a" && base != "https://f" {
			close(stop)
			<-done
			t.Fatalf("Ziel %q: weder Kopplungs- noch Ausweichadresse", base)
		}
		if (route == "tailnet") != (base == "https://f") {
			close(stop)
			<-done
			t.Fatalf("Ziel %q mit Weg %q", base, route)
		}
	}
	close(stop)
	<-done
}

// Beide Adressen tot: Die gescheiterte Wiederholung auf der Ausweichadresse wird gemeldet und stößt die Prüfung der
// Hauptadresse an; ebenso jede weitere Anfrage über die tote Ausweichadresse.
func TestDeadFallbackKicksProbe(t *testing.T) {
	main, fb := newRouteSrv(200), newRouteSrv(200)
	main.Close()
	fb.Close()
	rt := NewRoute(main.URL, fb.URL)
	c := Client{HTTP: trustClient(main, fb), Route: rt}
	d := Device{URL: main.URL, Secret: "s"}
	if st, _ := c.Check(context.Background(), d); st != StateUnreachable || rt.Name() != "tailnet" {
		t.Fatalf("Check: %v, Route %s", st, rt.Name())
	}
	select {
	case <-rt.kick:
	default:
		t.Fatal("kein Anstoß nach gescheiterter Wiederholung")
	}
	if st, _ := c.Check(context.Background(), d); st != StateUnreachable {
		t.Fatalf("Check: %v", st)
	}
	select {
	case <-rt.kick:
	default:
		t.Fatal("kein Anstoß bei Fehler auf der Ausweichadresse")
	}
}

func TestNoSwitchOnCallerContextExpiry(t *testing.T) {
	main, fb := newRouteSrv(200), newRouteSrv(200)
	defer fb.Close()
	main.Close()
	c := Client{HTTP: trustClient(main, fb), Route: NewRoute(main.URL, fb.URL)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if st, _ := c.Check(ctx, Device{URL: main.URL, Secret: "s"}); st != StateUnreachable {
		t.Fatalf("Check: %v", st)
	}
	if c.Route.Name() != "lan" || fb.hits.Load() != 0 {
		t.Fatalf("Route %s, Treffer %d", c.Route.Name(), fb.hits.Load())
	}
	dctx, dcancel := context.WithTimeout(context.Background(), 0)
	defer dcancel()
	c.Check(dctx, Device{URL: main.URL, Secret: "s"})
	if c.Route.Name() != "lan" || fb.hits.Load() != 0 {
		t.Fatalf("nach Frist: Route %s, Treffer %d", c.Route.Name(), fb.hits.Load())
	}
}
