package winstart

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
)

func TestChooseHighestValid(t *testing.T) {
	base := Candidate{Version: "0.4.0", Exe: "base", Base: true}
	ok := func(Candidate) error { return nil }
	fail6 := func(c Candidate) error {
		if c.Version == "0.6.0" {
			return errors.New("Signatur ungültig")
		}
		return nil
	}
	cands := []Candidate{
		base,
		{Version: "0.4.2"},
		{Version: "0.5.0", Bad: true},
		{Version: "0.6.0"},
	}
	got, err := Choose(cands, fail6)
	if err != nil || got.Version != "0.4.2" {
		t.Fatalf("Wahl %+v %v, erwartet 0.4.2", got, err)
	}
	if got, err = Choose([]Candidate{base}, ok); err != nil || !got.Base {
		t.Fatalf("nur Grundversion: %+v %v", got, err)
	}
	if got, err = Choose([]Candidate{base, {Version: "0.3.0"}}, ok); err != nil || !got.Base {
		t.Fatalf("Ordner unter der Grundversion: %+v %v", got, err)
	}
	// Numerisch, nicht als Text: 0.10.0 > 0.9.0.
	if got, err = Choose([]Candidate{base, {Version: "0.9.0"}, {Version: "0.10.0"}}, ok); err != nil || got.Version != "0.10.0" {
		t.Fatalf("numerischer Vergleich: %+v %v", got, err)
	}
	// Gleiche Version: die Grundversion gewinnt und wird nicht geprüft.
	called := false
	if got, err = Choose([]Candidate{{Version: "0.4.0"}, base}, func(Candidate) error { called = true; return nil }); err != nil || !got.Base || called {
		t.Fatalf("Gleichstand: %+v %v geprüft=%v", got, err, called)
	}
	// Alles schlecht und keine Grundversion: Fehler.
	if _, err = Choose([]Candidate{{Version: "1.0.0", Bad: true}}, ok); err == nil {
		t.Fatal("kein Kandidat sollte einen Fehler liefern")
	}
}

// Die Grundversion wird auch dann nicht geprüft, wenn verify sie ablehnen würde.
func TestChooseBaseNotVerified(t *testing.T) {
	got, err := Choose([]Candidate{{Version: "0.1.0", Base: true}}, func(Candidate) error { return errors.New("nein") })
	if err != nil || !got.Base {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"1.2.3", "1.2.3", 0}, {"1.2.4", "1.2.3", 1}, {"1.10.0", "1.9.9", 1}, {"0.0.2", "0.0.10", -1},
		{"01.2.3", "1.2.3", 0}, {"x", "1.0.0", -1}, {"1.0.0", "x", 1},
	} {
		got := CompareVersions(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("CompareVersions(%q,%q) = %d, erwartet Vorzeichen von %d", c.a, c.b, got, c.want)
		}
	}
}

// fixture packt ein Windows-Paket mit dem Inhalt exe und legt es wie update.Install unter app\<version> ab.
type fixture struct {
	pub  ed25519.PublicKey
	priv ed25519.PrivateKey
	root string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{pub: pub, priv: priv, root: t.TempDir()}
}

