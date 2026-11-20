//go:build windows

package pdhterminal

import (
	"context"
	"strings"

	"golang.org/x/sys/windows/registry"

	"hmikiosk/internal/config"
	"hmikiosk/internal/selftest"
)

// onWindows trennt die wenigen Stellen, an denen das Terminal unter Windows anders läuft.
const onWindows = true

// platformHardwareID ist die MachineGuid der Installation
// (HKLM\SOFTWARE\Microsoft\Cryptography); leer, wenn sie nicht lesbar ist.
func platformHardwareID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`,
		registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(v))
}

// initKeyboard: Die Bildschirmtastatur (wvkbd) gibt es nur unter Linux; s.kbd bleibt nil.
func (s *service) initKeyboard() {}

// cardPlatformReason: PN532 hängt an I²C, SPI oder UART des Raspberry Pi.
func cardPlatformReason(c config.CardCfg) string {
	if c.Type == config.CardTypePN532I2C {
		return "Nur am Raspberry Pi"
	}
	return ""
}

// displayPlatformReason: Das OLED hängt am I²C-Bus des Raspberry Pi.
func displayPlatformReason(c config.StatusDisplayCfg) string {
	if c.Type != "" {
		return "Statusdisplay nur am Raspberry Pi"
	}
	return ""
}

// platformBrowserNote: Der Browser (Edge) kommt mit seiner eigenen Anbindung.
const platformBrowserNote = "Die Anzeige im Browser ist unter Windows noch nicht angebunden"

// selftestSteps: Konfiguration, Kartenleser und „Edge gefunden“ (die Edge-Anbindung füllt den Schritt).
func (s *service) selftestSteps(*selftest.Deps) []selftest.Step {
	return []selftest.Step{
		{Name: "konfiguration", Run: func(context.Context, selftest.Deps) selftest.StepResult {
			s.mu.RLock()
			warn := s.cfgWarn
			s.mu.RUnlock()
			if warn != "" {
				return selftest.StepResult{Detail: warn}
			}
			if err := config.Validate(s.config()); err != nil {
				return selftest.StepResult{Detail: err.Error()}
			}
			return selftest.StepResult{OK: true, Detail: "Konfiguration gültig"}
		}},
		selftest.CardStep(),
		{Name: "edge", Run: func(context.Context, selftest.Deps) selftest.StepResult {
			return selftest.StepResult{OK: true, Detail: "Edge gefunden: noch nicht angebunden"}
		}},
	}
}
