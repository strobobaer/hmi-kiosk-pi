package updatecheck

import (
	"errors"
	"strings"
	"testing"
)

func rel(tag string, assets ...Asset) Release {
	return Release{TagName: tag, HTMLURL: "https://github.com/o/r/releases/tag/" + tag, Body: "Notiz " + tag, Assets: assets}
}

func pkg(v string, size int64) Asset {
	return Asset{Name: PackageName(v), Size: size, URL: "https://github.com/o/r/releases/download/v" + v + "/" + PackageName(v)}
}

func TestNewerComparesNumerically(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.1.10", "0.1.9", true},
		{"0.1.9", "0.1.10", false},
		{"1.0.0", "0.99.99", true},
		{"0.2.0", "0.2.0", false},
		{"0.0010.0", "0.9.0", true},
		{"00.1.0", "0.1.0", false},
		{"99999999999999999999.0.0", "1.0.0", true},
	} {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

func TestSelectPicksHighestNewerRelease(t *testing.T) {
	rels := []Release{
		rel("v0.1.9", pkg("0.1.9", 100)),
		rel("0.1.10", pkg("0.1.10", 100)),
		rel("v0.1.8", pkg("0.1.8", 100)),
		rel("nightly"),
		rel("v1.2"),
	}
	o, err := Select(rels, "0.1.8", false)
	if err != nil || o == nil {
		t.Fatalf("%+v %v", o, err)
	}
	if o.Version != "0.1.10" || !o.Installable() || o.AssetURL != rels[1].Assets[0].URL || o.AssetSize != 100 {
		t.Errorf("%+v", o)
	}
	if o.PageURL != rels[1].HTMLURL || o.Notes != "Notiz 0.1.10" {
		t.Errorf("%+v", o)
	}
	if o, err := Select(rels, "0.1.10", false); err != nil || o != nil {
		t.Errorf("nichts Neueres erwartet: %+v %v", o, err)
	}
	if o, err := Select(rels, "0.2.0", false); err != nil || o != nil {
		t.Errorf("ältere angeboten: %+v %v", o, err)
	}
}

func TestSelectSkipsDraftsAndPrereleasesUnlessWanted(t *testing.T) {
	draft := rel("v0.3.0", pkg("0.3.0", 1))
	draft.Draft = true
	pre := rel("v0.2.0", pkg("0.2.0", 1))
	pre.Prerelease = true
	rels := []Release{draft, pre, rel("v0.1.5", pkg("0.1.5", 1))}

	o, err := Select(rels, "0.1.0", false)
	if err != nil || o == nil || o.Version != "0.1.5" {
		t.Fatalf("ohne Vorabversionen: %+v %v", o, err)
	}
	o, err = Select(rels, "0.1.0", true)
	if err != nil || o == nil || o.Version != "0.2.0" || !o.Prerelease {
		t.Fatalf("mit Vorabversionen: %+v %v", o, err)
	}
}

func TestSelectWithoutPackageOffersNoInstall(t *testing.T) {
	other := Asset{Name: "hmi-kiosk-0.2.0-bundle.tar.gz", Size: 5, URL: "https://x/y"}
	o, err := Select([]Release{rel("v0.2.0", other)}, "0.1.0", false)
	if err != nil || o == nil {
		t.Fatalf("%+v %v", o, err)
	}
	if o.Installable() || o.AssetURL != "" || !strings.Contains(o.NoPackage, "Abbild") || o.PageURL == "" {
		t.Errorf("%+v", o)
	}
}

func TestSelectRejectsOversizedOrInsecurePackage(t *testing.T) {
	big := pkg("0.2.0", MaxPackageBytes+1)
	o, err := Select([]Release{rel("v0.2.0", big)}, "0.1.0", false)
	if err != nil || o == nil || o.Installable() || !strings.Contains(o.NoPackage, "200 MB") {
		t.Errorf("zu groß: %+v %v", o, err)
	}
	plain := pkg("0.2.0", 10)
	plain.URL = "http://github.com/x"
	o, err = Select([]Release{rel("v0.2.0", plain)}, "0.1.0", false)
	if err != nil || o == nil || o.Installable() {
		t.Errorf("http: %+v %v", o, err)
	}
	r := rel("v0.2.0", pkg("0.2.0", 10))
	r.HTMLURL = "javascript:alert(1)"
	o, _ = Select([]Release{r}, "0.1.0", false)
	if o == nil || o.PageURL != "" {
		t.Errorf("Seitenadresse: %+v", o)
	}
}

func TestSelectDevVersionOffersNothing(t *testing.T) {
	_, err := Select([]Release{rel("v0.2.0", pkg("0.2.0", 1))}, "dev", false)
	if !errors.Is(err, ErrNoRelease) {
		t.Errorf("%v", err)
	}
}

func TestNotesArePlainAndShortened(t *testing.T) {
	r := rel("v0.2.0", pkg("0.2.0", 1))
	r.Body = "Zeile 1\r\nZeile\x1b[31m 2\n" + strings.Repeat("ä", 2000)
	o, _ := Select([]Release{r}, "0.1.0", false)
	if o == nil {
		t.Fatal("kein Angebot")
	}
	if strings.ContainsAny(o.Notes, "\r\x1b") || !strings.HasPrefix(o.Notes, "Zeile 1\nZeile[31m 2\n") {
		t.Errorf("%q", o.Notes[:30])
	}
	if n := len([]rune(o.Notes)); n > maxNotesRunes+1 || !strings.HasSuffix(o.Notes, "…") {
		t.Errorf("%d Zeichen", n)
	}
}

func TestSelectIgnoresNonCanonicalTags(t *testing.T) {
	rels := []Release{rel("v0.10.01", pkg("0.10.01", 1)), rel("v00.11.0", pkg("00.11.0", 1)), rel("v0.10.0", pkg("0.10.0", 1))}
	o, err := Select(rels, "0.9.0", false)
	if err != nil || o == nil || o.Version != "0.10.0" {
		t.Errorf("%+v %v", o, err)
	}
	if o, _ := Select(rels, "0.10.0", false); o != nil {
		t.Errorf("Tag mit führender Null angeboten: %+v", o)
	}
}

func TestNotesDropFormatCharacters(t *testing.T) {
	r := rel("v0.2.0", pkg("0.2.0", 1))
	r.Body = "a" + string(rune(0x202e)) + "b" + string(rune(0x2066)) + "c" + string(rune(0x200b)) + "d" + string(rune(0xfeff)) + "e"
	o, _ := Select([]Release{r}, "0.1.0", false)
	if o == nil || o.Notes != "abcde" {
		t.Errorf("%+q", o.Notes)
	}
}
