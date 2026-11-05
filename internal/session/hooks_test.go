package session

import (
	"context"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/coord"
)

type endLog struct {
	mu sync.Mutex
	l  []Ended
}

func (e *endLog) add(x Ended) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.l = append(e.l, x)
}

func (e *endLog) all() []Ended {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]Ended(nil), e.l...)
}

func withEndLog(t *testing.T) (*harness, *endLog) {
	t.Helper()
	ends := &endLog{}
	h := newHarness(t, func(h *harness) { h.onEnd = ends.add })
	return h, ends
}

func TestOnEndReportsOperatorBackWithDuration(t *testing.T) {
	h, ends := withEndLog(t)
	h.connect("a")
	h.advance(10 * time.Second)
	h.m.Back()
	h.waitState(StateTiles)
	got := ends.all()
	if len(got) != 1 {
		t.Fatalf("Sitzungsenden %+v", got)
	}
	e := got[0]
	if e.HMI != "a" || e.HMIName != "Presse" || e.Duration < 10*time.Second || e.Reason == "" || !e.Connected {
		t.Fatalf("Ende %+v", e)
	}
}

func TestOnEndReportsTakeoverReason(t *testing.T) {
	h, ends := withEndLog(t)
	h.connect("a")
	h.coord.events <- coord.Event{HMI: "a", By: coord.Holder{Terminal: "t2", Name: "Nebenan"}}
	h.waitNotice(StateTiles, "Übernommen")
	got := ends.all()
	if len(got) != 1 || got[0].Reason != "Übernommen von Nebenan" {
		t.Fatalf("Sitzungsenden %+v", got)
	}
}

func TestOnEndNotCalledWithoutClient(t *testing.T) {
	h, ends := withEndLog(t)
	h.coord.queue(busy("Nebenan"))
	h.open("a")
	h.waitNotice(StateTiles, "Belegt")
	if got := ends.all(); len(got) != 0 {
		t.Fatalf("Sitzungsenden %+v", got)
	}
}

func TestOnEndCalledOnShutdown(t *testing.T) {
	h, ends := withEndLog(t)
	h.connect("a")
	h.shutdown()
	if got := ends.all(); len(got) != 1 || got[0].Reason != "Dienst beendet" {
		t.Fatalf("Sitzungsenden %+v", got)
	}
}

func TestPingAnswersThroughStateGoroutine(t *testing.T) {
	h := newHarness(t, nil)
	if err := h.m.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Eine hängende Zustands-Goroutine darf den Aufrufer nicht festhalten.
	release := make(chan struct{})
	h.m.cmds <- func() { <-release }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := h.m.Ping(ctx); err == nil {
		t.Fatal("Ping meldet eine hängende Zustands-Goroutine als gesund")
	}
	close(release)
	h.shutdown()
	if err := h.m.Ping(context.Background()); err != ErrStopped {
		t.Fatalf("nach Ende: %v", err)
	}
}
