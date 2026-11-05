package helper

import (
	"strings"
	"testing"
)

func TestSetOverlayAddsOnce(t *testing.T) {
	in := "dtparam=audio=on\n[all]\nenable_uart=1\n"
	once := SetOverlay(in, "disable-wifi", true)
	twice := SetOverlay(once, "disable-wifi", true)
	if once != twice {
		t.Fatalf("nicht idempotent:\n%q\n%q", once, twice)
	}
	if n := strings.Count(twice, "dtoverlay=disable-wifi"); n != 1 {
		t.Fatalf("Zeilen: %d", n)
	}
	want := "dtparam=audio=on\n[all]\nenable_uart=1\ndtoverlay=disable-wifi\n"
	if once != want {
		t.Fatalf("got %q want %q", once, want)
	}
}

func TestSetOverlayRemovesOnlyOwnLine(t *testing.T) {
	in := "[all]\ndtoverlay=vc4-kms-v3d\ndtoverlay=disable-wifi\ndtoverlay=disable-bt\n"
	got := SetOverlay(in, "disable-wifi", false)
	want := "[all]\ndtoverlay=vc4-kms-v3d\ndtoverlay=disable-bt\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if again := SetOverlay(got, "disable-wifi", false); again != got {
		t.Fatalf("Entfernen nicht idempotent: %q", again)
	}
}

func TestSetOverlayAppendsAllSectionWhenMissing(t *testing.T) {
	got := SetOverlay("[pi4]\nfoo=1\n", "disable-bt", true)
	want := "[pi4]\nfoo=1\n\n[all]\ndtoverlay=disable-bt\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if got := SetOverlay("", "disable-bt", true); got != "[all]\ndtoverlay=disable-bt\n" {
		t.Fatalf("leer: %q", got)
	}
}

func TestSetOverlayInsertsBeforeNextSection(t *testing.T) {
	in := "[all]\na=1\n\n[pi5]\nb=2\n"
	got := SetOverlay(in, "disable-wifi", true)
	want := "[all]\na=1\ndtoverlay=disable-wifi\n\n[pi5]\nb=2\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSetOverlayIgnoresOtherSectionsAndComments(t *testing.T) {
	in := "[pi5]\ndtoverlay=disable-wifi\n[all]\n#dtoverlay=disable-wifi\n"
	if got := SetOverlay(in, "disable-wifi", false); got != in {
		t.Fatalf("Entfernen berührte Fremdes: %q", got)
	}
	got := SetOverlay(in, "disable-wifi", true)
	want := in + "dtoverlay=disable-wifi\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSetOverlayNormalizesCRLF(t *testing.T) {
	got := SetOverlay("[all]\r\nx=1\r\n", "disable-bt", true)
	if strings.Contains(got, "\r") {
		t.Fatalf("CR übrig: %q", got)
	}
}

func TestSetOverlayWithParams(t *testing.T) {
	got := SetOverlay("[all]\ndtoverlay=disable-wifi,foo\n", "disable-wifi", false)
	if got != "[all]\n" {
		t.Fatalf("got %q", got)
	}
}
