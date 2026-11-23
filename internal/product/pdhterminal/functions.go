package pdhterminal

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Funktionen des Terminals, die am Gerät und in PDH ein- und ausgeschaltet werden. Es gilt die jüngere Änderung
// (Vertrag: docs/superpowers/specs/2026-11-22-funktionen-sync-contract.md).
const (
	FuncFault      = "stoerung"
	FuncTicket     = "ticket"
	FuncSetup      = "ruesten"
	FuncDowntime   = "stillstand"
	FuncHMI        = "hmi"
	FuncMaint      = "wartungen"
	FuncCases      = "vorgaenge"
	FuncShifts     = "schichtplan"
	FuncSaverSlide = "saver_slides"
)

// FuncDef beschreibt einen Schalter für die Admin-Seite.
type FuncDef struct {
	Key, Label string
	Default    bool
}

// FuncDefs sind alle Schalter in der Reihenfolge der Admin-Seite. Default gilt, solange weder Gerät noch PDH
// einen Wert gesetzt haben (Grundwerte von PDH: alle Funktionen an, die Folienschau aus).
var FuncDefs = []FuncDef{
	{FuncFault, "Störung", true},
	{FuncTicket, "Ticket", true},
	{FuncSetup, "Rüsten", true},
	{FuncDowntime, "Stillstand", true},
	{FuncHMI, "HMI", true},
	{FuncMaint, "Wartungen", true},
	{FuncCases, "Vorgänge", true},
	{FuncShifts, "Schichtplan", true},
	{FuncSaverSlide, "Folienschau als Bildschirmschoner", false},
}

func knownFunc(key string) bool {
	for _, d := range FuncDefs {
		if d.Key == key {
			return true
		}
	}
	return false
}

// timeFormat: RFC 3339, UTC, Millisekunden, immer mit Z.
const timeFormat = "2006-01-02T15:04:05.000Z"

// FuncValue ist ein Schalter mit dem Zeitpunkt seiner letzten Änderung. Der Nullzeitpunkt heißt: noch nie
// geändert (Grundwert).
type FuncValue struct {
	Enabled   bool
	UpdatedAt time.Time
}

// MarshalJSON schreibt updated_at im Vertragsformat.
func (v FuncValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Enabled   bool   `json:"enabled"`
		UpdatedAt string `json:"updated_at"`
	}{v.Enabled, v.UpdatedAt.UTC().Format(timeFormat)})
}

// FuncSet ist ein Stand aller oder einiger Schalter nach Schlüssel.
type FuncSet map[string]FuncValue

// funcStore hält den Stand im Speicher und in <data>/pdh/functions.json.
type funcStore struct {
	dir     string
	mu      sync.Mutex
	funcs   FuncSet
	pending map[string]bool // lokale Änderungen, die PDH noch nicht bestätigt hat
}

func functionsPath(dataDir string) string { return filepath.Join(dataDir, "pdh", "functions.json") }

type storedFuncs struct {
	Functions FuncSet         `json:"functions"`
	Pending   map[string]bool `json:"pending,omitempty"`
}

func openFuncStore(dataDir string) *funcStore {
	s := &funcStore{dir: dataDir, funcs: FuncSet{}, pending: map[string]bool{}}
	b, err := os.ReadFile(functionsPath(dataDir))
	if err != nil {
		return s
	}
	var f struct {
		Functions map[string]wireFunc `json:"functions"`
		Pending   map[string]bool     `json:"pending"`
	}
	if json.Unmarshal(b, &f) != nil {
		return s // beschädigt: Grundwerte, PDH füllt wieder auf
	}
	s.funcs = parseFuncs(f.Functions)
	for k, p := range f.Pending {
		if _, ok := s.funcs[k]; ok && p {
			s.pending[k] = true
		}
	}
	return s
}

