package updatecheck

import (
	"strings"
	"testing"

	"hmikiosk/internal/release"
)

func TestSelectByProduct(t *testing.T) {
	asset := func(p release.Product, tag, v string) Asset {
		return Asset{Name: p.PackageName(v), Size: 10, URL: "https://github.com/o/r/releases/download/" + tag + "/" + p.PackageName(v)}
	}
	rels := []Release{
		rel("v0.1.9", asset(release.HMIKiosk, "v0.1.9", "0.1.9")),
		rel("pdh-terminal-v0.3.0", asset(release.PDHTerminal, "pdh-terminal-v0.3.0", "0.3.0")),
		rel("hmi-kiosk-v0.1.10", asset(release.HMIKiosk, "hmi-kiosk-v0.1.10", "0.1.10")),
	}
	o, err := Select(rels, release.HMIKiosk, "0.1.0", false)
	if err != nil || o == nil || o.Version != "0.1.10" || !o.Installable() || !strings.HasSuffix(o.AssetURL, "/hmi-kiosk-0.1.10.pkg") {
		t.Fatalf("HMI: %+v %v", o, err)
	}
	o, err = Select(rels, release.PDHTerminal, "0.1.0", false)
	if err != nil || o == nil || o.Version != "0.3.0" || !o.Installable() || !strings.HasSuffix(o.AssetURL, "/pdh-terminal-0.3.0.pkg") {
		t.Fatalf("PDH: %+v %v", o, err)
	}
	if o, _ := Select(rels[:1], release.PDHTerminal, "0.1.0", false); o != nil {
		t.Fatalf("PDH sieht v-Tag: %+v", o)
	}
	if o, _ := Select(rels[1:2], release.HMIKiosk, "0.1.0", false); o != nil {
		t.Fatalf("HMI sieht PDH-Tag: %+v", o)
	}
	// PDH-Release mit einem Paket nur unter HMI-Namen ist nicht installierbar.
	wrong := rel("pdh-terminal-v0.3.0", asset(release.HMIKiosk, "x", "0.3.0"))
	if o, _ := Select([]Release{wrong}, release.PDHTerminal, "0.1.0", false); o == nil || o.Installable() {
		t.Fatalf("%+v", o)
	}
}
