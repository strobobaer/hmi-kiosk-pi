package card

import (
	"reflect"
	"testing"
	"time"
)

var (
	t0   = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	k1   = keyEvent{Scan: 2}             // Taste 1, Druck
	k1up = keyEvent{Scan: 2, Up: true}   // Taste 1, Loslassen
	kA   = keyEvent{Scan: 30}            // Taste A
	kEnt = keyEvent{Scan: 28}            // Enter
	kPad = keyEvent{Scan: 28, Ext: true} // Ziffernblock-Enter
	kX   = keyEvent{Scan: 45}            // X: keine Kartentaste
)

func hook(k keyEvent) hookEvent { return hookEvent{keyEvent: k} }

func raw(k keyEvent, dev uintptr, reader bool, at time.Time) rawEvent {
	return rawEvent{keyEvent: k, Dev: dev, Reader: reader, At: at}
}

func kinds(acts []action) []actionKind {
	var out []actionKind
	for _, a := range acts {
		out = append(out, a.Kind)
	}
	return out
}

// Der Hook kommt zuerst (gemessen): Die Taste wird gehalten, das Raw-Ereignis des Lesers entscheidet.
func TestGateReaderKeySwallowed(t *testing.T) {
	var g gate
	block, acts := g.Hook(hook(k1), t0)
	if !block || len(acts) != 0 {
		t.Fatalf("Hook: block=%v acts=%v, erwartet halten", block, acts)
	}
	acts = g.Raw(raw(k1, 7, true, t0.Add(time.Millisecond)), t0.Add(time.Millisecond))
	if len(acts) != 1 || acts[0].Kind != actDrop || acts[0].Key != k1 || acts[0].Dev != 7 {
		t.Fatalf("Leser-Taste muss verschluckt werden: %+v", acts)
	}
	if _, ok := g.Deadline(); ok {
		t.Error("nach der Entscheidung darf nichts mehr warten")
	}
}

func TestGateOtherDeviceReplayed(t *testing.T) {
	var g gate
	g.Hook(hook(kA), t0)
	acts := g.Raw(raw(kA, 9, false, t0), t0.Add(time.Millisecond))
	if len(acts) != 1 || acts[0].Kind != actReplay || acts[0].Key != kA {
		t.Fatalf("Taste eines anderen Geraets muss wiederholt werden: %+v", acts)
	}
}

func TestGateTimeoutReplays(t *testing.T) {
	var g gate
	g.Hook(hook(k1), t0)
	if at, ok := g.Deadline(); !ok || !at.Equal(t0.Add(holdMax)) {
		t.Fatalf("Frist %v %v", at, ok)
	}
	if acts := g.Tick(t0.Add(holdMax - time.Millisecond)); len(acts) != 0 {
		t.Fatalf("zu frueh: %+v", acts)
	}
	acts := g.Tick(t0.Add(holdMax))
	if len(acts) != 1 || acts[0].Kind != actReplay {
		t.Fatalf("Zeitablauf muss wiederholen: %+v", acts)
	}
	if _, ok := g.Deadline(); ok {
		t.Error("Warteschlange muss leer sein")
	}
}

// Raw kommt zuerst (Hardware, nicht gemessen): sofort entscheiden, ohne zu halten.
func TestGateRawBeforeHook(t *testing.T) {
	var g gate
	g.Raw(raw(k1, 7, true, t0), t0)
	block, acts := g.Hook(hook(k1), t0.Add(2*time.Millisecond))
	if !block || len(acts) != 1 || acts[0].Kind != actDrop {
		t.Fatalf("Leser: block=%v acts=%+v", block, acts)
	}
	g.Raw(raw(kA, 9, false, t0), t0)
	block, acts = g.Hook(hook(kA), t0.Add(2*time.Millisecond))
	if block || len(acts) != 0 {
		t.Fatalf("andere Tastatur muss unberuehrt durchlaufen: block=%v acts=%+v", block, acts)
	}
}

// Ein altes Raw-Ereignis (mehr als 50 ms) gilt nicht mehr.
func TestGateStaleRawIgnored(t *testing.T) {
	var g gate
	g.Raw(raw(k1, 7, true, t0), t0)
	block, acts := g.Hook(hook(k1), t0.Add(80*time.Millisecond))
	if !block || len(acts) != 0 {
		t.Fatalf("altes Raw darf nicht entscheiden: block=%v acts=%+v", block, acts)
	}
}

// Druck und Loslassen sowie Enter und Ziffernblock-Enter sind verschiedene Ereignisse.
func TestGateMakeBreakAndExtDistinct(t *testing.T) {
	var g gate
	g.Hook(hook(k1up), t0)
	if acts := g.Raw(raw(k1, 7, true, t0), t0); len(acts) != 0 {
		t.Fatalf("Druck darf Loslassen nicht entscheiden: %+v", acts)
	}
	g = gate{}
	g.Hook(hook(kEnt), t0)
	if acts := g.Raw(raw(kPad, 7, true, t0), t0); len(acts) != 0 {
		t.Fatalf("Ziffernblock-Enter darf Enter nicht entscheiden: %+v", acts)
	}
}

