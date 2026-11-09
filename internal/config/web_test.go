package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func webSample(url string) Config {
	c := sample()
	c.Kiosk.PermanentHMI = ""
	c.HMIs = append(c.HMIs, HMI{ID: "w1", Name: "Grafana", Kind: KindWeb, URL: url})
	return c
}

func TestWebURLAccepted(t *testing.T) {
	for _, u := range []string{
		"http://grafana:3000/d/abc?kiosk&refresh=30s",
		"https://grafana.werk.local/d/abc/linie-1?orgId=1&kiosk",
		"http://10.0.0.7",
		"http://10.0.0.7:3000/",
		"https://[fd00::7]:8443/x",
		"http://Grafana.Local:3000/Pfad#teil",
		"http://grafana_alt:3000/d/über",
	} {
		if err := Validate(webSample(u)); err != nil {
			t.Errorf("%q abgelehnt: %v", u, err)
		}
	}
}

func TestWebURLRejected(t *testing.T) {
	for _, u := range []string{
		"",
		"grafana:3000",
		"ftp://grafana/",
		"file:///etc/passwd",
		"javascript:alert(1)",
		"chrome://settings",
		"http:/grafana",
		"http://",
		"http://user:pw@grafana:3000/",
		"http://user@grafana/",
		"http://grafana:0/",
		"http://grafana:65536/",
		"http://grafana:30x0/",
		"http://-grafana/",
		"http://gra fana/",
		"http://grafana/a b",
		"http://grafana/\nx",
		"http://grafana/\x00",
		"http://*.grafana/",
		"http://grafana./",
		"http://a..b/",
		"http://[fe80::1%25eth0]/",
		" http://grafana/",
		"http://" + strings.Repeat("a", 2100) + "/",
	} {
		if err := Validate(webSample(u)); err == nil {
			t.Errorf("%q angenommen", u)
		}
	}
}

func TestWebCredentialsInURLNamedInError(t *testing.T) {
	err := Validate(webSample("http://admin:geheim@grafana:3000/"))
	if err == nil || !strings.Contains(err.Error(), "Benutzer") {
		t.Fatalf("Fehler %v", err)
	}
	if strings.Contains(err.Error(), "geheim") {
		t.Fatalf("Passwort im Fehlertext: %v", err)
	}
}

func TestWebNeedsNoVNCFields(t *testing.T) {
	c := webSample("http://grafana:3000/")
	if c.HMIs[1].Host != "" || c.HMIs[1].Port != 0 {
		t.Fatal("Beispiel falsch")
	}
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
	// VNC bleibt streng: ohne Host/Port ungültig.
	c = sample()
	c.HMIs[0].Kind = KindVNC
	c.HMIs[0].Host = ""
	if Validate(c) == nil {
		t.Fatal("VNC ohne Host angenommen")
	}
}

func TestKindChecked(t *testing.T) {
	for _, k := range []string{"", KindVNC} {
		c := sample()
		c.HMIs[0].Kind = k
		if err := Validate(c); err != nil {
			t.Errorf("Art %q abgelehnt: %v", k, err)
		}
	}
	c := sample()
	c.HMIs[0].Kind = "rdp"
	if Validate(c) == nil {
		t.Fatal("unbekannte Art angenommen")
	}
}

func TestWebReachModes(t *testing.T) {
	for _, r := range []string{"", ReachPing, ReachPort, ReachOff} {
		c := webSample("http://grafana:3000/")
		c.HMIs[1].Reach = r
		if err := Validate(c); err != nil {
			t.Errorf("%q: %v", r, err)
		}
	}
}

func TestOldConfigIsVNC(t *testing.T) {
	var h HMI
	if err := json.Unmarshal([]byte(`{"id":"a","name":"A","host":"10.0.0.1","port":5900}`), &h); err != nil {
		t.Fatal(err)
	}
	if h.IsWeb() || h.Kind != "" {
		t.Fatalf("alte Konfiguration ist keine VNC-Kachel: %+v", h)
	}
	b, _ := json.Marshal(sample().HMIs[0])
	if strings.Contains(string(b), `"kind"`) || strings.Contains(string(b), `"url"`) {
		t.Fatalf("VNC-HMI schreibt Web-Felder: %s", b)
	}
}

