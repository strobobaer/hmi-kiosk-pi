package coord

import (
	"bytes"
	"context"
	"errors"
	"log"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
)

type sentHeartbeat struct {
	peer, hmi string
	holder    Holder
}

type sentTakeover struct {
	peer, hmi string
	by        Holder
	warn      time.Duration
}

// fakeTransport antwortet je Peer per Funktion und zeichnet Sendungen auf.
type fakeTransport struct {
	mu         sync.Mutex
	claim      func(ctx context.Context, peer config.Peer, req ClaimReq) (ClaimResp, error)
	takeoverEr error
	heartbeats []sentHeartbeat
	takeovers  []sentTakeover
	beat       chan struct{}
}

func (f *fakeTransport) Claim(ctx context.Context, p config.Peer, req ClaimReq) (ClaimResp, error) {
	if f.claim == nil {
		return ClaimResp{}, nil
	}
	return f.claim(ctx, p, req)
}

func (f *fakeTransport) Heartbeat(_ context.Context, p config.Peer, hmi string, h Holder) error {
	f.mu.Lock()
	f.heartbeats = append(f.heartbeats, sentHeartbeat{p.Name, hmi, h})
	f.mu.Unlock()
	if f.beat != nil {
		f.beat <- struct{}{}
	}
	return nil
}

func (f *fakeTransport) Takeover(_ context.Context, p config.Peer, hmi string, by Holder, warn time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.takeoverEr != nil {
		return f.takeoverEr
	}
	f.takeovers = append(f.takeovers, sentTakeover{p.Name, hmi, by, warn})
	return nil
}

func (f *fakeTransport) sentBeats() []sentHeartbeat {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentHeartbeat(nil), f.heartbeats...)
}

func takeoverRules(string) Rules {
	return Rules{Mode: config.ModeTakeover, Style: config.StyleConfirm}
}

func settings(id string, role config.Role, peers ...string) Settings {
	s := Settings{
		Self:     Holder{Terminal: id, Name: id, Role: role},
		RulesFor: takeoverRules,
	}
	for _, p := range peers {
		s.Peers = append(s.Peers, config.Peer{Name: p, Address: p})
	}
	return s
}

func newClock() *fakeClock { return &fakeClock{t: time.Unix(1000, 0)} }

func noEvent(t *testing.T, c *Coordinator) {
	t.Helper()
	select {
	case e := <-c.Events():
		t.Fatalf("unexpected event %+v", e)
	default:
	}
}

