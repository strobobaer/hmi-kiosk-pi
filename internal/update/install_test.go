package update

import (
	"archive/tar"
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func appDir(root string) string { return filepath.Join(root, "app") }

func link(t *testing.T, root, name string) string {
	t.Helper()
	v, err := os.Readlink(filepath.Join(appDir(root), name))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatal(err)
	}
	return filepath.Base(v)
}

func mkVersion(t *testing.T, root, v string) {
	t.Helper()
	d := filepath.Join(appDir(root), v)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, BinaryName), []byte("bin "+v), 0o755); err != nil {
		t.Fatal(err)
	}
}

func newInstaller(root string, pub ed25519.PublicKey) *Installer {
	return &Installer{
		Root: root, PublicKey: pub,
		Activate:  func(_ context.Context, v string) error { return Activate(root, v) },
		FreeBytes: func(string) (uint64, error) { return 1 << 40, nil },
	}
}

func TestInstallRejectsPathTraversalInArchive(t *testing.T) {
	root := t.TempDir()
	bad := tarGz(t, []entry{
		{name: "VERSION", body: "1.0.0\n"},
		{name: "hmi-kiosk", body: "x", mode: 0o755},
		{name: "../../etc/passwd", body: "pwn"},
	})
	// Install darf auch ein Paket, das Verify nie gesehen hat, nicht entpacken.
	if _, err := Install(root, &Package{Version: "1.0.0", targz: bad}); err == nil {
		t.Fatal("Traversal angenommen")
	}
	for _, e := range []entry{
		{name: "hmi-kiosk", typeflag: tar.TypeSymlink, link: "/bin/sh"},
		{name: "hmi-kiosk", typeflag: tar.TypeLink, link: "/etc/passwd"},
	} {
		p := &Package{Version: "1.0.0", targz: tarGz(t, []entry{{name: "VERSION", body: "1.0.0\n"}, e})}
		if _, err := Install(root, p); err == nil {
			t.Errorf("Typ %c angenommen", e.typeflag)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "etc")); err == nil {
		t.Fatal("außerhalb geschrieben")
	}
	ents, _ := os.ReadDir(appDir(root))
	for _, e := range ents {
		t.Errorf("Rest im app-Ordner: %s", e.Name())
	}
}

func TestInstallWritesBinaryMode0755(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	p, err := Verify(goodBlob(t, priv, "1.4.0"), pub)
	if err != nil {
		t.Fatal(err)
	}
	v, err := Install(root, p)
	if err != nil || v != "1.4.0" {
		t.Fatalf("%q %v", v, err)
	}
	fi, err := os.Stat(filepath.Join(appDir(root), "1.4.0", BinaryName))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("Modus %v", fi.Mode().Perm())
	}
	b, _ := os.ReadFile(filepath.Join(appDir(root), "1.4.0", BinaryName))
	if string(b) != "#!binary 1.4.0" {
		t.Fatalf("Inhalt %q", b)
	}
	ents, _ := os.ReadDir(appDir(root))
	if len(ents) != 1 {
		t.Fatalf("app: %v", ents)
	}
}

func TestInstallSwitchesCurrentAndPreviousAtomically(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	in := newInstaller(root, pub)
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		got, err := in.Install(context.Background(), goodBlob(t, priv, v))
		if err != nil || got != v {
			t.Fatalf("%s: %q %v", v, got, err)
		}
	}
	if c, p := link(t, root, "current"), link(t, root, "previous"); c != "1.2.0" || p != "1.1.0" {
		t.Fatalf("current=%s previous=%s", c, p)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "current", BinaryName)); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(appDir(root), "pending"))
	if string(b) != "1.2.0" {
		t.Fatalf("pending %q", b)
	}
	ents, _ := os.ReadDir(appDir(root))
	for _, e := range ents {
		if strings.Contains(e.Name(), "tmp") {
			t.Errorf("temporärer Rest: %s", e.Name())
		}
	}
}

