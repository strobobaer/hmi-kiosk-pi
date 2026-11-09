package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidUpdateRepo(t *testing.T) {
	for _, ok := range []string{"", "strobobaer/hmi-kiosk-pi", "a/b", "A-1/x.y_z", "o/.hidden", "o/..x"} {
		if !ValidUpdateRepo(ok) {
			t.Errorf("%q sollte gültig sein", ok)
		}
	}
	long := "o/" + strings.Repeat("a", 101)
	for _, bad := range []string{"x", "/", "a/", "/b", "a/b/c", "-a/b", "a-/b", "a--b/c", "a/.", "a/..", "a b/c", "a/b c",
		"a/b?x", "a/b#", "a/b\n", "ä/b", "../b", "a/%2e", long, "abcdefghijklmnopqrstuvwxyzabcdefghijklmn/x"} {
		if ValidUpdateRepo(bad) {
			t.Errorf("%q sollte ungültig sein", bad)
		}
	}
}

func TestUpdateDefaultsAndValidation(t *testing.T) {
	if Default().Update.Repo != DefaultUpdateRepo || !Default().Update.Prerelease {
		t.Fatalf("Voreinstellung %+v", Default().Update)
	}
	c := sample()
	c.Update.Repo = "a/b/c"
	if Validate(c) == nil {
		t.Error("ungültige Quelle angenommen")
	}
	c.Update.Repo = ""
	if err := Validate(c); err != nil {
		t.Errorf("leere Quelle (Prüfung aus) abgelehnt: %v", err)
	}
}

// Eine Konfiguration ohne das Feld (älterer Stand) bekommt die Standardquelle und
// Vorabversionen; eine bewusst geleerte Quelle und abgewählte Vorabversionen bleiben so.
func TestUpdateRepoDefaultOnlyWhenFieldMissing(t *testing.T) {
	raw, err := json.Marshal(sample())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	delete(m, "update")
	old, _ := json.Marshal(m)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	got, _, err := Open(dir).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Update.Repo != DefaultUpdateRepo || !got.Update.Prerelease {
		t.Errorf("alte Datei: %+v", got.Update)
	}

	c := sample()
	c.Update = UpdateCfg{Repo: "", Prerelease: false}
	s := Open(t.TempDir())
	if err := s.Save(c); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Update.Repo != "" || got.Update.Prerelease {
		t.Errorf("geleerte Quelle, Vorabversionen abgewählt: %+v", got.Update)
	}
}

// Ein Abschnitt "update" ohne "prerelease" bekommt ebenfalls die Voreinstellung.
func TestUpdatePrereleaseDefaultWhenOnlyThatFieldMissing(t *testing.T) {
	var c Config
	if err := json.Unmarshal([]byte(`{"update":{"repo":"a/b"}}`), &c); err != nil {
		t.Fatal(err)
	}
	if c.Update.Repo != "a/b" || !c.Update.Prerelease {
		t.Errorf("%+v", c.Update)
	}
}
