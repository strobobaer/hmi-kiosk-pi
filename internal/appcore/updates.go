package appcore

import (
	"context"
	"math/rand/v2"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/release"
	"hmikiosk/internal/updatecheck"
)

// Die erste Prüfung nach dem Start kommt verzögert und mit Zufallsversatz, damit
// nicht alle Terminals einer Anlage nach einem Stromausfall zugleich fragen.
const (
	updateCheckFirst  = 3 * time.Minute
	updateCheckJitter = 5 * time.Minute
	updateCheckEvery  = 24 * time.Hour
)

// NewUpdateCheck legt die Update-Prüfung für Produkt p an. logf darf nil sein (Protokoll).
func NewUpdateCheck(p release.Product, settings func() config.UpdateCfg, logf func(string, ...any)) *updatecheck.Service {
	return &updatecheck.Service{
		Checker:  &updatecheck.Checker{UserAgent: p.ID + "/" + release.Version},
		Settings: settings,
		Current:  release.Version,
		Product:  p,
		Logf:     logf,
	}
}

// RunUpdateCheck prüft im Hintergrund und zeigt nur an; installiert wird nie von selbst.
func RunUpdateCheck(ctx context.Context, s *updatecheck.Service) {
	first := updateCheckFirst + rand.N(updateCheckJitter)
	s.Run(ctx, first, updateCheckEvery)
}
