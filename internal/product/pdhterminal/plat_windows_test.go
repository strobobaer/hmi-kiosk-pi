//go:build windows

package pdhterminal

import (
	"regexp"
	"testing"

	"hmikiosk/internal/config"
)

func TestPlatformHardwareIDIsMachineGuid(t *testing.T) {
	id := platformHardwareID()
	if !regexp.MustCompile(`^[0-9a-f]{8}(-[0-9a-f]{4}){3}-[0-9a-f]{12}$`).MatchString(id) {
		t.Errorf("MachineGuid: %q", id)
	}
}

func TestWindowsReasons(t *testing.T) {
	if r := cardPlatformReason(config.CardCfg{Type: config.CardTypePN532I2C}); r != "Nur am Raspberry Pi" {
		t.Errorf("PN532: %q", r)
	}
	if r := cardPlatformReason(config.CardCfg{Type: config.CardTypeUSB}); r != "" {
		t.Errorf("USB: %q", r)
	}
	if r := displayPlatformReason(config.StatusDisplayCfg{Type: config.StatusDisplaySH1106}); r != "Statusdisplay nur am Raspberry Pi" {
		t.Errorf("OLED: %q", r)
	}
}
