package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/appcore"
	"hmikiosk/internal/config"
	"hmikiosk/internal/release"
	"hmikiosk/internal/session"
	"hmikiosk/internal/web"
)

func remoteGet(t *testing.T, url, key string) (int, string, *http.Response) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := insecureClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp
}

// Die Fernbefehle gibt es nur auf dem HTTPS-Listener, nie auf der Kachelseite oder der
// Admin-Seite am Terminal (Loopback).
func TestRemoteAPIOnlyOnHTTPSListener(t *testing.T) {
	key, hash, err := web.NewRemoteKey()
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t)
	f.saveConfig(func(c *config.Config) { c.Admin.RemoteKeyHash = hash })
	f.start()

	code, body, resp := remoteGet(t, "https://"+f.bound.Admin+"/api/remote/v1/status", key)
	if code != http.StatusOK {
		t.Fatalf("HTTPS: %d %s", code, body)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(body), &st); err != nil {
		t.Fatal(err)
	}
	if st["terminal"] != "T1" || st["busy"] != false || st["version"] != release.Version {
		t.Fatalf("Status %v", st)
	}

	// Der Fingerabdruck der Admin-Seite ist der des Zertifikats, das der Listener zeigt.
	sum := sha256.Sum256(resp.TLS.PeerCertificates[0].RawSubjectPublicKeyInfo)
	want := hex.EncodeToString(sum[:])
	if got := strings.ReplaceAll(f.s.adminDeps().TLSFingerprint(), ":", ""); got != want {
		t.Fatalf("Fingerabdruck %q, Listener %q", got, want)
	}

	for name, addr := range map[string]string{"Kachelseite": f.bound.Local, "Admin am Terminal": f.bound.LocalAdmin} {
		if addr == "" {
			t.Fatalf("%s läuft nicht", name)
		}
		code, body, _ := remoteGet(t, "http://"+addr+"/api/remote/v1/status", key)
		if code == http.StatusOK || strings.Contains(body, `"terminal"`) {
			t.Fatalf("%s beantwortet Fernbefehle: %d %s", name, code, body)
		}
	}
}

func TestRemoteSessionJournalMode(t *testing.T) {
	f := newFixture(t)
	f.saveConfig(nil)
	f.start()
	f.s.onSessionEnd(session.Ended{HMIName: "L – von PDH: U", Remote: true, ViewOnly: true, Reason: "x"})
	f.s.onSessionEnd(session.Ended{HMIName: "L – von PDH: U", Remote: true, Reason: "y"})
	eventually(t, "Protokollzeilen", func() bool {
		b, _ := os.ReadFile(filepath.Join(f.data, "log", appcore.LogFileName))
		return strings.Contains(string(b), `modus="PDH, nur ansehen"`) && strings.Contains(string(b), `modus="PDH, bedienen"`)
	})
}
