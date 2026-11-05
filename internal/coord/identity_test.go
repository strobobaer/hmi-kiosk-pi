package coord

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"hmikiosk/internal/config"
)

func TestUpdateIdentityChangesPeersAndSelf(t *testing.T) {
	var asked []string
	tr := &fakeTransport{claim: func(_ context.Context, p config.Peer, req ClaimReq) (ClaimResp, error) {
		asked = append(asked, p.Name+"/"+req.Claim.Name)
		return ClaimResp{}, nil
	}}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.Request(context.Background(), "h1")
	c.Release("h1")

	c.UpdateIdentity(Holder{Terminal: "a", Name: "Neu", Role: nor}, []config.Peer{{Name: "c", Address: "c"}})
	c.Request(context.Background(), "h1")

	if len(asked) != 2 || asked[0] != "b/a" || asked[1] != "c/Neu" {
		t.Fatalf("gefragt: %v", asked)
	}
}

func TestUpdateIdentityEmptyPeersAsksNobody(t *testing.T) {
	tr := &fakeTransport{claim: func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		return ClaimResp{}, errors.New("darf nicht gefragt werden")
	}}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.UpdateIdentity(Holder{Terminal: "a", Name: "a", Role: nor}, nil)
	res := c.Request(context.Background(), "h1")
	if len(res.Unreachable) != 0 || res.Decision.Outcome != Allow {
		t.Fatalf("Ergebnis %+v", res)
	}
}

// Eine laufende Sitzung meldet sich nach einer Umbenennung unter dem neuen Namen.
func TestUpdateIdentityReachesRunningHold(t *testing.T) {
	tr := &fakeTransport{}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.Hold("h1", "s1")
	c.UpdateIdentity(Holder{Terminal: "a", Name: "Neu", Role: config.RolePrimary}, []config.Peer{{Name: "b", Address: "b"}})

	c.tick(context.Background())
	beats := tr.sentBeats()
	if len(beats) != 1 || beats[0].holder.Name != "Neu" || beats[0].holder.Role != config.RolePrimary || beats[0].holder.Session != "s1" {
		t.Fatalf("Herzschläge %+v", beats)
	}
	resp := c.handleClaim(ClaimReq{HMI: "h1", Claim: Claim{Terminal: "b", Role: nor}})
	if resp.Holder == nil || resp.Holder.Name != "Neu" {
		t.Fatalf("Antwort %+v", resp)
	}
}

func TestKeyGetterAppliesToNewRequests(t *testing.T) {
	remote := New(settings("r", nor, "x"), &fakeTransport{}, newClock())
	var key, sendKey atomic.Value
	key.Store([]byte("alter Schlüssel"))
	sendKey.Store([]byte("alter Schlüssel"))
	srv := tlsPeerHandler(t, NewHandlerFunc(remote, func() []byte { return key.Load().([]byte) }))
	tr := NewHTTPTransportFunc(func() []byte { return sendKey.Load().([]byte) }, nil)
	ctx := context.Background()
	req := ClaimReq{HMI: "h1", Claim: Claim{Terminal: "a", Role: nor}}

	if _, err := tr.Claim(ctx, srv, req); err != nil {
		t.Fatal(err)
	}
	key.Store([]byte("neuer Schlüssel"))
	if _, err := tr.Claim(ctx, srv, req); !errors.Is(err, ErrBadKey) {
		t.Fatalf("alter Schlüssel: %v", err)
	}
	sendKey.Store([]byte("neuer Schlüssel"))
	if _, err := tr.Claim(ctx, srv, req); err != nil {
		t.Fatal(err)
	}
}
