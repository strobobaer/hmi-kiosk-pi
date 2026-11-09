package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sync"
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

	// cmdSetWebOrigins ist der Helferbefehl (helper.CmdSetWebOrigins).
	cmdSetWebOrigins  = "set-web-origins"
	originRetry       = 30 * time.Second
	originCallTimeout = 30 * time.Second
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

// originSync hält die Ursprünge der Web-Kacheln in der Chromium-Richtlinie aktuell (über
// den root-Helfer, /etc ist auf dem Abbild ein tmpfs-Overlay). Er sendet nur Änderungen und
// wiederholt nach einem Fehler; set blockiert nie.
type originSync struct {
	call  func(ctx context.Context, cmd, arg string) error
	retry time.Duration

	mu      sync.Mutex
	want    []string
	written []string      // zuletzt erfolgreich geschrieben
	changed chan struct{} // schließt bei jedem erfolgreichen Schreiben
	kick    chan struct{}
}

func newOriginSync(call func(ctx context.Context, cmd, arg string) error) *originSync {
	return &originSync{call: call, retry: originRetry, kick: make(chan struct{}, 1), changed: make(chan struct{})}
}

// waitFor kehrt zurück, sobald die geschriebene Richtlinie origin enthält, oder mit dem
// Fehler von ctx. Ein frisch gestartetes Chromium liest die Richtlinie beim Start.
func (o *originSync) waitFor(ctx context.Context, origin string) error {
	for {
		o.mu.Lock()
		done, changed := slices.Contains(o.written, origin), o.changed
		o.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
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
	if err := s.origins.waitFor(ctx, origin); err != nil {
		return fmt.Errorf("Ursprung %s nicht in der Chromium-Richtlinie: %w", origin, err)
	}
	return nil
}

func (o *originSync) set(origins []string) {
	o.mu.Lock()
	o.want = slices.Clone(origins)
	o.mu.Unlock()
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

func (o *originSync) run(ctx context.Context) {
	var (
		sent    []string
		didSend bool
		retry   <-chan time.Time
		lastErr string
	)
	for {
		select {
		case <-ctx.Done():
			return
		case <-o.kick:
		case <-retry:
		}
		retry = nil
		o.mu.Lock()
		want := slices.Clone(o.want)
		o.mu.Unlock()
		if want == nil || didSend && slices.Equal(sent, want) {
			continue
		}
		arg, _ := json.Marshal(want)
		cctx, cancel := context.WithTimeout(ctx, originCallTimeout)
		err := o.call(cctx, cmdSetWebOrigins, string(arg))
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if msg := err.Error(); msg != lastErr {
				log.Printf("Chromium-Richtlinie für Web-Kacheln nicht geschrieben: %v (neuer Versuch alle %v)", err, o.retry)
				lastErr = msg
			}
			retry = time.After(o.retry)
			continue
		}
		sent, didSend, lastErr = want, true, ""
		o.mu.Lock()
		o.written = want
		close(o.changed)
		o.changed = make(chan struct{})
		o.mu.Unlock()
		log.Printf("Chromium-Richtlinie: %d Web-Server freigegeben", len(want))
	}
}

// tcpReachable prüft den Port einer Web-Kachel (im Test ersetzbar: Loopback-Adressen sind
// für Web-Kacheln verboten, ein echter Testserver ginge also nicht).
var tcpReachable = rfb.TCPReachable

// assumeWritten nimmt origins als geschrieben an (Start: der Helfer hat die Richtlinie vor dem
// Dienst aus seiner gespeicherten Liste geschrieben). Der Abgleich sendet trotzdem.
func (o *originSync) assumeWritten(origins []string) {
	o.mu.Lock()
	o.written = slices.Clone(origins)
	o.mu.Unlock()
}
