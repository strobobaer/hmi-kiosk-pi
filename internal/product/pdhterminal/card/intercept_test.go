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

func newG() *gate { return &gate{Active: true} }

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

// scanOf: Scancodes (Satz 1) der Zeichen einer UID, 0-9 und A-F.
var scanOf = map[byte]uint16{
	'1': 2, '2': 3, '3': 4, '4': 5, '5': 6, '6': 7, '7': 8, '8': 9, '9': 10, '0': 11,
	'A': 30, 'B': 48, 'C': 46, 'D': 32, 'E': 18, 'F': 33,
}

type run struct {
	acts  []action
	block []bool
	end   time.Time
}

// typeStr spielt Druck und Loslassen der Zeichen mit dem Abstand gap zwischen den Drücken ein und
// sammelt, was der Haken blockiert (block) und was daraus folgt (acts). Enter folgt, wenn enter gesetzt ist.
func typeStr(g *gate, start time.Time, s string, gap time.Duration, enter bool) run {
	var r run
	at := start
	send := func(k keyEvent, when time.Time) {
		b, a := g.Hook(hook(k), when)
		r.block = append(r.block, b)
		r.acts = append(r.acts, a...)
	}
	var keys []keyEvent
	for i := 0; i < len(s); i++ {
		keys = append(keys, keyEvent{Scan: scanOf[s[i]]})
	}
	if enter {
		keys = append(keys, kEnt)
	}
	for _, k := range keys {
		send(k, at)
		up := k
		up.Up = true
		send(up, at.Add(5*time.Millisecond))
		at = at.Add(gap)
	}
	r.end = at
	return r
}

func count(acts []action, k actionKind) int {
	n := 0
	for _, a := range acts {
		if a.Kind == k {
			n++
		}
	}
	return n
}

// Der Leser tippt schnell (hier 8 ms): Hexzeichen oder Ziffern und Enter werden verschluckt, alle Tasten.
func TestGateReaderBurstSwallowed(t *testing.T) {
	for name, uid := range map[string]string{"8 hex": "04A1B2C3", "14 hex": "04A1B2C3D4E5F6", "20 hex": "04A1B2C3D4E5F6A1B2C3", "10 dez": "0123456789"} {
		g := newG()
		r := typeStr(g, t0, uid, 8*time.Millisecond, true)
		for i, b := range r.block {
			if !b {
				t.Fatalf("%s: Ereignis %d nicht blockiert", name, i)
			}
		}
		if count(r.acts, actReplay) != 0 || count(r.acts, actDrop) != 2*len(uid)+1 { // das Loslassen von Enter folgt ohne Folge
			t.Fatalf("%s: %d verworfen, %d wiederholt; erwartet %d und 0", name, count(r.acts, actDrop), count(r.acts, actReplay), 2*len(uid)+1)
		}
		if _, ok := g.Deadline(); ok || len(g.queue) != 0 || len(g.ups) != 0 {
			t.Errorf("%s: Zustand nicht leer: %+v", name, g)
		}
	}
}

// Ein Mensch tippt (150 ms Abstand): Jede Taste wird nach höchstens gapMax in Reihenfolge wiederholt.
func TestGateHumanSpeedReplayedInOrder(t *testing.T) {
	g := newG()
	at := t0
	var got []keyEvent
	for _, k := range []keyEvent{k1, kA, {Scan: 3}} {
		_, acts := g.Hook(hook(k), at)
		up := k
		up.Up = true
		_, acts2 := g.Hook(hook(up), at.Add(5*time.Millisecond))
		if dl, ok := g.Deadline(); !ok || dl.Sub(at) != gapMax {
			t.Fatalf("Frist %v %v", dl, ok)
		}
		acts3 := g.Tick(at.Add(gapMax))
		for _, a := range append(append(acts, acts2...), acts3...) {
			if a.Kind != actReplay {
				t.Fatalf("Mensch darf nie verschluckt werden: %+v", a)
			}
			got = append(got, a.Key)
		}
		at = at.Add(150 * time.Millisecond)
	}
	want := []keyEvent{k1, k1up, kA, {Scan: 30, Up: true}, {Scan: 3}, {Scan: 3, Up: true}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Reihenfolge %+v, erwartet %+v", got, want)
	}
}

func TestGateMixedGapReplayed(t *testing.T) {
	g := newG()
	// 4 Zeichen schnell, dann Pause über 40 ms, dann 4 Zeichen und Enter: keine Leserfolge.
	r1 := typeStr(g, t0, "04A1", 8*time.Millisecond, false)
	r2 := typeStr(g, r1.end.Add(60*time.Millisecond), "B2C3", 8*time.Millisecond, true)
	acts := append(r1.acts, r2.acts...)
	if count(acts, actDrop) != 0 {
		t.Fatalf("verschluckt trotz Pause: %+v", kinds(acts))
	}
	if count(acts, actReplay) != 17 {
		t.Fatalf("%d wiederholt, erwartet 17 (alle bis auf das Loslassen von Enter, das unberührt folgt)", count(acts, actReplay))
	}
	for i := 0; i < 8; i++ { // Reihenfolge der Eingabe
		if want := []uint16{11, 5, 30, 2}[i/2]; acts[i].Key.Scan != want {
			t.Fatalf("Reihenfolge verletzt bei %d: %+v", i, acts[i])
		}
	}
}

