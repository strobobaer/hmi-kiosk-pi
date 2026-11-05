//go:build linux

package selftest

import (
	"context"
	"errors"
	"image"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
)

func TestRunReportsEverySpecStep(t *testing.T) {
	f := newFixture(t)
	res := Run(context.Background(), f.d)

	want := []string{"client-bild", "fenster-leiste", "touch", "rueckkehr", "prozessende", "terminals", "schutz"}
	if len(res) != len(want) {
		t.Fatalf("%d Schritte: %+v", len(res), res)
	}
	for i, n := range want {
		if res[i].Name != n {
			t.Errorf("Schritt %d heißt %q, erwartet %q", i, res[i].Name, n)
		}
	}
	m := byName(t, res)
	for _, n := range []string{"client-bild", "touch", "rueckkehr", "prozessende", "terminals"} {
		if !m[n].OK {
			t.Errorf("%s: %+v", n, m[n])
		}
	}
	if !m["client-bild"].NeedsHuman || !strings.Contains(m["client-bild"].Detail, "Rand") {
		t.Errorf("client-bild muss den Menschen auf den weißen Rand hinweisen: %+v", m["client-bild"])
	}
	if !strings.Contains(m["client-bild"].Detail, "1024×600") {
		t.Errorf("client-bild nennt die Bildgröße nicht: %q", m["client-bild"].Detail)
	}
	if !m["fenster-leiste"].NeedsHuman {
		t.Errorf("labwc: Fensterplatz ist nicht messbar, der Mensch muss prüfen: %+v", m["fenster-leiste"])
	}
	if m["terminals"].Detail != "keine Terminals eingetragen" {
		t.Errorf("terminals: %q", m["terminals"].Detail)
	}
	if !m["schutz"].OK || !m["schutz"].NeedsHuman {
		t.Errorf("schutz: Overlay, Boot, Watchdog gut, Pi-5-Overlay braucht den Menschen: %+v", m["schutz"])
	}
	for _, sub := range []string{"Zurück", "Absturz", "Trennung"} {
		if !strings.Contains(m["rueckkehr"].Detail, sub) {
			t.Errorf("rueckkehr nennt %q nicht: %q", sub, m["rueckkehr"].Detail)
		}
	}
	if n := f.runner.alive(); n != 0 {
		t.Errorf("%d Clients laufen nach der Prüfung noch", n)
	}
	pages := f.rec.got()
	if len(pages) == 0 || pages[len(pages)-1] != "" {
		t.Errorf("die Anzeige muss am Ende zurückgesetzt sein: %v", pages)
	}
}

func TestFailedStepDoesNotAbortOthers(t *testing.T) {
	f := newFixture(t)
	f.runner.startErr = errors.New("remmina fehlt")
	res := Run(context.Background(), f.d)
	if len(res) != 7 {
		t.Fatalf("%d Schritte", len(res))
	}
	m := byName(t, res)
	for _, n := range []string{"client-bild", "rueckkehr", "prozessende"} {
		if m[n].OK || !strings.Contains(m[n].Detail, "remmina fehlt") {
			t.Errorf("%s müsste scheitern und den Grund nennen: %+v", n, m[n])
		}
	}
	if !m["terminals"].OK || !m["schutz"].OK {
		t.Errorf("andere Schritte laufen weiter: %+v %+v", m["terminals"], m["schutz"])
	}
}

func TestPanickingStepIsReportedAndOthersRun(t *testing.T) {
	f := newFixture(t)
	f.d.Peer = func(context.Context, config.Peer) coord.PeerState { panic("kaputt") }
	f.cfg.Peers = []config.Peer{{Name: "T2", Address: "x:1"}}
	res := Run(context.Background(), f.d)
	m := byName(t, res)
	if m["terminals"].OK || !strings.Contains(m["terminals"].Detail, "kaputt") {
		t.Fatalf("terminals: %+v", m["terminals"])
	}
	if !m["schutz"].OK {
		t.Fatalf("schutz: %+v", m["schutz"])
	}
}

