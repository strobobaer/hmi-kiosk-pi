package winsignal

import (
	"context"
	"testing"
	"time"
)

func testName(t *testing.T) string {
	return `Local\PDH-Terminal-Test-` + t.Name()
}

func TestNotifyWithoutListener(t *testing.T) {
	ok, err := Notify(testName(t))
	if err != nil || ok {
		t.Fatalf("ohne Lauscher: ok=%v err=%v, erwartet false ohne Fehler (andere Sitzung: still beenden)", ok, err)
	}
}

func TestListenAndNotify(t *testing.T) {
	name := testName(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan struct{}, 4)
	done := make(chan error, 1)
	go func() { done <- Listen(ctx, name, func() { got <- struct{}{} }) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ok, err := Notify(name)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Lauscher meldet sich nicht an")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("Signal kommt nicht an")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Listen endet nicht")
	}
	if ok, _ := Notify(name); ok {
		t.Error("nach dem Ende darf niemand mehr lauschen")
	}
}
