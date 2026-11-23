package pdhterminal

import (
	"net/http"
	"testing"
	"time"
)

func newTestAdminURL(ips map[string]string, calls *int) (*adminURL, *time.Time) {
	now := time.Unix(1000, 0)
	a := &adminURL{now: func() time.Time { return now }}
	a.localIP = func(t string) string { *calls++; return ips[t] }
	return a, &now
}

func TestAdminURLFormat(t *testing.T) {
	n := 0
	a, _ := newTestAdminURL(map[string]string{"pdh:443": "192.168.1.7", "v6:443": "fd00::7"}, &n)
	a.setPort("8443")
	if got := a.value("pdh:443"); got != "https://192.168.1.7:8443/admin/" {
		t.Errorf("IPv4: %q", got)
	}
	if got := a.value("v6:443"); got != "https://[fd00::7]:8443/admin/" {
		t.Errorf("IPv6: %q", got)
	}
	a.setPort("9000")
	if got := a.value("pdh:443"); got != "https://192.168.1.7:9000/admin/" {
		t.Errorf("Port: %q", got)
	}
}

func TestAdminURLOmitted(t *testing.T) {
	n := 0
	a, _ := newTestAdminURL(map[string]string{"pdh:443": "10.0.0.2"}, &n)
	if a.value("pdh:443") != "" {
		t.Error("ohne Port keine Adresse")
	}
	a.setPort("abc")
	if a.value("pdh:443") != "" {
		t.Error("ungültiger Port")
	}
	a.setPort("8443")
	if a.value("unbekannt:443") != "" {
		t.Error("IP nicht ermittelbar")
	}
}

func TestAdminURLCache(t *testing.T) {
	n := 0
	ips := map[string]string{"pdh:443": "10.0.0.2"}
	a, now := newTestAdminURL(ips, &n)
	a.setPort("8443")
	a.value("pdh:443")
	a.value("pdh:443")
	if n != 1 {
		t.Fatalf("Cache: %d Aufrufe", n)
	}
	ips["pdh:443"] = "10.0.0.3"
	*now = now.Add(adminURLTTL - time.Second)
	if got := a.value("pdh:443"); got != "https://10.0.0.2:8443/admin/" {
		t.Errorf("vor Ablauf: %q", got)
	}
	*now = now.Add(2 * time.Second)
	if got := a.value("pdh:443"); got != "https://10.0.0.3:8443/admin/" || n != 2 {
		t.Errorf("nach Ablauf: %q (%d)", got, n)
	}
	ips["pdh:443"] = "10.0.0.4"
	a.invalidate()
	if got := a.value("pdh:443"); got != "https://10.0.0.4:8443/admin/" {
		t.Errorf("nach invalidate: %q", got)
	}
}

func TestSetClientHeadersAdminURL(t *testing.T) {
	old := adminURLState
	defer func() { adminURLState = old }()
	n := 0
	a, _ := newTestAdminURL(map[string]string{"pdh.firma.de:443": "192.168.1.7", "pdh.firma.de:8080": "192.168.1.7"}, &n)
	adminURLState = a
	req, _ := http.NewRequest("GET", "https://pdh.firma.de/api", nil)
	setClientHeaders(req)
	if req.Header.Get("X-Terminal-Admin-URL") != "" {
		t.Error("ohne Port darf die Kopfzeile fehlen")
	}
	a.setPort("8443")
	setClientHeaders(req)
	if got := req.Header.Get("X-Terminal-Admin-URL"); got != "https://192.168.1.7:8443/admin/" {
		t.Errorf("Kopfzeile: %q", got)
	}
	req, _ = http.NewRequest("GET", "http://pdh.firma.de:8080/api", nil)
	req.Header.Set("Authorization", "Bearer x")
	setClientHeaders(req)
	if got := req.Header.Get("X-Terminal-Admin-URL"); got != "https://192.168.1.7:8443/admin/" {
		t.Errorf("Kopfzeile mit Port: %q", got)
	}
}
