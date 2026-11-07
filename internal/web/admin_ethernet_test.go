package web

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/helperapi"
)

type fakeNet struct {
	mu         sync.Mutex
	st         helperapi.NetStatus
	stErr      error
	applied    []config.EthernetCfg
	applyErr   error
	confirms   int
	confirmErr error
	reverts    int
	revertErr  error
}

func (f *fakeNet) NetStatus(context.Context) (helperapi.NetStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, f.stErr
}

func (f *fakeNet) NetApply(_ context.Context, e config.EthernetCfg) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applied = append(f.applied, e)
	if f.applyErr == nil {
		f.st.Pending, f.st.PendingCfg, f.st.RemainingSec = true, e, 120
	}
	return f.applyErr
}

func (f *fakeNet) NetConfirm(context.Context) (config.EthernetCfg, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.confirms++
	if f.confirmErr != nil {
		return config.EthernetCfg{}, f.confirmErr
	}
	e := f.st.PendingCfg
	f.st.Pending, f.st.PendingCfg = false, config.EthernetCfg{}
	return e, nil
}

func (f *fakeNet) NetRevert(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reverts++
	f.st.Pending = false
	return f.revertErr
}

func (f *fakeNet) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.applied) + f.confirms + f.reverts
}

func ethValues(v map[string]string) url.Values {
	out := url.Values{}
	for k, x := range v {
		out.Set(k, x)
	}
	return out
}

func (b *browser) postNet(path string, v url.Values) *httptest.ResponseRecorder {
	b.e.t.Helper()
	return b.postVia("/admin/network", path, v)
}

func TestNetworkPageShowsEthernetSection(t *testing.T) {
	e, b := newLoggedIn(t)
	e.net.st = helperapi.NetStatus{Iface: "eth0", Addresses: []string{"192.168.1.20/24"}}
	r := b.get("/admin/network")
	body := r.Body.String()
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	for _, s := range []string{"Ethernet (IPv4)", "eth0", "192.168.1.20/24", "Automatisch (DHCP)", "Feste Adresse",
		`name="address"`, `name="prefix"`, `name="gateway"`, `name="dns1"`, `name="dns2"`, "Terminals", "HMI"} {
		wantContains(t, body, s)
	}
	if strings.Index(body, "Ethernet (IPv4)") > strings.Index(body, "<h2>WLAN</h2>") {
		t.Error("Ethernet steht nicht über WLAN")
	}
}

func TestEthernetApplyStatic(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.postNet("/admin/network/ethernet", ethValues(map[string]string{
		"method": "static", "address": " 192.168.1.50 ", "prefix": "255.255.255.0", "gateway": "192.168.1.1", "dns1": "192.168.1.1", "dns2": "",
	}))
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	want := config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.50", Prefix: 24, Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}}
	if len(e.net.applied) != 1 || !reflect.DeepEqual(e.net.applied[0], want) {
		t.Fatalf("angewendet %+v", e.net.applied)
	}
	body := r.Body.String()
	wantContains(t, body, "Neue Einstellung aktiv. Bitte innerhalb von 2 Minuten unter https://192.168.1.50:8443/admin/network anmelden und bestätigen, sonst wird die alte Einstellung wiederhergestellt.")
	// Gespeichert wird erst nach der Bestätigung.
	if got := e.cfg().Network.Ethernet; got.Method != "" {
		t.Fatalf("vorzeitig gespeichert: %+v", got)
	}
	if e.appliedCount() != 0 {
		t.Error("Apply ohne Bestätigung")
	}
}

func TestEthernetApplyDHCPIgnoresFields(t *testing.T) {
	e, b := newLoggedIn(t)
	r := b.postNet("/admin/network/ethernet", ethValues(map[string]string{"method": "dhcp", "address": "kaputt", "prefix": "99"}))
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d: %s", r.Code, r.Body.String())
	}
	if len(e.net.applied) != 1 || !reflect.DeepEqual(e.net.applied[0], config.EthernetCfg{Method: config.EthDHCP}) {
		t.Fatalf("angewendet %+v", e.net.applied)
	}
	wantContains(t, r.Body.String(), "DHCP")
	wantContains(t, r.Body.String(), "2 Minuten")
}

func TestEthernetApplyRejectsBadInput(t *testing.T) {
	e, b := newLoggedIn(t)
	for name, c := range map[string]struct {
		form map[string]string
		want string
	}{
		"netzadresse":   {map[string]string{"method": "static", "address": "192.168.1.0", "prefix": "24"}, "Netzadresse"},
		"broadcast":     {map[string]string{"method": "static", "address": "192.168.1.255", "prefix": "24"}, "Broadcast"},
		"gateway":       {map[string]string{"method": "static", "address": "192.168.1.50", "prefix": "24", "gateway": "10.0.0.1"}, "selben Netz"},
		"präfix":        {map[string]string{"method": "static", "address": "192.168.1.50", "prefix": "31"}, "Präfix"},
		"maske":         {map[string]string{"method": "static", "address": "192.168.1.50", "prefix": "255.0.255.0"}, "Netzmaske"},
		"dns":           {map[string]string{"method": "static", "address": "192.168.1.50", "prefix": "24", "dns2": "dns.example"}, "DNS"},
		"keine art":     {map[string]string{"address": "192.168.1.50"}, "DHCP oder"},
		"leere adresse": {map[string]string{"method": "static", "prefix": "24"}, "IP-Adresse"},
	} {
		r := b.postNet("/admin/network/ethernet", ethValues(c.form))
		if r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: Status %d", name, r.Code)
		}
		wantContains(t, r.Body.String(), c.want)
		if c.form["address"] != "" && !strings.Contains(r.Body.String(), `value="`+c.form["address"]+`"`) {
			t.Errorf("%s: Eingabe nicht erhalten", name)
		}
	}
	if e.net.calls() != 0 {
		t.Fatalf("Helfer gerufen: %+v", e.net.applied)
	}
}

