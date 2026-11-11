package selftest

import (
	"context"
	"strings"
	"testing"
)

func TestCoreStepsOnlyProtection(t *testing.T) {
	steps := CoreSteps()
	if len(steps) != 1 || steps[0].Name != "schutz" {
		t.Fatalf("Schritte %+v", steps)
	}
	hmi := HMISteps()
	if len(hmi) != len(StepNames) {
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
