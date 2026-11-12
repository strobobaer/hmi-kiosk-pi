package card

import (
	"errors"
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestHub() (*Hub, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return NewHub(c.now), c
}

func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case e := <-ch:
			out = append(out, e)
		default:
			return out
		}
	}
}

func TestHubDebounce(t *testing.T) {
	h, c := newTestHub()
	ch, cancel, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()

	h.Publish("A")
	c.advance(1900 * time.Millisecond)
	h.Publish("A")
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("nach Entprellung %d Ereignisse, want 1", len(got))
	}
	c.advance(200 * time.Millisecond) // t0+2,1 s
	h.Publish("A")
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("zweites A: %d Ereignisse, want 1", len(got))
	}

	h2, c2 := newTestHub()
	ch2, cancel2, _ := h2.Subscribe()
	defer cancel2()
	h2.Publish("A")
	c2.advance(100 * time.Millisecond)
	h2.Publish("B")
	got := drain(ch2)
	if len(got) != 2 || got[1].UID != "B" {
		t.Fatalf("B nicht sofort geliefert: %+v", got)
	}
}

func TestHubIgnoresEmptyUID(t *testing.T) {
	h, _ := newTestHub()
	ch, cancel, _ := h.Subscribe()
	defer cancel()
	h.Publish("")
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("leere UID geliefert: %+v", got)
	}
	if _, ok := h.Last(); ok {
		t.Fatal("Last nach leerer UID")
	}
}

func TestHubReplayFresh(t *testing.T) {
	h, c := newTestHub()
	h.Publish("A")
	c.advance(MaxAge)
	ch, cancel, err := h.Subscribe()
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(ch); len(got) != 1 || got[0].UID != "A" {
		t.Fatalf("Replay bei 5 s: %+v", got)
	}
	cancel()

	c.advance(100 * time.Millisecond)
	ch, cancel, _ = h.Subscribe()
	defer cancel()
	if got := drain(ch); len(got) != 0 {
		t.Fatalf("Replay bei 5,1 s: %+v", got)
	}
	e, ok := h.Last()
	if !ok || e.UID != "A" {
		t.Fatalf("Last = %+v, %v", e, ok)
	}
	if h.Fresh(e) {
		t.Fatal("Fresh bei 5,1 s")
	}
}

func TestHubSubscriberLimit(t *testing.T) {
	h, _ := newTestHub()
	var cancels []func()
	for i := 0; i < MaxSubscribers; i++ {
		_, cancel, err := h.Subscribe()
		if err != nil {
			t.Fatalf("Abo %d: %v", i+1, err)
		}
		cancels = append(cancels, cancel)
	}
	if _, _, err := h.Subscribe(); !errors.Is(err, ErrTooManySubscribers) {
		t.Fatalf("9. Abo: %v", err)
	}
	cancels[0]()
	cancels[0]() // idempotent
	_, cancel, err := h.Subscribe()
	if err != nil {
		t.Fatalf("nach cancel: %v", err)
	}
	cancel()
}

func TestHubSlowSubscriber(t *testing.T) {
	h, _ := newTestHub()
	ch, cancel, _ := h.Subscribe()
	defer cancel()
	done := make(chan struct{})
	go func() {
		for _, u := range []string{"1", "2", "3", "4", "5", "6"} {
			h.Publish(u)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blockiert")
	}
	if got := drain(ch); len(got) != 4 {
		t.Fatalf("Puffer: %d, want 4", len(got))
	}
}
