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
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{status: http.StatusOK, pkg: []byte("signiertes-paket")}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		f.ua.Store(r.Header.Get("User-Agent"))
		f.acc.Store(r.Header.Get("Accept"))
		w.WriteHeader(f.status)
		fmt.Fprint(w, f.releases)
	})
	mux.HandleFunc("GET /dl/hmi-kiosk-0.2.0.pkg", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(f.pkg)
	})
	mux.HandleFunc("GET /umleitung", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/dl/hmi-kiosk-0.2.0.pkg", http.StatusFound)
	})
	mux.HandleFunc("GET /umleitung-http", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://"+r.Host+"/dl/hmi-kiosk-0.2.0.pkg", http.StatusFound)
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
	o.AssetURL = f.srv.URL + "/umleitung-http"
	if _, err := c.Download(context.Background(), o); err == nil {
		t.Error("Weiterleitung auf http angenommen")
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