func TestRequestAllowsWhenFree(t *testing.T) {
	c := New(settings("a", nor, "b", "c"), &fakeTransport{}, newClock())
	r := c.Request(context.Background(), "h1")
	if r.Decision.Outcome != Allow || r.Decision.Holder != nil || r.Rejected {
		t.Fatalf("got %+v", r)
	}
	if len(r.Unreachable) != 0 || len(r.BadKey) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestRequestAllowsAndReportsUnreachablePeer(t *testing.T) {
	tr := &fakeTransport{claim: func(_ context.Context, p config.Peer, _ ClaimReq) (ClaimResp, error) {
		if p.Name == "b" {
			return ClaimResp{}, errors.New("connection refused")
		}
		return ClaimResp{}, nil
	}}
	r := New(settings("a", nor, "b", "c"), tr, newClock()).Request(context.Background(), "h1")
	if r.Decision.Outcome != Allow {
		t.Fatalf("got %+v", r)
	}
	if len(r.Unreachable) != 1 || r.Unreachable[0] != "b" || len(r.BadKey) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestRequestReportsBadKeySeparately(t *testing.T) {
	tr := &fakeTransport{claim: func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		return ClaimResp{}, ErrBadKey
	}}
	r := New(settings("a", nor, "b"), tr, newClock()).Request(context.Background(), "h1")
	if r.Decision.Outcome != Allow {
		t.Fatalf("got %+v", r)
	}
	if len(r.BadKey) != 1 || r.BadKey[0] != "b" || len(r.Unreachable) != 0 {
		t.Fatalf("got %+v", r)
	}
}

func TestRequestTimesOutPerPeerAfter1s(t *testing.T) {
	tr := &fakeTransport{claim: func(ctx context.Context, p config.Peer, _ ClaimReq) (ClaimResp, error) {
		if p.Name == "fast" {
			return ClaimResp{}, nil
		}
		<-ctx.Done()
		return ClaimResp{}, ctx.Err()
	}}
	c := New(settings("a", nor, "slow1", "slow2", "fast"), tr, newClock())
	start := time.Now()
	r := c.Request(context.Background(), "h1")
	took := time.Since(start)
	if took < 900*time.Millisecond || took > 1900*time.Millisecond {
		t.Fatalf("peers must run concurrently with a 1 s limit, took %v", took)
	}
	if r.Decision.Outcome != Allow || len(r.Unreachable) != 2 {
		t.Fatalf("got %+v", r)
	}
}

func TestRequestDecidesAgainstStrongestHolder(t *testing.T) {
	tr := &fakeTransport{claim: func(_ context.Context, p config.Peer, _ ClaimReq) (ClaimResp, error) {
		if p.Name == "b" {
			return ClaimResp{Holder: &Holder{Terminal: "b", Name: "b", Role: nor}}, nil
		}
		return ClaimResp{Holder: &Holder{Terminal: "c", Name: "c", Role: pri}}, nil
	}}
	r := New(settings("a", nor, "b", "c"), tr, newClock()).Request(context.Background(), "h1")
	if r.Decision.Outcome != Reject || r.Decision.Holder == nil || r.Decision.Holder.Terminal != "c" || r.Rejected {
		t.Fatalf("normal vs. primary must be rejected by the rules, got %+v", r)
	}
}

func TestRequestConsidersLocalTable(t *testing.T) {
	c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
	c.handleHeartbeat("h1", Holder{Terminal: "z", Name: "z", Role: pri})
	r := c.Request(context.Background(), "h1")
	if r.Decision.Outcome != Reject || r.Decision.Holder == nil || r.Decision.Holder.Terminal != "z" {
		t.Fatalf("got %+v", r)
	}
}

func TestRequestUsesRulesForHMI(t *testing.T) {
	s := settings("a", nor, "b")
	var asked string
	s.RulesFor = func(hmi string) Rules {
		asked = hmi
		return Rules{Mode: config.ModeLock}
	}
	tr := &fakeTransport{claim: func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		return ClaimResp{Holder: &Holder{Terminal: "b", Role: nor}}, nil
	}}
	r := New(s, tr, newClock()).Request(context.Background(), "h7")
	if asked != "h7" || r.Decision.Outcome != Reject {
		t.Fatalf("asked %q got %+v", asked, r)
	}
}

func TestTakeoverReachesReportingPeer(t *testing.T) {
	// Der Peer-Name weicht bewusst vom Terminalnamen ab.
	tr := &fakeTransport{claim: func(_ context.Context, p config.Peer, _ ClaimReq) (ClaimResp, error) {
		if p.Name == "pc" {
			return ClaimResp{Holder: &Holder{Terminal: "c", Name: "Halle", Role: nor}}, nil
		}
		return ClaimResp{}, nil
	}}
	c := New(settings("a", pri, "pb", "pc"), tr, newClock())
	r := c.Request(context.Background(), "h1")
	if r.Decision.Outcome != TakeOver || r.Decision.Warn != TakeoverWarn {
		t.Fatalf("got %+v", r)
	}
	if err := c.Takeover(context.Background(), "h1", r.Decision); err != nil {
		t.Fatal(err)
	}
	if len(tr.takeovers) != 1 {
		t.Fatalf("got %+v", tr.takeovers)
	}
	got := tr.takeovers[0]
	if got.peer != "pc" || got.hmi != "h1" || got.by.Terminal != "a" || got.warn != TakeoverWarn {
		t.Fatalf("got %+v", got)
	}
}

