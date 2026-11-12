package selftest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCoreStepsOnlyProtection(t *testing.T) {
	steps := CoreSteps()
	if len(steps) != 1 || steps[0].Name != "schutz" {
		t.Fatalf("Schritte %+v", steps)
	}
	hmi := HMISteps()
	if len(hmi) != len(StepNames)-1 {
		t.Fatalf("%d HMI-Schritte", len(hmi))
	}
	for i, s := range hmi {
		if s.Name != StepNames[i] {
			t.Errorf("Schritt %d: %q, erwartet %q", i, s.Name, StepNames[i])
		}
	}
}

func TestRunStepsOrder(t *testing.T) {
	var order []string
	steps := []Step{
		{"eins", func(context.Context, Deps) StepResult { order = append(order, "eins"); return StepResult{OK: true} }},
		{"zwei", func(context.Context, Deps) StepResult { order = append(order, "zwei"); panic("kaputt") }},
		{"drei", func(context.Context, Deps) StepResult { order = append(order, "drei"); return StepResult{OK: true} }},
	}
	res := RunSteps(context.Background(), Deps{}, steps)
	if strings.Join(order, ",") != "eins,zwei,drei" {
		t.Fatalf("Reihenfolge %v", order)
	}
	if len(res) != 3 || res[0].Name != "eins" || !res[0].OK {
		t.Fatalf("Ergebnis %+v", res)
	}
	if res[1].Name != "zwei" || res[1].OK || !strings.Contains(res[1].Detail, "kaputt") {
		t.Fatalf("Panik nicht gemeldet: %+v", res[1])
	}
	if res[2].Name != "drei" || !res[2].OK {
		t.Fatalf("nächster Schritt: %+v", res[2])
	}
}

// Der Kartenleser gehört nicht zum HMI-Kiosk: dessen sieben Schritte bleiben gleich.
func TestHMIStepsUnchanged(t *testing.T) {
	want := []string{"client-bild", "fenster-leiste", "touch", "rueckkehr", "prozessende", "terminals", "schutz"}
	hmi := HMISteps()
	if len(hmi) != len(want) {
		t.Fatalf("%d HMI-Schritte, erwartet %d", len(hmi), len(want))
	}
	for i, s := range hmi {
		if s.Name != want[i] {
			t.Errorf("Schritt %d: %q, erwartet %q", i, s.Name, want[i])
		}
	}
}

func TestCardStep(t *testing.T) {
	step := CardStep()
	if step.Name != StepCard || StepCard != "kartenleser" {
		t.Fatalf("Name %q", step.Name)
	}
	cases := []struct {
		name   string
		reader func(context.Context) (string, error)
		ok     bool
		detail string
	}{
		{"nicht eingestellt", nil, true, "kein Kartenleser eingestellt"},
		{"Fehler", func(context.Context) (string, error) { return "", errors.New("Gerät fehlt") }, false, "Gerät fehlt"},
		{"gefunden", func(context.Context) (string, error) { return "PN532 v1.6", nil }, true, "PN532 v1.6"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := step.Run(context.Background(), Deps{CardReader: c.reader})
			if r.OK != c.ok || r.Detail != c.detail {
				t.Fatalf("%+v", r)
			}
		})
	}
}

// Ein hängender Leser darf die Selbstprüfung nicht blockieren.
func TestCardStepTimeout(t *testing.T) {
	old := cardProbeTimeout
	cardProbeTimeout = 20 * time.Millisecond
	defer func() { cardProbeTimeout = old }()
	r := CardStep().Run(context.Background(), Deps{CardReader: func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}})
	if r.OK {
		t.Fatalf("%+v", r)
	}
}
