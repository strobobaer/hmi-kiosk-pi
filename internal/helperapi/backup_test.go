package helperapi

import (
	"strings"
	"testing"
	"time"
)

func TestBackupFileName(t *testing.T) {
	ts := time.Date(2026, 11, 5, 9, 7, 0, 0, time.UTC)
	cases := map[string]string{
		"Presse 1":    "hmikiosk-Presse-1-2026-11-05-0907.img.gz",
		"Halle_2-Ost": "hmikiosk-Halle_2-Ost-2026-11-05-0907.img.gz",
		"Größe/Ä":     "hmikiosk-Groesse-Ae-2026-11-05-0907.img.gz",
		"  ../..  ":   "hmikiosk-terminal-2026-11-05-0907.img.gz",
		"":            "hmikiosk-terminal-2026-11-05-0907.img.gz",
		"a    b":      "hmikiosk-a-b-2026-11-05-0907.img.gz",
		"日本":          "hmikiosk-terminal-2026-11-05-0907.img.gz",
	}
	for in, want := range cases {
		if got := BackupFileName(in, ts); got != want {
			t.Errorf("%q: %q, erwartet %q", in, got, want)
		}
	}
	long := BackupFileName(strings.Repeat("x", 80), ts)
	if want := "hmikiosk-" + strings.Repeat("x", MaxBackupNameLen) + "-2026-11-05-0907.img.gz"; long != want {
		t.Errorf("lang: %q", long)
	}
}

func TestSanitizeBackupName(t *testing.T) {
	if got := SanitizeBackupName("Presse 1"); got != "Presse-1" {
		t.Errorf("%q", got)
	}
	if got := SanitizeBackupName("?"); got != "terminal" {
		t.Errorf("%q", got)
	}
}
