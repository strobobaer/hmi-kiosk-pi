package tailnet

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const secretKey = "tskey-auth-kSECRET123CNTRL-abcdefghijkl"

func fakeBin(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tailscale.exe")
	if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCLIUpKeyNotInArgs(t *testing.T) {
	var args []string
	var path, during string
	run := func(_ context.Context, name string, a ...string) (string, error) {
		args = append([]string{name}, a...)
		for _, x := range a {
			if p, ok := strings.CutPrefix(x, "--auth-key=file:"); ok {
				path = p
				b, err := os.ReadFile(p)
				if err != nil {
					t.Errorf("Schlüsseldatei nicht lesbar: %v", err)
				}
				during = string(b)
			}
		}
		return "", nil
	}
	if err := NewCLI(fakeBin(t), run).Up(context.Background(), secretKey, "halle-3"); err != nil {
		t.Fatal(err)
	}
	for _, a := range args {
		if strings.Contains(a, secretKey) {
			t.Fatalf("Schlüssel im Argument: %q", a)
		}
	}
	if path == "" {
		t.Fatalf("kein --auth-key=file:-Argument: %q", args)
	}
	if during != secretKey {
		t.Errorf("Dateiinhalt während des Aufrufs: %q", during)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Datei existiert noch: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{" up ", "--reset", "--hostname=halle-3", "--unattended", "--timeout=50s"} {
		if !strings.Contains(" "+joined+" ", want) {
			t.Errorf("%q fehlt in %q", want, joined)
		}
	}
}

func TestCLIUpFailureRemovesFileAndHidesKey(t *testing.T) {
	var path string
	run := func(_ context.Context, _ string, a ...string) (string, error) {
		for _, x := range a {
			if p, ok := strings.CutPrefix(x, "--auth-key=file:"); ok {
				path = p
			}
		}
		return "", errors.New("exit 1: invalid key " + secretKey)
	}
	err := NewCLI(fakeBin(t), run).Up(context.Background(), secretKey, "x")
	if err == nil || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("Fehler: %v", err)
	}
	if _, serr := os.Stat(path); !os.IsNotExist(serr) {
		t.Error("Datei nicht gelöscht")
	}
}

func TestCLIUpRejectsBadInputBeforeFile(t *testing.T) {
	called := false
	run := func(context.Context, string, ...string) (string, error) { called = true; return "", nil }
	c := NewCLI(fakeBin(t), run)
	for _, k := range []string{"", "a\nb"} {
		if err := c.Up(context.Background(), k, "ok"); err == nil {
			t.Errorf("Schlüssel %q angenommen", k)
		}
	}
	if err := c.Up(context.Background(), secretKey, "Bad Host"); err == nil {
		t.Error("ungültiger Hostname angenommen")
	}
	if called {
		t.Error("tailscale wurde trotz ungültiger Eingabe gestartet")
	}
}

// Der Runner bekommt den Key über den Kontext, um ihn vor dem Kürzen aus der Ausgabe zu entfernen.
func TestCLIUpPassesSecretToRunner(t *testing.T) {
	run := func(ctx context.Context, _ string, _ ...string) (string, error) {
		msg := Scrub(ctx, strings.Repeat("x", 180)+secretKey)
		return "", errors.New(msg)
	}
	err := NewCLI(fakeBin(t), run).Up(context.Background(), secretKey, "x")
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "tskey-auth") {
		t.Fatalf("Fehler: %v", err)
	}
}

// Misslingt das Löschen, steht nur der Pfad im Protokoll, nie der Key.
func TestCLIUpLogsFailedRemoval(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	oldFn, oldTries, oldDelay := removeFile, removeTries, removeDelay
	t.Cleanup(func() { log.SetOutput(os.Stderr); removeFile, removeTries, removeDelay = oldFn, oldTries, oldDelay })
	removed := 0
	removeFile = func(string) error { removed++; return errors.New("in use") }
	removeTries, removeDelay = 3, 0
	var path string
	run := func(_ context.Context, _ string, a ...string) (string, error) {
		for _, x := range a {
			if p, ok := strings.CutPrefix(x, "--auth-key=file:"); ok {
				path = p
			}
		}
		return "", nil
	}
	if err := NewCLI(fakeBin(t), run).Up(context.Background(), secretKey, "x"); err != nil {
		t.Fatal(err)
	}
	oldFn(path)
	if removed != 3 {
		t.Errorf("Versuche: %d", removed)
	}
	if !strings.Contains(buf.String(), path) || strings.Contains(buf.String(), "SECRET") {
		t.Errorf("Protokoll: %q", buf.String())
	}
}

func TestCLIUpPermissionError(t *testing.T) {
	run := func(context.Context, string, ...string) (string, error) {
		return "", errors.New("Access is denied")
	}
	err := NewCLI(fakeBin(t), run).Up(context.Background(), secretKey, "x")
	if err == nil || !strings.Contains(err.Error(), "Administratorrechte oder unbeaufsichtigten Modus") {
		t.Fatalf("Fehler: %v", err)
	}
}

func TestCLIStatusNotInstalled(t *testing.T) {
	run := func(context.Context, string, ...string) (string, error) { t.Fatal("darf nicht laufen"); return "", nil }
	st, err := NewCLI(filepath.Join(t.TempDir(), "fehlt.exe"), run).Status(context.Background())
	if err != nil || st.Installed {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestCLIStatusAndDown(t *testing.T) {
	var got [][]string
	run := func(_ context.Context, _ string, a ...string) (string, error) {
		got = append(got, a)
		return statusRunning, nil
	}
	c := NewCLI(fakeBin(t), run)
	st, err := c.Status(context.Background())
	if err != nil || !st.LoggedIn || st.IP != "100.64.0.7" {
		t.Fatalf("%+v %v", st, err)
	}
	if err := c.Down(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || strings.Join(got[0], " ") != "status --json --peers=false" || strings.Join(got[1], " ") != "logout" {
		t.Fatalf("Aufrufe: %q", got)
	}
}