// Gemischte Folge: Die Reihenfolge der echten Tastatur bleibt, auch wenn das Raw-Ereignis des
// spaeteren Leser-Zeichens zuerst eintrifft.
func TestGateOrderPreserved(t *testing.T) {
	var g gate
	g.Hook(hook(kA), t0)                         // echte Tastatur
	g.Hook(hook(k1), t0.Add(time.Millisecond))   // Leser
	g.Hook(hook(kA), t0.Add(2*time.Millisecond)) // echte Tastatur
	if acts := g.Raw(raw(k1, 7, true, t0), t0.Add(3*time.Millisecond)); len(acts) != 0 {
		t.Fatalf("spaetere Taste darf nicht vorbei: %+v", acts)
	}
	acts := g.Raw(raw(kA, 9, false, t0), t0.Add(4*time.Millisecond))
	want := []actionKind{actReplay, actDrop}
	if !reflect.DeepEqual(kinds(acts), want) || acts[0].Key != kA || acts[1].Key != k1 {
		t.Fatalf("Reihenfolge: %+v, erwartet A wiederholt, dann 1 verschluckt", acts)
	}
	acts = g.Raw(raw(kA, 9, false, t0), t0.Add(5*time.Millisecond))
	if len(acts) != 1 || acts[0].Kind != actReplay {
		t.Fatalf("zweites A: %+v", acts)
	}
}

// Ein Zeitablauf der vorderen Taste gibt die dahinter entschiedenen frei.
func TestGateTimeoutReleasesQueue(t *testing.T) {
	var g gate
	g.Hook(hook(kA), t0)
	g.Hook(hook(k1), t0.Add(time.Millisecond))
	g.Raw(raw(k1, 7, true, t0), t0.Add(2*time.Millisecond))
	acts := g.Tick(t0.Add(holdMax))
	if !reflect.DeepEqual(kinds(acts), []actionKind{actReplay, actDrop}) {
		t.Fatalf("%+v", acts)
	}
}

// Eigene Wiederholungen (Markierung), fremd eingespielte Tasten und alles ausser Kartentasten laufen
// immer durch, auch hinter einer gehaltenen Taste.
func TestGatePassesMarkedInjectedAndOtherKeys(t *testing.T) {
	var g gate
	check := func(where string) {
		t.Helper()
		for name, h := range map[string]hookEvent{
			"markiert":          {keyEvent: k1, Marked: true},
			"eingespielt":       {keyEvent: k1, Injected: true},
			"keine Kartentaste": hook(kX),
		} {
			if block, acts := g.Hook(h, t0); block || len(acts) != 0 {
				t.Errorf("%s, %s: block=%v acts=%v", where, name, block, acts)
			}
		}
	}
	check("leer")
	g.Hook(hook(k1), t0)
	check("hinter Warteschlange")
}

func TestKeyCode(t *testing.T) {
	cases := []struct {
		k    keyEvent
		want uint16
		ok   bool
	}{
		{keyEvent{Scan: 2}, 2, true},
		{keyEvent{Scan: 11}, 11, true},
		{keyEvent{Scan: 30}, 30, true},
		{keyEvent{Scan: 33}, 33, true},
		{keyEvent{Scan: 28}, 28, true},
		{keyEvent{Scan: 28, Ext: true}, keyKPEnt, true},
		{keyEvent{Scan: 71}, 71, true},
		{keyEvent{Scan: 82}, 82, true},
		{keyEvent{Scan: 71, Ext: true}, 0, false}, // Pos1 der Navigationsgruppe
		{keyEvent{Scan: 34}, 0, false},            // G
		{keyEvent{Scan: 1}, 0, false},             // Esc
	}
	for _, c := range cases {
		got, ok := keyCode(c.k)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%+v: %d %v, erwartet %d %v", c.k, got, ok, c.want, c.ok)
		}
	}
	// Alle Zeichen einer UID sind Kartentasten; Umschalt ebenfalls (Leser tippen Grossbuchstaben so).
	for _, s := range []uint16{2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 30, 48, 46, 32, 18, 33, 28, 42, 54, 71, 72, 73, 75, 76, 77, 79, 80, 81, 82} {
		if !isCandidate(keyEvent{Scan: s}) {
			t.Errorf("Scancode %d muss Kartentaste sein", s)
		}
	}
}

func TestDeviceMatchAfterReplug(t *testing.T) {
	old := InputDevice{Path: `\\?\hid#vid_1a2b&pid_3c4d#7&aa&0&0000#{x}`, Name: "SYC ID&IC Card Reader", ID: "1a2b:3c4d"}
	neu := InputDevice{Path: `\\?\hid#vid_1a2b&pid_3c4d#8&bb&0&0000#{x}`, Name: "SYC ID&IC Card Reader", ID: "1a2b:3c4d"}
	other := InputDevice{Path: `\\?\hid#vid_046d&pid_c31c#9&cc&0&0000#{x}`, Name: "Logitech Keyboard", ID: "046d:c31c"}
	for _, sel := range []string{"1a2b:3c4d", "1A2B:3C4D", "syc id"} {
		if !deviceMatches(sel, old) || !deviceMatches(sel, neu) {
			t.Errorf("%q: Leser vor und nach dem Wiederanstecken muss passen", sel)
		}
		if deviceMatches(sel, other) {
			t.Errorf("%q passt zur falschen Tastatur", sel)
		}
	}
	if deviceMatches("", old) {
		t.Error("leere Auswahl passt zu nichts")
	}
}