func parseTime(s string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// saveLocked schreibt den Stand (s.mu gehalten).
func (s *funcStore) saveLocked() error {
	b, err := json.Marshal(storedFuncs{Functions: s.funcs, Pending: s.pending})
	if err != nil {
		return err
	}
	return writeFileAtomic(functionsPath(s.dir), b)
}

// Get liefert einen Schalter; ohne gespeicherten Wert den Grundwert mit Nullzeitpunkt.
func (s *funcStore) Get(key string) FuncValue {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getLocked(key)
}

func (s *funcStore) getLocked(key string) FuncValue {
	if v, ok := s.funcs[key]; ok {
		return v
	}
	for _, d := range FuncDefs {
		if d.Key == key {
			return FuncValue{Enabled: d.Default}
		}
	}
	return FuncValue{}
}

// Pending sagt, ob der Schlüssel noch nicht von PDH bestätigt ist.
func (s *funcStore) Pending(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pending[key]
}

// SetLocal übernimmt eine Änderung am Gerät mit dem Zeitpunkt now und merkt sie zur Übertragung vor.
// Ändert sich der Wert nicht, passiert nichts (kein neuer Zeitpunkt).
func (s *funcStore) SetLocal(key string, enabled bool, now time.Time) (changed bool, err error) {
	if !knownFunc(key) {
		return false, errors.New("unbekannte Funktion")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur := s.getLocked(key)
	if cur.Enabled == enabled {
		return false, nil
	}
	now = now.UTC().Truncate(time.Millisecond)
	// Der Zeitpunkt geht nie rückwärts (Uhr zurückgestellt): Die neue Änderung soll die jüngste bleiben.
	if !now.After(cur.UpdatedAt) {
		now = cur.UpdatedAt.Add(time.Millisecond)
	}
	old, had := s.funcs[key]
	oldPending := s.pending[key]
	s.funcs[key] = FuncValue{Enabled: enabled, UpdatedAt: now}
	s.pending[key] = true
	if err := s.saveLocked(); err != nil {
		if had {
			s.funcs[key] = old
		} else {
			delete(s.funcs, key)
		}
		if oldPending {
			s.pending[key] = true
		} else {
			delete(s.pending, key)
		}
		return false, errors.New("Einstellung konnte nicht gespeichert werden")
	}
	return true, nil
}

// PendingSet liefert die noch nicht bestätigten Schalter.
func (s *funcStore) PendingSet() FuncSet {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := FuncSet{}
	for k := range s.pending {
		out[k] = s.getLocked(k)
	}
	return out
}

// ApplyRemote wendet den Stand von PDH an. Je Schlüssel gilt der Wert von PDH, wenn sein Zeitpunkt nicht
// älter ist als der lokale (gleich: PDH gewinnt). Ist der lokale jünger, bleibt er und wird (weiter)
// übertragen. Führt PDH den Schlüssel, ist er bestätigt (nicht mehr vorgemerkt).
// Liefert die Schlüssel, deren Wert sich geändert hat.
func (s *funcStore) ApplyRemote(remote FuncSet) (changed []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dirty := false
	for k, rv := range remote {
		if !knownFunc(k) {
			continue
		}
		rv.UpdatedAt = rv.UpdatedAt.UTC()
		cur := s.getLocked(k)
		if rv.UpdatedAt.Before(cur.UpdatedAt) {
			if s.pending[k] && rv.Enabled == cur.Enabled {
				// Gleicher Wert, aber älterer (geklemmter) Zeitpunkt: PDH führt den Wert. Bestätigt wird nach Wert,
				// der Zeitpunkt von PDH gilt (Uhrenabweichung).
				s.funcs[k] = rv
				delete(s.pending, k)
				dirty = true
			}
			continue // lokal jünger mit anderem Wert: bleibt und geht an PDH
		}
		old, had := s.funcs[k]
		if had && old.Enabled == rv.Enabled && old.UpdatedAt.Equal(rv.UpdatedAt) && !s.pending[k] {
			continue
		}
		if cur.Enabled != rv.Enabled {
			changed = append(changed, k)
		}
		s.funcs[k] = rv
		delete(s.pending, k)
		dirty = true
	}
	if dirty {
		if err := s.saveLocked(); err != nil {
			log.Printf("Funktionen nicht gespeichert: %v", err)
		}
	}
	return changed
}

// --- Verbindung zu PDH ---

// errFuncUnsupported: PDH kennt die Schnittstelle nicht (404 oder 405): nur lokal, später wieder prüfen.
var errFuncUnsupported = errors.New("PDH kennt die Funktionen-Schnittstelle nicht")

type wireFunc struct {
	Enabled   *bool  `json:"enabled"`
	UpdatedAt string `json:"updated_at"`
}

// parseFuncs liest das Feld functions; ungültige oder unbekannte Einträge werden übergangen.
func parseFuncs(m map[string]wireFunc) FuncSet {
	out := FuncSet{}
	for k, v := range m {
		if !knownFunc(k) || v.Enabled == nil {
			continue
		}
		t, ok := parseTime(v.UpdatedAt)
		if !ok {
			continue
		}
		out[k] = FuncValue{Enabled: *v.Enabled, UpdatedAt: t}
	}
	return out
}

// Remote ist, was PDH über Funktionen und Farben mitteilt. Funcs nil: PDH hat das Feld nicht gesendet.
type Remote struct {
	Funcs FuncSet
	Theme *Theme
}

type remoteWire struct {
	Functions map[string]wireFunc `json:"functions"`
	Theme     *ThemeWire          `json:"theme"`
}

func (w remoteWire) remote() Remote {
	var r Remote
	if w.Functions != nil {
		r.Funcs = parseFuncs(w.Functions)
	}
	if w.Theme != nil {
		t := ThemeFromWire(*w.Theme)
		r.Theme = &t
	}
	return r
}

// FetchRemote fragt den Gerätezustand (Statusabfrage) und liest functions und theme daraus.
func (c Client) FetchRemote(ctx context.Context, d Device) (State, Remote, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.URL, "/")+"/api/terminal/v1/device", nil)
	if err != nil {
		return StateUnreachable, Remote{}, errUnreachable
	}
	req.Header.Set("Authorization", "Bearer "+d.Secret)
	req.Header.Set("Accept", "application/json")
	setClientHeaders(req)
	resp, err := c.client().Do(req)
	if err != nil {
		st, e := transportErr(err)
		return st, Remote{}, e
	}
	defer resp.Body.Close()
	data := readBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var w remoteWire
		_ = json.Unmarshal(data, &w) // ein älteres PDH hat die Felder nicht
		return StateOK, w.remote(), nil
	case http.StatusUnauthorized:
		return StateRevoked, Remote{}, nil
	}
	return StateUnreachable, Remote{}, errUnreachable
}

