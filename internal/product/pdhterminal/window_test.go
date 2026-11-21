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

// R9: Im Kiosk steht der Hinweis „Edge nicht gefunden“, solange Edge fehlt, und verschwindet, wenn Edge startet.
// In Zusatz gibt es keinen Hinweisfenster (Tray und Selbstprüfung genügen).
func TestEdgeMissingHintOnlyInKioskWhileMissing(t *testing.T) {
	var log []string
	oldShow, oldHide := showHint, hideHint
	showHint = func(title, detail string) { log = append(log, "show:"+title+"|"+detail) }
	hideHint = func() { log = append(log, "hide") }
	defer func() { showHint, hideHint = oldShow, oldHide }()

	edgeMissingHint(false, true)
	edgeMissingHint(false, false)
	if len(log) != 0 {
		t.Fatalf("Zusatz: kein Hinweis erwartet, bekam %v", log)
	}
	edgeMissingHint(true, true)
	edgeMissingHint(true, false)
	want := []string{"show:Edge nicht gefunden|Strg+Alt+Entf → Benutzer wechseln (Administrator)", "hide"}
	if len(log) != 2 || log[0] != want[0] || log[1] != want[1] {
		t.Errorf("Kiosk: %v, erwartet %v", log, want)
	}
}

// Die Verbindung mit der Suche: fehlt Edge zuerst, dann gefunden, geht der Hinweis an und wieder aus.
func TestEdgeHintFollowsEdgeSearch(t *testing.T) {
	var log []string
	oldShow, oldHide := showHint, hideHint
	showHint = func(string, string) { log = append(log, "show") }
	hideHint = func() { log = append(log, "hide") }
	defer func() { showHint, hideHint = oldShow, oldHide }()
	calls := 0
	read := func(string) (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("fehlt")
		}
		return `C:\E\msedge.exe`, nil
	}
	if _, ok := waitForEdgeNotify(context.Background(), time.Millisecond, read, func(m bool) { edgeMissingHint(true, m) }); !ok {
		t.Fatal("Edge nicht gefunden")
	}
	if len(log) != 2 || log[0] != "show" || log[1] != "hide" {
		t.Errorf("Ablauf: %v", log)
	}
}
