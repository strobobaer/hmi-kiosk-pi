package card

import "time"

// Abfangen der Leser-Tasten unter Windows (plattformneutral, damit die Entscheidung überall getestet wird).
//
// Der Tastaturhaken (WH_KEYBOARD_LL) sagt nicht, von welchem Gerät eine Taste kommt; das sagt nur
// Raw Input (WM_INPUT). Gemessen kommt der Haken zuerst, und eine vom Haken geschluckte Taste
// erzeugt kein WM_INPUT. Darum entscheidet ein Zeitmuster, mit dem Raw-Ereignis als Abkürzung:
//
//   - Eine Kartentaste wird gehalten (Haken liefert 1). Solange die Abstände aufeinanderfolgender
//     Tastendrücke höchstens gapMax betragen, wächst die Folge.
//   - Endet sie mit Enter und hat die Länge einer UID (8, 14 oder 20 Hexzeichen oder 10 Ziffern),
//     ist es der Leser: Alles wird verschluckt und der Karte zugeführt.
//   - Alles andere (Pause über gapMax, falsche Länge, andere Taste dazwischen, zu lang) wird sofort
//     in der ursprünglichen Reihenfolge mit Markierung wiederholt. Ein Mensch wird so je Taste um
//     höchstens gapMax verzögert.
//   - Kommt für eine gehaltene Taste doch ein Raw-Ereignis (echte Hardware kann sich anders
//     verhalten), entscheidet das Gerät.
//
// Verschluckt wird nur, solange der gewählte Leser angesteckt ist (Active), und nie eine
// eingespielte Taste (Passwortmanager, Fernwartung) oder eine eigene Wiederholung.

const (
	// gapMax ist der größte Abstand zweier Tastendrücke innerhalb einer Leserfolge.
	gapMax = 20 * time.Millisecond
	// rawMax ist, wie lange ein vorgemerktes Raw-Ereignis auf seinen Haken wartet.
	rawMax = 50 * time.Millisecond
	// maxDowns begrenzt die Folge: 20 Hexzeichen und Enter.
	maxDowns = 21
	maxRaws  = 16
)

// keyEvent ist ein Tastenereignis: Scancode (Satz 1), E0-Erweiterung, Loslassen.
type keyEvent struct {
	Scan    uint16
	Ext, Up bool
}

// hookEvent ist ein Ereignis des Tastaturhakens. Marked sind Tasten, die dieses Programm selbst
// wiederholt; Injected sind von anderen Programmen eingespielte.
type hookEvent struct {
	keyEvent
	Marked, Injected bool
}

// rawEvent ist ein Raw-Input-Ereignis. Reader sagt, ob das Gerät ein zu fangendes ist.
type rawEvent struct {
	keyEvent
	Dev    uintptr
	Reader bool
	At     time.Time
}

type holdState uint8

const (
	holdPending holdState = iota // Gerät unbekannt
	holdReader                   // vom Leser: verwerfen
	holdOther                    // von anderer Tastatur: wiederholen
)

type heldKey struct {
	keyEvent
	Dev   uintptr
	At    time.Time
	state holdState
}

type actionKind uint8

const (
	actDrop   actionKind = iota + 1 // verschluckt: Taste des Lesers, der Karte zuführen
	actReplay                       // mit Markierung neu einspielen
)

type action struct {
	Kind actionKind
	Key  keyEvent
	Dev  uintptr // 0: Gerät unbekannt (Zeitmuster)
	At   time.Time
}

// gate ist die Entscheidung, rein und ohne Uhr: alle Zeitpunkte kommen von außen. Nicht nebenläufig;
// der Aufrufer ruft sie aus genau einem Thread.
type gate struct {
	// Active: Der gewählte Leser ist angesteckt und es wird nicht angelernt. Sonst läuft alles durch.
	Active bool

	queue    []heldKey
	raws     []rawEvent
	lastDown time.Time
	downs    int
	chars    []byte
	// ups sind verschluckte Tasten, deren Loslassen noch aussteht.
	ups map[keyEvent]bool
}

// scanCandidates sind die Scancodes (ohne E0), die eine UID tippen: Ziffern, A-F, Enter,
// Ziffernblock und Umschalt (Leser tippen Großbuchstaben mit Umschalt).
var scanCandidates = map[uint16]bool{
	2: true, 3: true, 4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true, 11: true,
	30: true, 48: true, 46: true, 32: true, 18: true, 33: true, // A B C D E F
	28: true, // Enter
	71: true, 72: true, 73: true, 75: true, 76: true, 77: true, 79: true, 80: true, 81: true, 82: true,
	42: true, 54: true, // Umschalt links, rechts
}

const scanEnter = 28

