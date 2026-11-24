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
		err := (Client{HTTP: trustClient(s)}).Probe(context.Background(), Device{Secret: "s"}, s.URL)
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
