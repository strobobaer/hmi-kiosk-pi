package notify

import (
	"reflect"
	"strings"
	"testing"
)

func TestKinds(t *testing.T) {
	cases := []struct {
		in, want []string
	}{
		{nil, []string{}},
		{[]string{}, []string{}},
		{[]string{"banner", "overlay"}, []string{"overlay", "banner"}},
		{[]string{"off", "toast"}, []string{}},
		{[]string{"x", "front"}, []string{"front"}},
		{[]string{"front", "toast", "toast", "banner", "overlay"}, []string{"toast", "overlay", "banner", "front"}},
	}
	for _, c := range cases {
		if got := Kinds(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Kinds(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestText(t *testing.T) {
	cases := []struct {
		r          Result
		text, tone string
	}{
		{Result{OK: true, Name: "M. Muster"}, "Angemeldet: M. Muster", "ok"},
		{Result{OK: true, Name: "M. Muster", Logout: true}, "Abgemeldet: M. Muster", "neutral"},
		{Result{Message: "Karte unbekannt"}, "Karte unbekannt", "error"},
		{Result{}, "Anmeldung fehlgeschlagen", "error"},
	}
	for _, c := range cases {
		text, tone := Text(c.r)
		if text != c.text || tone != c.tone {
			t.Errorf("Text(%+v) = %q, %q", c.r, text, tone)
		}
	}
}

func TestClean(t *testing.T) {
	if got := Clean("  M.\x00 Mus\ter‮\n "); got != "M. Muser" {
		t.Errorf("Clean = %q", got)
	}
	if got := Clean(strings.Repeat("ä", 500)); len([]rune(got)) != MaxField {
		t.Errorf("Länge %d", len([]rune(got)))
	}
}
