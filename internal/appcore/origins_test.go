package appcore

import (
	"context"
	"errors"

	"slices"
	"sync"
	"testing"
	"time"
)

const waitLimit = 15 * time.Second

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(waitLimit)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("warte auf %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type helperCalls struct {
	mu   sync.Mutex
	args []string
	fail error
}

func (h *helperCalls) call(_ context.Context, cmd, arg string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cmd != "set-web-origins" {
		return errors.New("unerwarteter Befehl " + cmd)
	}
	h.args = append(h.args, arg)
	return h.fail
}

func (h *helperCalls) all() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.args)
}

func TestOriginSyncSendsOnlyChanges(t *testing.T) {
	calls := &helperCalls{}
	o := NewOriginSync(calls.call)
	o.retry = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Run(ctx)
	o.Set([]string{})
	eventually(t, "erster Abgleich", func() bool { return len(calls.all()) == 1 })
	o.Set([]string{})
	o.Set([]string{"http://grafana:3000"})
	eventually(t, "zweiter Abgleich", func() bool { return len(calls.all()) == 2 })
	if got := calls.all(); got[0] != "[]" || got[1] != `["http://grafana:3000"]` {
		t.Fatalf("Aufrufe %q", got)
	}
	time.Sleep(50 * time.Millisecond)
	if n := len(calls.all()); n != 2 {
		t.Fatalf("%d Aufrufe ohne Änderung", n)
	}
}

func TestOriginSyncRetriesAfterFailure(t *testing.T) {
	calls := &helperCalls{fail: errors.New("Der Helfer ist nicht erreichbar")}
	o := NewOriginSync(calls.call)
	o.retry = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Run(ctx)
	o.Set([]string{"http://grafana:3000"})
	eventually(t, "Wiederholung", func() bool { return len(calls.all()) >= 3 })
	calls.mu.Lock()
	calls.fail = nil
	calls.mu.Unlock()
	n := len(calls.all())
	eventually(t, "Erfolg", func() bool { return len(calls.all()) > n })
	time.Sleep(50 * time.Millisecond)
	m := len(calls.all())
	time.Sleep(50 * time.Millisecond)
	if len(calls.all()) != m {
		t.Fatal("wiederholt nach Erfolg")
	}
}

func TestOriginSyncWaitFor(t *testing.T) {
	calls := &helperCalls{}
	o := NewOriginSync(calls.call)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go o.Run(ctx)
	o.Set([]string{"http://grafana:3000"})
	wctx, wcancel := context.WithTimeout(ctx, waitLimit)
	defer wcancel()
	if err := o.WaitFor(wctx, "http://grafana:3000"); err != nil {
		t.Fatal(err)
	}
	short, scancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer scancel()
	if err := o.WaitFor(short, "http://andere:3000"); err == nil {
		t.Fatal("fremder Ursprung gilt als geschrieben")
	}
}