func TestInstallRejectsCurrentVersion(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	in := newInstaller(root, pub)
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "2.0.0")); err != nil {
		t.Fatal(err)
	}
	_, err := in.Install(context.Background(), goodBlob(t, priv, "2.0.0"))
	if err == nil || !strings.Contains(err.Error(), "bereits") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallEmptyPublicKeyRejectsAll(t *testing.T) {
	_, priv := newKeys(t)
	root := t.TempDir()
	called := false
	in := newInstaller(root, nil)
	in.Activate = func(context.Context, string) error { called = true; return nil }
	_, err := in.Install(context.Background(), goodBlob(t, priv, "1.0.0"))
	if err == nil || !strings.Contains(err.Error(), "Schlüssel") {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("Activate trotz fehlendem Schlüssel")
	}
	if _, err := os.Stat(appDir(root)); err == nil {
		t.Fatal("Dateien trotz fehlendem Schlüssel")
	}
}

func TestInstallFailureLeavesCurrentUntouched(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	in := newInstaller(root, pub)
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	// Zu wenig Platz: weniger als das Doppelte der Paketgröße.
	blob := goodBlob(t, priv, "1.1.0")
	var asked string
	in.FreeBytes = func(p string) (uint64, error) { asked = p; return uint64(2*len(blob) - 1), nil }
	_, err := in.Install(context.Background(), blob)
	if err == nil || !strings.Contains(err.Error(), "Speicherplatz") {
		t.Fatalf("err = %v", err)
	}
	if asked == "" || !strings.HasPrefix(asked, root) {
		t.Fatalf("Platzprüfung auf %q", asked)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "1.1.0")); err == nil {
		t.Fatal("entpackt trotz zu wenig Platz")
	}
	// Genau das Doppelte reicht.
	in.FreeBytes = func(string) (uint64, error) { return uint64(2 * len(blob)), nil }
	if _, err := in.Install(context.Background(), blob); err != nil {
		t.Fatal(err)
	}
	// Ein Fehler der Platzprüfung selbst sperrt ebenfalls.
	in.FreeBytes = func(string) (uint64, error) { return 0, errors.New("statfs kaputt") }
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "1.2.0")); err == nil {
		t.Fatal("Fehler der Platzprüfung ignoriert")
	}
}

func TestInstallActivateFailureRemovesNewVersionKeepsCurrent(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	in := newInstaller(root, pub)
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "1.0.0")); err != nil {
		t.Fatal(err)
	}
	in.Activate = func(context.Context, string) error { return errors.New("Helfer nicht erreichbar") }
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "1.1.0")); err == nil {
		t.Fatal("Fehler verschluckt")
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "1.1.0")); err == nil {
		t.Fatal("gescheiterte Version bleibt liegen")
	}
}

func TestOldVersionsPrunedKeepingCurrentAndPrevious(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	for _, v := range []string{"0.9.0", "0.9.1", "0.9.2"} {
		mkVersion(t, root, v)
	}
	os.MkdirAll(filepath.Join(appDir(root), ".tmp-9.9.9"), 0o755)
	in := newInstaller(root, pub)
	for _, v := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		if _, err := in.Install(context.Background(), goodBlob(t, priv, v)); err != nil {
			t.Fatal(err)
		}
	}
	ents, _ := os.ReadDir(appDir(root))
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	got := strings.Join(names, ",")
	want := "1.0.0,1.1.0,1.2.0,current,pending,previous"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestPruneKeepsCurrentAndPreviousOnly(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	mkVersion(t, root, "1.2.0")
	os.Symlink("1.0.0", filepath.Join(appDir(root), "previous"))
	os.Symlink("1.2.0", filepath.Join(appDir(root), "current"))
	if err := Prune(root); err != nil {
		t.Fatal(err)
	}
	for v, want := range map[string]bool{"1.0.0": true, "1.1.0": false, "1.2.0": true} {
		_, err := os.Stat(filepath.Join(appDir(root), v))
		if (err == nil) != want {
			t.Errorf("%s vorhanden=%v want %v", v, err == nil, want)
		}
	}
}

