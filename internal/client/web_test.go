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
		if strings.HasPrefix(r.Identifier, "chrome-") && strings.Contains(r.Identifier, WebAppProfileName) {
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

// Nach einem Absturz oder Stromausfall bleiben die Singleton-Verweise im dauerhaften
// Profil liegen; Chromium hielte sie für ein laufendes Fenster eines anderen Rechners
// (der Rechnername ändert sich auf dem Abbild) und startete nicht.
func TestWebAppRemovesStaleSingletonFiles(t *testing.T) {
	w, _ := webApp(t)
	if err := os.MkdirAll(w.ProfileDir, 0o700); err != nil {
		t.Fatal(err)
	}
	names := []string{"SingletonLock", "SingletonSocket", "SingletonCookie"}
	for _, n := range names {
		p := filepath.Join(w.ProfileDir, n)
		if err := os.Symlink("anderer-rechner-1234", p); err != nil {
			if err := os.WriteFile(p, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	keep := filepath.Join(w.ProfileDir, "Local State")
	if err := os.WriteFile(keep, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prepare("", Spec{URL: "http://grafana/", W: 800, H: 600}); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if _, err := os.Lstat(filepath.Join(w.ProfileDir, n)); !os.IsNotExist(err) {
			t.Errorf("%s noch da: %v", n, err)
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("Profildaten gelöscht")
	}
}

// Ohne Leiste: eigener Profilordner (andere app_id, keine Regel unter dem Rand) als
// Verweis auf das normale Profil, damit die Anmeldung gilt; Vollbild per --kiosk.
func TestWebAppFullScreenSharesProfile(t *testing.T) {
	w, _ := webApp(t)
	cmd, err := w.Prepare("", Spec{URL: "http://grafana/", W: 1920, H: 1080, FullScreen: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--kiosk", "--profile-directory=" + WebAppFullProfileName, "--class=" + WebAppFullProfileName, "--window-position=0,0", "--window-size=1920,1080"} {
		if !slices.Contains(cmd.Args, want) {
			t.Errorf("%q fehlt in %q", want, cmd.Args)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	link := filepath.Join(w.ProfileDir, WebAppFullProfileName)
	if target, err := os.Readlink(link); err != nil || target != WebAppProfileName {
		t.Fatalf("Verweis %q %v", target, err)
	}
	if fi, err := os.Stat(filepath.Join(w.ProfileDir, WebAppProfileName)); err != nil || !fi.IsDir() {
		t.Fatalf("Profil fehlt: %v", err)
	}
	// Ein echter Ordner an der Stelle (alter Stand, fremde Hand) wird zum Verweis.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(link, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prepare("", Spec{URL: "http://grafana/", FullScreen: true}); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != WebAppProfileName {
		t.Fatalf("Verweis %q %v", target, err)
	}
	// Mit Leiste weder --kiosk noch der Vollbild-Profilordner.
	cmd, _ = w.Prepare("", Spec{URL: "http://grafana/", W: 800, H: 600})
	if slices.Contains(cmd.Args, "--kiosk") || !slices.Contains(cmd.Args, "--profile-directory="+WebAppProfileName) {
		t.Fatalf("%q", cmd.Args)
	}
}

func TestWindowRulesFullScreenAndTabWindow(t *testing.T) {
	type action struct {
		Name string `xml:"name,attr"`
	}
	type rule struct {
		Identifier string   `xml:"identifier,attr"`
		Title      string   `xml:"title,attr"`
		Actions    []action `xml:"action"`
	}
	var lab struct {
		Rules []rule `xml:"windowRules>windowRule"`
	}
	parseXML(t, filepath.Join("..", "..", "packaging", "rootfs", "etc", "hmi-kiosk", "labwc", "rc.xml"), &lab)
	// matching liefert die Aktionen aller Regeln, die ein Fenster treffen (labwc wendet alle an).
	matching := func(id, title string) []string {
		var out []string
		for _, r := range lab.Rules {
			okID, _ := path.Match(r.Identifier, id)
			okTitle := r.Title == ""
			if !okTitle {
				okTitle, _ = path.Match(r.Title, title)
			}
			if okID && okTitle {
				for _, a := range r.Actions {
					out = append(out, a.Name)
				}
			}
		}
		return out
	}
	// Web-Kachel ohne Leiste (gemessen: app_id endet auf den Vollbild-Profilordner): keine Regel.
	if a := matching("chrome-grafana__d_x-"+WebAppFullProfileName, "grafana_/"); len(a) != 0 {
		t.Errorf("Vollbild-Web-Kachel von Regeln getroffen: %v", a)
	}
	// Normales Tab-Fenster aus target=_blank (gemessen: app_id = --class): wie die Kachel.
	for _, id := range []string{WebAppClass, WebAppFullProfileName} {
		if a := matching(id, "grafana:3000/t.html - Chromium"); !slices.Contains(a, "Maximize") {
			t.Errorf("Tab-Fenster %q: %v", id, a)
		}
	}
	if a := matching("chrome-127.0.0.1__bar-Default", "127.0.0.1_/bar"); slices.Contains(a, "Maximize") {
		t.Errorf("Leiste maximiert: %v", a)
	}
	// Remmina: mit Leiste maximiert unter dem Rand, ohne Leiste zusätzlich Vollbild.
	if a := matching("org.remmina.Remmina", RemminaTitlePrefix+" 1"); !slices.Contains(a, "Maximize") || slices.Contains(a, "ToggleFullscreen") {
		t.Errorf("Remmina mit Leiste: %v", a)
	}
	if a := matching("org.remmina.Remmina", RemminaFullTitlePrefix+" 1"); !slices.Contains(a, "ToggleFullscreen") {
		t.Errorf("Remmina ohne Leiste: %v", a)
	}
}
