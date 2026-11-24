package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tnKey = "tskey-auth-kSECRET123CNTRL-abcdefghijkl"

type tnEnv struct {
	*env
	keyPath string
	calls   [][]string
	during  string
	mode    os.FileMode
	runErr  error
}

func newTailnetEnv(t *testing.T) *tnEnv {
	t.Helper()
	e := &tnEnv{env: newEnv(t, "")}
	dir := filepath.Join(e.dir, "run")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	e.keyPath = filepath.Join(dir, "tailnet-key")
	e.srv.TailnetKeyPath = e.keyPath
	e.srv.exec = func(_ context.Context, name string, args ...string) error {
		e.calls = append(e.calls, append([]string{name}, args...))
		for _, a := range args {
			if p, ok := strings.CutPrefix(a, "--auth-key=file:"); ok {
				b, err := os.ReadFile(p)
				if err != nil {
					t.Errorf("Schlüsseldatei nicht lesbar: %v", err)
				}
				e.during = string(b)
				if fi, err := os.Stat(p); err == nil {
					e.mode = fi.Mode().Perm()
				}
			}
		}
		return e.runErr
	}
	return e
}

func tnArg(key, host string) string {
	b, _ := json.Marshal(map[string]string{"key": key, "host": host})
	return string(b)
}

func TestHelperTailnetUp(t *testing.T) {
	linuxOnly(t)
	e := newTailnetEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdTailnetUp, tnArg(tnKey, "halle-3")); err != nil {
		t.Fatal(err)
	}
	if len(e.calls) != 1 {
		t.Fatalf("Aufrufe: %q", e.calls)
	}
	c := e.calls[0]
	if c[0] != "tailscale" || c[1] != "up" || c[2] != "--auth-key=file:"+e.keyPath || c[3] != "--hostname=halle-3" || len(c) != 4 {
		t.Fatalf("Befehl: %q", c)
	}
	for _, a := range c {
		if strings.Contains(a, tnKey) {
			t.Fatalf("Key im Argument: %q", a)
		}
	}
	if e.during != tnKey || e.mode != 0o600 {
		t.Errorf("Datei während des Aufrufs: %q %o", e.during, e.mode)
	}
	if _, err := os.Lstat(e.keyPath); !os.IsNotExist(err) {
		t.Errorf("Datei nicht gelöscht: %v", err)
	}
}

func TestHelperTailnetUpRejectsBadInput(t *testing.T) {
	linuxOnly(t)
	e := newTailnetEnv(t)
	for name, arg := range map[string]string{
		"leerer Key":             tnArg("", "halle"),
		"Key mit LF":             tnArg("tskey\nxyz", "halle"),
		"Key mit CR":             tnArg("tskey\rxyz", "halle"),
		"Key mit NUL":            tnArg("tskey\x00xyz", "halle"),
		"Key mit Leerzeichen":    tnArg("tskey xyz", "halle"),
		"Key zu lang":            tnArg(strings.Repeat("k", 513), "halle"),
		"Host Großbuchstabe":     tnArg(tnKey, "Halle"),
		"Host Leerzeichen":       tnArg(tnKey, "ha lle"),
		"Host leer":              tnArg(tnKey, ""),
		"Host zu lang":           tnArg(tnKey, strings.Repeat("a", 64)),
		"Host mit Option":        tnArg(tnKey, "x --accept-routes"),
		"unbekanntes Feld":       `{"key":"` + tnKey + `","host":"a","extra":"b"}`,
		"Host fehlt":             `{"key":"` + tnKey + `"}`,
		"Key fehlt":              `{"host":"a"}`,
		"kein JSON":              tnKey,
		"Rest dahinter":          tnArg(tnKey, "a") + `{}`,
		"Key keine Zeichenkette": `{"key":5,"host":"a"}`,
	} {
		e.calls = nil
		err := e.srv.Dispatch(context.Background(), CmdTailnetUp, arg)
		if err == nil {
			t.Errorf("%s: angenommen", name)
		} else if strings.Contains(err.Error(), tnKey) {
			t.Errorf("%s: Key im Fehler: %v", name, err)
		}
		if len(e.calls) != 0 {
			t.Errorf("%s: tailscale gestartet: %q", name, e.calls)
		}
		if _, err := os.Lstat(e.keyPath); !os.IsNotExist(err) {
			t.Errorf("%s: Datei angelegt", name)
		}
	}
	if err := e.srv.Dispatch(context.Background(), CmdTailnetUp, ""); err == nil {
		t.Error("ohne Wert angenommen")
	}
}

