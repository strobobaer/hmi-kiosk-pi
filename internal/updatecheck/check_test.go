package updatecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeGitHub liefert die Release-Liste und die Paketdatei über HTTPS.
type fakeGitHub struct {
	srv      *httptest.Server
	releases string
	status   int
	pkg      []byte
	calls    atomic.Int32
	ua, acc  atomic.Value
	perPage  atomic.Value
	// redirect ist das Ziel von /umleitung-fremd.
	redirect atomic.Value
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{status: http.StatusOK, pkg: []byte("signiertes-paket")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.ua.Store(r.Header.Get("User-Agent"))
		f.acc.Store(r.Header.Get("Accept"))
		f.perPage.Store(r.URL.Query().Get("per_page"))
		w.WriteHeader(f.status)
		fmt.Fprint(w, f.releases)
	})
	mux.HandleFunc("GET /dl/hmi-kiosk-0.2.0.pkg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(f.pkg)
	})
	mux.HandleFunc("GET /umleitung", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dl/hmi-kiosk-0.2.0.pkg", http.StatusFound)
	})
	mux.HandleFunc("GET /umleitung-fremd", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.redirect.Load().(string), http.StatusFound)
	})
	f.srv = httptest.NewTLSServer(mux)
	t.Cleanup(f.srv.Close)
	f.releases = fmt.Sprintf(`[{"tag_name":"v0.2.0","draft":false,"prerelease":false,"html_url":"https://github.com/o/r/releases/tag/v0.2.0",
		"body":"Neu","assets":[{"name":"hmi-kiosk-0.2.0.pkg","size":%d,"browser_download_url":"%s/dl/hmi-kiosk-0.2.0.pkg"}]}]`, len(f.pkg), f.srv.URL)
	return f
}

func (f *fakeGitHub) checker() *Checker {
	return &Checker{BaseURL: f.srv.URL, Client: f.srv.Client(), UserAgent: "hmi-kiosk/0.1.0"}
}

func TestCheckerReadsReleasesWithHeaders(t *testing.T) {
	f := newFakeGitHub(t)
	rels, err := f.checker().Releases(context.Background(), "o/r")
	if err != nil || len(rels) != 1 || rels[0].TagName != "v0.2.0" || len(rels[0].Assets) != 1 {
		t.Fatalf("%+v %v", rels, err)
	}
	if f.ua.Load() != "hmi-kiosk/0.1.0" || f.acc.Load() != "application/vnd.github+json" {
		t.Errorf("Header: %v %v", f.ua.Load(), f.acc.Load())
	}
}

func TestCheckerRejectsBadAnswers(t *testing.T) {
	f := newFakeGitHub(t)
	for name, set := range map[string]func(){
		"kaputt":    func() { f.releases = `[{"tag_name":` },
		"kein feld": func() { f.releases = `{"message":"x"}` },
		"404":       func() { f.status = http.StatusNotFound; f.releases = `{}` },
		"zu groß":   func() { f.releases = `[` + strings.Repeat(" ", maxAPIBytes) + `]` },
	} {
		f.status = http.StatusOK
		set()
		if _, err := f.checker().Releases(context.Background(), "o/r"); err == nil {
			t.Errorf("%s: angenommen", name)
		}
	}
	if _, err := f.checker().Releases(context.Background(), "o/r/x"); err == nil {
		t.Error("ungültige Quelle angenommen")
	}
}

func TestCheckerRefusesPlainHTTPAPI(t *testing.T) {
	c := &Checker{BaseURL: "http://api.example", Client: http.DefaultClient}
	if _, err := c.Releases(context.Background(), "o/r"); err == nil {
		t.Error("http angenommen")
	}
}

