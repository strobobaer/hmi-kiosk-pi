// Package card verteilt Kartenlese-Ereignisse (UIDs) an SSE-Abonnenten.
package card

import (
	"errors"
	"sync"
	"time"
)

const (
	// DebounceWindow: dieselbe UID wird innerhalb dieses Fensters nur einmal geliefert.
	DebounceWindow = 2 * time.Second
	// MaxAge: aeltere Ereignisse gelten nicht mehr als frisch (kein Replay).
	MaxAge = 5 * time.Second
	// MaxSubscribers: Obergrenze gleichzeitiger Abonnenten.
	MaxSubscribers = 8

	subBuffer = 4
)

// ErrTooManySubscribers wird geliefert, wenn MaxSubscribers erreicht ist.
var ErrTooManySubscribers = errors.New("card: zu viele Abonnenten")

// Event ist ein gelesener Kartenwurf.
type Event struct {
	UID string    `json:"uid"`
	At  time.Time `json:"at"`
}

// Hub entprellt Ereignisse pro UID und verteilt sie nicht blockierend.
type Hub struct {
	now func() time.Time

	mu       sync.Mutex
	subs     map[int]chan Event
	nextID   int
	last     Event
	hasLast  bool
	lastSeen map[string]time.Time // UID -> Zeitpunkt der letzten Zustellung
}

// NewHub erzeugt einen Hub; now ist die (testbare) Uhr.
func NewHub(now func() time.Time) *Hub {
	return &Hub{
		now:      now,
		subs:     make(map[int]chan Event),
		lastSeen: make(map[string]time.Time),
	}
}

// Publish meldet eine gelesene UID. Leere UIDs und Wiederholungen derselben UID
// innerhalb von DebounceWindow (seit der letzten Zustellung) werden verworfen.
// Volle Abonnentenpuffer blockieren nicht, das Ereignis geht dort verloren.
func (h *Hub) Publish(uid string) {
	if uid == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	t := h.now()
	if prev, ok := h.lastSeen[uid]; ok && t.Sub(prev) < DebounceWindow {
		return
	}
	h.lastSeen[uid] = t
	// Abgelaufene Eintraege entfernen, damit die Map nicht waechst.
	for k, v := range h.lastSeen {
		if t.Sub(v) >= DebounceWindow {
			delete(h.lastSeen, k)
		}
	}

	e := Event{UID: uid, At: t}
	h.last, h.hasLast = e, true
	for _, ch := range h.subs {
		select {
		case ch <- e:
		default:
		}
	}
}

// Subscribe registriert einen Abonnenten. Ein frisches letztes Ereignis wird sofort
// zugestellt. cancel meldet ab (idempotent) und schliesst den Kanal nicht.
func (h *Hub) Subscribe() (<-chan Event, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= MaxSubscribers {
		return nil, nil, ErrTooManySubscribers
	}
	ch := make(chan Event, subBuffer)
	id := h.nextID
	h.nextID++
	h.subs[id] = ch
	if h.hasLast && h.fresh(h.last) {
		ch <- h.last
	}
	cancel := func() {
		h.mu.Lock()
		delete(h.subs, id)
		h.mu.Unlock()
	}
	return ch, cancel, nil
}

// Fresh meldet, ob e hoechstens MaxAge alt ist.
func (h *Hub) Fresh(e Event) bool { return h.fresh(e) }

func (h *Hub) fresh(e Event) bool { return h.now().Sub(e.At) <= MaxAge }

// Last liefert das letzte Ereignis beliebigen Alters (zum Anlernen von Karten).
func (h *Hub) Last() (Event, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last, h.hasLast
}
