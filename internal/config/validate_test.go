package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	if err := Validate(Default()); err != nil {
		t.Fatal(err)
	}
}

func TestInputModeDefaultsToTouchAndIsChecked(t *testing.T) {
	if Default().Kiosk.InputMode != InputTouch {
		t.Fatalf("Voreinstellung %q", Default().Kiosk.InputMode)
	}
	for _, m := range []string{"", InputTouch, InputMouse} {
		c := sample()
		c.Kiosk.InputMode = m
		if err := Validate(c); err != nil {
			t.Errorf("%q abgelehnt: %v", m, err)
		}
	}
	c := sample()
	c.Kiosk.InputMode = "stift"
	if Validate(c) == nil {
		t.Error("unbekannte Bedienart angenommen")
	}
}

func TestValidateRejectsControlCharsInNameAndHost(t *testing.T) {
	for _, name := range []string{"a\nb", "x\r", "t\x00"} {
		c := sample()
		c.HMIs[0].Name = name
		if Validate(c) == nil {
			t.Errorf("HMI name %q accepted", name)
		}
		c = sample()
		c.Terminal.Name = name
		if Validate(c) == nil {
			t.Errorf("terminal name %q accepted", name)
		}
	}
	for _, host := range []string{"-oProxyCommand=x", "-h", "a;b", "a=b", "a b", "a\nb", ""} {
		c := sample()
		c.HMIs[0].Host = host
		if Validate(c) == nil {
			t.Errorf("host %q accepted", host)
		}
	}
	c := sample()
	c.HMIs[0].Host = "[fe80::1]"
	if err := Validate(c); err != nil {
		t.Errorf("ipv6 host rejected: %v", err)
	}
}

func TestValidateRejectsDuplicateHMIIDsAndBadPort(t *testing.T) {
	c := sample()
	c.HMIs = append(c.HMIs, c.HMIs[0])
	if Validate(c) == nil {
		t.Error("duplicate ids accepted")
	}
	for _, p := range []int{0, -1, 65536} {
		c = sample()
		c.HMIs[0].Port = p
		if Validate(c) == nil {
			t.Errorf("port %d accepted", p)
		}
	}
}

func TestValidateRestrictsHMIIDs(t *testing.T) {
	for _, id := range []string{"new", "a/b", "../x", "a b", "a.b", "ä", "a?b", "a%2Fb", strings.Repeat("a", 65)} {
		c := sample()
		c.Kiosk.PermanentHMI = ""
		c.HMIs[0].ID = id
		if Validate(c) == nil {
			t.Errorf("ID %q accepted", id)
		}
	}
	for _, id := range []string{"a", "New", "h1", "A-b_9", "0123456789abcdef", strings.Repeat("a", 64)} {
		c := sample()
		c.Kiosk.PermanentHMI = ""
		c.HMIs[0].ID = id
		if err := Validate(c); err != nil {
			t.Errorf("ID %q rejected: %v", id, err)
		}
	}
}

func TestValidateRejectsPeerEqualToSelfName(t *testing.T) {
	c := sample()
	c.Peers = []Peer{{Name: c.Terminal.Name, Address: "10.0.0.9"}}
	if Validate(c) == nil {
		t.Error("peer equal to self accepted")
	}
	c.Peers = []Peer{{Name: "Halle 2", Address: "10.0.0.9"}}
	if err := Validate(c); err != nil {
		t.Error(err)
	}
}

func TestValidateRejectsPermanentHMIUnknown(t *testing.T) {
	c := sample()
	c.Kiosk.PermanentHMI = "gibtsnicht"
	if Validate(c) == nil {
		t.Error("unknown permanent HMI accepted")
	}
	c.Kiosk.PermanentHMI = ""
	if err := Validate(c); err != nil {
		t.Error(err)
	}
}

func TestValidateRejectsClientValuesOutsideTheClosedSets(t *testing.T) {
	for _, d := range []int{0, 24, 1} {
		c := sample()
		c.Client.ColorDepth = d
		if Validate(c) == nil {
			t.Errorf("color depth %d accepted", d)
		}
	}
	for _, q := range []int{-1, 3, 8, 10} {
		c := sample()
		c.Client.Quality = q
		if Validate(c) == nil {
			t.Errorf("quality %d accepted", q)
		}
	}
	for _, p := range []string{"", "vinagre", "Remmina"} {
		c := sample()
		c.Client.Profile = p
		if Validate(c) == nil {
			t.Errorf("profile %q accepted", p)
		}
	}
	for _, d := range []int{8, 16, 32} {
		for _, q := range []int{0, 1, 2, 9} {
			for _, p := range []string{"remmina", "tigervnc"} {
				c := sample()
				c.Client.ColorDepth, c.Client.Quality, c.Client.Profile = d, q, p
				if err := Validate(c); err != nil {
					t.Errorf("depth %d quality %d profile %s rejected: %v", d, q, p, err)
				}
			}
		}
	}
}