func TestTakeoverFindsTableHolderByPeerName(t *testing.T) {
	tr := &fakeTransport{}
	c := New(settings("a", pri, "b"), tr, newClock())
	c.handleHeartbeat("h1", Holder{Terminal: "tb", Name: "b", Role: nor})
	r := c.Request(context.Background(), "h1")
	if err := c.Takeover(context.Background(), "h1", r.Decision); err != nil {
		t.Fatal(err)
	}
	if len(tr.takeovers) != 1 || tr.takeovers[0].peer != "b" {
		t.Fatalf("got %+v", tr.takeovers)
	}
}

func TestTakeoverErrorWhenHolderPeerUnreachable(t *testing.T) {
	tr := &fakeTransport{takeoverEr: errors.New("connection refused")}
	tr.claim = func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		return ClaimResp{Holder: &Holder{Terminal: "tb", Name: "b", Role: nor}}, nil
	}
	c := New(settings("a", pri, "b"), tr, newClock())
	r := c.Request(context.Background(), "h1")
	if err := c.Takeover(context.Background(), "h1", r.Decision); err == nil {
		t.Fatal("want error")
	}
}

func TestTakeoverErrorWhenHolderPeerUnknown(t *testing.T) {
	c := New(settings("a", pri, "b"), &fakeTransport{}, newClock())
	d := Decision{Outcome: TakeOver, Holder: &Holder{Terminal: "x", Name: "x"}}
	if err := c.Takeover(context.Background(), "h1", d); err == nil {
		t.Fatal("want error")
	}
	if err := c.Takeover(context.Background(), "h1", Decision{}); err == nil {
		t.Fatal("want error without holder")
	}
}

func TestHeartbeatSentEvery2sWhileHeld(t *testing.T) {
	tr := &fakeTransport{beat: make(chan struct{}, 8)}
	c := New(settings("a", nor, "b", "c"), tr, newClock())
	ticks := make(chan time.Time)
	var interval time.Duration
	c.newTicker = func(d time.Duration) (<-chan time.Time, func()) {
		interval = d
		return ticks, func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(done)
	}()

	c.Hold("h1", "s1")
	ticks <- time.Time{} // blockiert, bis Run die Zeitgeber-Schleife erreicht hat
	<-tr.beat
	<-tr.beat
	if interval != 2*time.Second {
		t.Fatalf("interval %v", interval)
	}
	cancel()
	<-done

	beats := tr.sentBeats()
	if len(beats) != 2 {
		t.Fatalf("one beat per peer expected, got %+v", beats)
	}
	for _, b := range beats {
		if b.hmi != "h1" || b.holder.Terminal != "a" || b.holder.Session != "s1" {
			t.Fatalf("got %+v", b)
		}
	}
}

func TestTickSendsOnlyForHeldHMIs(t *testing.T) {
	tr := &fakeTransport{}
	c := New(settings("a", nor, "b"), tr, newClock())
	ctx := context.Background()
	c.tick(ctx)
	if n := len(tr.sentBeats()); n != 0 {
		t.Fatalf("nothing held, got %d", n)
	}
	c.Hold("h1", "s1")
	c.Hold("h2", "s2")
	c.tick(ctx)
	if n := len(tr.sentBeats()); n != 2 {
		t.Fatalf("got %d", n)
	}
	c.Release("h1")
	c.tick(ctx)
	beats := tr.sentBeats()
	if len(beats) != 3 || beats[2].hmi != "h2" {
		t.Fatalf("got %+v", beats)
	}
	c.Release("h2")
	c.tick(ctx)
	if n := len(tr.sentBeats()); n != 3 {
		t.Fatalf("released, got %d", n)
	}
}

// memNet verbindet Koordinatoren im Speicher.
type memNet struct {
	mu    sync.Mutex
	nodes map[string]*Coordinator
	// beforeClaim läuft vor der Zustellung einer Anfrage beim Empfänger.
	beforeClaim func()
}