// isCandidate: Nur Kartentasten werden je gehalten, alles andere läuft unberührt durch.
func isCandidate(k keyEvent) bool {
	if k.Ext {
		return k.Scan == scanEnter // Ziffernblock-Enter
	}
	return scanCandidates[k.Scan]
}

func isEnter(k keyEvent) bool { return k.Scan == scanEnter }

// keyCode bildet das Ereignis auf den Tastencode des keyBuffer ab: Hauptblock und Ziffernblock
// haben unter Windows (Satz 1) dieselben Werte wie unter Linux; Ziffernblock-Enter wird 96.
func keyCode(k keyEvent) (uint16, bool) {
	if !isCandidate(k) {
		return 0, false
	}
	if k.Ext {
		return keyKPEnt, true
	}
	return k.Scan, true
}

// uidChars: Hat die Zeichenfolge die Länge einer UID (8, 14, 20 Hexzeichen oder 10 Ziffern)?
func uidChars(s []byte) bool {
	digits, hexOK := true, true
	for _, c := range s {
		if c < '0' || c > '9' {
			digits = false
		}
		if (c < '0' || c > '9') && (c < 'A' || c > 'F') {
			hexOK = false
		}
	}
	switch len(s) {
	case 10:
		return digits
	case 8, 14, 20:
		return hexOK
	}
	return false
}

// deviceMatches sagt, ob ein Gerät zur Auswahl (vvvv:pppp oder Namensteil) gehört. Das Handle
// kommt nicht vor: Nach dem Wiederanstecken ist es neu, Kennung und Name bleiben.
func deviceMatches(sel string, d InputDevice) bool {
	return len(matchDevices([]InputDevice{d}, sel)) > 0
}

// SetActive schaltet das Abfangen ein oder aus; beim Ausschalten werden gehaltene Tasten wiederholt.
func (g *gate) SetActive(a bool) []action {
	g.Active = a
	if a {
		return nil
	}
	g.ups = nil
	return g.flush()
}

// Hook behandelt ein Ereignis des Tastaturhakens zum Zeitpunkt now. block ist die Antwort des
// Hakens (true: nicht weiterreichen); acts sind Folgen daraus, vor der Antwort auszuführen.
func (g *gate) Hook(h hookEvent, now time.Time) (block bool, acts []action) {
	if h.Marked || h.Injected || !g.Active {
		return false, nil
	}
	if !isCandidate(h.keyEvent) {
		// Eine andere Taste beendet jede Folge: Gehaltenes geht vor ihr hinaus.
		return false, g.flush()
	}
	if h.Up {
		down := h.keyEvent
		down.Up = false
		if g.ups[down] && !g.pendingDown(down) {
			delete(g.ups, down)
			return true, nil // Loslassen einer verschluckten Taste
		}
	} else if len(g.queue) > 0 && (now.Sub(g.lastDown) > gapMax || g.downs >= maxDowns) {
		acts = g.flush() // Pause oder zu lang: Die Folge war keine Karte
	}

	st, dev := holdPending, uintptr(0)
	if r, ok := g.takeRaw(h.keyEvent, now); ok {
		st, dev = holdOther, r.Dev
		if r.Reader {
			st = holdReader
		}
	}
	if len(g.queue) == 0 {
		switch {
		case st == holdOther:
			return false, acts
		case st == holdReader:
			g.markUp(h.keyEvent)
			return true, append(acts, action{Kind: actDrop, Key: h.keyEvent, Dev: dev, At: now})
		case h.Up:
			return false, acts // Loslassen ohne gehaltenes Drücken beginnt keine Folge
		}
	}
	g.queue = append(g.queue, heldKey{keyEvent: h.keyEvent, Dev: dev, At: now, state: st})
	if !h.Up {
		g.lastDown = now
		g.downs++
		if c, ok := keyChars[mustCode(h.keyEvent)]; ok && !isEnter(h.keyEvent) {
			g.chars = append(g.chars, c)
		}
		if isEnter(h.keyEvent) {
			acts = append(acts, g.finish()...)
			return true, acts
		}
	}
	return true, append(acts, g.drain()...)
}

func mustCode(k keyEvent) uint16 {
	c, _ := keyCode(k)
	return c
}

// finish wird mit dem Enter der Folge (schon eingereiht) aufgerufen: Passt die Länge und hat kein
// Raw-Ereignis schon entschieden, ist es der Leser, sonst wird alles wiederholt.
func (g *gate) finish() []action {
	allPending := true
	for _, q := range g.queue {
		if q.state != holdPending {
			allPending = false
		}
	}
	if allPending && uidChars(g.chars) {
		for i := range g.queue {
			g.queue[i].state = holdReader
		}
	} else {
		for i := range g.queue {
			if g.queue[i].state == holdPending {
				g.queue[i].state = holdOther
			}
		}
	}
	return g.drain()
}

