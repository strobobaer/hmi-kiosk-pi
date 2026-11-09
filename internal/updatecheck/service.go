package updatecheck

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/update"
)

// Result ist das Ergebnis der letzten Prüfung. Es liegt nur im Speicher.
type Result struct {
	At         time.Time
	Repo       string
	Prerelease bool
	Current    string
	Offer      *Offer // nil: nichts Neueres gefunden (oder Fehler)
	Dev        bool   // laufende Version ist kein veröffentlichter Stand
	Err        string
}

// Service hält das Prüfergebnis und lädt nur die Datei, die die letzte Prüfung gefunden hat.
// Er installiert nie selbst.
type Service struct {
	Checker  *Checker
	Settings func() config.UpdateCfg
	Current  string
	Now      func() time.Time
	// Logf meldet Ergebnisse der Hintergrundprüfung (nil: Protokoll).
	Logf func(format string, args ...any)

	checking sync.Mutex // eine Prüfung zugleich
	mu       sync.Mutex
	last     Result
	has      bool
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Check prüft sofort und merkt sich das Ergebnis.
func (s *Service) Check(ctx context.Context) Result {
	s.checking.Lock()
	defer s.checking.Unlock()
	cfg := s.Settings()
	res := Result{Repo: cfg.Repo, Prerelease: cfg.Prerelease, Current: s.Current}
	switch {
	case cfg.Repo == "":
		res.Err = "Keine Quelle eingetragen, die Update-Prüfung ist aus."
	case !update.ValidVersion(s.Current):
		res.Dev = true
	default:
		rels, err := s.Checker.Releases(ctx, cfg.Repo)
		if err == nil {
			res.Offer, err = Select(rels, s.Current, cfg.Prerelease)
		}
		if err != nil {
			res.Err = err.Error()
		}
	}
	res.At = s.now()
	s.mu.Lock()
	s.last, s.has = res, true
	s.mu.Unlock()
	return res
}

// Last liefert das letzte Ergebnis; false, solange noch keine Prüfung lief.
func (s *Service) Last() (Result, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last, s.has
}

// Download lädt die Paketdatei der zuletzt gefundenen Version. Andere Versionen oder
// Adressen gibt es nicht: Die Adresse stammt nur aus dem Prüfergebnis.
func (s *Service) Download(ctx context.Context, version string) ([]byte, error) {
	last, ok := s.Last()
	if !ok || last.Offer == nil || last.Offer.Version != version {
		return nil, errors.New("Diese Version hat die letzte Prüfung nicht gefunden. Bitte erneut nach Updates suchen.")
	}
	if cfg := s.Settings(); cfg.Repo != last.Repo || cfg.Prerelease != last.Prerelease {
		return nil, errors.New("Die Quelle wurde seit der letzten Prüfung geändert. Bitte erneut nach Updates suchen.")
	}
	if !last.Offer.Installable() {
		return nil, fmt.Errorf("Version %s lässt sich nicht per Klick installieren: %s", version, last.Offer.NoPackage)
	}
	return s.Checker.Download(ctx, *last.Offer)
}

// Run prüft nach first und danach alle every, bis ctx endet. Ohne Quelle wird nicht
// geprüft. Das Ergebnis wird nur angezeigt, nie installiert.
func (s *Service) Run(ctx context.Context, first, every time.Duration) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if s.Settings().Repo != "" {
			res := s.Check(ctx)
			switch {
			case res.Err != "" && ctx.Err() == nil:
				s.logf("Update-Prüfung: %s", res.Err)
			case res.Offer != nil:
				s.logf("Update-Prüfung: Version %s verfügbar", res.Offer.Version)
			}
		}
		t.Reset(every)
	}
}