func (n *memNet) node(name string) (*Coordinator, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	c, ok := n.nodes[name]
	if !ok {
		return nil, errors.New("down")
	}
	return c, nil
}

func (n *memNet) Claim(_ context.Context, p config.Peer, req ClaimReq) (ClaimResp, error) {
	if n.beforeClaim != nil {
		n.beforeClaim()
	}
	c, err := n.node(p.Name)
	if err != nil {
		return ClaimResp{}, err
	}
	return c.handleClaim(req), nil
}

func (n *memNet) Heartbeat(_ context.Context, p config.Peer, hmi string, h Holder) error {
	c, err := n.node(p.Name)
	if err != nil {
		return err
	}
	c.handleHeartbeat(hmi, h)
	return nil
}

func (n *memNet) Takeover(_ context.Context, p config.Peer, hmi string, by Holder, warn time.Duration) error {
	c, err := n.node(p.Name)
	if err != nil {
		return err
	}
	c.handleTakeover(hmi, by, warn)
	return nil
}

func TestPeerCrashFreesHMIAfter6s(t *testing.T) {
	clk := newClock()
	net := &memNet{nodes: map[string]*Coordinator{}}
	a := New(settings("a", nor, "b"), net, clk)
	b := New(settings("b", nor, "a"), net, clk)
	net.nodes["a"], net.nodes["b"] = a, b
	ctx := context.Background()

	b.Hold("h1", "s1")
	b.tick(ctx)
	if h, ok := a.Occupancy()["h1"]; !ok || h.Terminal != "b" || h.Session != "s1" {
		t.Fatalf("got %+v %v", h, ok)
	}

	delete(net.nodes, "b") // Absturz: keine Antwort, kein Herzschlag mehr
	clk.Advance(5900 * time.Millisecond)
	r := a.Request(ctx, "h1")
	if r.Decision.Outcome != AskConfirm || r.Decision.Holder == nil || r.Decision.Holder.Terminal != "b" {
		t.Fatalf("lease must still count at 5.9 s, got %+v", r)
	}
	if len(r.Unreachable) != 1 || r.Unreachable[0] != "b" {
		t.Fatalf("got %+v", r)
	}
	a.Release("h1")

	clk.Advance(100 * time.Millisecond)
	if _, ok := a.Occupancy()["h1"]; ok {
		t.Fatal("lease must be gone at 6 s")
	}
	r = a.Request(ctx, "h1")
	if r.Decision.Outcome != Allow || len(r.Unreachable) != 1 {
		t.Fatalf("got %+v", r)
	}
}

// pair baut zwei Koordinatoren, die sich über net kennen.
func pair(idA string, roleA config.Role, idB string, roleB config.Role, net *memNet, clk Clock) (*Coordinator, *Coordinator) {
	a := New(settings(idA, roleA, idB), net, clk)
	b := New(settings(idB, roleB, idA), net, clk)
	net.nodes = map[string]*Coordinator{idA: a, idB: b}
	return a, b
}

// won: Allow erhalten und nicht nachträglich unterlegen.
func won(r Result, c *Coordinator) bool {
	if r.Rejected || r.Decision.Outcome != Allow {
		return false
	}
	select {
	case <-c.Events():
		return false
	default:
		return true
	}
}

func TestSimultaneousClaimsAgreeOnOneWinner(t *testing.T) {
	cases := []struct {
		name       string
		idA        string
		roleA      config.Role
		idB        string
		roleB      config.Role
		aShouldWin bool
	}{
		{"same role smaller id", "a", nor, "b", nor, true},
		{"primary beats smaller id", "a", nor, "b", pri, false},
		{"both primary", "x", pri, "y", pri, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			net := &memNet{}
			var arrived sync.WaitGroup
			arrived.Add(2)
			net.beforeClaim = func() { // beide Anfragen laufen, bevor eine zugestellt wird
				arrived.Done()
				arrived.Wait()
			}
			a, b := pair(tc.idA, tc.roleA, tc.idB, tc.roleB, net, newClock())
			var ra, rb Result
			var wg sync.WaitGroup
			wg.Add(2)
			go func() { defer wg.Done(); ra = a.Request(context.Background(), "h1") }()
			go func() { defer wg.Done(); rb = b.Request(context.Background(), "h1") }()
			wg.Wait()
			winner, loser, loserC := ra, rb, b
			if !tc.aShouldWin {
				winner, loser, loserC = rb, ra, a
			}
			if winner.Rejected || winner.Decision.Outcome != Allow {
				t.Fatalf("winner got %+v", winner)
			}
			if !loser.Rejected || loser.Decision.Outcome != Reject {
				t.Fatalf("loser got %+v", loser)
			}
			noEvent(t, loserC)
		})
	}
}

