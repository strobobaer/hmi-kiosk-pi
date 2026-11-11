package app

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

func (s *service) newUpdateCheck() *updatecheck.Service {
	return &updatecheck.Service{
		Checker:  &updatecheck.Checker{UserAgent: release.HMIKiosk.ID + "/" + release.Version},
		Settings: func() config.UpdateCfg { return s.config().Update },
		Current:  release.Version,
		Product:  release.HMIKiosk,
	}
}

// runUpdateCheck prüft im Hintergrund und zeigt nur an; installiert wird nie von selbst.
func (s *service) runUpdateCheck(ctx context.Context) {
	first := updateCheckFirst + rand.N(updateCheckJitter)
	s.updates.Run(ctx, first, updateCheckEvery)
}