func TestGateWrongLengthReplayed(t *testing.T) {
	g := newG()
	r := typeStr(g, t0, "04A1B2", 8*time.Millisecond, true) // 6 Zeichen
	if count(r.acts, actDrop) != 0 || count(r.acts, actReplay) != 13 {
		t.Fatalf("falsche Länge: %d verworfen, %d wiederholt", count(r.acts, actDrop), count(r.acts, actReplay))
	}
	// 10 Zeichen mit Buchstaben sind weder Dezimal noch eine Hexlänge
	g = newG()
	r = typeStr(g, t0, "04A1B2C3D4", 8*time.Millisecond, true)
	if count(r.acts, actDrop) != 0 {
		t.Fatalf("10 Zeichen mit Buchstaben dürfen nicht gelten")
	}
}

func TestGateTooLongReplayed(t *testing.T) {
	g := newG()
	r := typeStr(g, t0, "04A1B2C3D4E5F6A1B2C3D4", 8*time.Millisecond, true) // 22 Zeichen
	if count(r.acts, actDrop) != 0 {
		t.Fatalf("zu lange Folge verschluckt")
	}
	if len(g.queue) > maxQueue {
		t.Fatalf("Warteschlange unbegrenzt: %d", len(g.queue))
	}
}

func TestGateNonCandidateInBetweenReplays(t *testing.T) {
	g := newG()
	g.Hook(hook(k1), t0)
	block, acts := g.Hook(hook(kX), t0.Add(5*time.Millisecond))
	if block || len(acts) != 1 || acts[0].Kind != actReplay || acts[0].Key != k1 {
		t.Fatalf("andere Taste: block=%v acts=%+v, erwartet 1 zuerst wiederholt, dann X unberührt", block, acts)
	}
}

func TestGatePassesMarkedAndInjectedKeys(t *testing.T) {
	g := newG()
	check := func(where string) {
		t.Helper()
		for name, h := range map[string]hookEvent{
			"markiert":    {keyEvent: k1, Marked: true},
			"eingespielt": {keyEvent: k1, Injected: true},
		} {
			if block, acts := g.Hook(h, t0); block || len(acts) != 0 {
				t.Errorf("%s, %s: block=%v acts=%v", where, name, block, acts)
			}
		}
	}
	check("leer")
	g.Hook(hook(k1), t0)
	check("hinter Warteschlange")
	// Eine ganze Leserfolge, als eingespielt (Passwortmanager) wird nie verschluckt.
	g = newG()
	at := t0
	for _, c := range []byte("04A1B2C3") {
		for _, up := range []bool{false, true} {
			if b, a := g.Hook(hookEvent{keyEvent: keyEvent{Scan: scanOf[c], Up: up}, Injected: true}, at); b || len(a) != 0 {
				t.Fatalf("eingespielte Folge angefasst")
			}
		}
		at = at.Add(8 * time.Millisecond)
	}
}

func TestGateInactiveWhenReaderNotAttached(t *testing.T) {
	g := &gate{}
	r := typeStr(g, t0, "04A1B2C3", 8*time.Millisecond, true)
	for i, b := range r.block {
		if b {
			t.Fatalf("Ereignis %d blockiert, obwohl kein Leser angesteckt ist", i)
		}
	}
	if len(r.acts) != 0 {
		t.Fatalf("Folgen ohne Leser: %+v", r.acts)
	}
	// Abziehen mitten in einer Folge gibt Gehaltenes frei.
	g = newG()
	g.Hook(hook(k1), t0)
	if acts := g.SetActive(false); len(acts) != 1 || acts[0].Kind != actReplay {
		t.Fatalf("SetActive(false): %+v", acts)
	}
}

// Kommt für eine gehaltene Taste doch ein Raw-Ereignis, entscheidet das Gerät.
func TestGateRawDeviceDecides(t *testing.T) {
	g := newG()
	g.Hook(hook(k1), t0)
	acts := g.Raw(raw(k1, 7, true, t0), t0.Add(time.Millisecond))
	if len(acts) != 1 || acts[0].Kind != actDrop || acts[0].Dev != 7 {
		t.Fatalf("Leser: %+v", acts)
	}
	// ... das Loslassen der verschluckten Taste auch
	if block, acts := g.Hook(hook(k1up), t0.Add(5*time.Millisecond)); !block || len(acts) != 0 {
		t.Fatalf("Loslassen: block=%v acts=%+v", block, acts)
	}
	// andere Tastatur: sofort wiederholt, ohne gapMax abzuwarten
	g = newG()
	g.Hook(hook(kA), t0)
	acts = g.Raw(raw(kA, 9, false, t0), t0.Add(time.Millisecond))
	if len(acts) != 1 || acts[0].Kind != actReplay {
		t.Fatalf("andere Tastatur: %+v", acts)
	}
}