func TestRunRefusesWhileSessionIsActive(t *testing.T) {
	f := newFixture(t)
	f.d.Busy = func() string { return "Eine Sitzung läuft. Bitte zuerst beenden." }
	res := Run(context.Background(), f.d)
	if len(res) != 1 || res[0].OK || !strings.Contains(res[0].Detail, "Sitzung läuft") {
		t.Fatalf("%+v", res)
	}
	if f.runner.starts != 0 {
		t.Fatal("während einer Sitzung darf kein Client starten")
	}
}

func TestMissingDependencyIsAFailureNotAPass(t *testing.T) {
	f := newFixture(t)
	f.d.Mounts, f.d.Exec, f.d.BootConfig, f.d.Peer = nil, nil, nil, nil
	f.cfg.Peers = []config.Peer{{Name: "T2", Address: "x:1"}}
	m := byName(t, Run(context.Background(), f.d))
	if m["schutz"].OK || m["terminals"].OK {
		t.Fatalf("fehlende Abhängigkeiten dürfen nicht bestehen: %+v %+v", m["schutz"], m["terminals"])
	}
}

func TestClientImageChecksWrongPassword(t *testing.T) {
	f := newFixture(t)
	m := byName(t, Run(context.Background(), f.d))
	if !strings.Contains(m["client-bild"].Detail, "falsches Passwort") {
		t.Fatalf("Detail nennt die Prüfung des falschen Passworts nicht: %q", m["client-bild"].Detail)
	}
}

func TestWindowStepMeasuresOnX11(t *testing.T) {
	const tree = `
xwininfo: Window id: 0x1e0 (the root window) (has no name)

  Root window id: 0x1e0 (the root window) (has no name)
  Parent window id: 0x0 (none)
     2 children:
     0x600004 "hmi-kiosk session 9001": ("remmina" "Remmina")  1280x752+0+48  +0+48
     0x400001 "Sitzungsleiste": ("chromium" "Chromium")  1280x48+0+0  +0+0
`
	for _, tc := range []struct {
		name, out string
		ok        bool
	}{
		{"richtig", tree, true},
		{"falsche Lage", strings.Replace(tree, "1280x752+0+48  +0+48", "1280x752+0+0  +0+0", 1), false},
		{"falsche Größe", strings.Replace(tree, "1280x752+0+48", "1000x752+0+48", 1), false},
		{"kein Fenster", "  2 children:\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Display.GraphicsProfile = "x11"
			f.d.Exec = func(_ context.Context, name string, args ...string) (string, error) {
				if name == "xwininfo" {
					return tc.out, nil
				}
				return "", errors.New("nicht vorhanden")
			}
			r := byName(t, Run(context.Background(), f.d))["fenster-leiste"]
			if r.OK != tc.ok {
				t.Fatalf("OK=%v: %+v", r.OK, r)
			}
		})
	}
	t.Run("xwininfo fehlt", func(t *testing.T) {
		f := newFixture(t)
		f.cfg.Display.GraphicsProfile = "x11"
		r := byName(t, Run(context.Background(), f.d))["fenster-leiste"]
		if r.OK || !strings.Contains(r.Detail, "xwininfo") {
			t.Fatalf("fehlendes Werkzeug muss scheitern: %+v", r)
		}
	})
}

