package web

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// serveNet startet die Kachelseite mit umschaltbarer Netzfreigabe.
func serveNet(t *testing.T, s SessionAPI, on *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(NewLocalWith(s, nil, nil, LocalExtras{Network: on.Load}))
	t.Cleanup(srv.Close)
	return srv
}

func doReq(t *testing.T, method, url, host string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	req.Host = host
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestNetworkTilesAcceptIPHostsOnly(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	fs := newFakeSession()
	srv := serveNet(t, fs, &on)
	for _, host := range []string{"192.168.1.20:8080", "10.0.0.7:8080", "[fe80::1]:8080", "[2001:db8::5]:8080", "127.0.0.1:8080", "localhost:8080"} {
		if code := doReq(t, http.MethodGet, srv.URL+"/", host, nil); code != http.StatusOK {
			t.Errorf("GET Host %q: %d", host, code)
		}
	}
	for _, host := range []string{
		"evil.example:8080", "evil.example", "192.168.1.20", "192.168.1.20:9999", "192.168.1.20:80",
		"fe80::1:8080", "0x7f.0.0.1:8080", "192.168.1.20.nip.io:8080", "",
	} {
		if code := doReq(t, http.MethodGet, srv.URL+"/", host, nil); code != http.StatusForbidden {
			t.Errorf("GET Host %q: %d, erwartet 403", host, code)
		}
	}
	if got := fs.got(); len(got) != 0 {
		t.Fatalf("Sitzung aufgerufen: %v", got)
	}
}

func TestNetworkTilesPostNeedsSameOrigin(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	fs := newFakeSession()
	srv := serveNet(t, fs, &on)
	const host = "192.168.1.20:8080"
	for _, hdr := range []map[string]string{
		{"Origin": "http://192.168.1.20:8080"},
		{"Referer": "http://192.168.1.20:8080/"},
	} {
		if code := doReq(t, http.MethodPost, srv.URL+"/api/back", host, hdr); code != http.StatusNoContent {
			t.Errorf("%v: %d", hdr, code)
		}
	}
	for _, hdr := range []map[string]string{
		{},
		{"Origin": "http://192.168.1.21:8080"},
		{"Origin": "http://evil.example:8080"},
		{"Origin": "https://192.168.1.20:8080"},
		{"Origin": "http://192.168.1.20:8080.evil.example"},
		{"Origin": "null"},
		{"Referer": "http://192.168.1.99:8080/"},
		{"Origin": "http://evil.example", "Referer": "http://192.168.1.20:8080/"},
	} {
		if code := doReq(t, http.MethodPost, srv.URL+"/api/back", host, hdr); code != http.StatusForbidden {
			t.Errorf("%v: %d, erwartet 403", hdr, code)
		}
	}
	// Name im Host, passender Origin: weiterhin abgelehnt (DNS-Rebinding).
	if code := doReq(t, http.MethodPost, srv.URL+"/api/back", "evil.example:8080", map[string]string{"Origin": "http://evil.example:8080"}); code != http.StatusForbidden {
		t.Errorf("evil.example:8080: %d", code)
	}
	// Lokal wie bisher.
	if code := doReq(t, http.MethodPost, srv.URL+"/api/back", "127.0.0.1:8080", map[string]string{"Origin": "http://127.0.0.1:8080"}); code != http.StatusNoContent {
		t.Errorf("lokal: %d", code)
	}
	if got := fs.got(); len(got) != 3 {
		t.Fatalf("Aufrufe: %v", got)
	}
}

func TestNetworkTilesOffKeepsLocalOnly(t *testing.T) {
	var on atomic.Bool // aus
	fs := newFakeSession()
	srv := serveNet(t, fs, &on)
	if code := doReq(t, http.MethodGet, srv.URL+"/", "192.168.1.20:8080", nil); code != http.StatusForbidden {
		t.Errorf("IP-Host ohne Freigabe: %d", code)
	}
	if code := doReq(t, http.MethodPost, srv.URL+"/api/back", "192.168.1.20:8080", map[string]string{"Origin": "http://192.168.1.20:8080"}); code != http.StatusForbidden {
		t.Errorf("POST ohne Freigabe: %d", code)
	}
	if code := doReq(t, http.MethodPost, srv.URL+"/api/back", "127.0.0.1:8080", map[string]string{"Origin": "http://192.168.1.20:8080"}); code != http.StatusForbidden {
		t.Errorf("fremder Origin lokal: %d", code)
	}
	if code := doReq(t, http.MethodGet, srv.URL+"/", "127.0.0.1:8080", nil); code != http.StatusOK {
		t.Errorf("lokal: %d", code)
	}
	// Umschalten wirkt sofort.
	on.Store(true)
	if code := doReq(t, http.MethodGet, srv.URL+"/", "192.168.1.20:8080", nil); code != http.StatusOK {
		t.Errorf("nach dem Einschalten: %d", code)
	}
	on.Store(false)
	if code := doReq(t, http.MethodGet, srv.URL+"/", "192.168.1.20:8080", nil); code != http.StatusForbidden {
		t.Errorf("nach dem Ausschalten: %d", code)
	}
	if got := fs.got(); len(got) != 0 {
		t.Fatalf("Sitzung aufgerufen: %v", got)
	}
}

// Der Go-Client entfernt die Zone aus dem Host, daher direkt geprüft.
func TestIPHostRejectsZonesAndNames(t *testing.T) {
	for host, want := range map[string]bool{
		"192.168.1.20:8080": true, "[2001:db8::5]:8080": true,
		"[fe80::1%eth0]:8080": false, "[fe80::1%25eth0]:8080": false, "fe80::1:8080": false,
		"evil.example:8080": false, "192.168.1.20:8081": false, "192.168.1.20": false, ":8080": false,
	} {
		if got := ipHost(host); got != want {
			t.Errorf("ipHost(%q) = %v", host, got)
		}
	}
}