func TestGateRawBeforeHook(t *testing.T) {
	g := newG()
	g.Raw(raw(k1, 7, true, t0), t0)
	block, acts := g.Hook(hook(k1), t0.Add(2*time.Millisecond))
	if !block || len(acts) != 1 || acts[0].Kind != actDrop {
		t.Fatalf("Leser: block=%v acts=%+v", block, acts)
	}
	g.Raw(raw(kA, 9, false, t0), t0)
	block, acts = g.Hook(hook(kA), t0.Add(2*time.Millisecond))
	if block || len(acts) != 0 {
		t.Fatalf("andere Tastatur: block=%v acts=%+v", block, acts)
	}
	// altes Raw (über 50 ms) entscheidet nichts
	g = newG()
	g.Raw(raw(k1, 7, true, t0), t0)
	if block, acts := g.Hook(hook(k1), t0.Add(80*time.Millisecond)); !block || len(acts) != 0 {
		t.Fatalf("altes Raw: block=%v acts=%+v", block, acts)
	}
}

// Ein Raw-Ereignis einer anderen Tastatur: Gerät schlägt Zeitmuster, auch bei schneller Folge.
func TestGateRawOtherBeatsPattern(t *testing.T) {
	g := newG()
	at := t0
	var acts []action
	for _, c := range []byte("04A1B2C3") {
		g.Raw(raw(keyEvent{Scan: scanOf[c]}, 9, false, at), at)
		_, a := g.Hook(hook(keyEvent{Scan: scanOf[c]}), at)
		acts = append(acts, a...)
		at = at.Add(8 * time.Millisecond)
	}
	g.Raw(raw(kEnt, 9, false, at), at)
	_, a := g.Hook(hook(kEnt), at)
	acts = append(acts, a...)
	if count(acts, actDrop) != 0 {
		t.Fatalf("Tastatur 9 darf nicht verschluckt werden: %+v", acts)
	}
}

func TestGateNumpadEnterEndsBurst(t *testing.T) {
	g := newG()
	r := typeStr(g, t0, "04A1B2C3", 8*time.Millisecond, false)
	b, a := g.Hook(hook(kPad), r.end)
	if !b || count(a, actDrop) != 17 {
		t.Fatalf("Ziffernblock-Enter: block=%v %d verworfen, erwartet 17", b, count(a, actDrop))
	}
}

