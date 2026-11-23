package pdhterminal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

// sentPrevious liest "previous" aus dem letzten Koppel-Anfragetext.
func (p *fakePDH) sentPrevious(t *testing.T) (map[string]string, bool) {
	t.Helper()
	raw, _ := p.gotBody.Load().([]byte)
	var in struct {
		Previous map[string]string `json:"previous"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("Anfragetext: %v", err)
	}
	return in.Previous, in.Previous != nil
}

func oldDevice(base string) *Device {
	return &Device{DeviceID: "alt-1", Secret: "pdht_altaltalt", User: "Berta", URL: base, PairedAt: time.Now()}
}

func TestPairSendsPreviousForSameBase(t *testing.T) {
	p := newFakePDH(t)
	// auch mit Schrägstrich am Ende der Adresse
	if _, _, err := p.client().PairMove(context.Background(), p.hostURL()+"/", "ABCD-1234", "x", oldDevice(p.hostURL())); err != nil {
		t.Fatal(err)
	}
	prev, ok := p.sentPrevious(t)
	if !ok || prev["device_id"] != "alt-1" || prev["secret"] != "pdht_altaltalt" || len(prev) != 2 {
		t.Errorf("previous: %v", prev)
	}
}

func TestPairOmitsPrevious(t *testing.T) {
	p := newFakePDH(t)
	ctx := context.Background()
	if _, _, err := p.client().PairMove(ctx, p.hostURL(), "ABCD-1234", "x", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.sentPrevious(t); ok {
		t.Error("previous ohne gespeichertes Gerät gesendet")
	}
	if _, _, err := p.client().PairMove(ctx, p.hostURL(), "ABCD-1234", "x", oldDevice("https://anders.firma.de")); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.sentPrevious(t); ok {
		t.Error("altes Geheimnis an andere PDH-Adresse gesendet")
	}
	if raw, _ := p.gotBody.Load().([]byte); strings.Contains(string(raw), "altaltalt") {
		t.Error("altes Geheimnis im Anfragetext")
	}
	// Pair ohne Vorgänger bleibt wie bisher
	if _, err := p.client().Pair(ctx, p.hostURL(), "ABCD-1234", "x"); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.sentPrevious(t); ok {
		t.Error("Pair sendet previous")
	}
}

func TestPairMovedFlag(t *testing.T) {
	p := newFakePDH(t)
	_, moved, err := p.client().PairMove(context.Background(), p.hostURL(), "ABCD-1234", "x", oldDevice(p.hostURL()))
	if err != nil || moved {
		t.Fatalf("ohne moved: %v %v", moved, err)
	}
	p.moved.Store(true)
	if _, moved, err = p.client().PairMove(context.Background(), p.hostURL(), "ABCD-1234", "x", oldDevice(p.hostURL())); err != nil || !moved {
		t.Fatalf("mit moved: %v %v", moved, err)
	}
}

func TestAdminPairReplacesAndMoves(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	if err := SaveDevice(e.dir, *oldDevice(p.hostURL())); err != nil {
		t.Fatal(err)
	}
	p.moved.Store(true)
	r := e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	body := r.Body.String()
	if r.Code != http.StatusOK || !strings.Contains(body, "Terminal aus altem Benutzer übernommen") {
		t.Fatalf("Koppeln: %d %s", r.Code, body)
	}
	if prev, ok := p.sentPrevious(t); !ok || prev["device_id"] != "alt-1" {
		t.Errorf("previous: %v", prev)
	}
	got, err := LoadDevice(e.dir)
	if err != nil || got == nil || got.DeviceID != "a1-1" || got.Secret != testSecret {
		t.Errorf("Gerät nicht ersetzt: %+v %v", got, err)
	}
	if strings.Contains(body, "altaltalt") || strings.Contains(body, "pdht_") {
		t.Error("Geheimnis in der Seite")
	}

	// Ohne moved keine Meldung (ältere PDH)
	p.moved.Store(false)
	r = e.post("/admin/pdh/pair", url.Values{"code": {"ABCD-1234"}})
	if strings.Contains(r.Body.String(), "übernommen") {
		t.Error("Meldung ohne moved")
	}
}

func TestAdminPairFailureKeepsOldDevice(t *testing.T) {
	p := newFakePDH(t)
	e := newAdminEnv(t, p.client())
	e.setPDH(config.PDHCfg{URL: p.hostURL()})
	old := oldDevice(p.hostURL())
	if err := SaveDevice(e.dir, *old); err != nil {
		t.Fatal(err)
	}
	r := e.post("/admin/pdh/pair", url.Values{"code": {"FALSCH"}})
	if r.Code == http.StatusOK {
		t.Fatalf("Koppeln müsste scheitern: %s", r.Body.String())
	}
	got, err := LoadDevice(e.dir)
	if err != nil || got == nil || got.DeviceID != old.DeviceID || got.Secret != old.Secret {
		t.Errorf("altes Gerät verändert: %+v %v", got, err)
	}
}
