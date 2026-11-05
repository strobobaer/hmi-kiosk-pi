package coord

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"slices"
	"sync"
	"time"

	"hmikiosk/internal/config"
)

const (
	peerTimeout = 1 * time.Second
	eventBuffer = 8
	claimTTL    = LeaseTTL // eine nie bestätigte Anfrage blockiert andere nur so lange
)

// Settings beschreibt dieses Terminal und seine Nachbarn.
type Settings struct {
	Self     Holder
	Peers    []config.Peer
	RulesFor func(hmi string) Rules
	// IsPermanent meldet die Daueranzeige je HMI; nil heißt nein. Self.Permanent zählt nicht.
	IsPermanent func(hmi string) bool
}

// Result ist das Ergebnis einer Anfrage. Unreachable und BadKey enthalten
// Peer-Namen; beides blockiert nicht, die Oberfläche zeigt nur einen Hinweis.
type Result struct {
	Decision    Decision
	Unreachable []string
	BadKey      []string
	Rejected    bool // gleichzeitige Anfrage verloren
}

// Event meldet, dass ein anderes Terminal dieses HMI übernimmt. Der
// Sitzungsmanager beendet die Sitzung nach After.
type Event struct {
	HMI   string
	By    Holder
	After time.Duration
}

type claimState struct {
	claim    Claim
	at       time.Time
	returned bool   // Request hat dem Aufrufer schon geantwortet
	lostTo   *Claim // stärkere Anfrage traf ein, solange Request noch lief
}

type holderSource struct {
	terminal string
	peer     config.Peer
}

// Coordinator stimmt die Belegung von HMIs mit den anderen Terminals ab.
type Coordinator struct {
	sMu sync.RWMutex
	s   Settings // Self und Peers ändert UpdateIdentity; nur über settings lesen

	tr     Transport
	clock  Clock
	table  *Table
	events chan Event

	newTicker func(time.Duration) (<-chan time.Time, func())

	mu      sync.Mutex
	held    map[string]Holder
	pending map[string]*claimState
	sources map[string]holderSource // wer den Halter zuletzt gemeldet hat, je HMI
}

// New erzeugt den Koordinator; Herzschläge laufen erst mit Run.
func New(s Settings, tr Transport, c Clock) *Coordinator {
	return &Coordinator{
		s:      s,
		tr:     tr,
		clock:  c,
		table:  NewTable(c),
		events: make(chan Event, eventBuffer),
		newTicker: func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		},
		held:    map[string]Holder{},
		pending: map[string]*claimState{},
		sources: map[string]holderSource{},
	}
}

// UpdateIdentity übernimmt geänderte Terminaldaten. Laufende Anfragen behalten ihren Stand.
func (c *Coordinator) UpdateIdentity(self Holder, peers []config.Peer) {
	c.sMu.Lock()
	defer c.sMu.Unlock()
	c.s.Self = self
	c.s.Peers = slices.Clone(peers)
}

func (c *Coordinator) settings() Settings {
	c.sMu.RLock()
	defer c.sMu.RUnlock()
	return c.s
}

// withIdentity meldet eine Belegung unter den aktuellen Terminaldaten.
func withIdentity(h, self Holder) Holder {
	h.Terminal, h.Name, h.Role = self.Terminal, self.Name, self.Role
	return h
}

// Events liefert eingehende Übernahmen.
func (c *Coordinator) Events() <-chan Event { return c.events }

type peerReply struct {
	resp ClaimResp
	err  error
}

