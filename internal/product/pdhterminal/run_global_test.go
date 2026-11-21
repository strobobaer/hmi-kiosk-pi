package pdhterminal

import (
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/product/pdhterminal/card"
)

// TestGlobalTerminalSwitchesReader: Mit Zwischenspeicher ist der Kartenleser aus (Statusdetail),
// ohne wieder gemäß Konfiguration; der Wechsel braucht keinen Neustart (applyCards).
func TestGlobalTerminalSwitchesReader(t *testing.T) {
	s, err := newService(Options{DataDir: t.TempDir(), RunDir: t.TempDir(), HardwareID: func() string { return "test-0001" }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	if cs := s.cardSettings(config.CardCfg{Type: "usb"}); cs.Global {
		t.Fatal("ohne Zwischenspeicher global")
	}
	s.applyCards()
	if d := s.cards.Status().Detail; d == card.GlobalDetail {
		t.Fatalf("Detail %q ohne Zwischenspeicher", d)
	}
	if err := s.slides.Save([]byte(`{"version":"v","slides":[]}`)); err != nil {
		t.Fatal(err)
	}
	if cs := s.cardSettings(config.CardCfg{Type: "usb"}); !cs.Global {
		t.Fatal("mit Zwischenspeicher nicht global")
	}
	s.applyCards()
	if st := s.cards.Status(); st.Active || st.Detail != card.GlobalDetail {
		t.Fatalf("Status %+v", st)
	}
	if err := s.slides.Clear(); err != nil {
		t.Fatal(err)
	}
	s.applyCards()
	if d := s.cards.Status().Detail; d == card.GlobalDetail {
		t.Fatalf("Detail %q nach Löschen", d)
	}
}