// Raw behandelt ein Raw-Input-Ereignis.
func (g *gate) Raw(r rawEvent, now time.Time) []action {
	if !g.Active || !isCandidate(r.keyEvent) {
		return nil
	}
	for i := range g.queue {
		q := &g.queue[i]
		if q.state != holdPending || q.keyEvent != r.keyEvent {
			continue
		}
		q.Dev, q.state = r.Dev, holdOther
		if r.Reader {
			q.state = holdReader
		}
		return g.drain()
	}
	g.pruneRaws(now)
	if len(g.raws) >= maxRaws {
		g.raws = g.raws[1:]
	}
	if r.At.IsZero() {
		r.At = now
	}
	g.raws = append(g.raws, r)
	return nil
}

// Tick beendet eine Folge, die länger als gapMax still ist.
func (g *gate) Tick(now time.Time) []action {
	g.pruneRaws(now)
	if len(g.queue) > 0 && now.Sub(g.lastDown) >= gapMax {
		return g.flush()
	}
	return nil
}

// Deadline ist der Zeitpunkt, zu dem Tick etwas bewirkt.
func (g *gate) Deadline() (time.Time, bool) {
	for _, q := range g.queue {
		if q.state == holdPending {
			return g.lastDown.Add(gapMax), true
		}
	}
	return time.Time{}, false
}

// flush gibt alles Gehaltene frei: Unentschiedenes gilt als fremd und wird wiederholt.
func (g *gate) flush() []action {
	for i := range g.queue {
		if g.queue[i].state == holdPending {
			g.queue[i].state = holdOther
		}
	}
	return g.drain()
}

// drain gibt die entschiedenen Tasten am Anfang der Warteschlange frei (Reihenfolge bleibt).
func (g *gate) drain() []action {
	var acts []action
	for len(g.queue) > 0 && g.queue[0].state != holdPending {
		q := g.queue[0]
		g.queue = g.queue[1:]
		kind := actReplay
		if q.state == holdReader {
			kind = actDrop
			if q.Up {
				down := q.keyEvent
				down.Up = false
				delete(g.ups, down) // das Loslassen war schon eingereiht
			} else {
				g.markUp(q.keyEvent)
			}
		}
		acts = append(acts, action{Kind: kind, Key: q.keyEvent, Dev: q.Dev, At: q.At})
	}
	if len(g.queue) == 0 {
		g.queue, g.chars, g.downs = nil, nil, 0
	}
	return acts
}

// markUp merkt, dass das Loslassen einer verschluckten Taste auch zu verschlucken ist.
func (g *gate) markUp(k keyEvent) {
	if k.Up {
		return
	}
	if g.ups == nil {
		g.ups = map[keyEvent]bool{}
	}
	g.ups[k] = true
}

func (g *gate) pendingDown(k keyEvent) bool {
	for _, q := range g.queue {
		if q.keyEvent == k {
			return true
		}
	}
	return false
}

func (g *gate) pruneRaws(now time.Time) {
	n := 0
	for _, r := range g.raws {
		if now.Sub(r.At) <= rawMax {
			g.raws[n] = r
			n++
		}
	}
	g.raws = g.raws[:n]
}

// takeRaw nimmt das älteste noch gültige Raw-Ereignis zur Taste.
func (g *gate) takeRaw(k keyEvent, now time.Time) (rawEvent, bool) {
	g.pruneRaws(now)
	for i, r := range g.raws {
		if r.keyEvent == k {
			g.raws = append(g.raws[:i], g.raws[i+1:]...)
			return r, true
		}
	}
	return rawEvent{}, false
}

// learner erkennt beim Anlernen die Leserfolge an den Raw-Ereignissen (nichts wird verschluckt):
// Tastendrücke eines Geräts im Abstand von höchstens gapMax, die mit Enter enden und die Länge
// einer UID haben.
type learner struct {
	dev   uintptr
	chars []byte
	last  time.Time
}

// Key nimmt ein Raw-Ereignis entgegen; done zeigt eine vollständige Folge von Gerät dev mit Text.
func (l *learner) Key(dev uintptr, k keyEvent, at time.Time) (outDev uintptr, text string, done bool) {
	if k.Up || dev == 0 {
		return 0, "", false
	}
	if !isCandidate(k) {
		l.chars = nil
		return 0, "", false
	}
	if len(l.chars) > 0 && (dev != l.dev || at.Sub(l.last) > gapMax) {
		l.chars = nil
	}
	l.dev, l.last = dev, at
	if isEnter(k) {
		defer func() { l.chars = nil }()
		if uidChars(l.chars) {
			return dev, string(l.chars), true
		}
		return 0, "", false
	}
	if c, ok := keyChars[mustCode(k)]; ok {
		l.chars = append(l.chars, c)
	}
	return 0, "", false
}
