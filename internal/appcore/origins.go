package appcore

import (
	"context"
	"encoding/json"
	"log"
	"slices"
	"sync"
	"time"
)

const (
	// cmdSetWebOrigins ist der Helferbefehl (helper.CmdSetWebOrigins).
	cmdSetWebOrigins  = "set-web-origins"
	originRetry       = 30 * time.Second
	originCallTimeout = 30 * time.Second
)

// OriginSync hält die Ursprünge der Web-Kacheln in der Chromium-Richtlinie aktuell (über
// den root-Helfer, /etc ist auf dem Abbild ein tmpfs-Overlay). Er sendet nur Änderungen und
// wiederholt nach einem Fehler; Set blockiert nie.
type OriginSync struct {
	call  func(ctx context.Context, cmd, arg string) error
	retry time.Duration

	mu      sync.Mutex
	want    []string
	written []string      // zuletzt erfolgreich geschrieben
	changed chan struct{} // schließt bei jedem erfolgreichen Schreiben
	kick    chan struct{}
}

// NewOriginSync legt den Abgleich an; call ist der Helferaufruf (Befehl, Argument).
func NewOriginSync(call func(ctx context.Context, cmd, arg string) error) *OriginSync {
	return &OriginSync{call: call, retry: originRetry, kick: make(chan struct{}, 1), changed: make(chan struct{})}
}

// WaitFor kehrt zurück, sobald die geschriebene Richtlinie origin enthält, oder mit dem
// Fehler von ctx. Ein frisch gestartetes Chromium liest die Richtlinie beim Start.
func (o *OriginSync) WaitFor(ctx context.Context, origin string) error {
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

// Set merkt die gewünschten Ursprünge vor; Run sendet sie.
func (o *OriginSync) Set(origins []string) {
	o.mu.Lock()
	o.want = slices.Clone(origins)
	o.mu.Unlock()
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// Run gleicht ab, bis ctx endet.
func (o *OriginSync) Run(ctx context.Context) {
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

// AssumeWritten nimmt origins als geschrieben an (Start: der Helfer hat die Richtlinie vor dem
// Dienst aus seiner gespeicherten Liste geschrieben). Der Abgleich sendet trotzdem.
func (o *OriginSync) AssumeWritten(origins []string) {
	o.mu.Lock()
	o.written = slices.Clone(origins)
	o.mu.Unlock()
}