// Request fragt alle Terminals an und entscheidet. Bleibt die Anfrage offen
// (kein Reject), gilt sie bis Hold, Release oder Fristablauf als laufend.
func (c *Coordinator) Request(ctx context.Context, hmi string) Result {
	s := c.settings()
	self := s.Self.Terminal
	own := &claimState{
		claim: Claim{Terminal: self, Role: s.Self.Role, Nonce: rand.Text(), Name: s.Self.Name},
		at:    c.clock.Now(),
	}
	c.mu.Lock()
	c.pending[hmi] = own
	c.mu.Unlock()

	replies := c.askPeers(ctx, s.Peers, ClaimReq{HMI: hmi, Claim: own.claim})

	var (
		res        Result
		best       *Holder
		bestSource *config.Peer
		winner     *Claim
	)
	for i, rep := range replies {
		peer := s.Peers[i]
		switch {
		case errors.Is(rep.err, ErrBadKey):
			res.BadKey = append(res.BadKey, peer.Name)
			continue
		case rep.err != nil:
			res.Unreachable = append(res.Unreachable, peer.Name)
			continue
		}
		if h := rep.resp.Holder; h != nil && h.Terminal != self && (best == nil || stronger(*h, *best)) {
			best, bestSource = h, &peer
		}
		if cb := rep.resp.ContestedBy; cb != nil && cb.Terminal != self {
			if w := ResolveSimultaneous(own.claim, *cb); w.Terminal != self {
				merged := strongerClaim(winner, w)
				winner = &merged
			}
		}
	}
	// Die Tabelle zählt auch dann, wenn der Halter gerade nicht antwortet: Sein
	// letzter Herzschlag ist jünger als die Frist, die Belegung gilt noch.
	if h, ok := c.table.Holder(hmi); ok && h.Terminal != self && (best == nil || stronger(h, *best)) {
		best, bestSource = &h, nil
	}
	res.Decision = Decide(s.Self.Role, best, s.RulesFor(hmi))

	c.mu.Lock()
	defer c.mu.Unlock()
	if own.lostTo != nil {
		merged := strongerClaim(winner, *own.lostTo)
		winner = &merged
	}
	// Mitschauen schließt niemanden aus, also gibt es dabei nichts zu verlieren.
	if winner != nil && res.Decision.Outcome != AllowWatch {
		res.Rejected = true
		res.Decision = Decision{
			Outcome: Reject,
			Holder:  &Holder{Terminal: winner.Terminal, Name: winner.Name, Role: winner.Role},
		}
	}
	if best != nil && bestSource != nil {
		c.sources[hmi] = holderSource{terminal: best.Terminal, peer: *bestSource}
	} else {
		delete(c.sources, hmi)
	}
	if c.pending[hmi] == own {
		// Mitschauen belegt nicht und tritt daher auch nicht in Konkurrenz.
		if o := res.Decision.Outcome; o == Reject || o == AllowWatch {
			delete(c.pending, hmi)
		} else {
			own.returned = true
		}
	}
	return res
}

func (c *Coordinator) askPeers(ctx context.Context, peers []config.Peer, req ClaimReq) []peerReply {
	replies := make([]peerReply, len(peers))
	var wg sync.WaitGroup
	for i, peer := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, peerTimeout)
			defer cancel()
			replies[i].resp, replies[i].err = c.tr.Claim(pctx, peer, req)
		}()
	}
	wg.Wait()
	return replies
}

// stronger ordnet Halter für die Entscheidung: Mitschauer zuletzt, primär vor
// normal. Der Rest macht das Ergebnis unabhängig von der Antwortreihenfolge.
func stronger(a, b Holder) bool {
	if a.Watch != b.Watch {
		return b.Watch
	}
	if (a.Role == config.RolePrimary) != (b.Role == config.RolePrimary) {
		return a.Role == config.RolePrimary
	}
	if a.Permanent != b.Permanent {
		return a.Permanent
	}
	return a.Terminal < b.Terminal
}

func strongerClaim(cur *Claim, next Claim) Claim {
	if cur == nil {
		return next
	}
	return ResolveSimultaneous(*cur, next)
}

// Takeover fordert den Halter aus der Entscheidung auf, das HMI freizugeben.
func (c *Coordinator) Takeover(ctx context.Context, hmi string, d Decision) error {
	if d.Holder == nil {
		return errors.New("Übernahme ohne Halter")
	}
	s := c.settings()
	if d.Holder.Terminal == s.Self.Terminal {
		return nil
	}
	peer, ok := c.peerOf(hmi, *d.Holder)
	if !ok {
		return fmt.Errorf("Terminal „%s“ ist nicht bekannt", d.Holder.Name)
	}
	pctx, cancel := context.WithTimeout(ctx, peerTimeout)
	defer cancel()
	err := c.tr.Takeover(pctx, peer, hmi, s.Self, d.Warn)
	switch {
	case err == nil:
		c.table.Drop(hmi, d.Holder.Terminal)
		return nil
	case errors.Is(err, ErrBadKey):
		return fmt.Errorf("Terminal „%s“ lehnt den Schlüssel ab: %w", peer.Name, err)
	default:
		return fmt.Errorf("Terminal „%s“ ist nicht erreichbar: %w", peer.Name, err)
	}
}

// peerOf findet den Peer des Halters. Halter nur aus der Tabelle hat kein Peer
// gemeldet; dann bleibt nur der Name, denn Peers tragen keine Kennung.
func (c *Coordinator) peerOf(hmi string, h Holder) (config.Peer, bool) {
	c.mu.Lock()
	src, ok := c.sources[hmi]
	c.mu.Unlock()
	if ok && src.terminal == h.Terminal {
		return src.peer, true
	}
	for _, p := range c.settings().Peers {
		if p.Name == h.Name {
			return p, true
		}
	}
	return config.Peer{}, false
}

