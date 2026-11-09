package client

import (
	"encoding/xml"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func webApp(t *testing.T) (WebApp, string) {
	t.Helper()
	dir := t.TempDir()
	return WebApp{
		Chromium:   "/usr/bin/chromium",
		Platform:   "labwc",
		ProfileDir: filepath.Join(dir, "data", "web-profile"),
		CacheDir:   filepath.Join(dir, "run", "web-cache"),
	}, dir
}

func TestWebAppCommand(t *testing.T) {
	w, _ := webApp(t)
	url := "http://grafana:3000/d/abc?kiosk&refresh=30s"
	cmd, err := w.Prepare("", Spec{URL: url, Y: 48, W: 1280, H: 752})
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != "/usr/bin/chromium" {
		t.Fatalf("Pfad %q", cmd.Path)
	}
	for _, want := range []string{
		"--ozone-platform=wayland",
		"--user-data-dir=" + w.ProfileDir,
		"--profile-directory=" + WebAppProfileName,
		"--class=" + WebAppClass,
		"--disk-cache-dir=" + w.CacheDir,
		"--window-position=0,48",
		"--window-size=1280,752",
		"--no-first-run",
		"--touch-events=enabled",
		"--app=" + url,
	} {
		if !slices.Contains(cmd.Args, want) {
			t.Errorf("Argument %q fehlt in %q", want, cmd.Args)
		}
	}
	if cmd.Args[len(cmd.Args)-1] != "--app="+url {
		t.Errorf("--app nicht zuletzt: %q", cmd.Args)
	}
	for _, a := range cmd.Args {
		// Zertifikatsfehler werden nie übergangen. Kein --kiosk: Die Fensterregel (maximiert unter
		// dem Rand von 48) hebt den Vollbildmodus unter labwc ohnehin auf (gemessen).
		if strings.Contains(a, "ignore-certificate") || a == "--kiosk" || a == "--no-sandbox" {
			t.Errorf("unerwartetes Argument %q", a)
		}
	}
}

func TestWebAppCreatesPrivateDirs(t *testing.T) {
	w, _ := webApp(t)
	if err := os.MkdirAll(w.ProfileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prepare("", Spec{URL: "http://grafana/", W: 800, H: 600}); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for _, d := range []string{w.ProfileDir, w.CacheDir} {
		fi, err := os.Stat(d)
		if err != nil || fi.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, fi.Mode(), err)
		}
	}
}

func TestWebAppX11(t *testing.T) {
	w, _ := webApp(t)
	w.Platform = "x11"
	cmd, err := w.Prepare("", Spec{URL: "http://grafana/", W: 800, H: 600})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(cmd.Args, "--ozone-platform=x11") {
		t.Fatalf("%q", cmd.Args)
	}
}

func TestWebAppRejects(t *testing.T) {
	w, _ := webApp(t)
	for _, u := range []string{"", "--kiosk", "file:///etc/passwd", "http://a:b@c/", "http://x/\n--no-sandbox"} {
		if _, err := w.Prepare("", Spec{URL: u, W: 800, H: 600}); err == nil {
			t.Errorf("%q angenommen", u)
		}
	}
	w.Chromium = ""
	if _, err := w.Prepare("", Spec{URL: "http://grafana/"}); err == nil {
		t.Error("ohne Chromium angenommen")
	}
	w, _ = webApp(t)
	w.ProfileDir = ""
	if _, err := w.Prepare("", Spec{URL: "http://grafana/"}); err == nil {
		t.Error("ohne Profil angenommen")
	}
}

// Die Fensterregeln stehen im Paket; sie müssen gültiges XML sein (labwc verwirft sonst die
// ganze Datei, etwa bei "--" in einem Kommentar) und genau die Web-Kachel treffen.
func TestWindowRulesMatchWebAppButNotBar(t *testing.T) {
	root := filepath.Join("..", "..", "packaging", "rootfs", "etc")
	type rule struct {
		Identifier string `xml:"identifier,attr"`
	}
	var lab struct {
		Rules []rule `xml:"windowRules>windowRule"`
	}
	parseXML(t, filepath.Join(root, "hmi-kiosk", "labwc", "rc.xml"), &lab)
	var web, bar string
	for _, r := range lab.Rules {
		if strings.Contains(r.Identifier, WebAppProfileName) {
			web = r.Identifier
		}
		if strings.Contains(r.Identifier, "__bar") {
			bar = r.Identifier
		}
	}
	// Gemessene app_ids (Chromium 156, Wayland).
	webIDs := []string{"chrome-grafana.test__d_abc-hmi-kiosk-web", "chrome-10.1.2.3__-hmi-kiosk-web"}
	barID := "chrome-127.0.0.1__bar-Default"
	for _, id := range webIDs {
		if ok, _ := path.Match(web, id); !ok {
			t.Errorf("Regel %q trifft %q nicht", web, id)
		}
		if ok, _ := path.Match(bar, id); ok {
			t.Errorf("Leistenregel %q trifft die Web-Kachel %q", bar, id)
		}
	}
	if ok, _ := path.Match(web, barID); ok || web == "" {
		t.Errorf("Regel %q trifft die Leiste", web)
	}
	var ob struct {
		Apps []struct {
			Class string `xml:"class,attr"`
			Name  string `xml:"name,attr"`
		} `xml:"applications>application"`
	}
	parseXML(t, filepath.Join(root, "xdg", "openbox", "rc.xml"), &ob)
	found := false
	for _, a := range ob.Apps {
		found = found || a.Class == WebAppClass
	}
	if !found {
		t.Errorf("openbox-Regel für class=%q fehlt", WebAppClass)
	}
}

func parseXML(t *testing.T, p string, v any) {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", p, err)
	}
}
