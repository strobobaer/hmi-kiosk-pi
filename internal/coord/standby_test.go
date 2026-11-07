package coord

import (
	"context"
	"net/http"
	"testing"

	"hmikiosk/internal/config"
)

func TestStandbyRequestAsksNobody(t *testing.T) {
	asked := 0
	tr := &fakeTransport{claim: func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		asked++
		return ClaimResp{}, nil
	}}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.SetStandby(true)
	r := c.Request(context.Background(), "h1")
	if r.Decision.Outcome != Reject || !r.Rejected {
		t.Fatalf("got %+v", r)
	}
	if asked != 0 {
		t.Fatalf("%d Anfragen im Wartungsmodus", asked)
	}
	c.SetStandby(false)
	if r := c.Request(context.Background(), "h1"); r.Decision.Outcome != Allow {
		t.Fatalf("nach dem Wartungsmodus: %+v", r)
	}
}

func TestStandbySendsNoHeartbeats(t *testing.T) {
	tr := &fakeTransport{}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.Hold("h1", "s1")
	c.SetStandby(true)
	c.tick(context.Background())
	if n := len(tr.sentBeats()); n != 0 {
		t.Fatalf("%d Herzschläge im Wartungsmodus", n)
	}
	c.SetStandby(false)
	c.tick(context.Background())
	if n := len(tr.sentBeats()); n != 1 {
		t.Fatalf("danach %d", n)
	}
}

func TestStandbyPeerAPIAnswers503(t *testing.T) {
	c, _, srv := newAPI(t, testKey)
	c.SetStandby(true)
	for _, path := range []string{pathNonce, pathClaim, pathHeartbeat, pathTakeover} {
		resp, err := srv.Client().Post(srv.URL+path, "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	c.SetStandby(false)
	if got := fetchNonce(t, srv); got == "" {
		t.Fatal("keine Zufallszahl nach dem Wartungsmodus")
	}
}

func TestStandbyTakeoverIsRefused(t *testing.T) {
	tr := &fakeTransport{}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.SetStandby(true)
	err := c.Takeover(context.Background(), "h1", Decision{Outcome: TakeOver, Holder: &Holder{Terminal: "b", Name: "b"}})
	if err == nil || len(tr.takeovers) != 0 {
		t.Fatalf("err=%v sent=%d", err, len(tr.takeovers))
	}
}
