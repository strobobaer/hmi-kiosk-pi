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
