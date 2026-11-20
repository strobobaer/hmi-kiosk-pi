package release

import "testing"

func TestTagVersion(t *testing.T) {
	for _, c := range []struct {
		p    Product
		tag  string
		want string
		ok   bool
	}{
		{HMIKiosk, "v0.1.9", "0.1.9", true},
		{PDHTerminal, "v0.1.9", "", false},
		{HMIKiosk, "hmi-kiosk-v0.1.9", "0.1.9", true},
		{PDHTerminal, "hmi-kiosk-v0.1.9", "", false},
		{PDHTerminal, "pdh-terminal-v0.2.0", "0.2.0", true},
		{HMIKiosk, "pdh-terminal-v0.2.0", "", false},
		{PDHTerminal, "pdh-terminal-0.2.0", "", false},
		{HMIKiosk, "hmi-kiosk-0.1.9", "", false},
		{HMIKiosk, "v0.1", "", false},
		{PDHTerminal, "pdh-terminal-v0.2.0-rc1", "", false},
		{HMIKiosk, "nightly", "", false},
	} {
		got, ok := c.p.TagVersion(c.tag)
		if got != c.want || ok != c.ok {
			t.Errorf("%s.TagVersion(%q) = %q, %v", c.p.ID, c.tag, got, ok)
		}
	}
}

func TestProducts(t *testing.T) {
	if HMIKiosk.SystemLevel != SystemLevel || !HMIKiosk.LegacyTags || PDHTerminal.LegacyTags {
		t.Fatalf("%+v %+v", HMIKiosk, PDHTerminal)
	}
	for _, p := range []Product{HMIKiosk, PDHTerminal} {
		got, ok := ProductByID(p.ID)
		if !ok || got != p {
			t.Errorf("ProductByID(%q) = %+v, %v", p.ID, got, ok)
		}
	}
	if _, ok := ProductByID("evil"); ok {
		t.Error("unbekannte Kennung angenommen")
	}
	if _, ok := ProductByID(""); ok {
		t.Error("leere Kennung angenommen")
	}
	if HMIKiosk.PackageName("0.1.9") != "hmi-kiosk-0.1.9.pkg" || PDHTerminal.PackageName("0.2.0") != "pdh-terminal-0.2.0.pkg" {
		t.Error("Paketname")
	}
}

func TestPackageNameFor(t *testing.T) {
	if got := PDHTerminal.PackageNameFor("1.2.3", ""); got != "pdh-terminal-1.2.3.pkg" {
		t.Errorf("Linux: %q", got)
	}
	if got := PDHTerminal.PackageNameFor("1.2.3", PlatformWindowsAMD64); got != "pdh-terminal-1.2.3-windows-amd64.pkg" {
		t.Errorf("Windows: %q", got)
	}
	if PDHTerminal.PackageName("1.2.3") != PDHTerminal.PackageNameFor("1.2.3", "") {
		t.Error("PackageName weicht ab")
	}
}

func TestPlatformFor(t *testing.T) {
	for _, c := range []struct{ goos, goarch, want string }{
		{"windows", "amd64", "windows-amd64"},
		{"linux", "arm64", ""},
		{"linux", "amd64", ""},
		{"windows", "arm64", ""}, // kein Paket dafür
	} {
		if got := PlatformFor(c.goos, c.goarch); got != c.want {
			t.Errorf("%s/%s: %q", c.goos, c.goarch, got)
		}
	}
	if PlatformWindowsAMD64 != "windows-amd64" {
		t.Error("Konstante")
	}
	_ = CurrentPlatform()
}

func TestBinaryFor(t *testing.T) {
	if PDHTerminal.BinaryFor("") != "pdh-terminal" || PDHTerminal.BinaryFor(PlatformWindowsAMD64) != "pdh-terminal.exe" {
		t.Error("Programmname je Plattform")
	}
}
