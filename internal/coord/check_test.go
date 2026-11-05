package coord

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

func TestCheckPeerDistinguishesStates(t *testing.T) {
	c, _, srv := newAPI(t, testKey)
	peer := config.Peer{Name: "T2", Address: strings.TrimPrefix(srv.URL, "http://")}
	ctx := context.Background()

	// httptest.NewServer spricht ohne TLS; der Transport verlangt https.
	tls := httptest.NewTLSServer(NewHandler(c, testKey))
	t.Cleanup(tls.Close)
	peer.Address = strings.TrimPrefix(tls.URL, "https://")

	ok := CheckPeer(ctx, func() []byte { return testKey }, tls.Client(), peer)
	if !ok.Reachable || !ok.KeyOK || ok.Err != nil {
		t.Fatalf("richtiger Schlüssel: %+v", ok)
	}
	bad := CheckPeer(ctx, func() []byte { return []byte("anderer-schluessel-anderer-schluessel") }, tls.Client(), peer)
	if !bad.Reachable || bad.KeyOK {
		t.Fatalf("falscher Schlüssel: %+v", bad)
	}
	tls.Close()
	gone := CheckPeer(ctx, func() []byte { return testKey }, tls.Client(), peer)
	if gone.Reachable || gone.KeyOK || gone.Err == nil {
		t.Fatalf("nicht erreichbar: %+v", gone)
	}
}

func TestCheckPeerHasNoSideEffectOnPeer(t *testing.T) {
	c, _, _ := newAPI(t, testKey)
	tls := httptest.NewTLSServer(NewHandler(c, testKey))
	t.Cleanup(tls.Close)
	peer := config.Peer{Name: "T2", Address: strings.TrimPrefix(tls.URL, "https://")}
	CheckPeer(context.Background(), func() []byte { return testKey }, tls.Client(), peer)
	select {
	case ev := <-c.Events():
		t.Fatalf("Prüfung löste ein Ereignis aus: %+v", ev)
	case <-time.After(50 * time.Millisecond):
	}
	if len(c.Occupancy()) != 0 {
		t.Fatalf("Prüfung hat Belegung erzeugt: %v", c.Occupancy())
	}
}
