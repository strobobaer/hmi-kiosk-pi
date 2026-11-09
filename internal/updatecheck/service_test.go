package updatecheck

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

type settings struct {
	mu  sync.Mutex
	cfg config.UpdateCfg
}

func (s *settings) get() config.UpdateCfg {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg
}

func (s *settings) set(c config.UpdateCfg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cfg = c
}

func newService(f *fakeGitHub, current string) (*Service, *settings) {
	st := &settings{cfg: config.UpdateCfg{Repo: "o/r"}}
	at := time.Date(2026, 11, 9, 8, 0, 0, 0, time.UTC)
	return &Service{Checker: f.checker(), Settings: st.get, Current: current, Now: func() time.Time { return at }, Logf: func(string, ...any) {}}, st
}

func TestServiceCheckStoresOfferAndDownloadsOnlyIt(t *testing.T) {
	f := newFakeGitHub(t)
	s, st := newService(f, "0.1.0")
	if _, ok := s.Last(); ok {
		t.Fatal("Ergebnis vor der ersten Prüfung")
	}
	if _, err := s.Download(context.Background(), "0.2.0"); err == nil {
		t.Error("Download ohne Prüfung")
	}
	res := s.Check(context.Background())
	if res.Err != "" || res.Offer == nil || res.Offer.Version != "0.2.0" || res.Repo != "o/r" || res.At.IsZero() {
		t.Fatalf("%+v", res)
	}
	if last, ok := s.Last(); !ok || last.Offer == nil || last.Offer.Version != "0.2.0" {
		t.Errorf("%+v", last)
	}
	if _, err := s.Download(context.Background(), "0.3.0"); err == nil {
		t.Error("nicht gefundene Version geladen")
	}
	blob, err := s.Download(context.Background(), "0.2.0")
	if err != nil || string(blob) != "signiertes-paket" {
		t.Fatalf("%q %v", blob, err)
	}
	// Nach einem Wechsel der Quelle gilt das alte Ergebnis nicht mehr.
	st.set(config.UpdateCfg{Repo: "o/anders"})
	if _, err := s.Download(context.Background(), "0.2.0"); err == nil || !strings.Contains(err.Error(), "erneut") {
		t.Errorf("alte Quelle: %v", err)
	}
}

func TestServiceCheckWithoutPackageCannotDownload(t *testing.T) {
	f := newFakeGitHub(t)
	f.releases = `[{"tag_name":"v0.2.0","html_url":"https://github.com/o/r/releases/tag/v0.2.0","assets":[]}]`
	s, _ := newService(f, "0.1.0")
	res := s.Check(context.Background())
	if res.Offer == nil || res.Offer.Installable() {
		t.Fatalf("%+v", res)
	}
	if _, err := s.Download(context.Background(), "0.2.0"); err == nil {
		t.Error("ohne Paket geladen")
	}
}

func TestServiceDevAndEmptyRepoDoNotAsk(t *testing.T) {
	f := newFakeGitHub(t)
	s, _ := newService(f, "dev")
	if res := s.Check(context.Background()); !res.Dev || res.Offer != nil {
		t.Errorf("dev: %+v", res)
	}
	s, st := newService(f, "0.1.0")
	st.set(config.UpdateCfg{})
	if res := s.Check(context.Background()); res.Err == "" || res.Offer != nil {
		t.Errorf("leere Quelle: %+v", res)
	}
	if n := f.calls.Load(); n != 0 {
		t.Errorf("%d Anfragen an GitHub", n)
	}
}

func TestServiceCheckReportsError(t *testing.T) {
	f := newFakeGitHub(t)
	f.releases = "kaputt"
	s, _ := newService(f, "0.1.0")
	if res := s.Check(context.Background()); res.Err == "" || res.Offer != nil {
		t.Errorf("%+v", res)
	}
}

func TestServiceRunChecksPeriodicallyAndSkipsEmptyRepo(t *testing.T) {
	f := newFakeGitHub(t)
	s, st := newService(f, "0.1.0")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx, time.Millisecond, 5*time.Millisecond)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for f.calls.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if f.calls.Load() < 2 {
		t.Fatalf("%d Prüfungen", f.calls.Load())
	}
	st.set(config.UpdateCfg{})
	time.Sleep(50 * time.Millisecond) // eine laufende Prüfung endet noch
	n := f.calls.Load()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	if got := f.calls.Load(); got != n {
		t.Errorf("ohne Quelle weiter geprüft: %d, dann %d", n, got)
	}
	if last, _ := s.Last(); last.Offer == nil {
		t.Errorf("leere Quelle hat das Ergebnis überschrieben: %+v", last)
	}
}
