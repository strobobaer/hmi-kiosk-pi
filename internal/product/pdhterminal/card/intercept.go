package card

import "time"

// Abfangen der Leser-Tasten unter Windows (plattformneutral, damit die Entscheidung überall getestet wird).
//
// Der Tastaturhaken (WH_KEYBOARD_LL) sagt nicht, von welchem Gerät eine Taste kommt; das sagt nur
// Raw Input (WM_INPUT). Gemessen kommt der Haken zuerst; wird er mit „verschlucken“ beendet, kommt
// für eingespielte Tasten gar kein WM_INPUT. Darum „halten und wiederholen“: Eine Kartentaste
// wird im Haken gehalten, bis das passende Raw-Ereignis das Gerät nennt. Gehört sie dem Leser,
// wird sie verworfen und der Karte zugeführt; gehört sie einer anderen Tastatur oder kommt innerhalb
// von holdMax nichts, wird sie mit Markierung neu eingespielt. Eine gehaltene Taste hält alle
// späteren Kartentasten zurück, damit die Reihenfolge der echten Tastatur bleibt. Trifft das
// Raw-Ereignis ausnahmsweise vor dem Haken ein, wird es vorgemerkt und der Haken entscheidet sofort.

// holdMax ist die längste Zeit, die eine Taste auf ihr Raw-Ereignis wartet.
const holdMax = 50 * time.Millisecond

// maxRaws begrenzt die vorgemerkten Raw-Ereignisse.
const maxRaws = 16

// keyEvent ist ein Tastenereignis: Scancode (Satz 1), E0-Erweiterung, Loslassen.
type keyEvent struct {
	Scan    uint16
	Ext, Up bool
}

// hookEvent ist ein Ereignis des Tastaturhakens. Marked sind Tasten, die dieses Programm selbst
// wiederholt; Injected sind von anderen Programmen eingespielte (Passwortmanager, Fernwartung).
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
	holdPending holdState = iota // Gerät noch unbekannt
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
	Dev  uintptr
	At   time.Time
}

// gate ist die Entscheidung, rein und ohne Uhr: alle Zeitpunkte kommen von außen. Nicht nebenläufig;
// der Aufrufer ruft sie aus genau einem Thread.
type gate struct {
	queue []heldKey
	raws  []rawEvent
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

// deviceMatches sagt, ob ein Gerät zur Auswahl (vvvv:pppp oder Namensteil) gehört. Das Handle
// kommt nicht vor: Nach dem Wiederanstecken ist es neu, Kennung und Name bleiben.
func deviceMatches(sel string, d InputDevice) bool {
	return len(matchDevices([]InputDevice{d}, sel)) > 0
}

// Hook behandelt ein Ereignis des Tastaturhakens. block ist die Antwort des Hakens (true: die Taste
// nicht weiterreichen); acts sind Folgen daraus, in der Reihenfolge der Ausführung.
func (g *gate) Hook(h hookEvent, now time.Time) (block bool, acts []action) {
	if h.Marked || h.Injected || !isCandidate(h.keyEvent) {
		return false, nil
	}
	st, dev := holdPending, uintptr(0)
	if r, ok := g.takeRaw(h.keyEvent, now); ok {
		st, dev = holdOther, r.Dev
		if r.Reader {
			st = holdReader
		}
		if len(g.queue) == 0 {
			if st == holdOther {
				return false, nil
			}
			return true, []action{{Kind: actDrop, Key: h.keyEvent, Dev: dev, At: now}}
		}
	}
	g.queue = append(g.queue, heldKey{keyEvent: h.keyEvent, Dev: dev, At: now, state: st})
	return true, g.drain(now)
}

// Raw behandelt ein Raw-Input-Ereignis.
func (g *gate) Raw(r rawEvent, now time.Time) []action {
	if !isCandidate(r.keyEvent) {
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
		return g.drain(now)
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

// Tick lässt abgelaufene Wartezeiten ablaufen.
func (g *gate) Tick(now time.Time) []action {
	g.pruneRaws(now)
	return g.drain(now)
}

// Deadline ist der Zeitpunkt, zu dem Tick etwas bewirkt.
func (g *gate) Deadline() (time.Time, bool) {
	if len(g.queue) == 0 {
		return time.Time{}, false
	}
	return g.queue[0].At.Add(holdMax), true
}

// drain gibt die entschiedenen Tasten am Anfang der Warteschlange frei (Reihenfolge bleibt);
// eine unentschiedene wartet bis holdMax, dann gilt sie als fremd.
func (g *gate) drain(now time.Time) []action {
	var acts []action
	for len(g.queue) > 0 {
		q := g.queue[0]
		if q.state == holdPending {
			if now.Sub(q.At) < holdMax {
				break
			}
			q.state = holdOther
		}
		g.queue = g.queue[1:]
		kind := actReplay
		if q.state == holdReader {
			kind = actDrop
		}
		acts = append(acts, action{Kind: kind, Key: q.keyEvent, Dev: q.Dev, At: q.At})
	}
	if len(g.queue) == 0 {
		g.queue = nil
	}
	return acts
}

func (g *gate) pruneRaws(now time.Time) {
	n := 0
	for _, r := range g.raws {
		if now.Sub(r.At) <= holdMax {
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
