package web

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
)

// keyFixture hat zwei Schlüssel: Was mit dem Geräteschlüssel versiegelt ist, lässt
// sich öffnen; was mit dem fremden versiegelt ist, nicht.
type keyFixture struct {
	t       *testing.T
	path    string
	device  *secrets.Box
	foreign *secrets.Box
	cfg     config.Config
}

func newKeyFixture(t *testing.T) *keyFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.key")
	device, err := secrets.OpenBox(path, false)
	if err != nil {
		t.Fatal(err)
	}
	fk, _ := secrets.GenerateKey()
	foreign, _ := secrets.NewBox(fk)
	seal := func(b *secrets.Box, s string) string {
		enc, err := b.Seal(s)
		if err != nil {
			t.Fatal(err)
		}
		return enc
	}
	cfg := config.Default()
	cfg.Terminal.Name = "Terminal 1"
	cfg.SharedKeyEnc = seal(foreign, "gemeinsam")
	cfg.HMIs = []config.HMI{
		{ID: "a", Name: "Presse", Host: "10.0.0.1", Port: 5900, PasswordEnc: seal(device, "pw-a"), Icon: "press", Color: "blue"},
		{ID: "b", Name: "Ofen", Host: "10.0.0.2", Port: 5900, PasswordEnc: seal(foreign, "pw-b"), Icon: "press", Color: "blue"},
		{ID: "c", Name: "Tank", Host: "10.0.0.3", Port: 5900, Icon: "tank", Color: "blue"},
	}
	return &keyFixture{t: t, path: path, device: device, foreign: foreign, cfg: cfg}
}

func (f *keyFixture) keyBytes() []byte {
	f.t.Helper()
	b, err := os.ReadFile(f.path)
	if err != nil {
		f.t.Fatal(err)
	}
	return b
}

func TestScanKeysFindsEachUnreadableValue(t *testing.T) {
	f := newKeyFixture(t)
	got := scanKeys(f.cfg, f.device)
	if !slices.Equal(got.HMIs, []string{"Ofen"}) || !got.Shared {
		t.Fatalf("got %+v", got)
	}
	if want := []string{"Ofen", sharedKeyLabel}; !slices.Equal(got.Names(), want) {
		t.Fatalf("Namen %v, erwartet %v", got.Names(), want)
	}
	f.cfg.HMIs[1].PasswordEnc, f.cfg.SharedKeyEnc = "", ""
	if got := scanKeys(f.cfg, f.device); len(got.Names()) != 0 {
		t.Fatalf("lesbar, aber gemeldet: %+v", got)
	}
	// Ohne Schlüssel in der Box ist jeder gespeicherte Wert unlesbar.
	if got := scanKeys(f.cfg, secrets.Unavailable()); !slices.Equal(got.HMIs, []string{"Presse"}) {
		t.Fatalf("Box ohne Schlüssel: %+v", got)
	}
}