func TestPermanentMayBeWeb(t *testing.T) {
	c := webSample("http://grafana:3000/")
	c.Kiosk.PermanentHMI = "w1"
	c.Kiosk.HideBar = true
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestEndpoint(t *testing.T) {
	cases := []struct {
		h    HMI
		host string
		port int
	}{
		{HMI{Host: "10.0.0.5", Port: 5901}, "10.0.0.5", 5901},
		{HMI{Kind: KindWeb, URL: "http://grafana:3000/d/x"}, "grafana", 3000},
		{HMI{Kind: KindWeb, URL: "http://Grafana/"}, "grafana", 80},
		{HMI{Kind: KindWeb, URL: "https://grafana/"}, "grafana", 443},
		{HMI{Kind: KindWeb, URL: "https://[fd00::7]/"}, "fd00::7", 443},
	}
	for _, c := range cases {
		host, port, ok := c.h.Endpoint()
		if !ok || host != c.host || port != c.port {
			t.Errorf("%+v: %q %d %v", c.h, host, port, ok)
		}
	}
	if _, _, ok := (HMI{Kind: KindWeb, URL: "kaputt"}).Endpoint(); ok {
		t.Error("ungültige URL hat einen Endpunkt")
	}
}

func TestWebOrigins(t *testing.T) {
	c := sample()
	c.HMIs = append(c.HMIs,
		HMI{ID: "w1", Name: "A", Kind: KindWeb, URL: "http://Grafana:3000/d/a?kiosk"},
		HMI{ID: "w2", Name: "B", Kind: KindWeb, URL: "http://grafana:3000/d/b"},
		HMI{ID: "w3", Name: "C", Kind: KindWeb, URL: "https://10.0.0.9/"},
		HMI{ID: "w4", Name: "D", Kind: KindWeb, URL: "http://[fd00::7]:8080/"},
		HMI{ID: "w5", Name: "E", Kind: KindWeb, URL: "kaputt"},
	)
	got := WebOrigins(c)
	want := []string{"http://[fd00::7]:8080", "http://grafana:3000", "https://10.0.0.9:443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
	for _, o := range got {
		if !ValidOrigin(o) {
			t.Errorf("eigener Ursprung %q ungültig", o)
		}
	}
	if WebOrigins(sample()) == nil || len(WebOrigins(sample())) != 0 {
		t.Fatal("ohne Web-Kacheln: leere Liste (nicht nil) erwartet")
	}
}

func TestValidOrigin(t *testing.T) {
	for _, o := range []string{"http://grafana:3000", "https://10.0.0.9:443", "http://[fd00::7]:8080", "http://a-b.c_d.local:1"} {
		if !ValidOrigin(o) {
			t.Errorf("%q abgelehnt", o)
		}
	}
	for _, o := range []string{
		"", "http://grafana", "http://grafana:3000/", "http://grafana:3000/x", "ftp://grafana:21",
		"http://Grafana:3000", "http://*:3000", "http://gra fana:1", "http://grafana:0", "http://grafana:65536",
		"http://user@grafana:3000", "http://[zz::1]:80", "http://grafana:3000\n", `http://"x":1`, "http://-a:1",
		"http://a..b:1", "http://grafana:03000", "http://127.0.0.1:8080", "http://localhost:8080", "http://[::1]:80", "http://010.1.1.1:80", "http://0x7f.1:80",
	} {
		if ValidOrigin(o) {
			t.Errorf("%q angenommen", o)
		}
	}
}

// Chromium liest Namen mit Zahl als letztem Teil (oder 0x…) als IPv4-Adresse, auch in
// Schreibweisen, die netip nicht kennt. Solche Namen wären in Richtlinie und Anzeige mehrdeutig.
func TestWebURLRejectsAmbiguousNumericHosts(t *testing.T) {
	for _, h := range []string{"010.1.1.1", "1.2.3", "0x7f.1", "999.1.1.1", "1.2.3.4.5", "grafana.1", "0x7f000001", "4294967295", "a.0X1f"} {
		if err := Validate(webSample("http://" + h + ":3000/")); err == nil {
			t.Errorf("%q angenommen", h)
		}
	}
	for _, h := range []string{"10.1.1.1", "grafana1", "1grafana.werk", "x0x1"} {
		if err := Validate(webSample("http://" + h + ":3000/")); err != nil {
			t.Errorf("%q abgelehnt: %v", h, err)
		}
	}
}

func TestWebURLRejectsLoopback(t *testing.T) {
	for _, u := range []string{
		"http://127.0.0.1:3000/", "http://127.1.2.3/", "http://localhost:3000/", "http://LocalHost/",
		"http://grafana.localhost/", "http://[::1]:3000/", "http://[::ffff:127.0.0.1]/", "http://0.0.0.0/", "http://[::]/",
	} {
		err := Validate(webSample(u))
		if err == nil {
			t.Errorf("%q angenommen", u)
			continue
		}
		if !strings.Contains(err.Error(), "Terminal selbst") && !strings.Contains(err.Error(), "ungültig") {
			t.Errorf("%q: Meldung %v", u, err)
		}
	}
}

func TestValidateLimitsWebOrigins(t *testing.T) {
	c := sample()
	c.Kiosk.PermanentHMI = ""
	for i := range MaxWebOrigins + 1 {
		c.HMIs = append(c.HMIs, HMI{ID: fmt.Sprintf("w%d", i), Name: "W", Kind: KindWeb, URL: fmt.Sprintf("http://grafana%d:3000/", i)})
	}
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "Höchstens 32") {
		t.Fatalf("33 Server: %v", err)
	}
	c.HMIs = c.HMIs[:len(c.HMIs)-1]
	if err := Validate(c); err != nil {
		t.Fatalf("32 Server: %v", err)
	}
	// Viele Kacheln auf demselben Server zählen einmal.
	c.HMIs = append(c.HMIs, HMI{ID: "dup", Name: "W", Kind: KindWeb, URL: "http://grafana0:3000/d/anders"})
	if err := Validate(c); err != nil {
		t.Fatalf("doppelter Server: %v", err)
	}
	// Lange Namen: die Liste muss in die Anfrage an den Helfer passen.
	c = sample()
	c.Kiosk.PermanentHMI = ""
	for i := range 20 {
		host := fmt.Sprintf("%s.%s.%s%02d.werk", strings.Repeat("a", 60), strings.Repeat("a", 60), strings.Repeat("a", 55), i)
		c.HMIs = append(c.HMIs, HMI{ID: fmt.Sprintf("l%d", i), Name: "L", Kind: KindWeb, URL: "http://" + host + ":3000/"})
	}
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "zu lang") {
		t.Fatalf("lange Liste: %v", err)
	}
}

func TestWebOriginsFitHelperRequest(t *testing.T) {
	// Größte erlaubte Liste: 32 Ursprünge bis zur Grenze; die Anfrage an den Helfer
	// ({"cmd":…,"arg":"<JSON mit maskierten Anführungszeichen>"}) bleibt unter 4096 Byte.
	var list []string
	for i := 0; ; i++ {
		next := append(list, fmt.Sprintf("https://%s%02d:65535", strings.Repeat("b", 60), i))
		b, _ := json.Marshal(next)
		if len(next) > MaxWebOrigins || len(b) > MaxWebOriginsJSON {
			break
		}
		list = next
	}
	arg, _ := json.Marshal(list)
	req, _ := json.Marshal(struct {
		Cmd string `json:"cmd"`
		Arg string `json:"arg"`
	}{"set-web-origins", string(arg)})
	if len(req)+1 > 4096 {
		t.Fatalf("Anfrage %d Byte", len(req)+1)
	}
}