func TestEthernetApplyShowsHelperError(t *testing.T) {
	e, b := newLoggedIn(t)
	e.net.applyErr = errors.New("Die Adresse 192.168.1.50 wird bereits von einem anderen Gerät benutzt.")
	r := b.postNet("/admin/network/ethernet", ethValues(map[string]string{"method": "static", "address": "192.168.1.50", "prefix": "24"}))
	if r.Code != http.StatusConflict {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "wird bereits von einem anderen Gerät benutzt")
	wantContains(t, r.Body.String(), `value="192.168.1.50"`)
}

func TestEthernetPendingBoxConfirmAndRevert(t *testing.T) {
	e, b := newLoggedIn(t)
	pend := config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.50", Prefix: 24, Gateway: "192.168.1.1"}
	e.net.st = helperapi.NetStatus{Iface: "eth0", Addresses: []string{"192.168.1.50/24"}, Pending: true, PendingCfg: pend, RemainingSec: 95}
	r := b.get("/admin/network")
	body := r.Body.String()
	for _, s := range []string{"Restzeit", "1:35", "192.168.1.50/24", "Bestätigen", "Sofort zurücksetzen",
		`action="/admin/network/ethernet/confirm"`, `action="/admin/network/ethernet/revert"`, `http-equiv="refresh"`} {
		wantContains(t, body, s)
	}
	b.local = &net.TCPAddr{IP: net.ParseIP("192.168.1.50"), Port: 8443}
	body = b.follow(b.postNet("/admin/network/ethernet/confirm", nil))
	wantContains(t, body, "bestätigt")
	if got := e.cfg().Network.Ethernet; !reflect.DeepEqual(got, pend) {
		t.Fatalf("gespeichert %+v", got)
	}
	if e.appliedCount() != 1 {
		t.Error("Apply fehlt")
	}
	if strings.Contains(body, "Restzeit") {
		t.Error("Fristkasten nach Bestätigung")
	}

	e.net.st.Pending, e.net.st.PendingCfg = true, config.EthernetCfg{Method: config.EthDHCP}
	body = b.follow(b.postNet("/admin/network/ethernet/revert", nil))
	wantContains(t, body, "wiederhergestellt")
	if e.net.reverts != 1 {
		t.Fatal("nicht zurückgesetzt")
	}
	if got := e.cfg().Network.Ethernet; !reflect.DeepEqual(got, pend) {
		t.Fatalf("Rücksetzen hat die Konfiguration geändert: %+v", got)
	}
}

func TestEthernetConfirmErrorKeepsConfig(t *testing.T) {
	e, b := newLoggedIn(t)
	e.net.st.Pending = true
	e.net.confirmErr = errors.New("Es wartet keine Netzwerkänderung auf Bestätigung.")
	r := b.postNet("/admin/network/ethernet/confirm", nil)
	if r.Code == http.StatusSeeOther {
		t.Fatal("Fehler nicht gemeldet")
	}
	wantContains(t, r.Body.String(), "wartet keine")
	if e.cfg().Network.Ethernet.Method != "" || e.appliedCount() != 0 {
		t.Fatal("trotz Fehler gespeichert")
	}
}

func TestEthernetStatusErrorShown(t *testing.T) {
	e, b := newLoggedIn(t)
	e.net.stErr = errors.New("Der Helfer ist nicht erreichbar")
	r := b.get("/admin/network")
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	wantContains(t, r.Body.String(), "Der Helfer ist nicht erreichbar")
}

func TestEthernetPostsNeedCSRF(t *testing.T) {
	e, b := newLoggedIn(t)
	for _, p := range []string{"/admin/network/ethernet", "/admin/network/ethernet/confirm", "/admin/network/ethernet/revert"} {
		if r := b.postRaw(p, url.Values{"method": {"dhcp"}}); r.Code != http.StatusForbidden {
			t.Errorf("%s: Status %d", p, r.Code)
		}
	}
	if e.net.calls() != 0 {
		t.Fatal("Helfer ohne CSRF gerufen")
	}
}

func TestEthernetStaticConfirmNeedsNewAddress(t *testing.T) {
	e, b := newLoggedIn(t)
	pend := config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.50", Prefix: 24}
	e.net.st = helperapi.NetStatus{Iface: "eth0", Pending: true, PendingCfg: pend, RemainingSec: 100}
	for _, local := range []net.Addr{nil, &net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 8443}, &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8443}} {
		b.local = local
		r := b.postNet("/admin/network/ethernet/confirm", nil)
		if r.Code != http.StatusConflict {
			t.Fatalf("%v: Status %d", local, r.Code)
		}
		wantContains(t, r.Body.String(), "Bitte über die neue Adresse https://192.168.1.50:8443/admin/network bestätigen.")
	}
	if e.net.confirms != 0 || e.cfg().Network.Ethernet.Method != "" {
		t.Fatal("über die alte Adresse bestätigt")
	}
}

func TestEthernetDHCPConfirmFromAnyAddress(t *testing.T) {
	e, b := newLoggedIn(t)
	e.net.st = helperapi.NetStatus{Iface: "eth0", Pending: true, PendingCfg: config.EthernetCfg{Method: config.EthDHCP}, RemainingSec: 100}
	b.local = &net.TCPAddr{IP: net.ParseIP("192.168.1.20"), Port: 8443}
	wantContains(t, b.follow(b.postNet("/admin/network/ethernet/confirm", nil)), "bestätigt")
	if e.net.confirms != 1 || e.cfg().Network.Ethernet.Method != config.EthDHCP {
		t.Fatal("DHCP nicht bestätigt")
	}
}
