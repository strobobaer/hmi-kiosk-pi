package appcore

import (
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/release"
)

func TestNewUpdateCheckUsesProduct(t *testing.T) {
	for _, p := range []release.Product{release.HMIKiosk, release.PDHTerminal} {
		s := NewUpdateCheck(p, func() config.UpdateCfg { return config.UpdateCfg{} }, nil)
		if s.Product != p {
			t.Errorf("Produkt %v", s.Product)
		}
		if want := p.ID + "/" + release.Version; s.Checker.UserAgent != want {
			t.Errorf("User-Agent %q, erwartet %q", s.Checker.UserAgent, want)
		}
		if s.Current != release.Version {
			t.Errorf("Current %q", s.Current)
		}
	}
}
