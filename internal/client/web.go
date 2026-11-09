package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"hmikiosk/internal/config"
)

// Kennung des Fensters einer Web-Kachel für die Fensterregeln (gemessen mit Chromium 156
// aus Trixie): Unter Wayland ist die app_id "chrome-<aus --app abgeleitet>-<Profilordner>",
// --class zählt dort nicht; also endet sie auf "-hmi-kiosk-web" (labwc: "chrome-*-hmi-kiosk-web").
// Unter X11 setzt --class die Klasse von WM_CLASS (openbox: class="hmi-kiosk-web"). Die
// Leiste (chrome-127.0.0.1__bar-Default, WM_CLASS "127.0.0.1__bar", "Chromium") trifft keins davon.
const (
	WebAppClass       = "hmi-kiosk-web"
	WebAppProfileName = "hmi-kiosk-web"
	// WebAppFullProfileName ist Profilordner und Klasse ohne Leiste (Daueranzeige): Keine Regel
	// trifft dieses Fenster, also gilt Chromiums Vollbild (--kiosk); die Regel unter dem Rand hob
	// es auf (gemessen). Der Ordner ist ein Verweis auf das normale Profil, die Anmeldung gilt also.
	WebAppFullProfileName = "hmi-kiosk-web-voll"
)

// WebApp ist das „Profil“ einer Web-Kachel: ein eigener Chromium-Prozess im App-Fenster
// mit dauerhaftem Profil (Anmeldungen bleiben über Neustarts erhalten).
type WebApp struct {
	Chromium   string // Pfad zu Chromium
	Platform   string // Grafikprofil: "labwc" (Wayland) oder "x11"
	ProfileDir string // dauerhaftes Profil auf der Datenpartition
	CacheDir   string // Zwischenspeicher im RAM
}

func (WebApp) Name() string { return "web" }

// Prepare baut den Aufruf; dir (RAM-Ordner der VNC-Profile) braucht es nicht.
func (w WebApp) Prepare(_ string, s Spec) (Command, error) {
	if w.Chromium == "" {
		return Command{}, errors.New("kein Chromium gefunden")
	}
	if w.ProfileDir == "" || w.CacheDir == "" {
		return Command{}, errors.New("Profilordner fehlt")
	}
	if _, err := config.ParseWebURL(s.URL); err != nil {
		return Command{}, err
	}
	for _, d := range []string{w.ProfileDir, w.CacheDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Command{}, err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return Command{}, err
		}
	}
	// Es läuft immer nur ein Web-Chromium (der Sitzungsautomat startet erst nach dem Ende des
	// vorigen). Übrig gebliebene Singleton-Verweise eines abgestürzten Laufs würden den Start
	// verhindern oder die Adresse an einen toten Prozess weitergeben.
	for _, n := range []string{"SingletonLock", "SingletonSocket", "SingletonCookie"} {
		if err := os.Remove(filepath.Join(w.ProfileDir, n)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Command{}, err
		}
	}
	profile, class := WebAppProfileName, WebAppClass
	if err := os.MkdirAll(filepath.Join(w.ProfileDir, WebAppProfileName), 0o700); err != nil {
		return Command{}, err
	}
	if s.FullScreen {
		if err := linkFullProfile(w.ProfileDir); err != nil {
			return Command{}, err
		}
		profile, class = WebAppFullProfileName, WebAppFullProfileName
	}
	var a []string
	switch w.Platform {
	case "labwc":
		a = append(a, "--ozone-platform=wayland")
	case "x11":
		a = append(a, "--ozone-platform=x11")
	}
	a = append(a,
		"--no-first-run", "--no-default-browser-check",
		"--disable-session-crashed-bubble", "--hide-crash-restore-bubble",
		"--password-store=basic", "--use-mock-keychain", "--disable-pinch",
		"--overscroll-history-navigation=0", "--disable-features=Translate",
		"--check-for-update-interval=31536000",
		"--noerrdialogs", "--disable-infobars", "--touch-events=enabled",
		"--user-data-dir="+w.ProfileDir,
		"--profile-directory="+profile,
		"--class="+class,
		"--disk-cache-dir="+w.CacheDir,
		fmt.Sprintf("--window-position=%d,%d", s.X, s.Y),
		fmt.Sprintf("--window-size=%d,%d", s.W, s.H),
	)
	if s.FullScreen {
		a = append(a, "--kiosk")
	}
	// Die Adresse beginnt nach der Prüfung mit http, ist also nie selbst ein Schalter.
	a = append(a, "--app="+s.URL)
	return Command{Path: w.Chromium, Args: a}, nil
}

// linkFullProfile legt den Vollbild-Profilordner als Verweis auf das normale Profil an.
func linkFullProfile(dir string) error {
	link := filepath.Join(dir, WebAppFullProfileName)
	if target, err := os.Readlink(link); err == nil && target == WebAppProfileName {
		return nil
	}
	if err := os.RemoveAll(link); err != nil {
		return err
	}
	return os.Symlink(WebAppProfileName, link)
}