// PushFunctions überträgt Schalter an PDH (PUT /api/terminal/v1/functions) und liefert den Stand danach.
// 404 und 405 ergeben errFuncUnsupported.
func (c Client) PushFunctions(ctx context.Context, d Device, set FuncSet) (State, Remote, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	body, err := json.Marshal(map[string]FuncSet{"functions": set})
	if err != nil {
		return StateUnreachable, Remote{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, strings.TrimRight(d.URL, "/")+"/api/terminal/v1/functions", bytes.NewReader(body))
	if err != nil {
		return StateUnreachable, Remote{}, errUnreachable
	}
	req.Header.Set("Authorization", "Bearer "+d.Secret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	setClientHeaders(req)
	resp, err := c.client().Do(req)
	if err != nil {
		st, e := transportErr(err)
		return st, Remote{}, e
	}
	defer resp.Body.Close()
	data := readBody(resp)
	switch resp.StatusCode {
	case http.StatusOK:
		var w remoteWire
		if json.Unmarshal(data, &w) != nil || w.Functions == nil {
			return StateUnreachable, Remote{}, errors.New("Ungültige Antwort von PDH")
		}
		return StateOK, w.remote(), nil
	case http.StatusUnauthorized:
		return StateRevoked, Remote{}, nil
	case http.StatusNotFound, http.StatusMethodNotAllowed:
		return StateOK, Remote{}, errFuncUnsupported
	}
	return StateUnreachable, Remote{}, errUnreachable
}

// FuncSync gleicht die Schalter mit PDH ab: Statusabfrage, Übertragen lokaler Änderungen, Ereigniskanal.
type FuncSync struct {
	store  *funcStore
	dir    string
	client Client
	now    func() time.Time
	kick   chan struct{}

	mu           sync.Mutex
	unsupported  time.Time // bis dahin keine Anfrage an die Schnittstelle (PDH antwortete 404)
	blockedUntil time.Time // bis dahin gar keine Anfrage (Gerät gesperrt)
	lastOK       time.Time
	lastErr      string
}

// Zeiten, die Tests verkürzen.
var (
	funcPollEvery     = 30 * time.Second
	funcUnsupportedBy = time.Hour
	funcRevokedBy     = 10 * time.Minute
)

// NewFuncSync legt den Abgleich an.
func NewFuncSync(dataDir string, client Client) *FuncSync {
	return &FuncSync{store: openFuncStore(dataDir), dir: dataDir, client: client, now: time.Now, kick: make(chan struct{}, 1)}
}

// Get liefert einen Schalter.
func (f *FuncSync) Get(key string) FuncValue { return f.store.Get(key) }

// Pending sagt, ob die Änderung noch nicht von PDH bestätigt ist.
func (f *FuncSync) Pending(key string) bool { return f.store.Pending(key) }

// Set übernimmt eine Änderung am Gerät und stößt die Übertragung an.
func (f *FuncSync) Set(key string, enabled bool) (bool, error) {
	ch, err := f.store.SetLocal(key, enabled, f.now())
	if ch {
		f.Kick()
	}
	return ch, err
}

// Kick löst einen Abgleich sofort aus.
func (f *FuncSync) Kick() {
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// SyncStatus ist, was die Admin-Seite zum Abgleich zeigt.
type SyncStatus struct {
	Unsupported bool      // PDH kennt die Schnittstelle nicht
	LastOK      time.Time // letzter gelungener Abgleich
	LastErr     string
}

// Status liefert den Stand des Abgleichs.
func (f *FuncSync) Status() SyncStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return SyncStatus{Unsupported: f.now().Before(f.unsupported), LastOK: f.lastOK, LastErr: f.lastErr}
}

func (f *FuncSync) setErr(msg string) {
	f.mu.Lock()
	f.lastErr = msg
	f.mu.Unlock()
}

func (f *FuncSync) block(d time.Duration, msg string) {
	f.mu.Lock()
	f.blockedUntil = f.now().Add(d)
	f.lastErr = msg
	f.mu.Unlock()
}

// applyRemote übernimmt Schalter und Farben von PDH.
func (f *FuncSync) applyRemote(r Remote) {
	if r.Funcs != nil {
		f.store.ApplyRemote(r.Funcs)
	}
	if r.Theme != nil && *r.Theme != LoadTheme(f.dir) {
		if err := SaveTheme(f.dir, *r.Theme); err != nil {
			log.Printf("Farben nicht gespeichert: %v", err)
		}
	}
}

// SyncOnce ist ein Durchgang: Stand von PDH holen und anwenden, danach lokal Neueres übertragen.
// Fehler sind nicht schlimm: Der lokale Wert bleibt, der nächste Durchgang versucht es wieder.
func (f *FuncSync) SyncOnce(ctx context.Context) {
	now := f.now()
	f.mu.Lock()
	blocked := now.Before(f.blockedUntil)
	f.mu.Unlock()
	if blocked {
		return
	}
	d, err := LoadDevice(f.dir)
	if err != nil || d == nil {
		return
	}
	st, rem, _ := f.client.FetchRemote(ctx, *d)
	switch st {
	case StateRevoked:
		f.block(funcRevokedBy, "Gerät in PDH gesperrt")
		return
	case StateUntrusted, StateUnreachable:
		f.setErr("PDH nicht erreichbar")
		return
	}
	f.applyRemote(rem)
	f.mu.Lock()
	f.lastOK, f.lastErr = now, ""
	skip := now.Before(f.unsupported)
	f.mu.Unlock()

	pend := f.store.PendingSet()
	if len(pend) == 0 || skip {
		return
	}
	st, rem, err = f.client.PushFunctions(ctx, *d, pend)
	switch {
	case errors.Is(err, errFuncUnsupported):
		f.mu.Lock()
		f.unsupported = now.Add(funcUnsupportedBy)
		f.mu.Unlock()
	case st == StateRevoked:
		f.block(funcRevokedBy, "Gerät in PDH gesperrt")
	case err != nil:
		f.setErr("PDH nicht erreichbar")
	default:
		f.applyRemote(rem)
	}
}

// Run gleicht ab, bis ctx endet: beim Start, alle funcPollEvery und nach jeder lokalen Änderung; dazu
// kommen Ereignisse von PDH.
func (f *FuncSync) Run(ctx context.Context) {
	go f.runEvents(ctx)
	f.SyncOnce(ctx)
	t := time.NewTicker(funcPollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			f.SyncOnce(ctx)
		case <-f.kick:
			f.SyncOnce(ctx)
		}
	}
}

// runEvents liest den Ereigniskanal /terminal/events mit dem Geräte-Geheimnis. Antwortet PDH nicht mit
// 200 (ältere Version), wird fünf Minuten später neu versucht; bis dahin genügt die Statusabfrage.
func (f *FuncSync) runEvents(ctx context.Context) {
	for ctx.Err() == nil {
		wait := 5 * time.Minute
		if d, err := LoadDevice(f.dir); err == nil && d != nil && f.readEvents(ctx, *d) {
			wait = 5 * time.Second // der Kanal stand und ist abgerissen
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

// readEvents liest den Kanal bis zum Ende; true, wenn er mit 200 geöffnet wurde.
func (f *FuncSync) readEvents(ctx context.Context, d Device) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(d.URL, "/")+"/terminal/events", nil)
	if err != nil {
		return false
	}
	req.Header.Set("Authorization", "Bearer "+d.Secret)
	req.Header.Set("Accept", "text/event-stream")
	setClientHeaders(req)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req = req.WithContext(ctx)
	hc := f.client.client()
	hc.Timeout = 0 // Dauerverbindung
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return false
	}
	// Ist der Kanal eventIdle lang still (tote Verbindung), wird er abgebrochen und neu aufgebaut.
	idle := time.AfterFunc(eventIdle, cancel)
	defer idle.Stop()
	f.consumeEvents(&idleReader{r: resp.Body, t: idle})
	return true
}

// eventIdle: So lange ohne Daten gilt der Ereigniskanal als tot. PDH muss keine Herzschläge senden; der
// Neuaufbau ist harmlos, die Statusabfrage bleibt der Rückfall.
var eventIdle = 90 * time.Second

// idleReader setzt die Frist bei jedem Lesen mit Daten zurück.
type idleReader struct {
	r io.Reader
	t *time.Timer
}

func (i *idleReader) Read(p []byte) (int, error) {
	n, err := i.r.Read(p)
	if n > 0 {
		i.t.Reset(eventIdle)
	}
	return n, err
}

// consumeEvents liest Server-Sent Events; „settings“ wird angewendet.
func (f *FuncSync) consumeEvents(r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 16<<10), 64<<10)
	var event, data string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if event == "settings" && data != "" {
				f.HandleSettingsEvent([]byte(data))
			}
			event, data = "", ""
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			data += strings.TrimSpace(line[5:])
		}
	}
}

// HandleSettingsEvent wendet die Nutzdaten eines Ereignisses „settings“ an (Felder functions und theme).
// Liegt lokal etwas Jüngeres vor, wird es sofort übertragen.
func (f *FuncSync) HandleSettingsEvent(data []byte) {
	var w remoteWire
	if json.Unmarshal(data, &w) != nil {
		return
	}
	f.applyRemote(w.remote())
	if len(f.store.PendingSet()) > 0 {
		f.Kick()
	}
}