func TestRepairKeysClearsOnlyUnreadableAndKeepsReadable(t *testing.T) {
	f := newKeyFixture(t)
	oldKey := f.keyBytes()
	var saved *config.Config
	out, res, err := repairKeys(f.path, f.device, f.cfg, func(c config.Config) error { saved = &c; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if saved == nil || saved.HMIs[0].PasswordEnc != out.HMIs[0].PasswordEnc {
		t.Fatal("neue Konfiguration nicht gespeichert")
	}
	if bytes.Equal(oldKey, f.keyBytes()) {
		t.Fatal("Schlüssel unverändert")
	}
	if !slices.Equal(res.Cleared, []string{"Ofen", sharedKeyLabel}) {
		t.Fatalf("geleert: %v", res.Cleared)
	}
	if pw, err := f.device.Open(out.HMIs[0].PasswordEnc); err != nil || pw != "pw-a" {
		t.Fatalf("lesbares Passwort verloren: %q %v", pw, err)
	}
	reopened, err := secrets.OpenBox(f.path, true)
	if err != nil {
		t.Fatal(err)
	}
	if pw, err := reopened.Open(out.HMIs[0].PasswordEnc); err != nil || pw != "pw-a" {
		t.Fatalf("mit der Datei nicht lesbar: %q %v", pw, err)
	}
	b := out.HMIs[1]
	if b.PasswordEnc != "" || !b.PasswordLost || out.SharedKeyEnc != "" {
		t.Fatalf("unlesbare Werte nicht geleert: %+v / %q", b, out.SharedKeyEnc)
	}
	if out.HMIs[0].PasswordLost || out.HMIs[2].PasswordLost {
		t.Fatal("falsch als verloren markiert")
	}
	if n := len(scanKeys(out, f.device).Names()); n != 0 {
		t.Fatalf("nach der Reparatur noch %d unlesbar", n)
	}
}

func TestRepairKeysWorksWithoutAnyKeyFile(t *testing.T) {
	f := newKeyFixture(t)
	if err := os.Remove(f.path); err != nil {
		t.Fatal(err)
	}
	box := secrets.Unavailable()
	out, res, err := repairKeys(f.path, box, f.cfg, func(config.Config) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(res.Cleared, []string{"Presse", "Ofen", sharedKeyLabel}) {
		t.Fatalf("geleert: %v", res.Cleared)
	}
	if !box.Available() {
		t.Fatal("Box nach der Reparatur ohne Schlüssel")
	}
	if enc, err := box.Seal("neu"); err != nil || enc == "" {
		t.Fatalf("Seal: %v", err)
	}
	if len(scanKeys(out, box).Names()) != 0 || !out.HMIs[0].PasswordLost || out.HMIs[2].PasswordLost {
		t.Fatalf("Zustand danach: %+v", out.HMIs)
	}
}

// Fällt das Schreiben des Schlüssels aus, ändert sich nichts: Weder Datei noch
// Konfiguration noch Box.
func TestRepairKeysKeyWriteFailureChangesNothing(t *testing.T) {
	f := newKeyFixture(t)
	old := f.keyBytes()
	blocked := filepath.Join(t.TempDir(), "gibt-es-nicht", "secrets.key")
	called := false
	_, _, err := repairKeys(blocked, f.device, f.cfg, func(config.Config) error { called = true; return nil })
	if err == nil || called {
		t.Fatalf("err=%v, gespeichert=%v", err, called)
	}
	if !bytes.Equal(old, f.keyBytes()) {
		t.Fatal("Schlüsseldatei verändert")
	}
	if pw, err := f.device.Open(f.cfg.HMIs[0].PasswordEnc); err != nil || pw != "pw-a" {
		t.Fatalf("Box verändert: %q %v", pw, err)
	}
}

// Scheitert das Speichern der Konfiguration nach dem Schlüssel, kehrt der alte
// Schlüssel zurück: Die alte Konfiguration bleibt lesbar.
func TestRepairKeysSaveFailureRestoresOldKey(t *testing.T) {
	f := newKeyFixture(t)
	old := f.keyBytes()
	_, _, err := repairKeys(f.path, f.device, f.cfg, func(config.Config) error { return errors.New("Platte voll") })
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	if !bytes.Equal(old, f.keyBytes()) {
		t.Fatal("alter Schlüssel nicht wiederhergestellt")
	}
	reopened, err := secrets.OpenBox(f.path, true)
	if err != nil {
		t.Fatal(err)
	}
	if pw, err := reopened.Open(f.cfg.HMIs[0].PasswordEnc); err != nil || pw != "pw-a" {
		t.Fatalf("alte Konfiguration unlesbar: %q %v", pw, err)
	}
	if pw, err := f.device.Open(f.cfg.HMIs[0].PasswordEnc); err != nil || pw != "pw-a" {
		t.Fatalf("Box verändert: %q %v", pw, err)
	}
}

func TestRepairKeysSaveFailureWithoutOldFileRemovesNewKey(t *testing.T) {
	f := newKeyFixture(t)
	os.Remove(f.path)
	box := secrets.Unavailable()
	if _, _, err := repairKeys(f.path, box, f.cfg, func(config.Config) error { return errors.New("kaputt") }); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if _, err := os.Stat(f.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("neue Schlüsseldatei blieb liegen: %v", err)
	}
	if box.Available() {
		t.Fatal("Box hat den verworfenen Schlüssel")
	}
}

// Scheitert zusätzlich die Wiederherstellung, gilt auf der Platte der neue
// Schlüssel zur alten Konfiguration. Dann ist jeder Wert unlesbar, und die
// Prüfung meldet sie, statt sie still zu lassen.
func TestRepairKeysAfterLostRestoreEveryValueIsReported(t *testing.T) {
	f := newKeyFixture(t)
	_, _, err := repairKeys(f.path, f.device, f.cfg, func(config.Config) error {
		if err := os.Remove(f.path); err != nil {
			t.Fatal(err)
		}
		if err := os.Mkdir(f.path, 0o700); err != nil { // die Wiederherstellung kann nicht schreiben
			t.Fatal(err)
		}
		return errors.New("kaputt")
	})
	if err == nil {
		t.Fatal("Fehler erwartet")
	}
	if got := scanKeys(f.cfg, f.device).Names(); !slices.Equal(got, []string{"Presse", "Ofen", sharedKeyLabel}) {
		t.Fatalf("gemeldet: %v", got)
	}
}

func TestPowerLossBetweenKeyAndConfigIsDetectedAfterRestart(t *testing.T) {
	f := newKeyFixture(t)
	fresh, _ := secrets.GenerateKey()
	if err := secrets.WriteKeyFile(f.path, fresh); err != nil { // Strom weg, bevor die Konfiguration gespeichert ist
		t.Fatal(err)
	}
	restarted, err := secrets.OpenBox(f.path, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := scanKeys(f.cfg, restarted).Names(); !slices.Equal(got, []string{"Presse", "Ofen", sharedKeyLabel}) {
		t.Fatalf("gemeldet: %v", got)
	}
}
