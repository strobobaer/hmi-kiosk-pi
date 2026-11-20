package winstart

import (
	"slices"
	"testing"
)

func TestParseArgs(t *testing.T) {
	def := Paths{Base: "B", Data: "D", Run: "R"}
	for _, c := range []struct {
		in       []string
		want     Paths
		wantPass []string
	}{
		{nil, def, nil},
		{[]string{"-base", "X", "-data", "Y", "-run=Z", "-no-chromium"}, Paths{"X", "Y", "Z"}, []string{"-data", "Y", "-run=Z", "-no-chromium"}},
		{[]string{"--base=X", "-local", "127.0.0.1:1"}, Paths{"X", "D", "R"}, []string{"-local", "127.0.0.1:1"}},
		{[]string{"-run"}, def, []string{"-run"}},
	} {
		got, pass := ParseArgs(c.in, def)
		if got != c.want || !slices.Equal(pass, c.wantPass) {
			t.Errorf("ParseArgs(%q) = %+v %q, erwartet %+v %q", c.in, got, pass, c.want, c.wantPass)
		}
	}
}
