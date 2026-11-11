//go:build e2e

// Ende-zu-Ende gegen einen echten PDH-Server über TLS (Gegenstelle: PDH
// internal/web/terminal_pairing_e2e_test.go, Ablauf: test/pdh-e2e/run.sh):
// koppeln, Zustand prüfen, Geräteanmeldung, Sperren in PDH.
//
//	HMIK_E2E_PDH_URL=https://pdh-e2e:8444 HMIK_E2E_CODE=XXXX-XXXX HMIK_E2E_CA=<Base64-PEM> \
//	  go test -tags e2e -run TestPDHKopplungE2E -v ./internal/product/pdhterminal/
package pdhterminal

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
)

// mitschnitt merkt sich Antworten (Kopfzeilen und Körper) außer denen der Kopplung.
type mitschnitt struct {
	next http.RoundTripper
	seen []string
}

func (m *mitschnitt) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := m.next.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if !strings.HasSuffix(req.URL.Path, "/api/terminal/v1/pair") {
		var h bytes.Buffer
		_ = resp.Header.Write(&h)
		m.seen = append(m.seen, req.Method+" "+req.URL.Path+"\n"+h.String()+string(body))
	}
	return resp, nil
}

func TestPDHKopplungE2E(t *testing.T) {
	base, code, caB64 := os.Getenv("HMIK_E2E_PDH_URL"), os.Getenv("HMIK_E2E_CODE"), os.Getenv("HMIK_E2E_CA")
	if base == "" || code == "" || caB64 == "" {
		t.Skip("HMIK_E2E_PDH_URL, HMIK_E2E_CODE, HMIK_E2E_CA fehlen (test/pdh-e2e/run.sh)")
	}
	ca, err := base64.StdEncoding.DecodeString(caB64)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		t.Fatal("CA-Zertifikat nicht lesbar")
	}
	rec := &mitschnitt{next: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
	ctx := context.Background()
	c := Client{HTTP: &http.Client{Transport: rec}}

	// ohne Vertrauen zum Zertifikat: keine Kopplung
	if _, err := (Client{}).Pair(ctx, base, code, "Prüfstand Pi"); err == nil || !strings.Contains(err.Error(), "Zertifikat") {
		t.Errorf("Kopplung ohne Vertrauen: %v", err)
	}

	d, err := c.Pair(ctx, base, code, "Prüfstand Pi")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if d.Secret == "" || d.DeviceID == "" {
		t.Fatalf("Gerät unvollständig: %#v", d)
	}
	t.Logf("OK gekoppelt als %q", d.User)

	if _, err := c.Pair(ctx, base, code, "Prüfstand Pi"); err == nil {
		t.Error("Code ließ sich zweimal einlösen")
	}
	if st, err := c.Check(ctx, d); st != StateOK || err != nil {
		t.Fatalf("Check nach Kopplung: %v %v", st, err)
	}

	// Geräteanmeldung im Browser-Stil: Cookie-Topf, keine Weiterleitung folgen
	jar, _ := cookiejar.New(nil)
	br := &http.Client{Transport: rec, Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := br.PostForm(base+"/terminal/geraet", url.Values{"secret": {d.Secret}})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/terminal" {
		t.Fatalf("Geräteanmeldung: %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if len(resp.Cookies()) == 0 {
		t.Fatal("kein Sitzungs-Cookie")
	}
	get := func() (int, string) {
		t.Helper()
		r, err := br.Get(base + "/terminal")
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode >= 300 && r.StatusCode < 400 {
			return r.StatusCode, "Location: " + r.Header.Get("Location")
		}
		return r.StatusCode, string(b)
	}
	if code, body := get(); code != http.StatusOK {
		t.Fatalf("GET /terminal mit Gerätesitzung: %d %s", code, body)
	}
	t.Log("OK /terminal 200 mit Gerätesitzung")

	// Sperren in PDH
	rr, err := br.Post(base+"/e2e/revoke", "text/plain", nil)
	if err != nil {
		t.Fatal(err)
	}
	rr.Body.Close()
	if rr.StatusCode != http.StatusNoContent {
		t.Fatalf("Sperren: %d", rr.StatusCode)
	}
	if code, body := get(); code != http.StatusForbidden || !strings.Contains(body, "Gerät nicht gekoppelt oder gesperrt") {
		t.Errorf("GET /terminal nach Sperre: %d %.200s", code, body)
	}
	if st, err := c.Check(ctx, d); st != StateRevoked || err != nil {
		t.Errorf("Check nach Sperre: %v %v", st, err)
	}
	t.Log("OK nach Sperre /terminal 403, Check = Revoked")

	// Das Geheimnis kommt nur in der Kopplungsantwort vor
	for _, s := range rec.seen {
		if strings.Contains(s, d.Secret) {
			t.Errorf("Geheimnis in Antwort: %.80s", s)
		}
	}
}
