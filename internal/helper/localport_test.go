package helper

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func portEnv(t *testing.T) *env {
	t.Helper()
	e := webEnv(t)
	e.srv.LocalPortStatePath = filepath.Join(e.dir, "data", "system", "local-port")
	return e
}

func fixedFor(port int) []string {
	p, q := strconv.Itoa(port), strconv.Itoa(port+1)
	return []string{"http://127.0.0.1:" + p, "http://localhost:" + p, "http://127.0.0.1:" + q, "http://localhost:" + q}
}

// Ohne eingestellten Port bleibt die Richtlinie wie bisher (8080/8081, sonst nichts).
func TestChromiumPolicyDefaultPortUnchanged(t *testing.T) {
	if string(ChromiumPolicy(nil, false)) != string(ChromiumPolicyFor(8080, nil, false)) {
		t.Fatal("ChromiumPolicy muss dem Grundwert entsprechen")
	}
	if !strings.Contains(string(ChromiumPolicy(nil, false)),
		`"URLAllowlist": ["http://127.0.0.1:8080", "http://localhost:8080", "http://127.0.0.1:8081", "http://localhost:8081"]`) {
		t.Fatalf("Grundwert geändert:\n%s", ChromiumPolicy(nil, false))
	}
}

// Mit Port P erlaubt die Richtlinie genau P und P+1 (je 127.0.0.1 und localhost), nie den Grundwert daneben, nie ein Muster.
func TestChromiumPolicyForPortAllowsOnlyThatPort(t *testing.T) {
	got := string(ChromiumPolicyFor(18080, []string{"http://grafana:3000"}, true))
	want := `"URLAllowlist": ["http://127.0.0.1:18080", "http://localhost:18080", "http://127.0.0.1:18081", "http://localhost:18081", "http://.grafana:3000"]`
	if !strings.Contains(got, want) {
		t.Fatalf("Allowlist:\n%s", got)
	}
	for _, bad := range []string{":8080", ":8081", "*:", "127.0.0.1\"", "http://127.0.0.1\"", "http://localhost\""} {
		if strings.Contains(strings.ReplaceAll(got, `"URLBlocklist": ["*"]`, ""), bad) {
			t.Errorf("%q in der Richtlinie:\n%s", bad, got)
		}
	}
	if !strings.Contains(got, `"URLBlocklist": ["*"]`) {
		t.Error("Sperre fehlt")
	}
}

func TestSetLocalPortWritesStateAndPolicy(t *testing.T) {
	linuxOnly(t)
	e := portEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `["http://grafana:3000"]`); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Dispatch(context.Background(), CmdSetLocalPort, "18080"); err != nil {
		t.Fatal(err)
	}
	if b := e.read(t, e.srv.LocalPortStatePath); strings.TrimSpace(b) != "18080" {
		t.Fatalf("Zustandsdatei %q", b)
	}
	pf := readPolicy(t, e.srv.ChromiumPolicyPath)
	want := append(fixedFor(18080), "http://.grafana:3000")
	if !reflect.DeepEqual(pf.URLAllowlist, want) {
		t.Fatalf("Allowlist\n got %q\nwant %q", pf.URLAllowlist, want)
	}
	// Eine neue Liste der Web-Ursprünge behält den Port.
	if err := e.srv.Dispatch(context.Background(), CmdSetWebOrigins, `[]`); err != nil {
		t.Fatal(err)
	}
	if pf := readPolicy(t, e.srv.ChromiumPolicyPath); !reflect.DeepEqual(pf.URLAllowlist, fixedFor(18080)) {
		t.Fatalf("Port nach set-web-origins verloren: %q", pf.URLAllowlist)
	}
	// Zurück auf den Grundwert.
	if err := e.srv.Dispatch(context.Background(), CmdSetLocalPort, "8080"); err != nil {
		t.Fatal(err)
	}
	if pf := readPolicy(t, e.srv.ChromiumPolicyPath); !reflect.DeepEqual(pf.URLAllowlist, wantFixed) {
		t.Fatalf("Grundwert: %q", pf.URLAllowlist)
	}
}

func TestSetLocalPortRejectsBadValues(t *testing.T) {
	linuxOnly(t)
	for _, arg := range []string{"0", "80", "1023", "65535", "70000", "-1", "x", "8080 ", " 8080", "8e3", "08080x", "0x1f90", "18080\n", "1.5", "+8080"} {
		e := portEnv(t)
		if err := e.srv.Dispatch(context.Background(), CmdSetLocalPort, arg); err == nil {
			t.Errorf("%q angenommen", arg)
		}
		if _, err := os.Stat(e.srv.LocalPortStatePath); err == nil {
			t.Errorf("%q: Zustandsdatei geschrieben", arg)
		}
		if _, err := os.Stat(e.srv.ChromiumPolicyPath); err == nil {
			t.Errorf("%q: Richtlinie geschrieben", arg)
		}
	}
	e := portEnv(t)
	if err := e.srv.Dispatch(context.Background(), CmdSetLocalPort, ""); err == nil {
		t.Error("leerer Wert angenommen")
	}
}

func TestApplyWebPolicyReadsPortState(t *testing.T) {
	linuxOnly(t)
	e := portEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.srv.LocalPortStatePath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Nur der Port ist gespeichert (keine Web-Ursprünge): Die Richtlinie folgt trotzdem dem Port.
	if err := os.WriteFile(e.srv.LocalPortStatePath, []byte("18080\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.ApplyWebPolicy(); err != nil {
		t.Fatal(err)
	}
	if pf := readPolicy(t, e.srv.ChromiumPolicyPath); !reflect.DeepEqual(pf.URLAllowlist, fixedFor(18080)) {
		t.Fatalf("Allowlist %q", pf.URLAllowlist)
	}
}

// Eine beschädigte Zustandsdatei ergibt den Grundwert (und eine Meldung), nie eine offene Richtlinie.
func TestApplyWebPolicyBadPortStateFallsBackToDefault(t *testing.T) {
	linuxOnly(t)
	for _, content := range []string{"x", "80", "99999", "", "8080 8081", "18080x"} {
		e := portEnv(t)
		if err := os.MkdirAll(filepath.Dir(e.srv.LocalPortStatePath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(e.srv.LocalPortStatePath, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := e.srv.ApplyWebPolicy(); err == nil {
			t.Errorf("%q: keine Meldung", content)
		}
		if pf := readPolicy(t, e.srv.ChromiumPolicyPath); !reflect.DeepEqual(pf.URLAllowlist, wantFixed) {
			t.Errorf("%q: Allowlist %q", content, pf.URLAllowlist)
		}
	}
}

// Ohne gespeicherte Ursprünge und ohne gespeicherten Port bleibt die Richtlinie aus dem Paket unberührt.
func TestApplyWebPolicyWithoutStateKeepsPackagePolicy(t *testing.T) {
	linuxOnly(t)
	e := portEnv(t)
	if err := e.srv.ApplyWebPolicy(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.srv.ChromiumPolicyPath); err == nil {
		t.Fatal("Richtlinie ohne gespeicherten Zustand geschrieben")
	}
}

func TestLocalPortStatePathDerivedFromWebOrigins(t *testing.T) {
	if got := LocalPortStatePathFor("/data/system/web-origins"); got != "/data/system/local-port" {
		t.Fatalf("%q", got)
	}
}