func TestHelperTailnetUpFailureRemovesFileAndHidesKey(t *testing.T) {
	linuxOnly(t)
	e := newTailnetEnv(t)
	e.runErr = errors.New("exit status 1: invalid key " + tnKey)
	err := e.srv.Dispatch(context.Background(), CmdTailnetUp, tnArg(tnKey, "halle"))
	if err == nil || strings.Contains(err.Error(), tnKey) {
		t.Fatalf("Fehler: %v", err)
	}
	if _, err := os.Lstat(e.keyPath); !os.IsNotExist(err) {
		t.Errorf("Datei nicht gelöscht: %v", err)
	}
}

// Ein Rest aus einem Abbruch (auch ein Verweis) wird ersetzt, nie hindurch geschrieben.
func TestHelperTailnetUpReplacesLeftoverAndSymlink(t *testing.T) {
	linuxOnly(t)
	e := newTailnetEnv(t)
	victim := filepath.Join(e.dir, "victim")
	if err := os.WriteFile(victim, []byte("unberührt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, e.keyPath); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Dispatch(context.Background(), CmdTailnetUp, tnArg(tnKey, "halle")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "unberührt" {
		t.Fatalf("Ziel des Verweises überschrieben: %q", b)
	}
	if _, err := os.Lstat(e.keyPath); !os.IsNotExist(err) {
		t.Errorf("Datei nicht gelöscht: %v", err)
	}
}

// Ein Ordner, in den andere schreiben dürfen, ist nicht vertrauenswürdig.
func TestHelperTailnetUpRefusesWritableDir(t *testing.T) {
	linuxOnly(t)
	e := newTailnetEnv(t)
	if err := os.Chmod(filepath.Dir(e.keyPath), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Dispatch(context.Background(), CmdTailnetUp, tnArg(tnKey, "halle")); err == nil {
		t.Fatal("beschreibbarer Ordner angenommen")
	}
	if len(e.calls) != 0 {
		t.Errorf("tailscale gestartet: %q", e.calls)
	}
}

func TestHelperTailnetDown(t *testing.T) {
	e := newTailnetEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdTailnetDown, ""); err != nil {
		t.Fatal(err)
	}
	if len(e.calls) != 1 || strings.Join(e.calls[0], " ") != "tailscale logout" {
		t.Fatalf("Aufrufe: %q", e.calls)
	}
	if err := e.srv.Dispatch(context.Background(), CmdTailnetDown, "x"); err == nil {
		t.Error("Wert angenommen")
	}
}

func TestHelperTailnetStatus(t *testing.T) {
	e := newTailnetEnv(t)
	full := `{"Version":"1","BackendState":"Running","Self":{"HostName":"kasse","DNSName":"kasse.t.ts.net.","TailscaleIPs":["100.64.0.7"],"Relay":"fra"},"Peer":{"a":{"HostName":"andere"}}}`
	var gotArgs []string
	e.srv.ExecOut = func(_ context.Context, name string, args ...string) (string, error) {
		gotArgs = append([]string{name}, args...)
		return full, nil
	}
	data, err := e.srv.DispatchData(context.Background(), CmdTailnetStatus, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(gotArgs, " ") != "tailscale status --json" {
		t.Errorf("Befehl: %q", gotArgs)
	}
	b, _ := json.Marshal(data)
	s := string(b)
	if !strings.Contains(s, `"BackendState":"Running"`) || !strings.Contains(s, "100.64.0.7") || strings.Contains(s, "andere") {
		t.Errorf("Antwort: %s", s)
	}

	e.srv.ExecOut = func(context.Context, string, ...string) (string, error) {
		return "", notInstalledError{"tailscale"}
	}
	data, err = e.srv.DispatchData(context.Background(), CmdTailnetStatus, "")
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(data); string(b) != `{"installed":false}` {
		t.Errorf("nicht installiert: %s", b)
	}

	e.srv.ExecOut = func(context.Context, string, ...string) (string, error) { return "", errors.New("boom") }
	if _, err := e.srv.DispatchData(context.Background(), CmdTailnetStatus, ""); err == nil {
		t.Error("Fehler verschluckt")
	}
	e.srv.ExecOut = nil
	if _, err := e.srv.DispatchData(context.Background(), CmdTailnetStatus, ""); err == nil {
		t.Error("ohne ExecOut kein Fehler")
	}
}
