package config

import "testing"

func TestValidNTPServer(t *testing.T) {
	for _, ok := range []string{"pool.ntp.org", "10.0.0.1", "ntp", "2001:db8::1"} {
		if !ValidNTPServer(ok) {
			t.Errorf("%q sollte gültig sein", ok)
		}
	}
	for _, bad := range []string{"", "-x", "a b", "a;b", "fe80::1%eth0", "a\nb", ".x", "x."} {
		if ValidNTPServer(bad) {
			t.Errorf("%q sollte ungültig sein", bad)
		}
	}
}