// Hold beginnt die Belegung: Run meldet sie ab jetzt per Herzschlag.
func (c *Coordinator) Hold(hmi, session string) {
	h := c.settings().Self
	h.Session = session
	h.Permanent = c.isPermanent(hmi)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held[hmi] = h
	delete(c.pending, hmi)
}

func (c *Coordinator) isPermanent(hmi string) bool {
	isPermanent := c.settings().IsPermanent
	return isPermanent != nil && isPermanent(hmi)
}

// Release beendet die Belegung. Die anderen erfahren es nicht, ihre Frist läuft ab.
func (c *Coordinator) Release(hmi string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.held, hmi)
	delete(c.pending, hmi)
	delete(c.sources, hmi)
}

// Run sendet Herzschläge, bis ctx endet.
func (c *Coordinator) Run(ctx context.Context) {
	ticks, stop := c.newTicker(HeartbeatEvery)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			c.tick(ctx)
		}
	}
}

func (c *Coordinator) tick(ctx context.Context) {
	c.mu.Lock()
	held := make(map[string]Holder, len(c.held))
	for hmi, h := range c.held {
		held[hmi] = h
	}
	c.mu.Unlock()

	s := c.settings()
	var wg sync.WaitGroup
	for hmi, h := range held {
		h = withIdentity(h, s.Self)
		h.Permanent = c.isPermanent(hmi)
		for _, peer := range s.Peers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				pctx, cancel := context.WithTimeout(ctx, peerTimeout)
				defer cancel()
				// Fehler zeigt die nächste Anfrage; alle 2 s protokollieren brächte nur Lärm.
				_ = c.tr.Heartbeat(pctx, peer, hmi, h)
			}()
		}
	}
	wg.Wait()
}

// Occupancy liefert die von anderen Terminals belegten HMIs.
func (c *Coordinator) Occupancy() map[string]Holder {
	self := c.settings().Self.Terminal
	occ := c.table.Snapshot()
	for hmi, h := range occ {
		if h.Watch || h.Terminal == self {
			delete(occ, hmi)
		}
	}
	return occ
}

func (c *Coordinator) handleClaim(req ClaimReq) ClaimResp {
	self := c.settings().Self
	if req.Claim.Terminal == self.Terminal {
		return ClaimResp{}
	}
	permanent := c.isPermanent(req.HMI)
	c.mu.Lock()
	defer c.mu.Unlock()
	var resp ClaimResp
	// Wer schon hält, konkurriert nicht mehr, auch wenn er erneut angefragt hat:
	// Verdrängt wird er nur per Übernahme mit Vorwarnung.
	if h, ok := c.held[req.HMI]; ok {
		h = withIdentity(h, self)
		h.Permanent = permanent
		resp.Holder = &h
		return resp
	}
	p, ok := c.pending[req.HMI]
	if !ok {
		return resp
	}
	if c.clock.Now().Sub(p.at) >= claimTTL {
		delete(c.pending, req.HMI)
		return resp
	}
	claim := p.claim
	resp.ContestedBy = &claim
	if w := ResolveSimultaneous(claim, req.Claim); w.Terminal != claim.Terminal {
		// Der Anfragende hält sich für den Gewinner, also muss auch diese Seite
		// aufgeben, sonst hingen beide am HMI. Hat Request schon "erlaubt"
		// geantwortet, bleibt dafür nur das Ereignis.
		if p.returned {
			delete(c.pending, req.HMI)
			c.emit(Event{HMI: req.HMI, By: Holder{Terminal: w.Terminal, Name: w.Name, Role: w.Role}})
		} else {
			p.lostTo = &w
		}
	}
	return resp
}

func (c *Coordinator) handleHeartbeat(hmi string, h Holder) {
	if h.Terminal == c.settings().Self.Terminal {
		return
	}
	c.table.Renew(hmi, h)
}

func (c *Coordinator) handleTakeover(hmi string, by Holder, warn time.Duration) {
	if by.Terminal == c.settings().Self.Terminal {
		return
	}
	c.emit(Event{HMI: hmi, By: by, After: warn})
}

func (c *Coordinator) emit(e Event) {
	select {
	case c.events <- e:
	default:
		log.Printf("Koordinator: Ereignis für HMI %q verworfen, Puffer voll", e.HMI)
	}
}