func TestTouchFailsWithoutHits(t *testing.T) {
	t.Run("Ziele nicht getroffen", func(t *testing.T) {
		f := newFixture(t)
		f.rec.onTouch = func() { f.d.Touch.Hit(0); f.d.Touch.Hit(2) }
		r := byName(t, Run(context.Background(), f.d))["touch"]
		if r.OK || !strings.Contains(r.Detail, "2 von 5") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("kein Tipp auf dem Bild", func(t *testing.T) {
		f := newFixture(t)
		f.runner.tap = nil
		r := byName(t, Run(context.Background(), f.d))["touch"]
		if r.OK || !strings.Contains(r.Detail, "kein Tipp") {
			t.Fatalf("%+v", r)
		}
	})
	t.Run("Tipp weit neben jeder Markierung", func(t *testing.T) {
		f := newFixture(t)
		off := image.Pt(250, 350)
		f.runner.tap = &off
		r := byName(t, Run(context.Background(), f.d))["touch"]
		if r.OK || !strings.Contains(r.Detail, "Markierung") {
			t.Fatalf("%+v", r)
		}
	})
}

func TestReturnStepFollowsTheSessionRules(t *testing.T) {
	f := newFixture(t)
	f.runner.exitOnDrop = false // bleibt stehen; die Überwachung muss ihn nach LostAfter stoppen
	r := byName(t, Run(context.Background(), f.d))["rueckkehr"]
	if !r.OK {
		t.Fatalf("die Überwachung beendet den Client nach dem Verlust: %+v", r)
	}
	f = newFixture(t)
	f.runner.exitOnDrop = false
	f.d.Timing.DropWithin = 50 * time.Millisecond // kürzer als LostAfter
	r = byName(t, Run(context.Background(), f.d))["rueckkehr"]
	if r.OK || !strings.Contains(r.Detail, "Trennung") {
		t.Fatalf("zu langsam muss scheitern: %+v", r)
	}
}

func TestProcessEndRecordsHowTheClientEnded(t *testing.T) {
	f := newFixture(t)
	r := byName(t, Run(context.Background(), f.d))["prozessende"]
	if !r.OK || !strings.Contains(r.Detail, "von selbst") {
		t.Fatalf("%+v", r)
	}
	f = newFixture(t)
	f.runner.exitOnDrop = false
	r = byName(t, Run(context.Background(), f.d))["prozessende"]
	if !r.OK || !strings.Contains(r.Detail, "SIGTERM") {
		t.Fatalf("beendet erst durch SIGTERM: %+v", r)
	}
	f = newFixture(t)
	f.runner.exitOnDrop = false
	f.runner.ignoreTerm = true
	r = byName(t, Run(context.Background(), f.d))["prozessende"]
	if r.OK || !strings.Contains(r.Detail, "SIGKILL") {
		t.Fatalf("wer SIGTERM ignoriert, besteht nicht: %+v", r)
	}
}

func TestTerminalsReportsEachPeer(t *testing.T) {
	f := newFixture(t)
	f.cfg.Peers = []config.Peer{{Name: "Halle", Address: "a:1"}, {Name: "Büro", Address: "b:1"}, {Name: "Lager", Address: "c:1"}}
	f.d.Peer = func(_ context.Context, p config.Peer) coord.PeerState {
		switch p.Name {
		case "Büro":
			return coord.PeerState{Reachable: true}
		case "Lager":
			return coord.PeerState{Err: errors.New("timeout")}
		}
		return coord.PeerState{Reachable: true, KeyOK: true}
	}
	r := byName(t, Run(context.Background(), f.d))["terminals"]
	if r.OK {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{"Halle: erreichbar, Schlüssel stimmt", "Büro: erreichbar, Schlüssel falsch", "Lager: nicht erreichbar"} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("Detail ohne %q: %q", want, r.Detail)
		}
	}
}

func TestOverlayAndWatchdogDetection(t *testing.T) {
	t.Run("Overlay auf /", func(t *testing.T) {
		for _, tc := range []struct {
			name, mounts string
			want         bool
		}{
			{"aktiv", goodMounts, true},
			{"normales Dateisystem", "/dev/mmcblk0p2 / ext4 rw 0 0\n", false},
			{"letzter Eintrag gilt", "overlay / overlay rw 0 0\n/dev/sda1 / ext4 rw 0 0\n", false},
			{"Overlay nicht auf /", "overlay /var overlay rw 0 0\n/dev/sda1 / ext4 rw 0 0\n", false},
			{"leer", "", false},
		} {
			if got := overlayOnRoot(tc.mounts); got != tc.want {
				t.Errorf("%s: %v", tc.name, got)
			}
		}
	})
	t.Run("Boot-Partition schreibgeschützt", func(t *testing.T) {
		for _, tc := range []struct {
			name, mounts string
			ro, found    bool
		}{
			{"ro", goodMounts, true, true},
			{"rw", "/dev/mmcblk0p1 /boot/firmware vfat rw,relatime 0 0\n", false, true},
			{"alter Pfad", "/dev/mmcblk0p1 /boot vfat ro,relatime 0 0\n", true, true},
			{"fehlt", "overlay / overlay rw 0 0\n", false, false},
			{"Option nur als Teilwort", "/dev/x /boot/firmware vfat rw,errors=ro,nosuid 0 0\n", false, true},
		} {
			ro, found := bootReadOnly(tc.mounts)
			if ro != tc.ro || found != tc.found {
				t.Errorf("%s: ro=%v found=%v", tc.name, ro, found)
			}
		}
	})
	t.Run("Watchdog", func(t *testing.T) {
		for _, tc := range []struct {
			out  string
			want bool
		}{
			{"RuntimeWatchdogUSec=15s\n", true},
			{"RuntimeWatchdogUSec=1min 30s\n", true},
			{"RuntimeWatchdogUSec=15000000\n", true},
			{"RuntimeWatchdogUSec=0\n", false},
			{"RuntimeWatchdogUSec=infinity\n", false},
			{"RuntimeWatchdogUSec=\n", false},
			{"", false},
			{"Anderes=1\n", false},
		} {
			if got := watchdogOn(tc.out); got != tc.want {
				t.Errorf("%q: %v", tc.out, got)
			}
		}
	})
	t.Run("Funk-Overlays in config.txt", func(t *testing.T) {
		wifi, bt := radioOverlays("# dtoverlay=disable-wifi\n[pi4]\ndtoverlay=disable-bt\n[all]\ndtoverlay=disable-wifi,foo\n")
		if !wifi || bt {
			t.Errorf("wifi=%v bt=%v", wifi, bt)
		}
		wifi, bt = radioOverlays("dtoverlay=disable-bt\n")
		if wifi || !bt {
			t.Errorf("ohne Abschnitt: wifi=%v bt=%v", wifi, bt)
		}
	})
	t.Run("Schritt wertet alle Teile aus", func(t *testing.T) {
		f := newFixture(t)
		f.d.Exec = func(context.Context, string, ...string) (string, error) { return "RuntimeWatchdogUSec=0\n", nil }
		r := byName(t, Run(context.Background(), f.d))["schutz"]
		if r.OK || !strings.Contains(r.Detail, "Watchdog") {
			t.Fatalf("Watchdog aus muss scheitern: %+v", r)
		}
		f = newFixture(t)
		f.cfg.Network.WifiOff = true
		f.d.BootConfig = func() (string, error) { return "[all]\n", nil }
		r = byName(t, Run(context.Background(), f.d))["schutz"]
		if r.OK || !strings.Contains(r.Detail, "WLAN") {
			t.Fatalf("WLAN-Einstellung ohne Overlay muss scheitern: %+v", r)
		}
	})
}

func TestFormatMarksHumanChecks(t *testing.T) {
	out := Format([]StepResult{
		{Name: "a", OK: true, Detail: "gut"},
		{Name: "b", OK: true, NeedsHuman: true, Detail: "bitte schauen"},
		{Name: "c", OK: false, Detail: "kaputt"},
	})
	for _, want := range []string{"OK", "PRÜFEN", "FEHLER", "bitte schauen"} {
		if !strings.Contains(out, want) {
			t.Errorf("Ausgabe ohne %q:\n%s", want, out)
		}
	}
	if Failed([]StepResult{{OK: true}, {OK: true, NeedsHuman: true}}) {
		t.Error("PRÜFEN ist kein Fehler")
	}
	if !Failed([]StepResult{{OK: true}, {OK: false}}) {
		t.Error("OK=false ist ein Fehler")
	}
}

func TestTouchBoard(t *testing.T) {
	b := NewTouchBoard()
	if b.Hit(-1) || b.Hit(TouchTargets) {
		t.Fatal("ungültiges Ziel")
	}
	b.Hit(1)
	b.Hit(1)
	if b.Count() != 1 {
		t.Fatalf("Count %d", b.Count())
	}
	b.Reset()
	if b.Count() != 0 {
		t.Fatal("Reset")
	}
}

func TestReturnStepToleratesLateServerBookkeeping(t *testing.T) {
	f := newFixture(t)
	var mu sync.Mutex
	var lagUntil time.Time
	real := f.d.Established
	f.d.Established = func(ip net.IP, port int) (bool, error) {
		mu.Lock()
		lag := time.Now().Before(lagUntil)
		mu.Unlock()
		if lag {
			return true, nil // der Server zählt die Verbindung noch
		}
		return real(ip, port)
	}
	f.runner.onExit = func() { mu.Lock(); lagUntil = time.Now().Add(300 * time.Millisecond); mu.Unlock() }
	r := byName(t, Run(context.Background(), f.d))["rueckkehr"]
	if !r.OK {
		t.Fatalf("späte Buchführung des Servers ist kein Fehler: %+v", r)
	}
}

func TestProtectionDistinguishesInstallScriptFromImage(t *testing.T) {
	// install.sh: kein Overlay und keine Datenpartition → Hinweis, kein Fehler.
	f := newFixture(t)
	f.d.Mounts = func() ([]byte, error) { return []byte("/dev/mmcblk0p2 / ext4 rw,relatime 0 0\n"), nil }
	r := byName(t, Run(context.Background(), f.d))["schutz"]
	if !r.OK || !r.NeedsHuman || !strings.Contains(r.Detail, "Warnung") || !strings.Contains(r.Detail, "install.sh") {
		t.Fatalf("install.sh-System: %+v", r)
	}
	if r.Label() != "PRÜFEN" {
		t.Fatalf("Label %q", r.Label())
	}
	// Abbild (Kennzeichen vorhanden): Overlay aus und /data fehlt → Fehler, nicht Hinweis.
	f = newFixture(t)
	f.d.ImageMarker = func() bool { return true }
	f.d.Mounts = func() ([]byte, error) {
		return []byte("/dev/mmcblk0p2 / ext4 rw 0 0\n/dev/mmcblk0p1 /boot/firmware vfat ro 0 0\n"), nil
	}
	r = byName(t, Run(context.Background(), f.d))["schutz"]
	if r.OK || !strings.Contains(r.Detail, "Overlay") {
		t.Fatalf("Abbild ohne Overlay: %+v", r)
	}
	// Auch auf install.sh-Systemen bleibt ein abgeschalteter Watchdog ein Fehler.
	f = newFixture(t)
	f.d.Mounts = func() ([]byte, error) { return []byte("/dev/mmcblk0p2 / ext4 rw 0 0\n"), nil }
	f.d.Exec = func(context.Context, string, ...string) (string, error) { return "RuntimeWatchdogUSec=0\n", nil }
	if r := byName(t, Run(context.Background(), f.d))["schutz"]; r.OK {
		t.Fatalf("Watchdog aus: %+v", r)
	}
}

func TestWaitDisconnectedGraceIsTwoSecondsRegardlessOfPoll(t *testing.T) {
	f := newFixture(t)
	f.d.Timing.Poll = 700 * time.Millisecond // 2000/700 ganzzahlig wären nur 2 Runden
	f.d.Timing = f.d.Timing.withDefaults()
	r, err := newRig(f.d)
	if err != nil {
		t.Fatal(err)
	}
	defer r.close()
	start := time.Now()
	r.d.Established = func(net.IP, int) (bool, error) { return true, nil }
	r.srv.DropClients()
	// Ohne Client gilt connected() als falsch; mit Established=true und ClientCount 0 ebenso.
	_ = start
	if !r.waitDisconnected(context.Background()) {
		t.Fatal("kein Client: muss sofort frei sein")
	}
	p, _ := r.start(r.password, false)
	if _, err := r.waitConnected(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	begin := time.Now()
	if r.waitDisconnected(context.Background()) {
		t.Fatal("Verbindung besteht noch")
	}
	if el := time.Since(begin); el < 1900*time.Millisecond || el > 3500*time.Millisecond {
		t.Fatalf("Wartezeit %v statt etwa 2 s", el)
	}
}
