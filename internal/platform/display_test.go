package platform

import "testing"

func TestDisplaySizeReadsConnectedOutput(t *testing.T) {
	w, h, found := DisplaySize("testdata/sys")
	if !found || w != 1280 || h != 720 {
		// first connected output in sorted order is card0-HDMI-A-1, first mode line
		t.Fatalf("got %dx%d found=%v, want 1280x720 true", w, h, found)
	}
}

func TestDisplaySizeIgnoresDisconnected(t *testing.T) {
	if _, _, found := DisplaySize("testdata/sysdis"); found {
		t.Fatal("disconnected output must not count")
	}
}

func TestDisplaySizeFallback1920x1080(t *testing.T) {
	_, _, found := DisplaySize("testdata/sysempty")
	if found {
		t.Fatal("expected not found")
	}
	if w, h := Resolve(0, 0, 0, 0, found); w != 1920 || h != 1080 {
		t.Fatalf("got %dx%d", w, h)
	}
	if _, _, found := DisplaySize("testdata/does-not-exist"); found {
		t.Fatal("missing root must not be found")
	}
}

func TestResolveOverrideWins(t *testing.T) {
	cases := []struct {
		ow, oh, w, h int
		found        bool
		wantW, wantH int
	}{
		{800, 600, 1280, 720, true, 800, 600},
		{0, 0, 1280, 720, true, 1280, 720},
		{800, 0, 1280, 720, true, 1280, 720},
		{0, 600, 0, 0, false, 1920, 1080},
		{-1, -1, 0, 0, false, 1920, 1080},
	}
	for _, c := range cases {
		if w, h := Resolve(c.ow, c.oh, c.w, c.h, c.found); w != c.wantW || h != c.wantH {
			t.Errorf("Resolve(%d,%d,%d,%d,%v)=%dx%d want %dx%d", c.ow, c.oh, c.w, c.h, c.found, w, h, c.wantW, c.wantH)
		}
	}
}
