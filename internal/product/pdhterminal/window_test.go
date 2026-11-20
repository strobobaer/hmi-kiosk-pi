package pdhterminal

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWaitForEdgeRetriesUntilFound(t *testing.T) {
	calls := 0
	read := func(string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("fehlt")
		}
		return `C:\E\msedge.exe`, nil
	}
	p, ok := waitForEdge(context.Background(), time.Millisecond, read)
	if !ok || p != `C:\E\msedge.exe` || calls != 3 {
		t.Fatalf("%q %v nach %d Versuchen", p, ok, calls)
	}
}

func TestWaitForEdgeStopsWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	if _, ok := waitForEdge(ctx, time.Hour, func(string) (string, error) { return "", nil }); ok {
		t.Fatal("ohne Edge darf es nichts liefern")
	}
}

type fakeWin struct{ errs, calls int }

func (w *fakeWin) Foreground() bool { return false }
func (w *fakeWin) Raise() error     { return nil }
func (w *fakeWin) MinimizeNoActivate() error {
	w.calls++
	if w.calls <= w.errs {
		return errors.New("noch kein Fenster")
	}
	return nil
}

func TestMinimizeWhenShownWaitsForWindow(t *testing.T) {
	w := &fakeWin{errs: 3}
	minimizeWhenShown(context.Background(), w)
	if w.calls != 4 {
		t.Errorf("Versuche: %d", w.calls)
	}
}
