package app

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"hmikiosk/internal/browser"
	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
)

const (
	// webProfileDir liegt auf der Datenpartition: Die Anmeldung einer Web-Kachel (Cookie)
	// überlebt so Neustarts, auch auf dem Abbild mit tmpfs-Wurzel.
	webProfileDir = "web-profile"
	webCacheDir   = "web-cache" // im RAM-Ordner von Chromium
)

// webProfile liefert das Profil der Web-Kacheln; nil ohne Chromium.
func (s *service) webProfile() client.Profile {
	if !s.opts.ChromiumEnabled {
		return nil
	}
	path, err := browser.FindChromium(s.lookPath)
	if err != nil {
		log.Printf("Web-Kachel: %v", err)
		return nil
	}
	return client.WebApp{
		Chromium:   path,
		Platform:   s.sessionProfile(),
		ProfileDir: filepath.Join(s.opts.DataDir, webProfileDir),
		CacheDir:   filepath.Join(s.opts.RunDir, chromiumDir, webCacheDir),
	}
}

// clearWebLogins löscht das Profil der Web-Kacheln samt gespeicherter Anmeldungen. Bei
// offener Web-Kachel lehnt es ab (session.ErrWebOpen); solange es läuft, startet keine.
func (s *service) clearWebLogins() error {
	return s.mgr.WhenNoWeb(func() error {
		if err := os.RemoveAll(filepath.Join(s.opts.DataDir, webProfileDir)); err != nil {
			log.Printf("Web-Profil nicht gelöscht: %v", err)
			return fmt.Errorf("Die gespeicherten Web-Anmeldungen ließen sich nicht löschen: %v", err)
		}
		log.Print("Gespeicherte Web-Anmeldungen gelöscht")
		return nil
	})
}

// webReadyLimit begrenzt das Warten auf die Richtlinie vor dem Start einer Web-Kachel.
var webReadyLimit = 5 * time.Second

// webReady wartet, bis der Ursprung der Web-Kachel in der Chromium-Richtlinie steht (nach
// einer Änderung schreibt der Helfer ihn erst; sonst zeigte Chromium die Sperrseite).
func (s *service) webReady(ctx context.Context, h config.HMI) error {
	origin, err := config.WebOrigin(h.URL)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, webReadyLimit)
	defer cancel()
	if err := s.origins.WaitFor(ctx, origin); err != nil {
		return fmt.Errorf("Ursprung %s nicht in der Chromium-Richtlinie: %w", origin, err)
	}
	return nil
}

// tcpReachable prüft den Port einer Web-Kachel (im Test ersetzbar: Loopback-Adressen sind
// für Web-Kacheln verboten, ein echter Testserver ginge also nicht).
var tcpReachable = rfb.TCPReachable