// Die Anfrage des Unterlegenen ist schon beendet, als die stärkere eintrifft.
func TestLateStrongerClaimBeatsFinishedRequest(t *testing.T) {
	net := &memNet{}
	strong, weak := pair("a", nor, "b", nor, net, newClock())
	ctx := context.Background()

	rw := weak.Request(ctx, "h1")
	if rw.Decision.Outcome != Allow {
		t.Fatalf("got %+v", rw)
	}
	rs := strong.Request(ctx, "h1")
	if rs.Rejected || rs.Decision.Outcome != Allow {
		t.Fatalf("got %+v", rs)
	}
	select {
	case e := <-weak.Events():
		if e.HMI != "h1" || e.By.Terminal != "a" || e.After != 0 {
			t.Fatalf("got %+v", e)
		}
	default:
		t.Fatal("loser must be told")
	}
	noEvent(t, strong)
}

// Die Anfrage des Schwächeren läuft noch, als die stärkere bei ihm eintrifft;
// seine eigene war beim Stärkeren vor dessen Anfrage angekommen.
func TestStrongerClaimArrivesWhileRequestStillRunning(t *testing.T) {
	clk := newClock()
	delivered, release := make(chan struct{}), make(chan struct{})
	var strong, weak *Coordinator
	weak = New(settings("b", nor, "a"), &fakeTransport{
		claim: func(_ context.Context, _ config.Peer, req ClaimReq) (ClaimResp, error) {
			resp := strong.handleClaim(req)
			close(delivered)
			<-release
			return resp, nil
		},
	}, clk)
	strong = New(settings("a", nor, "b"), &fakeTransport{
		claim: func(_ context.Context, _ config.Peer, req ClaimReq) (ClaimResp, error) {
			return weak.handleClaim(req), nil
		},
	}, clk)

	result := make(chan Result)
	go func() { result <- weak.Request(context.Background(), "h1") }()
	<-delivered
	rs := strong.Request(context.Background(), "h1")
	close(release)
	rw := <-result

	if rs.Rejected || rs.Decision.Outcome != Allow {
		t.Fatalf("strong got %+v", rs)
	}
	if !rw.Rejected || rw.Decision.Outcome != Reject {
		t.Fatalf("weak got %+v", rw)
	}
	noEvent(t, weak)
}

// Der Stärkere ist fertig, bevor der Schwächere fragt.
func TestLateWeakerClaimIsRejected(t *testing.T) {
	net := &memNet{}
	strong, weak := pair("a", nor, "b", nor, net, newClock())
	ctx := context.Background()
	strong.Request(ctx, "h1")
	r := weak.Request(ctx, "h1")
	if !r.Rejected || r.Decision.Outcome != Reject {
		t.Fatalf("got %+v", r)
	}
	noEvent(t, strong)
	noEvent(t, weak)
}

// Ohne Absprache über den Ablauf: In jeder Verschränkung genau ein Gewinner.
func TestSimultaneousClaimsNoSynchronisation(t *testing.T) {
	for i := 0; i < 300; i++ {
		net := &memNet{}
		a, b := pair("a", nor, "b", nor, net, newClock())
		var ra, rb Result
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); ra = a.Request(context.Background(), "h1") }()
		go func() { defer wg.Done(); rb = b.Request(context.Background(), "h1") }()
		wg.Wait()
		if wa, wb := won(ra, a), won(rb, b); !wa || wb {
			t.Fatalf("round %d: a=%v b=%v (%+v / %+v)", i, wa, wb, ra, rb)
		}
	}
}

