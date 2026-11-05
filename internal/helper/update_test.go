package helper

import (
	"context"
	"errors"
	"os"
	"reflect"
	"testing"
)

var updateSeq = []string{
	"systemctl restart hmi-kiosk.service",
	"systemctl start --no-block hmi-kiosk-update-guard.service",
}

func TestUpdateActivateSequenceRemovesMarkerFirst(t *testing.T) {
	e := newEnv(t, "")
	if err := os.WriteFile(e.srv.HealthyMarker, []byte("1.0.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.upd.onActivate = func() {
		if _, err := os.Stat(e.srv.HealthyMarker); err == nil {
			t.Error("Gesundmarke bei Activate noch vorhanden")
		}
		if len(e.fx.got()) != 0 {
			t.Errorf("Dienst vor Activate neu gestartet: %v", e.fx.got())
		}
	}
	if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, "1.2.0"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.upd.activated, []string{"1.2.0"}) {
		t.Fatalf("activated %v", e.upd.activated)
	}
	if got := e.fx.got(); !reflect.DeepEqual(got, updateSeq) {
		t.Fatalf("got %v want %v", got, updateSeq)
	}
}

func TestUpdateActivateWithoutMarkerFileWorks(t *testing.T) {
	e := newEnv(t, "")
	if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, "1.2.0"); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateActivateFailureSkipsRestart(t *testing.T) {
	e := newEnv(t, "")
	e.upd.activateErr = errors.New("Version nicht installiert")
	if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, "1.2.0"); err == nil {
		t.Fatal("Fehler verschluckt")
	}
	if len(e.fx.got()) != 0 {
		t.Fatalf("Aufrufe trotz Fehler: %v", e.fx.got())
	}
}

func TestUpdateActivateRestartFailureStillStartsGuard(t *testing.T) {
	e := newEnv(t, "")
	e.fx.fail["systemctl restart hmi-kiosk.service"] = errors.New("boom")
	if err := e.srv.Dispatch(context.Background(), CmdUpdateActivate, "1.2.0"); err == nil {
		t.Fatal("Fehler verschluckt")
	}
	if got := e.fx.got(); !reflect.DeepEqual(got, updateSeq) {
		t.Fatalf("got %v", got)
	}
}

func TestUpdateRollbackSequence(t *testing.T) {
	e := newEnv(t, "")
	if err := e.srv.Dispatch(context.Background(), CmdUpdateRollback, ""); err != nil {
		t.Fatal(err)
	}
	if e.upd.rollbacks != 1 {
		t.Fatal("Rollback nicht aufgerufen")
	}
	if got := e.fx.got(); !reflect.DeepEqual(got, updateSeq[:1]) {
		t.Fatalf("got %v", got)
	}
	e = newEnv(t, "")
	e.upd.rollbackErr = errors.New("Kein Vorgänger")
	if err := e.srv.Dispatch(context.Background(), CmdUpdateRollback, ""); err == nil || len(e.fx.got()) != 0 {
		t.Fatalf("err=%v calls=%v", err, e.fx.got())
	}
}

func TestDefaultHealthyMarkerPath(t *testing.T) {
	s := NewServer("s", 1, nil, "c", nil)
	if s.HealthyMarker != "/run/hmi-kiosk/healthy" {
		t.Fatal(s.HealthyMarker)
	}
}
