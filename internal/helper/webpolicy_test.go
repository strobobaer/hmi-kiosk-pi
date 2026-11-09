package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hmikiosk/internal/config"
)

func webEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, "")
	e.srv.ChromiumPolicyPath = filepath.Join(e.dir, "etc", "chromium", "policies", "managed", "hmi-kiosk.json")
	e.srv.WebOriginsStatePath = filepath.Join(e.dir, "data", "system", "web-origins")
	if err := os.MkdirAll(filepath.Dir(e.srv.ChromiumPolicyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	return e
}

type policyFile struct {
	URLBlocklist               []string
	URLAllowlist               []string
	PasswordManagerEnabled     *bool
	DownloadRestrictions       *int
	BrowserSignin              *int
	DeveloperToolsAvailability *int
}

func readPolicy(t *testing.T, p string) policyFile {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var pf policyFile
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&pf); err != nil {
		t.Fatalf("Richtlinie kein gültiges JSON: %v\n%s", err, b)
	}
	return pf
}

var wantFixed = []string{"http://127.0.0.1:8080", "http://localhost:8080", "http://127.0.0.1:8081", "http://localhost:8081"}

func TestSetWebOriginsWritesPolicyWithFixedEntries(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	arg := `["http://grafana:3000","https://10.0.0.9:443","http://[fd00::7]:8080"]`
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, arg); err != nil {
		t.Fatal(err)
	}
	pf := readPolicy(t, e.srv.ChromiumPolicyPath)
	if !reflect.DeepEqual(pf.URLBlocklist, []string{"*"}) {
		t.Fatalf("Blocklist %q", pf.URLBlocklist)
	}
	// Rechnernamen mit führendem Punkt: genau dieser Rechner, keine Unternamen.
	want := append(append([]string{}, wantFixed...), "http://[fd00::7]:8080", "http://.grafana:3000", "https://10.0.0.9:443")
	if !reflect.DeepEqual(pf.URLAllowlist, want) {
		t.Fatalf("Allowlist\n got %q\nwant %q", pf.URLAllowlist, want)
	}
	if pf.PasswordManagerEnabled == nil || *pf.PasswordManagerEnabled {
		t.Fatal("Passwortmanager nicht abgeschaltet")
	}
	if pf.DownloadRestrictions == nil || *pf.DownloadRestrictions != 3 {
		t.Fatal("Downloads nicht gesperrt (DownloadRestrictions 3)")
	}
	if pf.BrowserSignin == nil || *pf.BrowserSignin != 0 || pf.DeveloperToolsAvailability == nil || *pf.DeveloperToolsAvailability != 2 {
		t.Fatal("Anmeldung bei Chromium oder Entwicklerwerkzeuge nicht abgeschaltet")
	}
	fi, err := os.Stat(e.srv.ChromiumPolicyPath)
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("Rechte %v %v", fi.Mode(), err)
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe: %v", e.fx.got())
	}
	ents, _ := os.ReadDir(filepath.Dir(e.srv.ChromiumPolicyPath))
	if len(ents) != 1 {
		t.Fatalf("Reste im Richtlinienordner: %v", ents)
	}
}

func TestSetWebOriginsEmptyKeepsFixedEntries(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `[]`); err != nil {
		t.Fatal(err)
	}
	if pf := readPolicy(t, e.srv.ChromiumPolicyPath); !reflect.DeepEqual(pf.URLAllowlist, wantFixed) {
		t.Fatalf("Allowlist %q", pf.URLAllowlist)
	}
}

func TestSetWebOriginsRejectsBadInput(t *testing.T) {
	linuxOnly(t)
	many := make([]string, maxWebOrigins+1)
	for i := range many {
		many[i] = fmt.Sprintf("http://h%d:1", i)
	}
	if len(many[0]) == 0 || !config.ValidOrigin(many[0]) {
		t.Fatal("Beispiel ungültig")
	}
	manyJSON, _ := json.Marshal(many)
	for _, arg := range []string{
		`x`, `{}`, `"http://a:1"`, `[1]`, `[null]`, `["http://a:1"] x`, `["http://a:1",]`,
		`["*"]`, `["http://a"]`, `["http://a:1/x"]`, `["http://a:1\n"]`, `["javascript:alert(1)"]`,
		`["http://A:1"]`, `["http://*.a:1"]`, `["file:///etc:1"]`, `["http://a:1\"]}"]`,
		string(manyJSON),
	} {
		e := webEnv(t)
		if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, arg); err == nil {
			t.Errorf("%q angenommen", arg)
		}
		if _, err := os.Stat(e.srv.ChromiumPolicyPath); err == nil {
			t.Errorf("%q: Richtlinie geschrieben", arg)
		}
		if _, err := os.Stat(e.srv.WebOriginsStatePath); err == nil {
			t.Errorf("%q: Zustand geschrieben", arg)
		}
	}
}