func TestValidateRejectsNegativeInactivity(t *testing.T) {
	c := sample()
	c.Kiosk.InactivityMinutes = -1
	if Validate(c) == nil {
		t.Error("negative inactivity accepted")
	}
	c.Kiosk.InactivityMinutes = 0
	if err := Validate(c); err != nil {
		t.Error(err)
	}
}

func TestValidateDisplayOverrideIsAPair(t *testing.T) {
	for _, wh := range [][2]int{{1280, 0}, {0, 800}, {-1, 800}, {1280, -5}, {-1, -1}} {
		c := sample()
		c.Display.OverrideW, c.Display.OverrideH = wh[0], wh[1]
		if Validate(c) == nil {
			t.Errorf("override %v accepted", wh)
		}
	}
	for _, wh := range [][2]int{{0, 0}, {1280, 800}} {
		c := sample()
		c.Display.OverrideW, c.Display.OverrideH = wh[0], wh[1]
		if err := Validate(c); err != nil {
			t.Errorf("override %v rejected: %v", wh, err)
		}
	}
}

func TestValidateRestrictsIconAndColor(t *testing.T) {
	for _, icon := range []string{"skull", "Monitor", "<svg>"} {
		c := sample()
		c.HMIs[0].Icon = icon
		if Validate(c) == nil {
			t.Errorf("icon %q accepted", icon)
		}
	}
	for _, color := range []string{"pink", "Blue", "#fff"} {
		c := sample()
		c.HMIs[0].Color = color
		if Validate(c) == nil {
			t.Errorf("color %q accepted", color)
		}
	}
	for _, icon := range append([]string{""}, HMIIcons...) {
		c := sample()
		c.HMIs[0].Icon = icon
		if err := Validate(c); err != nil {
			t.Errorf("icon %q rejected: %v", icon, err)
		}
	}
	for _, color := range append([]string{""}, HMIColors...) {
		c := sample()
		c.HMIs[0].Color = color
		if err := Validate(c); err != nil {
			t.Errorf("color %q rejected: %v", color, err)
		}
	}
}

func TestValidateRejectsDuplicatePeerNames(t *testing.T) {
	c := sample()
	c.Peers = []Peer{{Name: "Halle 2", Address: "10.0.0.9"}, {Name: "Halle 2", Address: "10.0.0.10"}}
	if Validate(c) == nil {
		t.Error("duplicate peer names accepted")
	}
}

func TestValidateRejectsUnknownSchemaVersion(t *testing.T) {
	for _, v := range []int{0, SchemaVersion + 1, -1} {
		c := sample()
		c.SchemaVersion = v
		if Validate(c) == nil {
			t.Errorf("schema version %d accepted", v)
		}
	}
}

func TestValidateRejectsHideBarWithoutPermanentHMI(t *testing.T) {
	c := sample()
	c.Kiosk.PermanentHMI = ""
	c.Kiosk.HideBar = true
	err := Validate(c)
	if err == nil || !strings.Contains(err.Error(), "Daueranzeige") {
		t.Fatalf("hide bar without permanent HMI: %v", err)
	}
	c.Kiosk.PermanentHMI = "h1"
	if err := Validate(c); err != nil {
		t.Error(err)
	}
}

func TestReachModeIsCheckedAndDefaultsToPing(t *testing.T) {
	for _, m := range []string{"", ReachPing, ReachPort, ReachOff} {
		c := sample()
		c.HMIs[0].Reach = m
		if err := Validate(c); err != nil {
			t.Errorf("%q abgelehnt: %v", m, err)
		}
	}
	c := sample()
	c.HMIs[0].Reach = "tcp"
	if err := Validate(c); err == nil || !strings.Contains(err.Error(), "Erreichbarkeit") {
		t.Errorf("unbekannte Prüfart angenommen: %v", err)
	}
	for in, want := range map[string]string{"": ReachPing, "ping": ReachPing, "port": ReachPort, "off": ReachOff} {
		if got := (HMI{Reach: in}).ReachMode(); got != want {
			t.Errorf("ReachMode(%q) = %q, erwartet %q", in, got, want)
		}
	}
}
