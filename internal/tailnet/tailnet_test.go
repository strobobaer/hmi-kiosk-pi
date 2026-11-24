package tailnet

import (
	"strings"
	"testing"
)

const statusRunning = `{"Version":"1.80.0","BackendState":"Running","Self":{"HostName":"pdh-terminal","DNSName":"pdh-terminal.tail1234.ts.net.","TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.7"]},"Peer":{"x":{}}}`
const statusNeedsLogin = `{"BackendState":"NeedsLogin","Self":{"HostName":"","DNSName":"","TailscaleIPs":null}}`
const statusStopped = `{"BackendState":"Stopped","Self":{"HostName":"kasse","DNSName":"kasse.tail1.ts.net.","TailscaleIPs":["100.64.0.9"]}}`

func TestParseStatus(t *testing.T) {
	st, err := ParseStatus([]byte(statusRunning))
	if err != nil {
		t.Fatal(err)
	}
	want := Status{Installed: true, LoggedIn: true, Name: "pdh-terminal", DNSName: "pdh-terminal.tail1234.ts.net", IP: "100.64.0.7", State: "Running"}
	if st != want {
		t.Fatalf("Running: %+v, erwartet %+v", st, want)
	}
	st, err = ParseStatus([]byte(statusNeedsLogin))
	if err != nil || !st.Installed || st.LoggedIn || st.State != "NeedsLogin" || st.IP != "" {
		t.Fatalf("NeedsLogin: %+v %v", st, err)
	}
	st, err = ParseStatus([]byte(statusStopped))
	if err != nil || !st.Installed || st.LoggedIn || st.State != "Stopped" || st.Name != "kasse" {
		t.Fatalf("Stopped: %+v %v", st, err)
	}
	st, err = ParseStatus([]byte(`{"installed":false}`))
	if err != nil || st.Installed {
		t.Fatalf("nicht installiert: %+v %v", st, err)
	}
	if _, err := ParseStatus([]byte(`kein json`)); err == nil {
		t.Fatal("Unsinn muss ein Fehler sein")
	}
}

func TestHostnameFrom(t *testing.T) {
	if got := HostnameFrom("Halle 3 / Säge"); got != "halle-3---s-ge" {
		t.Errorf("got %q", got)
	}
	if got := HostnameFrom(""); got != "pdh-terminal" {
		t.Errorf("leer: %q", got)
	}
	if got := HostnameFrom(strings.Repeat("a", 80)); len(got) != 63 {
		t.Errorf("Länge %d", len(got))
	}
	if !ValidHostname(HostnameFrom("Ä/ö x")) {
		t.Error("abgeleiteter Name muss gültig sein")
	}
	for in, want := range map[string]string{"Halle 3 ": "halle-3", "Ölwanne": "lwanne", " - ": "pdh-terminal", "---": "pdh-terminal", "-a-": "a"} {
		if got := HostnameFrom(in); got != want {
			t.Errorf("HostnameFrom(%q) = %q, erwartet %q", in, got, want)
		}
	}
	if got := HostnameFrom(strings.Repeat("a", 62) + " b"); got != strings.Repeat("a", 62) {
		t.Errorf("Schnitt nach 63 Zeichen: %q", got)
	}
}

func TestValidHostname(t *testing.T) {
	for _, h := range []string{"", "-a", "a-", "A", "a b", "a_b", strings.Repeat("a", 64)} {
		if ValidHostname(h) {
			t.Errorf("%q angenommen", h)
		}
	}
	for _, h := range []string{"a", "halle-3---s-ge", strings.Repeat("a", 63)} {
		if !ValidHostname(h) {
			t.Errorf("%q abgelehnt", h)
		}
	}
}

func TestValidateKey(t *testing.T) {
	for _, k := range []string{"", "a\nb", "a\rb", "a\x00b", "a\x7fb", strings.Repeat("k", 513), "a b", "a\u0085b", "a\u2028b", "k\xffk", "kä"} {
		if err := ValidateKey(k); err == nil {
			t.Errorf("%q muss abgelehnt werden", k)
		} else if k != "" && strings.Contains(err.Error(), k) {
			t.Errorf("Fehler enthält den Schlüssel: %v", err)
		}
	}
	if err := ValidateKey("tskey-auth-kABC123CNTRL-xyz"); err != nil {
		t.Error(err)
	}
	if err := ValidateKey(strings.Repeat("k", 512)); err != nil {
		t.Error(err)
	}
}