func TestSetWebOriginsDeduplicates(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `["http://b:1","http://a:1","http://b:1"]`); err != nil {
		t.Fatal(err)
	}
	pf := readPolicy(t, e.srv.ChromiumPolicyPath)
	if got := pf.URLAllowlist[len(wantFixed):]; !reflect.DeepEqual(got, []string{"http://.a:1", "http://.b:1"}) {
		t.Fatalf("%q", got)
	}
}

func TestApplyWebPolicyOnStart(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `["http://grafana:3000"]`); err != nil {
		t.Fatal(err)
	}
	// Das Abbild beginnt nach dem Neustart mit der Richtlinie aus dem Paket (tmpfs-Overlay).
	if err := os.WriteFile(e.srv.ChromiumPolicyPath, []byte(`{"URLBlocklist":["*"],"URLAllowlist":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.ApplyWebPolicy(); err != nil {
		t.Fatal(err)
	}
	pf := readPolicy(t, e.srv.ChromiumPolicyPath)
	if pf.URLAllowlist[len(pf.URLAllowlist)-1] != "http://.grafana:3000" {
		t.Fatalf("%q", pf.URLAllowlist)
	}
}

func TestApplyWebPolicyWithoutStateLeavesPackagedPolicy(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	orig := `{"URLBlocklist":["*"]}`
	if err := os.WriteFile(e.srv.ChromiumPolicyPath, []byte(orig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.ApplyWebPolicy(); err != nil {
		t.Fatal(err)
	}
	if got := e.read(t, e.srv.ChromiumPolicyPath); got != orig {
		t.Fatalf("verändert: %s", got)
	}
}

func TestApplyWebPolicyWithBadStateWritesFixedOnly(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `["http://grafana:3000"]`); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.srv.WebOriginsStatePath, []byte(`["*"]`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.ApplyWebPolicy(); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if pf := readPolicy(t, e.srv.ChromiumPolicyPath); !reflect.DeepEqual(pf.URLAllowlist, wantFixed) {
		t.Fatalf("Allowlist %q", pf.URLAllowlist)
	}
}

func TestSetWebOriginsRejectsSymlinkedStateDir(t *testing.T) {
	linuxOnly(t)
	e := webEnv(t)
	target := filepath.Join(e.dir, "fremd")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Dir(e.srv.WebOriginsStatePath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Dir(e.srv.WebOriginsStatePath)); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `["http://a:1"]`); err == nil {
		t.Fatal("Fehler erwartet")
	}
	if ents, _ := os.ReadDir(target); len(ents) != 0 {
		t.Fatalf("im Ziel des Verweises geschrieben: %v", ents)
	}
	if _, err := os.Stat(e.srv.ChromiumPolicyPath); err == nil {
		t.Fatal("Richtlinie trotz Fehler geschrieben")
	}
}

func TestPackagedPolicyMatchesHelperWithoutOrigins(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "rootfs", "etc", "chromium", "policies", "managed", "hmi-kiosk.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(ChromiumPolicy(nil)) {
		t.Fatalf("Paket-Richtlinie weicht vom Helfer ab:\n%s\n---\n%s", b, ChromiumPolicy(nil))
	}
}

func TestSetWebOriginsRejectsLoopbackAndTooLong(t *testing.T) {
	linuxOnly(t)
	long := make([]string, 0, 30)
	for i := range 30 {
		long = append(long, fmt.Sprintf("http://%s.%s%02d:1", strings.Repeat("c", 60), strings.Repeat("c", 60), i))
	}
	longJSON, _ := json.Marshal(long)
	for _, arg := range []string{`["http://127.0.0.1:8080"]`, `["http://localhost:3000"]`, string(longJSON)} {
		e := webEnv(t)
		if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, arg); err == nil {
			t.Errorf("%.60q angenommen", arg)
		}
	}
}