func (f *fixture) pack(t *testing.T, version, platform, exe string) []byte {
	t.Helper()
	dir := t.TempDir()
	name := release.PDHTerminal.BinaryFor(platform)
	if err := os.WriteFile(filepath.Join(dir, name), []byte(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	blob, err := update.PackFor(dir, version, release.PDHTerminal, platform, 1, f.priv)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// install legt Version wie der Installer unter Windows ab und liefert den Kandidaten.
func (f *fixture) install(t *testing.T, version string) Candidate {
	t.Helper()
	blob := f.pack(t, version, release.PlatformWindowsAMD64, "exe-"+version)
	p, err := update.Verify(blob, f.pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := update.Install(f.root, p); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(f.root, "app", version)
	return Candidate{Version: version, Exe: filepath.Join(dir, ExeName), Pkg: filepath.Join(dir, PackageName)}
}

func TestVerifyCandidate(t *testing.T) {
	f := newFixture(t)
	good := f.install(t, "0.0.2")
	if err := VerifyCandidate(good, f.pub); err != nil {
		t.Fatalf("gültig: %v", err)
	}

	// Exe ausgetauscht.
	swapped := f.install(t, "0.0.3")
	if err := os.WriteFile(swapped.Exe, []byte("fremde Exe"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCandidate(swapped, f.pub); err == nil || !strings.Contains(err.Error(), "Prüfsumme") {
		t.Errorf("ausgetauschte Exe: %v", err)
	}

	// Linux-Paket im Windows-Ordner.
	lin := f.install(t, "0.0.4")
	if err := os.WriteFile(lin.Pkg, f.pack(t, "0.0.4", "", "exe-0.0.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCandidate(lin, f.pub); err == nil || !strings.Contains(err.Error(), "Betriebssystem") {
		t.Errorf("Linux-Paket: %v", err)
	}

	// Ordnername ≠ Version.
	renamed := f.install(t, "0.0.5")
	other := filepath.Join(f.root, "app", "0.0.6")
	if err := os.Rename(filepath.Dir(renamed.Exe), other); err != nil {
		t.Fatal(err)
	}
	c := Candidate{Version: "0.0.6", Exe: filepath.Join(other, ExeName), Pkg: filepath.Join(other, PackageName)}
	if err := VerifyCandidate(c, f.pub); err == nil || !strings.Contains(err.Error(), "Ordner") {
		t.Errorf("Ordnername: %v", err)
	}
	// Auch wenn die Kandidatenangabe selbst zum Paket passt, zählt der Ordnername.
	c.Version = "0.0.5"
	if err := VerifyCandidate(c, f.pub); err == nil {
		t.Error("Ordnername 0.0.6 mit Version 0.0.5 muss scheitern")
	}

	// Fehlendes package.pkg.
	nopkg := f.install(t, "0.0.7")
	if err := os.Remove(nopkg.Pkg); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCandidate(nopkg, f.pub); err == nil {
		t.Error("fehlendes Paket muss scheitern")
	}

	// Fehlende Exe, falscher Schlüssel, beschädigtes Paket.
	noexe := f.install(t, "0.0.8")
	os.Remove(noexe.Exe)
	if err := VerifyCandidate(noexe, f.pub); err == nil {
		t.Error("fehlende Exe muss scheitern")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyCandidate(good, otherPub); err == nil || !strings.Contains(err.Error(), "Signatur") {
		t.Errorf("fremder Schlüssel: %v", err)
	}
	if err := VerifyCandidate(good, nil); err == nil {
		t.Error("ohne Schlüssel darf nichts gelten")
	}
}

// Ein Paket mit gültiger Signatur, aber einer anderen Version als der Ordner (altes Paket in neuen Ordner kopiert).
func TestVerifyCandidateVersionMismatch(t *testing.T) {
	f := newFixture(t)
	a := f.install(t, "0.0.2")
	b := f.install(t, "0.0.3")
	blob, _ := os.ReadFile(a.Pkg)
	if err := os.WriteFile(b.Pkg, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCandidate(b, f.pub); err == nil {
		t.Error("Paket 0.0.2 im Ordner 0.0.3 muss scheitern")
	}
}

// Manipulierter oder halber Versionsordner: übersprungen, die nächstniedrigere gültige Version startet (Review-Fokus 5).
func TestChooseSkipsTamperedAndHalfWritten(t *testing.T) {
	f := newFixture(t)
	f.install(t, "0.0.2")
	tampered := f.install(t, "0.0.3")
	os.WriteFile(tampered.Exe, []byte("Trojaner"), 0o755)
	half := f.install(t, "0.0.4")
	os.Remove(half.Pkg)
	f.install(t, "0.0.5")
	os.WriteFile(filepath.Join(f.root, "app", "0.0.5", update.BadMarker), nil, 0o644)
	// Ordner ohne Paket und ohne Exe, den jemand von Hand angelegt hat.
	os.MkdirAll(filepath.Join(f.root, "app", "0.0.6"), 0o755)

	base := filepath.Join(t.TempDir(), "base")
	os.MkdirAll(base, 0o755)
	os.WriteFile(filepath.Join(base, ExeName), []byte("Grundversion"), 0o755)
	cands := Discover(base, "0.0.1", filepath.Join(f.root, "app"))
	got, err := Choose(cands, func(c Candidate) error { return VerifyCandidate(c, f.pub) })
	if err != nil || got.Version != "0.0.2" {
		t.Fatalf("Wahl %+v %v, erwartet 0.0.2", got, err)
	}
	// Ohne gültigen Ordner startet die Grundversion.
	os.RemoveAll(filepath.Join(f.root, "app", "0.0.2"))
	got, err = Choose(Discover(base, "0.0.1", filepath.Join(f.root, "app")), func(c Candidate) error { return VerifyCandidate(c, f.pub) })
	if err != nil || !got.Base {
		t.Fatalf("Wahl %+v %v, erwartet Grundversion", got, err)
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "pf")
	app := filepath.Join(root, "app")
	os.MkdirAll(filepath.Join(app, "1.0.0"), 0o755)
	os.MkdirAll(filepath.Join(app, "1.1.0"), 0o755)
	os.WriteFile(filepath.Join(app, "1.1.0", update.BadMarker), nil, 0o644)
	os.MkdirAll(filepath.Join(app, ".tmp-1.2.0"), 0o755)
	os.MkdirAll(filepath.Join(app, "kaputt"), 0o755)
	os.WriteFile(filepath.Join(app, "1.3.0"), nil, 0o644) // Datei statt Ordner
	cands := Discover(base, "0.9.0", app)
	if len(cands) != 2 { // keine Grundversion: base\pdh-terminal.exe fehlt
		t.Fatalf("Kandidaten %+v", cands)
	}
	bad := map[string]bool{}
	for _, c := range cands {
		bad[c.Version] = c.Bad
	}
	if bad["1.0.0"] || !bad["1.1.0"] {
		t.Errorf("Bad-Markierung %v", bad)
	}
	os.MkdirAll(base, 0o755)
	os.WriteFile(filepath.Join(base, ExeName), []byte("x"), 0o755)
	cands = Discover(base, "0.9.0", app)
	if len(cands) != 3 {
		t.Fatalf("Kandidaten mit Grundversion %+v", cands)
	}
}

func TestCleanup(t *testing.T) {
	app := filepath.Join(t.TempDir(), "app")
	for _, v := range []string{"0.3.0", "0.4.0", "0.4.1", "0.4.5", "0.4.6", "0.5.0", "0.6.0", ".tmp-0.7.0"} {
		os.MkdirAll(filepath.Join(app, v), 0o755)
	}
	os.WriteFile(filepath.Join(app, "0.4.6", update.BadMarker), nil, 0o644)
	os.WriteFile(filepath.Join(app, "datei"), nil, 0o644)
	// Grundversion 0.4.0, laufend 0.5.0: bleibt 0.5.0 und die nächstniedrigere gültige (0.4.5; 0.4.6 ist schlecht).
	if err := Cleanup(app, "0.4.0", "0.5.0"); err != nil {
		t.Fatal(err)
	}
	var left []string
	ents, _ := os.ReadDir(app)
	for _, e := range ents {
		left = append(left, e.Name())
	}
	want := "0.4.5 0.5.0 0.6.0 datei"
	if got := strings.Join(left, " "); got != want {
		t.Errorf("übrig: %q, erwartet %q", got, want)
	}
	// Läuft die Grundversion selbst, fallen alle Ordner unter ihr weg; höhere bleiben.
	if err := Cleanup(app, "0.5.5", "0.5.5"); err != nil {
		t.Fatal(err)
	}
	ents, _ = os.ReadDir(app)
	left = nil
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if got := strings.Join(left, " "); got != "0.6.0 datei" {
		t.Errorf("übrig nach Grundversion: %q", got)
	}
}

func TestMarkBad(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "0.0.2")
	os.MkdirAll(dir, 0o755)
	if err := MarkBad(Candidate{Version: "0.0.2", Exe: filepath.Join(dir, ExeName)}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, update.BadMarker)); err != nil {
		t.Fatal(err)
	}
	// Die Grundversion wird nie markiert.
	base := t.TempDir()
	if err := MarkBad(Candidate{Version: "0.0.1", Exe: filepath.Join(base, ExeName), Base: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, update.BadMarker)); err == nil {
		t.Error("Grundversion markiert")
	}
}