func TestPendingClaimExpiresWithoutHold(t *testing.T) {
	clk := newClock()
	net := &memNet{}
	strong, weak := pair("a", nor, "b", nor, net, clk)
	ctx := context.Background()
	strong.Request(ctx, "h1") // Bediener bricht ab, Hold kommt nie
	clk.Advance(LeaseTTL)
	if r := weak.Request(ctx, "h1"); r.Rejected {
		t.Fatalf("abandoned claim still blocks: %+v", r)
	}
}

func TestHoldAndReleaseClearPendingClaim(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(c *Coordinator)
	}{
		{"hold", func(c *Coordinator) { c.Hold("h1", "s") }},
		{"release", func(c *Coordinator) { c.Release("h1") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
			c.Request(context.Background(), "h1")
			tc.f(c)
			resp := c.handleClaim(ClaimReq{HMI: "h1", Claim: Claim{Terminal: "b", Role: nor}})
			if resp.ContestedBy != nil {
				t.Fatalf("got %+v", resp.ContestedBy)
			}
		})
	}
}

func TestRejectedRequestLeavesNoPendingClaim(t *testing.T) {
	tr := &fakeTransport{claim: func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		return ClaimResp{Holder: &Holder{Terminal: "b", Role: pri}}, nil
	}}
	c := New(settings("a", nor, "b"), tr, newClock())
	if r := c.Request(context.Background(), "h1"); r.Decision.Outcome != Reject {
		t.Fatalf("got %+v", r)
	}
	if resp := c.handleClaim(ClaimReq{HMI: "h1", Claim: Claim{Terminal: "c", Role: nor}}); resp.ContestedBy != nil {
		t.Fatalf("got %+v", resp.ContestedBy)
	}
}

func TestIncomingClaimReportsOwnHoldNotThirdParty(t *testing.T) {
	c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
	c.handleHeartbeat("h1", Holder{Terminal: "z", Role: nor})
	if resp := c.handleClaim(ClaimReq{HMI: "h1", Claim: Claim{Terminal: "b", Role: nor}}); resp.Holder != nil {
		t.Fatalf("third-party lease reported: %+v", resp.Holder)
	}
	c.Hold("h2", "s2")
	resp := c.handleClaim(ClaimReq{HMI: "h2", Claim: Claim{Terminal: "b", Role: nor}})
	if resp.Holder == nil || resp.Holder.Terminal != "a" || resp.Holder.Session != "s2" {
		t.Fatalf("got %+v", resp.Holder)
	}
}

func TestIncomingTakeoverEmitsEvent(t *testing.T) {
	tr := &fakeTransport{}
	c := New(settings("a", nor, "b"), tr, newClock())
	c.Hold("h1", "s1")
	by := Holder{Terminal: "b", Name: "B", Role: pri}
	c.handleTakeover("h1", by, 5*time.Second)
	select {
	case e := <-c.Events():
		if e.HMI != "h1" || e.By != by || e.After != 5*time.Second {
			t.Fatalf("got %+v", e)
		}
	default:
		t.Fatal("no event")
	}
	c.tick(context.Background())
	if len(tr.sentBeats()) != 1 {
		t.Fatal("takeover must not stop anything itself")
	}
}

func TestEventDroppedAndLoggedWhenBufferFull(t *testing.T) {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
	for i := 0; i < 8; i++ {
		c.handleTakeover("h1", Holder{Terminal: "b"}, 0)
	}
	if buf.Len() != 0 {
		t.Fatalf("logged too early: %s", buf.String())
	}
	c.handleTakeover("h1", Holder{Terminal: "b"}, 0)
	if n := len(c.Events()); n != 8 {
		t.Fatalf("buffer %d", n)
	}
	if buf.Len() == 0 {
		t.Fatal("drop must be logged")
	}
}

