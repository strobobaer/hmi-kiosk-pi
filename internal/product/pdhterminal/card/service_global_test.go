package card

import (
	"context"
	"strings"
	"testing"
)

// Globales Terminal: Der Leser bleibt aus (Statusdetail), der Wechsel geschieht ohne Neustart.
func TestServiceGlobalSwitchesReaderOffAndOn(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	defer s.Close()
	s.Apply(Settings{Type: "usb"})
	waitFor(t, "aktiv", func() bool { return s.Status().Active })

	s.Apply(Settings{Type: "usb", Global: true})
	st := s.Status()
	if st.Active || st.Detail != "Globales Terminal: Kartenleser aus" || st.State != StateNone {
		t.Fatalf("Status %+v", st)
	}
	if got := strings.Join(l.get(), ","); got != "open usb,close usb" {
		t.Errorf("Leser nicht geschlossen: %s", got)
	}
	if _, err := s.Probe(context.Background()); err == nil {
		t.Error("Probe bei globalem Terminal")
	}

	s.Apply(Settings{Type: "usb"})
	waitFor(t, "wieder an", func() bool { return s.Status().Active })
	if got := strings.Join(l.get(), ","); got != "open usb,close usb,open usb" {
		t.Errorf("Leser nicht wieder geöffnet: %s", got)
	}
}

func TestServiceGlobalFromStartNeverOpens(t *testing.T) {
	l := &opLog{}
	s := newTestService(l, nil)
	defer s.Close()
	s.Apply(Settings{Type: "usb", Global: true})
	if got := l.get(); len(got) != 0 {
		t.Errorf("Leser geöffnet: %v", got)
	}
	if st := s.Status(); st.Detail != GlobalDetail {
		t.Errorf("Status %+v", st)
	}
}
