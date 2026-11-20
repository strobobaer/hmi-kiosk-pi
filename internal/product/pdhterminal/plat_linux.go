//go:build linux

package pdhterminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"hmikiosk/internal/config"
	"hmikiosk/internal/keyboard"
	"hmikiosk/internal/keyboard/clientrun"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/product/pdhterminal/oled"
	"hmikiosk/internal/selftest"
)

// onWindows trennt die wenigen Stellen, an denen das Terminal unter Windows anders läuft.
const onWindows = false

// platformHardwareID: Seriennummer aus Device-Tree oder DMI.
func platformHardwareID() string { return platform.HardwareID(procRoot, sysRoot) }

// initKeyboard legt die Bildschirmtastatur (wvkbd) an; sie gibt es nur unter labwc (Wayland).
func (s *service) initKeyboard() {
	s.kbd = clientrun.NewExec(func() (string, error) { return keyboard.Find(nil) }, func() int { _, h := s.screen(); return h })
	s.kbd.SetEnabled(s.sessionProfile() == "labwc")
}

// cardPlatformReason: Auf dem Raspberry Pi gibt es keine zusätzliche Einschränkung.
func cardPlatformReason(config.CardCfg) string { return "" }

// displayPlatformReason: Auf dem Raspberry Pi gibt es keine zusätzliche Einschränkung.
func displayPlatformReason(config.StatusDisplayCfg) string { return "" }

// platformBrowserNote ist leer: Chromium wird gestartet.
const platformBrowserNote = ""

// selftestSteps: Schutz von System und Funk, Kartenleser und Statusdisplay.
func (s *service) selftestSteps(d *selftest.Deps) []selftest.Step {
	d.Mounts = func() ([]byte, error) { return os.ReadFile(filepath.Join(procRoot, "mounts")) }
	d.Exec = runCommand
	d.BootConfig = readBootConfig
	d.ImageMarker = func() bool { _, err := os.Stat(selftest.ImageMarkerFile); return err == nil }
	if sd := s.config().StatusDisplay; sd.Type != "" {
		if ok, reason := sd.Known(); ok {
			d.StatusDisplay = func(_ context.Context, bus string, addr int) error { return oled.Probe(bus, addr) }
		} else {
			d.StatusDisplay = func(context.Context, string, int) error { return errors.New(reason) }
		}
	}
	return append(selftest.CoreSteps(), selftest.CardStep(), selftest.StatusDisplayStep())
}