func TestActivateSwapsAndWritesPending(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	if err := Activate(root, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if c, p := link(t, root, "current"), link(t, root, "previous"); c != "1.0.0" || p != "" {
		t.Fatalf("c=%s p=%s", c, p)
	}
	if err := Activate(root, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if c, p := link(t, root, "current"), link(t, root, "previous"); c != "1.1.0" || p != "1.0.0" {
		t.Fatalf("c=%s p=%s", c, p)
	}
	b, _ := os.ReadFile(filepath.Join(appDir(root), "pending"))
	if string(b) != "1.1.0" {
		t.Fatalf("pending %q", b)
	}
	// Wiederholung ändert previous nicht.
	if err := Activate(root, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if p := link(t, root, "previous"); p != "1.0.0" {
		t.Fatalf("previous nach Wiederholung: %s", p)
	}
}

func TestActivateRejectsMissingOrInvalid(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	if err := Activate(root, "9.9.9"); err == nil {
		t.Error("fehlende Version aktiviert")
	}
	if err := Activate(root, "../1.0.0"); err == nil {
		t.Error("ungültige Version aktiviert")
	}
	os.MkdirAll(filepath.Join(appDir(root), "2.0.0"), 0o755) // ohne Binärdatei
	if err := Activate(root, "2.0.0"); err == nil {
		t.Error("Version ohne Binärdatei aktiviert")
	}
	if c := link(t, root, "current"); c != "" {
		t.Errorf("current=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "pending")); err == nil {
		t.Error("pending trotz Fehler")
	}
}

func TestRollbackRestoresPreviousAndClearsPendingIdempotently(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	Activate(root, "1.0.0")
	Activate(root, "1.1.0")
	if err := Rollback(root); err != nil {
		t.Fatal(err)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "pending")); err == nil {
		t.Fatal("pending bleibt")
	}
	// Unterbrochener Rückfall: current ist schon zurück, pending noch da.
	os.WriteFile(filepath.Join(appDir(root), "pending"), []byte("1.1.0"), 0o644)
	if err := Rollback(root); err != nil {
		t.Fatal(err)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current nach Wiederholung=%s", c)
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "pending")); err == nil {
		t.Fatal("pending bleibt nach Wiederholung")
	}
}

func TestRollbackWithoutPreviousFailsAndKeepsCurrent(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	Activate(root, "1.0.0")
	err := Rollback(root)
	if err == nil || !strings.Contains(err.Error(), "Vorgänger") {
		t.Fatalf("err = %v", err)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
}

func TestOpsDelegates(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	o := Ops{Root: root}
	if err := o.Activate("1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := o.Activate("1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := o.Rollback(); err != nil {
		t.Fatal(err)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
}

func TestFreeBytesReal(t *testing.T) {
	n, err := FreeBytes(t.TempDir())
	if err != nil || n == 0 {
		t.Fatalf("%d %v", n, err)
	}
}

func dirNames(t *testing.T, root string) string {
	t.Helper()
	ents, _ := os.ReadDir(appDir(root))
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return strings.Join(names, ",")
}

// Prune läuft zu Beginn von Install (vor der Platzprüfung), damit der Platz
// alter Versionen für die neue frei wird.
func TestInstallPrunesBeforeFreeSpaceCheck(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	for _, v := range []string{"0.9.0", "1.0.0", "1.1.0"} {
		mkVersion(t, root, v)
	}
	os.Symlink("1.0.0", filepath.Join(appDir(root), "previous"))
	os.Symlink("1.1.0", filepath.Join(appDir(root), "current"))
	in := newInstaller(root, pub)
	seen := ""
	in.FreeBytes = func(string) (uint64, error) {
		seen = dirNames(t, root)
		return 0, nil // Platz reicht nicht: Install scheitert, Prune war trotzdem schon dran
	}
	if _, err := in.Install(context.Background(), goodBlob(t, priv, "1.2.0")); err == nil {
		t.Fatal("zu wenig Platz angenommen")
	}
	if seen != "1.0.0,1.1.0,current,previous" {
		t.Fatalf("bei der Platzprüfung: %s", seen)
	}
}

func TestPackageInstallPlantedTempFilesDoNotFollowSymlinks(t *testing.T) {
	root := t.TempDir()
	mkVersion(t, root, "1.0.0")
	mkVersion(t, root, "1.1.0")
	victim := filepath.Join(t.TempDir(), "victim")
	os.WriteFile(victim, []byte("unberührt"), 0o644)
	for _, n := range []string{"pending.tmp", ".pending.tmp", ".link-current", ".link-previous", ".tmp-pending"} {
		os.Symlink(victim, filepath.Join(appDir(root), n))
	}
	if err := Activate(root, "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := Activate(root, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(root); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(victim)
	if string(b) != "unberührt" {
		t.Fatalf("Ziel verändert: %q", b)
	}
	if c := link(t, root, "current"); c != "1.0.0" {
		t.Fatalf("current=%s", c)
	}
}

func TestInstallFailsOnBrokenLinkInsteadOfReplacingDest(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	mkVersion(t, root, "1.4.0")
	os.Symlink("../../etc", filepath.Join(appDir(root), "current"))
	p, err := Verify(goodBlob(t, priv, "1.4.0"), pub)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(root, p); err == nil {
		t.Fatal("kaputter Verweis ignoriert")
	}
	b, _ := os.ReadFile(filepath.Join(appDir(root), "1.4.0", BinaryName))
	if string(b) != "bin 1.4.0" {
		t.Fatalf("Zielordner ersetzt: %q", b)
	}
	if dirNames(t, root) != "1.4.0,current" {
		t.Fatalf("Reste: %s", dirNames(t, root))
	}
}

// Beim Update per Klick muss das Paket die angebotene Version enthalten: Ein älteres,
// gültig signiertes Paket unter neuem Namen darf nicht still zurückstufen.
func TestInstallExpectRejectsOtherVersion(t *testing.T) {
	pub, priv := newKeys(t)
	root := t.TempDir()
	in := newInstaller(root, pub)
	called := false
	in.Activate = func(ctx context.Context, v string) error { called = true; return Activate(root, v) }
	_, err := in.InstallExpect(context.Background(), goodBlob(t, priv, "0.1.2"), "0.3.0")
	if err == nil || !strings.Contains(err.Error(), "0.1.2") || !strings.Contains(err.Error(), "0.3.0") {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Error("abweichende Version aktiviert")
	}
	if _, err := os.Stat(filepath.Join(appDir(root), "0.1.2")); !os.IsNotExist(err) {
		t.Errorf("Versionsordner angelegt: %v", err)
	}
	got, err := in.InstallExpect(context.Background(), goodBlob(t, priv, "0.3.0"), "0.3.0")
	if err != nil || got != "0.3.0" || !called {
		t.Fatalf("%q %v", got, err)
	}
}

func TestActivateBinaryPerProduct(t *testing.T) {
	root := t.TempDir()
	mk := func(v, bin string) {
		d := filepath.Join(appDir(root), v)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, bin), []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mk("1.0.0", "pdh-terminal")
	mk("1.1.0", "hmi-kiosk")
	if err := ActivateBinary(root, "pdh-terminal", "1.0.0"); err != nil {
		t.Fatalf("pdh-terminal-Version: %v", err)
	}
	if err := (Ops{Root: root, Binary: "pdh-terminal"}).Activate("1.1.0"); err == nil {
		t.Fatal("Version nur mit hmi-kiosk wurde für pdh-terminal aktiviert")
	}
	if err := Activate(root, "1.0.0"); err == nil {
		t.Fatal("leeres Binary muss hmi-kiosk verlangen")
	}
	if err := (Ops{Root: root}).Activate("1.1.0"); err != nil {
		t.Fatalf("ohne Binary gilt hmi-kiosk: %v", err)
	}
	// Rückfall prüft ebenfalls das Programm des Produkts: previous (1.0.0) hat nur pdh-terminal.
	if err := (Ops{Root: root}).Rollback(); err == nil {
		t.Fatal("Rückfall auf pdh-terminal-Version ohne hmi-kiosk")
	}
	if err := (Ops{Root: root, Binary: "pdh-terminal"}).Rollback(); err != nil {
		t.Fatalf("Rückfall mit pdh-terminal: %v", err)
	}
}