func TestDownloadFollowsHTTPSRedirectOnly(t *testing.T) {
	f := newFakeGitHub(t)
	c := f.checker()
	o := Offer{Version: "0.2.0", AssetURL: f.srv.URL + "/umleitung", AssetSize: int64(len(f.pkg))}
	blob, err := c.Download(context.Background(), o)
	if err != nil || string(blob) != string(f.pkg) {
		t.Fatalf("%q %v", blob, err)
	}
	// Eigener Server ohne TLS: Er darf keine Anfrage bekommen.
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits.Add(1)
		_, _ = w.Write(f.pkg)
	}))
	defer plain.Close()
	f.redirect.Store(plain.URL + "/dl/hmi-kiosk-0.2.0.pkg")
	o.AssetURL = f.srv.URL + "/umleitung-fremd"
	if _, err := c.Download(context.Background(), o); err == nil || !strings.Contains(err.Error(), "ohne HTTPS") {
		t.Errorf("Weiterleitung auf http: %v", err)
	}
	if n := plainHits.Load(); n != 0 {
		t.Errorf("http-Server bekam %d Anfragen", n)
	}
	o.AssetURL = strings.Replace(f.srv.URL, "https:", "http:", 1) + "/dl/hmi-kiosk-0.2.0.pkg"
	if _, err := c.Download(context.Background(), o); err == nil {
		t.Error("http-Adresse angenommen")
	}
}

func TestDownloadEnforcesSize(t *testing.T) {
	f := newFakeGitHub(t)
	c := f.checker()
	o := Offer{Version: "0.2.0", AssetURL: f.srv.URL + "/dl/hmi-kiosk-0.2.0.pkg", AssetSize: int64(len(f.pkg)) - 1}
	if _, err := c.Download(context.Background(), o); err == nil {
		t.Error("abweichende Größe angenommen")
	}
	old := MaxPackageBytes
	MaxPackageBytes = 4
	defer func() { MaxPackageBytes = old }()
	o.AssetSize = 4
	f.pkg = []byte("zu-lang")
	if _, err := c.Download(context.Background(), o); err == nil || !strings.Contains(err.Error(), "größer") {
		t.Errorf("Grenze: %v", err)
	}
}

func TestCheckerAsksForHundredReleases(t *testing.T) {
	f := newFakeGitHub(t)
	if _, err := f.checker().Releases(context.Background(), "o/r"); err != nil {
		t.Fatal(err)
	}
	if got := f.perPage.Load(); got != "100" {
		t.Errorf("per_page = %v", got)
	}
}

func TestGitHubHost(t *testing.T) {
	for _, ok := range []string{"api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com", "GitHub.com", "github.com:443"} {
		if !githubHost(ok) {
			t.Errorf("%q sollte erlaubt sein", ok)
		}
	}
	for _, bad := range []string{"", "githubusercontent.com", "github.com.evil.example", "evilgithub.com", "evil.example", "127.0.0.1",
		"10.0.0.1:443", "github.com:8443", "gist.github.com", "x.githubusercontent.com.evil.example"} {
		if githubHost(bad) {
			t.Errorf("%q sollte gesperrt sein", bad)
		}
	}
}

// Abfrage und Download gehen nur an GitHub (im Test zusätzlich an den Rechner der Basis-URL).
func TestDownloadOnlyFromAllowedHosts(t *testing.T) {
	f := newFakeGitHub(t)
	c := f.checker()
	var otherHits atomic.Int32
	other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
		_, _ = w.Write(f.pkg)
	}))
	defer other.Close()
	c.Client = other.Client() // vertraut dem fremden Zertifikat, die Sperre muss trotzdem greifen

	o := Offer{Version: "0.2.0", AssetURL: other.URL + "/dl/hmi-kiosk-0.2.0.pkg", AssetSize: int64(len(f.pkg))}
	if _, err := c.Download(context.Background(), o); err == nil || !strings.Contains(err.Error(), "nicht erlaubt") {
		t.Errorf("fremder Rechner: %v", err)
	}
	f.redirect.Store(other.URL + "/dl/hmi-kiosk-0.2.0.pkg")
	o.AssetURL = f.srv.URL + "/umleitung-fremd"
	c.Client = f.srv.Client()
	if _, err := c.Download(context.Background(), o); err == nil || !strings.Contains(err.Error(), "nicht erlaubt") {
		t.Errorf("Weiterleitung auf fremden Rechner: %v", err)
	}
	if n := otherHits.Load(); n != 0 {
		t.Errorf("fremder Rechner bekam %d Anfragen", n)
	}
	// Ohne Basis-URL (Produktion) gilt nur GitHub.
	prod := &Checker{Client: f.srv.Client()}
	o.AssetURL = f.srv.URL + "/dl/hmi-kiosk-0.2.0.pkg"
	if _, err := prod.Download(context.Background(), o); err == nil || !strings.Contains(err.Error(), "nicht erlaubt") {
		t.Errorf("Produktion, Testserver: %v", err)
	}
}
