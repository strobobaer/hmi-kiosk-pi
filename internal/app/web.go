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

	mu   sync.Mutex
	want []string
	kick chan struct{}
}

func newOriginSync(call func(ctx context.Context, cmd, arg string) error) *originSync {
	return &originSync{call: call, retry: originRetry, kick: make(chan struct{}, 1)}
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
		log.Printf("Chromium-Richtlinie: %d Web-Server freigegeben", len(want))
	}
}