func TestIncomingHeartbeatRenewsEvenWhileHolding(t *testing.T) {
	c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
	c.Hold("h1", "s1")
	c.handleHeartbeat("h1", Holder{Terminal: "b", Role: nor})
	if h := c.Occupancy()["h1"]; h.Terminal != "b" {
		t.Fatalf("got %+v", h)
	}
	noEvent(t, c)
}

func TestIncomingClaimFromSelfIDIgnored(t *testing.T) {
	c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
	c.Hold("h1", "s1")
	resp := c.handleClaim(ClaimReq{HMI: "h1", Claim: Claim{Terminal: "a", Role: pri}})
	if resp.Holder != nil || resp.ContestedBy != nil {
		t.Fatalf("got %+v", resp)
	}
	c.Release("h1")
	c.Request(context.Background(), "h2")
	if resp := c.handleClaim(ClaimReq{HMI: "h2", Claim: Claim{Terminal: "a", Role: pri}}); resp.ContestedBy != nil {
		t.Fatalf("got %+v", resp)
	}
	noEvent(t, c)
}

func TestSelfIgnoredInHeartbeatAndTakeover(t *testing.T) {
	c := New(settings("a", nor, "b"), &fakeTransport{}, newClock())
	c.handleHeartbeat("h1", Holder{Terminal: "a"})
	if len(c.Occupancy()) != 0 {
		t.Fatal("own heartbeat counted")
	}
	c.handleTakeover("h1", Holder{Terminal: "a"}, 0)
	noEvent(t, c)
}

// Ein Terminal, das sich selbst als Peer führt, darf sich nie selbst übernehmen.
func TestTerminalListedAsOwnPeerNeverTakesOverItself(t *testing.T) {
	net := &memNet{nodes: map[string]*Coordinator{}}
	c := New(settings("a", pri, "a"), net, newClock())
	net.nodes["a"] = c
	ctx := context.Background()

	if r := c.Request(ctx, "h1"); r.Decision.Outcome != Allow || r.Rejected {
		t.Fatalf("got %+v", r)
	}
	c.Hold("h1", "s1")
	c.tick(ctx)
	if len(c.Occupancy()) != 0 {
		t.Fatal("own heartbeat counted")
	}
	r := c.Request(ctx, "h1")
	if r.Decision.Outcome != Allow || r.Decision.Holder != nil {
		t.Fatalf("got %+v", r)
	}
	self := Holder{Terminal: "a", Name: "a"}
	if err := c.Takeover(ctx, "h1", Decision{Outcome: TakeOver, Holder: &self}); err != nil {
		t.Fatal(err)
	}
	noEvent(t, c)
}

func TestSelfRepliesDoNotCount(t *testing.T) {
	tr := &fakeTransport{claim: func(context.Context, config.Peer, ClaimReq) (ClaimResp, error) {
		return ClaimResp{
			Holder:      &Holder{Terminal: "b", Role: pri},
			ContestedBy: &Claim{Terminal: "b", Role: pri},
		}, nil
	}}
	r := New(settings("b", nor, "x"), tr, newClock()).Request(context.Background(), "h1")
	if r.Decision.Outcome != Allow || r.Rejected {
		t.Fatalf("got %+v", r)
	}
}

func TestOccupancyListsOtherTerminalsOnly(t *testing.T) {
	clk := newClock()
	c := New(settings("a", nor, "b"), &fakeTransport{}, clk)
	c.Hold("mine", "s")
	c.handleHeartbeat("h1", Holder{Terminal: "b"})
	c.handleHeartbeat("h2", Holder{Terminal: "c", Watch: true})
	occ := c.Occupancy()
	if len(occ) != 1 || occ["h1"].Terminal != "b" {
		t.Fatalf("got %+v", occ)
	}
	clk.Advance(LeaseTTL)
	if len(c.Occupancy()) != 0 {
		t.Fatal("expired lease listed")
	}
}
