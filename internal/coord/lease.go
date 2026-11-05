package coord

import (
	"sync"
	"time"
)

const (
	LeaseTTL       = 6 * time.Second
	HeartbeatEvery = 2 * time.Second
)

type lease struct {
	holder Holder
	seen   time.Time
}

// Table hält die Belegungen je HMI. Die Frist wird mit der eigenen Uhr des
// Empfängers gemessen. Sicher für nebenläufige Nutzung.
type Table struct {
	mu     sync.Mutex
	clock  Clock
	leases map[string]lease
}

func NewTable(c Clock) *Table {
	return &Table{clock: c, leases: map[string]lease{}}
}

// Renew setzt oder erneuert die Belegung; ein anderer Halter wird ersetzt.
func (t *Table) Renew(hmi string, h Holder) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.leases[hmi] = lease{holder: h, seen: t.clock.Now()}
}

// Holder liefert den aktuellen Halter, abgelaufene Belegungen gelten als frei.
func (t *Table) Holder(hmi string) (Holder, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	l, ok := t.leases[hmi]
	if !ok {
		return Holder{}, false
	}
	if t.clock.Now().Sub(l.seen) >= LeaseTTL {
		delete(t.leases, hmi)
		return Holder{}, false
	}
	return l.holder, true
}

// Snapshot liefert alle nicht abgelaufenen Belegungen.
func (t *Table) Snapshot() map[string]Holder {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	out := make(map[string]Holder, len(t.leases))
	for hmi, l := range t.leases {
		if now.Sub(l.seen) >= LeaseTTL {
			delete(t.leases, hmi)
			continue
		}
		out[hmi] = l.holder
	}
	return out
}

// Drop entfernt die Belegung nur, wenn sie diesem Terminal gehört.
func (t *Table) Drop(hmi, terminal string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if l, ok := t.leases[hmi]; ok && l.holder.Terminal == terminal {
		delete(t.leases, hmi)
	}
}
