package config

import (
	"strings"
	"testing"
)

func TestRemoteKeyHashValidated(t *testing.T) {
	ok := []string{"", strings.Repeat("0a", 32)}
	for _, h := range ok {
		c := sample()
		c.Admin.RemoteKeyHash = h
		if err := Validate(c); err != nil {
			t.Errorf("%q abgelehnt: %v", h, err)
		}
	}
	bad := []string{"abc", strings.Repeat("0A", 32), strings.Repeat("0a", 31), strings.Repeat("0a", 33), strings.Repeat("zz", 32), "hmik_" + strings.Repeat("a", 43)}
	for _, h := range bad {
		c := sample()
		c.Admin.RemoteKeyHash = h
		if Validate(c) == nil {
			t.Errorf("%q angenommen", h)
		}
	}
}

func TestExportedNameAndHostRules(t *testing.T) {
	if CheckHost("Host", "10.1.2.3") != nil || CheckHost("Host", "hmi-3.local") != nil {
		t.Error("gültiger Host abgelehnt")
	}
	for _, h := range []string{"", "a b", "x\ny", "-x", "a;b", strings.Repeat("a", 254)} {
		if CheckHost("Host", h) == nil {
			t.Errorf("Host %q angenommen", h)
		}
	}
	if CheckName("Name", "Linie 3 / Presse 2") != nil {
		t.Error("gültiger Name abgelehnt")
	}
	for _, n := range []string{"", "a\nb", strings.Repeat("ä", 121)} {
		if CheckName("Name", n) == nil {
			t.Errorf("Name %q angenommen", n)
		}
	}
}
