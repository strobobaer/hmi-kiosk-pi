package coord

import (
	"sync"
	"testing"
	"time"
)

func TestLeaseExpiresAfter6sAndRenews(t *testing.T) {
	clk := &fakeClock{t: time.Unix(1000, 0)}
	tb := NewTable(clk)
	tb.Renew("hmi1", Holder{Terminal: "a"})
	clk.Advance(5900 * time.Millisecond)
	if _, ok := tb.Holder("hmi1"); !ok {
		t.Fatal("should still be held at 5.9s")
	}
	tb.Renew("hmi1", Holder{Terminal: "a"})
	clk.Advance(5900 * time.Millisecond)
	if _, ok := tb.Holder("hmi1"); !ok {
		t.Fatal("renew should extend")
	}
	clk.Advance(200 * time.Millisecond)
	if _, ok := tb.Holder("hmi1"); ok {
		t.Fatal("should be gone at 6.1s")
	}
}

func TestRenewReplacesHolderAndKeepsWatch(t *testing.T) {
	tb := NewTable(&fakeClock{t: time.Unix(1000, 0)})
	tb.Renew("hmi1", Holder{Terminal: "a"})
	tb.Renew("hmi1", Holder{Terminal: "b", Watch: true})
	h, ok := tb.Holder("hmi1")
	if !ok || h.Terminal != "b" || !h.Watch {
		t.Fatalf("got %+v %v", h, ok)
	}
}

func TestDropOnlyRemovesOwnLease(t *testing.T) {
	tb := NewTable(&fakeClock{t: time.Unix(1000, 0)})
	tb.Renew("hmi1", Holder{Terminal: "a"})
	tb.Drop("hmi1", "b")
	if _, ok := tb.Holder("hmi1"); !ok {
		t.Fatal("foreign Drop removed lease")
	}
	tb.Drop("hmi1", "a")
	if _, ok := tb.Holder("hmi1"); ok {
		t.Fatal("own Drop kept lease")
	}
}

func TestTableConcurrent(t *testing.T) {
	tb := NewTable(SystemClock{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				tb.Renew("h", Holder{Terminal: "a"})
				tb.Holder("h")
				tb.Drop("h", "a")
			}
		}()
	}
	wg.Wait()
}