func TestKeyCode(t *testing.T) {
	cases := []struct {
		k    keyEvent
		want uint16
		ok   bool
	}{
		{keyEvent{Scan: 2}, 2, true}, {keyEvent{Scan: 11}, 11, true}, {keyEvent{Scan: 30}, 30, true},
		{keyEvent{Scan: 33}, 33, true}, {keyEvent{Scan: 28}, 28, true},
		{keyEvent{Scan: 28, Ext: true}, keyKPEnt, true},
		{keyEvent{Scan: 71}, 71, true}, {keyEvent{Scan: 82}, 82, true},
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
}

func TestUIDChars(t *testing.T) {
	for s, want := range map[string]bool{
		"04A1B2C3": true, "04A1B2C3D4E5F6": true, "04A1B2C3D4E5F6A1B2C3": true, "0123456789": true,
		"04A1B2C3D4": false, "12345": false, "": false, "04A1B2C": false, "04A1B2C3D4E5F6A1B2C3D4": false,
	} {
		if got := uidChars([]byte(s)); got != want {
			t.Errorf("%q: %v, erwartet %v", s, got, want)
		}
	}
}

func TestLearnerBurst(t *testing.T) {
	var l learner
	at := t0
	for _, c := range []byte("04A1B2C3") {
		l.Key(5, keyEvent{Scan: scanOf[c]}, at)
		l.Key(5, keyEvent{Scan: scanOf[c], Up: true}, at.Add(3*time.Millisecond))
		at = at.Add(8 * time.Millisecond)
	}
	dev, text, done := l.Key(5, kEnt, at)
	if !done || dev != 5 || text != "04A1B2C3" {
		t.Fatalf("%d %q %v", dev, text, done)
	}
	// Mensch (Pause) oder gemischte Geräte: nichts
	l = learner{}
	l.Key(5, k1, t0)
	l.Key(5, kA, t0.Add(100*time.Millisecond))
	if _, _, done := l.Key(5, kEnt, t0.Add(110*time.Millisecond)); done {
		t.Error("Pause muss die Folge verwerfen")
	}
	l = learner{}
	for i, c := range []byte("04A1B2C3") {
		l.Key(uintptr(1+i%2), keyEvent{Scan: scanOf[c]}, t0.Add(time.Duration(i)*5*time.Millisecond))
	}
	if _, _, done := l.Key(1, kEnt, t0.Add(50*time.Millisecond)); done {
		t.Error("zwei Geräte gemischt gelten nicht")
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

// typeShift tippt wie ein Leser, der jeden Buchstaben mit eigenem Umschalt-Druck setzt (Umschalt hinunter,
// Zeichen hinunter, Zeichen hoch, Umschalt hoch); gap ist der Abstand zwischen den Drücken.
func typeShift(g *gate, start time.Time, s string, gap time.Duration) run {
	var r run
	at := start
	send := func(k keyEvent, when time.Time) {
		b, a := g.Hook(hook(k), when)
		r.block = append(r.block, b)
		r.acts = append(r.acts, a...)
	}
	shift := keyEvent{Scan: 42}
	var keys []keyEvent
	for i := 0; i < len(s); i++ {
		keys = append(keys, keyEvent{Scan: scanOf[s[i]]})
	}
	keys = append(keys, kEnt)
	for _, k := range keys {
		letter := k.Scan == 30 || k.Scan == 48 || k.Scan == 46 || k.Scan == 32 || k.Scan == 18 || k.Scan == 33
		if letter {
			send(shift, at)
			at = at.Add(gap / 2)
		}
		send(k, at)
		up := k
		up.Up = true
		send(up, at.Add(4*time.Millisecond))
		if letter {
			send(keyEvent{Scan: 42, Up: true}, at.Add(6*time.Millisecond))
		}
		at = at.Add(gap)
	}
	r.end = at
	return r
}

// Ein Leser, der jeden Großbuchstaben mit eigenem Umschalt-Druck tippt, bleibt eine Leserfolge: Umschalt
// zählt nicht gegen die Länge (sonst liefen 14 und 20 Zeichen über die Grenze und würden durchgereicht).
func TestGateShiftWrappedBurstSwallowed(t *testing.T) {
	for name, uid := range map[string]string{
		"8 hex": "ABCDEF12", "14 hex": "ABCDEFABCDEF12", "20 hex": "ABCDEFABCDEFABCDEFAB",
	} {
		g := newG()
		r := typeShift(g, t0, uid, 16*time.Millisecond)
		for i, b := range r.block {
			if !b {
				t.Fatalf("%s: Ereignis %d nicht blockiert", name, i)
			}
		}
		if count(r.acts, actReplay) != 0 {
			t.Fatalf("%s: %d Ereignisse wiederholt, erwartet keines", name, count(r.acts, actReplay))
		}
		if count(r.acts, actDrop) == 0 || len(g.queue) != 0 {
			t.Fatalf("%s: nicht verschluckt (%d verworfen, Warteschlange %d)", name, count(r.acts, actDrop), len(g.queue))
		}
	}
}

// Schlägt das Wiederholen fehl (Vordergrundfenster mit höheren Rechten), wird nichts mehr gehalten, bis der
// Aufrufer Bypass zurücknimmt.
func TestGateExecFailsOpen(t *testing.T) {
	g := newG()
	g.Hook(hook(k1), t0)
	g.Hook(hook(kA), t0.Add(5*time.Millisecond))
	acts := g.Tick(t0.Add(time.Second))
	fail := func(keyEvent) bool { return false }
	if !g.Exec(acts, fail, func(action) {}) {
		t.Fatal("Fehlschlag wurde nicht gemeldet")
	}
	if !g.Bypass || len(g.queue) != 0 {
		t.Fatalf("Bypass=%v Warteschlange=%d", g.Bypass, len(g.queue))
	}
	// jetzt läuft alles durch, auch eine schnelle UID-Folge
	r := typeStr(g, t0.Add(2*time.Second), "04A1B2C3", 8*time.Millisecond, true)
	for i, b := range r.block {
		if b {
			t.Fatalf("Ereignis %d blockiert trotz Bypass", i)
		}
	}
	// anderes Vordergrundfenster: wieder wie vorher
	g.Bypass = false
	if b, _ := g.Hook(hook(k1), t0.Add(3*time.Second)); !b {
		t.Fatal("nach Bypass-Ende muss wieder gehalten werden")
	}
	// gelingt das Wiederholen, bleibt alles wie es ist
	g = newG()
	g.Hook(hook(k1), t0)
	ok := func(keyEvent) bool { return true }
	if g.Exec(g.Tick(t0.Add(time.Second)), ok, func(action) {}) || g.Bypass {
		t.Fatal("Bypass trotz erfolgreichem Wiederholen")
	}
}
