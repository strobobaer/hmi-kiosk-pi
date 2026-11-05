package selftest

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	"math"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/rfb"
)

const (
	testW, testH = 1024, 600
	// barHeight ist die Höhe der Sitzungsleiste wie in session und browser.
	barHeight = 48
	// sessionBase hält die Selbstprüfung von den App-IDs echter Sitzungen fern:
	// Remmina übergibt bei gleicher App-ID die Verbindung an die erste Instanz.
	sessionBase = 9000
	// markerTolerance: Marker sind 7 Pixel groß; so weit darf ein Tipp daneben liegen.
	markerTolerance = 15.0
)

func fail(format string, a ...any) StepResult {
	return StepResult{Detail: fmt.Sprintf(format, a...)}
}

func secs(d time.Duration) string { return strconv.FormatFloat(d.Seconds(), 'f', 1, 64) + " s" }

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// tracked merkt sich das Ende eines Prozesses; Done liefert den Wert nur einmal.
type tracked struct {
	client.Process
	exited chan struct{}
	err    error
}

func track(p client.Process) *tracked {
	t := &tracked{Process: p, exited: make(chan struct{})}
	go func() {
		t.err = <-p.Done()
		close(t.exited)
	}()
	return t
}

func (t *tracked) gone() bool {
	select {
	case <-t.exited:
		return true
	default:
		return false
	}
}

// rig ist ein Test-VNC-Server auf 127.0.0.1 mit zufälligem Passwort und den
// Clients, die dagegen laufen.
type rig struct {
	d        Deps
	srv      *rfb.TestServer
	ln       net.Listener
	port     int
	password string
	n        int
	procs    []*tracked
	owned    map[*tracked]func() // RAM-Ordner-Aufräumer je Client
}

func newRig(d Deps) (*rig, error) {
	switch {
	case d.Runner == nil:
		return nil, errors.New("kein Programmstarter für den Client angebunden")
	case d.Profile == nil:
		return nil, errors.New("kein Client-Profil angebunden")
	case d.Screen == nil:
		return nil, errors.New("Bildschirmgröße nicht verfügbar")
	case d.RAMDir == nil:
		return nil, errors.New("kein RAM-Ordner angebunden")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("Test-VNC-Server: %w", err)
	}
	pw, err := randomPassword()
	if err != nil {
		ln.Close()
		return nil, err
	}
	r := &rig{d: d, ln: ln, owned: map[*tracked]func(){}, port: ln.Addr().(*net.TCPAddr).Port, password: pw, srv: rfb.NewTestServer(testW, testH, pw)}
	go func() { _ = r.srv.Serve(ln) }()
	return r, nil
}

func randomPassword() (string, error) {
	const alphabet = "abcdefghijkmnpqrstuvwxyz23456789"
	b := make([]byte, 8) // VNC-Passwörter gelten nur bis 8 Zeichen
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b), nil
}

// start startet einen Client wie die Sitzung: unter der Leiste, mit den Einstellungen des Terminals.
func (r *rig) start(password string, viewOnly bool) (*tracked, error) {
	cfg := r.d.config()
	w, h := r.d.Screen()
	spec := client.Spec{
		Host: "127.0.0.1", Port: r.port, Password: password, ViewOnly: viewOnly,
		Quality: cfg.Client.Quality, ColorDepth: cfg.Client.ColorDepth, Scale: cfg.Client.Scale,
		W: w, H: h, SessionN: sessionBase + r.n,
	}
	if !cfg.Kiosk.HideBar {
		spec.Y, spec.H = barHeight, h-barHeight
	}
	r.n++
	dir, cleanup, err := r.d.RAMDir()
	if err != nil {
		return nil, fmt.Errorf("RAM-Ordner: %w", err)
	}
	cmd, err := r.d.Profile().Prepare(dir, spec)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("Client-Profil: %w", err)
	}
	r.d.show("bar")
	p, err := r.d.Runner.Start(cmd)
	if err != nil {
		cleanup()
		return nil, fmt.Errorf("Client startet nicht: %w", err)
	}
	t := track(p)
	r.procs = append(r.procs, t)
	r.owned[t] = cleanup
	return t, nil
}

// connected entspricht der Prüfung der Sitzung: ESTABLISHED zum Server.
func (r *rig) connected() bool {
	if r.srv.ClientCount() < 1 {
		return false
	}
	if r.d.Established == nil {
		return true
	}
	ok, _ := r.d.Established(net.IPv4(127, 0, 0, 1), r.port)
	return ok
}

// waitConnected wartet bis zur Verbindung; ein vorher beendeter Client ist ein Fehler.
func (r *rig) waitConnected(ctx context.Context, p *tracked) (time.Duration, error) {
	tm := r.d.Timing
	start := tm.Now()
	deadline := time.NewTimer(tm.Connect)
	defer deadline.Stop()
	tick := time.NewTicker(tm.Poll)
	defer tick.Stop()
	for {
		if r.connected() {
			return tm.Now().Sub(start), nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-p.exited:
			if r.connected() {
				continue
			}
			return 0, fmt.Errorf("Client endete vor der Verbindung (%v)", p.err)
		case <-deadline.C:
			return 0, fmt.Errorf("keine Verbindung innerhalb von %s", secs(tm.Connect))
		case <-tick.C:
		}
	}
}

// waitExit wartet auf das Ende; false heißt: noch da.
func (r *rig) waitExit(ctx context.Context, p *tracked, within time.Duration) (time.Duration, bool) {
	start := time.Now()
	t := time.NewTimer(within)
	defer t.Stop()
	select {
	case <-p.exited:
		return time.Since(start), true
	case <-t.C:
	case <-ctx.Done():
	}
	return time.Since(start), false
}

func (r *rig) waitNoClients(ctx context.Context) {
	for i := 0; i < 50 && r.srv.ClientCount() > 0; i++ {
		if !sleepCtx(ctx, r.d.Timing.Poll) {
			return
		}
	}
}

func (r *rig) kill(p *tracked) error {
	if r.d.Kill != nil {
		return r.d.Kill(p.Process)
	}
	return killGroup(p.Process)
}

func (r *rig) close() {
	for _, p := range r.procs {
		if !p.gone() {
			_ = p.Stop(r.d.Timing.StopGrace)
		}
	}
	r.srv.DropClients()
	r.ln.Close()
	for _, c := range r.owned {
		c()
	}
	r.owned = nil
	r.d.show("")
}

// --- 1. Client zeigt das Bild ---

const lookHint = "Bitte am Bildschirm prüfen: Das Testbild (dunkel, weißer Rand, orange Markierungen im 100-Pixel-Raster) " +
	"ist sichtbar, der weiße Rand ist rundum vollständig zu sehen, das Bild ist nicht verzerrt (Markierungen quadratisch) und liegt unter der Leiste."

func clientImage(ctx context.Context, d Deps) StepResult {
	r, err := newRig(d)
	if err != nil {
		return fail("%v", err)
	}
	defer r.close()
	p, err := r.start(r.password, false)
	if err != nil {
		return fail("%v", err)
	}
	took, err := r.waitConnected(ctx, p)
	if err != nil {
		return fail("%v", err)
	}
	if !r.waitFrame(ctx, max(d.Timing.Connect-took, d.Timing.Poll)) {
		return fail("verbunden nach %s, aber der Client forderte kein Bild %d×%d an", secs(took), testW, testH)
	}
	if !sleepCtx(ctx, d.Timing.ViewHold) { // das Bild bleibt stehen, damit jemand hinsieht
		return fail("abgebrochen")
	}
	if !p.gone() {
		_ = p.Stop(d.Timing.StopGrace)
	}
	r.waitNoClients(ctx)

	// Ein falsches Passwort darf keine Verbindung hinterlassen (Remmina zeigt dann nur einen Dialog).
	wp, err := r.start("falsch"+r.password, false)
	if err != nil {
		return fail("Client mit falschem Passwort: %v", err)
	}
	sleepCtx(ctx, d.Timing.WrongPassword)
	left := r.srv.ClientCount()
	state := "lief weiter (Dialog)"
	if wp.gone() {
		state = "beendete sich"
	}
	_ = wp.Stop(d.Timing.StopGrace)
	if left != 0 {
		return fail("falsches Passwort: %d Verbindung(en) blieben bestehen", left)
	}
	return StepResult{
		OK: true, NeedsHuman: true,
		Detail: fmt.Sprintf("Verbunden nach %s, Bild %d×%d angefordert und geliefert; falsches Passwort: keine Verbindung, Client %s. %s",
			secs(took), testW, testH, state, lookHint),
	}
}

// waitFrame wartet, bis der Client das ganze Testbild angefordert und bekommen hat.
func (r *rig) waitFrame(ctx context.Context, within time.Duration) bool {
	deadline := time.NewTimer(within)
	defer deadline.Stop()
	tick := time.NewTicker(r.d.Timing.Poll)
	defer tick.Stop()
	for r.srv.FullFrames() < 1 {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
		}
	}
	return true
}

// --- 2. Fensterplatz und Leiste ---

var geometryRe = regexp.MustCompile(`(\d+)x(\d+)\+(-?\d+)\+(-?\d+)\s+\+(-?\d+)\+(-?\d+)`)

type windowGeom struct{ w, h, x, y int }

// parseWindows liest aus der Ausgabe von "xwininfo -root -tree" die Fenster, deren
// Titel mit prefix beginnt: Größe und absolute Lage.
func parseWindows(out, prefix string) []windowGeom {
	var ws []windowGeom
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, `"`+prefix) {
			continue
		}
		m := geometryRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
		ws = append(ws, windowGeom{w: n(1), h: n(2), x: n(5), y: n(6)})
	}
	return ws
}

func windowAndBar(ctx context.Context, d Deps) StepResult {
	cfg := d.config()
	if d.Screen == nil {
		return fail("Bildschirmgröße nicht verfügbar")
	}
	r, err := newRig(d)
	if err != nil {
		return fail("%v", err)
	}
	defer r.close()
	p, err := r.start(r.password, true)
	if err != nil {
		return fail("%v", err)
	}
	if _, err := r.waitConnected(ctx, p); err != nil {
		return fail("%v", err)
	}
	sw, sh := d.Screen()
	want := windowGeom{w: sw, h: sh - barHeight, x: 0, y: barHeight}
	if cfg.Kiosk.HideBar {
		want = windowGeom{w: sw, h: sh}
	}
	check := fmt.Sprintf("Bitte prüfen: Die Leiste (Name, Modus, Zurück) liegt oben, das Bild darunter füllt die Fläche %d×%d ab y=%d, "+
		"das Clientfenster hat keinen Rahmen und keine Titelleiste.", want.w, want.h, want.y)

	if cfg.Display.GraphicsProfile != "x11" || d.Profile().Name() != "remmina" {
		return StepResult{OK: true, NeedsHuman: true, Detail: "Nicht automatisch gemessen (unter labwc fehlt ein Werkzeug zum Auslesen der Fenster, " +
			"und der Client läuft nicht als Fenster im X11-Profil). Der Client ist verbunden. " + check}
	}
	if d.Exec == nil {
		return StepResult{NeedsHuman: true, Detail: "Fensterlage nicht messbar: kein Programmaufruf angebunden. " + check}
	}
	var found []windowGeom
	deadline := time.Now().Add(d.Timing.Window)
	for {
		out, err := d.Exec(ctx, "xwininfo", "-root", "-tree")
		if err != nil {
			return StepResult{NeedsHuman: true, Detail: fmt.Sprintf("Fensterlage nicht messbar: xwininfo fehlt oder schlug fehl (%v). %s", err, check)}
		}
		found = parseWindows(out, client.RemminaTitlePrefix)
		if len(found) > 0 || time.Now().After(deadline) {
			break
		}
		if !sleepCtx(ctx, d.Timing.Poll) {
			return fail("abgebrochen")
		}
	}
	if len(found) == 0 {
		return StepResult{NeedsHuman: true, Detail: "Kein Fenster mit dem Titel „" + client.RemminaTitlePrefix + "“ gefunden, obwohl der Client verbunden ist. " + check}
	}
	for _, g := range found {
		if g == want {
			return StepResult{OK: true, NeedsHuman: true, Detail: fmt.Sprintf("Gemessen: Fenster %d×%d bei (%d,%d), wie erwartet; Rahmenlosigkeit folgt daraus, "+
				"die Leiste oben ist nicht gemessen. %s", g.w, g.h, g.x, g.y, check)}
		}
	}
	g := found[0]
	return StepResult{NeedsHuman: true, Detail: fmt.Sprintf("Fenster liegt bei (%d,%d) mit %d×%d, erwartet (%d,%d) mit %d×%d. Eine Rahmen- oder Platzierungsregel greift nicht. %s",
		g.x, g.y, g.w, g.h, want.x, want.y, want.w, want.h, check)}
}

// --- 3. Touch ---

var targetNames = [TouchTargets]string{"oben links", "oben rechts", "Mitte", "unten links", "unten rechts"}

func touch(ctx context.Context, d Deps) StepResult {
	if d.Touch == nil {
		return fail("Touch-Seite nicht angebunden")
	}
	web, webOK := touchTargets(ctx, d)
	vnc, vncOK := touchImage(ctx, d)
	return StepResult{OK: webOK && vncOK, Detail: web + " " + vnc}
}

func touchTargets(ctx context.Context, d Deps) (string, bool) {
	d.Touch.Reset()
	d.show("touch")
	defer d.show("")
	deadline := time.NewTimer(d.Timing.TouchWait)
	defer deadline.Stop()
	tick := time.NewTicker(d.Timing.Poll)
	defer tick.Stop()
	for d.Touch.Count() < TouchTargets {
		select {
		case <-ctx.Done():
		case <-deadline.C:
		case <-tick.C:
			continue
		}
		var names []string
		for _, i := range d.Touch.missing() {
			names = append(names, targetNames[i])
		}
		return fmt.Sprintf("Touch-Seite: %d von %d Zielen getroffen, es fehlen: %s.", d.Touch.Count(), TouchTargets, strings.Join(names, ", ")), false
	}
	return fmt.Sprintf("Touch-Seite: alle %d Ziele getroffen.", TouchTargets), true
}

// nearestMarker liefert die Markierung des Testbilds (Raster 100, ohne Rand) zu pt.
func nearestMarker(pt image.Point) image.Point {
	snap := func(v, hi int) int { return min(max((v+50)/100*100, 100), hi) }
	return image.Pt(snap(pt.X, ((testW-1)/100)*100), snap(pt.Y, ((testH-1)/100)*100))
}

func touchImage(ctx context.Context, d Deps) (string, bool) {
	r, err := newRig(d)
	if err != nil {
		return "Tipp auf dem VNC-Bild: " + err.Error() + ".", false
	}
	defer r.close()
	p, err := r.start(r.password, false)
	if err != nil {
		return "Tipp auf dem VNC-Bild: " + err.Error() + ".", false
	}
	if _, err := r.waitConnected(ctx, p); err != nil {
		return "Tipp auf dem VNC-Bild: " + err.Error() + ".", false
	}
	deadline := time.NewTimer(d.Timing.PointerWait)
	defer deadline.Stop()
	tick := time.NewTicker(d.Timing.Poll)
	defer tick.Stop()
	for len(r.srv.Pointers()) == 0 {
		select {
		case <-ctx.Done():
			return "Tipp auf dem VNC-Bild: abgebrochen.", false
		case <-deadline.C:
			return fmt.Sprintf("Tipp auf dem VNC-Bild: kein Tipp innerhalb von %s angekommen (eine orange Markierung antippen).", secs(d.Timing.PointerWait)), false
		case <-p.exited:
			return "Tipp auf dem VNC-Bild: der Client endete währenddessen.", false
		case <-tick.C:
		}
	}
	best, bestDist, bestMark := image.Point{}, math.MaxFloat64, image.Point{}
	for _, pt := range r.srv.Pointers() {
		m := nearestMarker(pt)
		if dist := math.Hypot(float64(pt.X-m.X), float64(pt.Y-m.Y)); dist < bestDist {
			best, bestDist, bestMark = pt, dist, m
		}
	}
	if bestDist > markerTolerance {
		return fmt.Sprintf("Tipp auf dem VNC-Bild: nächster Tipp (%d,%d) liegt %.0f Pixel neben der Markierung (%d,%d); erlaubt sind %.0f. Touch-Eichung oder Skalierung prüfen.",
			best.X, best.Y, bestDist, bestMark.X, bestMark.Y, markerTolerance), false
	}
	return fmt.Sprintf("Tipp auf dem VNC-Bild: (%d,%d), Markierung (%d,%d), Abweichung %.0f Pixel.", best.X, best.Y, bestMark.X, bestMark.Y, bestDist), true
}

// --- 4. Rückkehr ---

func returnToTiles(ctx context.Context, d Deps) StepResult {
	r, err := newRig(d)
	if err != nil {
		return fail("%v", err)
	}
	defer r.close()
	tm := d.Timing
	cases := []struct {
		name  string
		limit time.Duration
		do    func(p *tracked) error
	}{
		{"Zurück", tm.StopWithin, func(p *tracked) error {
			go func() { _ = p.Stop(tm.StopGrace) }() // wie die Sitzung: Stop läuft nebenher
			return nil
		}},
		{"Absturz", tm.StopWithin, func(p *tracked) error { return r.kill(p) }},
		{"Trennung", tm.DropWithin, func(p *tracked) error {
			r.srv.DropClients()
			return nil
		}},
	}
	var parts []string
	ok := true
	for _, c := range cases {
		msg, good := r.returnCase(ctx, c.name, c.limit, c.do)
		parts = append(parts, c.name+": "+msg)
		ok = ok && good
	}
	return StepResult{OK: ok, Detail: strings.Join(parts, "; ")}
}

// returnCase startet einen Client, löst das Ende aus und prüft, dass er innerhalb
// der Frist weg ist. Bei der Trennung gilt die Überwachung der Sitzung: Client
// weg oder nach LostAfter ohne ESTABLISHED gestoppt.
func (r *rig) returnCase(ctx context.Context, name string, limit time.Duration, trigger func(*tracked) error) (string, bool) {
	p, err := r.start(r.password, false)
	if err != nil {
		return err.Error(), false
	}
	if _, err := r.waitConnected(ctx, p); err != nil {
		return err.Error(), false
	}
	if err := trigger(p); err != nil {
		return "Auslösen nicht möglich: " + err.Error(), false
	}
	var took time.Duration
	var how string
	if name == "Trennung" {
		took, how = r.supervise(ctx, p, limit)
	} else {
		var gone bool
		if took, gone = r.waitExit(ctx, p, limit); !gone {
			return fmt.Sprintf("Prozess nach %s noch da (erlaubt %s)", secs(limit), secs(limit)), false
		}
		how = "beendet"
	}
	if !p.gone() {
		return fmt.Sprintf("Prozess nach %s noch da (erlaubt %s)", secs(took), secs(limit)), false
	}
	if !r.waitDisconnected(ctx) {
		return "Prozess weg, aber die Verbindung zum Server besteht noch", false
	}
	return fmt.Sprintf("Prozess %s nach %s", how, secs(took)), true
}

// supervise bildet pollConnected nach: Client selbst beendet, oder nach LostAfter
// ohne Verbindung gestoppt. limit begrenzt das Ganze.
func (r *rig) supervise(ctx context.Context, p *tracked, limit time.Duration) (time.Duration, string) {
	tm := r.d.Timing
	start := time.Now()
	var lostSince time.Time
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	tick := time.NewTicker(tm.Poll)
	defer tick.Stop()
	stopping := false
	for {
		select {
		case <-ctx.Done():
			return time.Since(start), "nicht beendet (abgebrochen)"
		case <-deadline.C:
			return time.Since(start), "nicht beendet"
		case <-p.exited:
			if stopping {
				return time.Since(start), "von der Überwachung gestoppt"
			}
			return time.Since(start), "von selbst beendet"
		case <-tick.C:
		}
		if stopping {
			continue
		}
		if r.connected() {
			lostSince = time.Time{}
			continue
		}
		if lostSince.IsZero() {
			lostSince = time.Now()
		}
		if time.Since(lostSince) >= tm.LostAfter {
			stopping = true
			go func() { _ = p.Stop(tm.StopGrace) }()
		}
	}
}

// --- 5. Prozessende ---

func processEnd(ctx context.Context, d Deps) StepResult {
	r, err := newRig(d)
	if err != nil {
		return fail("%v", err)
	}
	defer r.close()
	p, err := r.start(r.password, false)
	if err != nil {
		return fail("%v", err)
	}
	if _, err := r.waitConnected(ctx, p); err != nil {
		return fail("%v", err)
	}
	r.srv.DropClients()
	if took, gone := r.waitExit(ctx, p, d.Timing.ExitWithin); gone {
		return StepResult{OK: true, Detail: fmt.Sprintf("Der Client endete nach der Trennung durch den Server von selbst, nach %s.", secs(took))}
	}
	if ctx.Err() != nil {
		return fail("abgebrochen")
	}
	start := time.Now()
	_ = p.Stop(d.Timing.StopGrace)
	took := time.Since(start)
	if took >= d.Timing.StopGrace {
		return fail("Der Client endete nach der Trennung nicht von selbst (%s) und reagierte nicht auf SIGTERM: erst SIGKILL beendete ihn nach %s.",
			secs(d.Timing.ExitWithin), secs(took))
	}
	return StepResult{OK: true, Detail: fmt.Sprintf("Der Client endete nach der Trennung nicht von selbst innerhalb von %s; SIGTERM beendete ihn nach %s. "+
		"Die Sitzung stoppt ihn deshalb selbst.", secs(d.Timing.ExitWithin), secs(took))}
}

// --- 6. Andere Terminals ---

func terminals(ctx context.Context, d Deps) StepResult {
	peers := d.config().Peers
	if len(peers) == 0 {
		return StepResult{OK: true, Detail: "keine Terminals eingetragen"}
	}
	if d.Peer == nil {
		return fail("Prüfung der Terminals nicht angebunden")
	}
	var lines []string
	ok := true
	for _, p := range peers {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		st := d.Peer(pctx, p)
		cancel()
		switch {
		case !st.Reachable:
			ok = false
			lines = append(lines, fmt.Sprintf("%s: nicht erreichbar%s", p.Name, errSuffix(st.Err)))
		case st.KeyOK:
			lines = append(lines, p.Name+": erreichbar, Schlüssel stimmt")
		case st.Err != nil:
			ok = false
			lines = append(lines, fmt.Sprintf("%s: erreichbar, Schlüssel nicht prüfbar%s", p.Name, errSuffix(st.Err)))
		default:
			ok = false
			lines = append(lines, p.Name+": erreichbar, Schlüssel falsch")
		}
	}
	return StepResult{OK: ok, Detail: strings.Join(lines, "; ")}
}

func errSuffix(err error) string {
	if err == nil {
		return ""
	}
	return " (" + err.Error() + ")"
}

// --- 7. Schreibschutz und Watchdog ---

// overlayOnRoot wertet den letzten Eintrag für / aus: Er verdeckt die früheren.
func overlayOnRoot(mounts string) bool {
	on := false
	for _, line := range strings.Split(mounts, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[1] == "/" {
			on = f[2] == "overlay"
		}
	}
	return on
}

// bootReadOnly sucht die Boot-Partition (/boot/firmware, sonst /boot) und prüft
// die Einhängeoption ro als ganzes Wort.
func bootReadOnly(mounts string) (readOnly, found bool) {
	for _, target := range []string{"/boot/firmware", "/boot"} {
		for _, line := range strings.Split(mounts, "\n") {
			f := strings.Fields(line)
			if len(f) < 4 || f[1] != target {
				continue
			}
			found, readOnly = true, false
			for _, o := range strings.Split(f[3], ",") {
				if o == "ro" {
					readOnly = true
				}
			}
		}
		if found {
			return readOnly, true
		}
	}
	return false, false
}

// watchdogOn wertet "systemctl show -p RuntimeWatchdogUSec" aus: 0, leer und
// infinity heißen abgeschaltet.
func watchdogOn(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(line), "RuntimeWatchdogUSec=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		return v != "" && v != "0" && v != "0s" && v != "infinity"
	}
	return false
}

// radioOverlays liest dtoverlay=disable-wifi und disable-bt in config.txt; es
// zählen nur Zeilen vor dem ersten Abschnitt und in [all], wie beim Schreiben im Helfer.
func radioOverlays(cfg string) (wifiOff, btOff bool) {
	section := ""
	has := func(line, name string) bool {
		rest, ok := strings.CutPrefix(line, "dtoverlay="+name)
		return ok && (rest == "" || rest[0] == ',')
	}
	for _, l := range strings.Split(cfg, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section = t
			continue
		}
		if section != "" && section != "[all]" {
			continue
		}
		wifiOff = wifiOff || has(t, "disable-wifi")
		btOff = btOff || has(t, "disable-bt")
	}
	return wifiOff, btOff
}

func protection(ctx context.Context, d Deps) StepResult {
	cfg := d.config()
	ok, human := true, false
	var lines []string
	bad := func(format string, a ...any) {
		ok = false
		lines = append(lines, fmt.Sprintf(format, a...))
	}

	var mounts string
	if d.Mounts == nil {
		bad("Einhängepunkte nicht lesbar: nicht angebunden")
	} else if b, err := d.Mounts(); err != nil {
		bad("Einhängepunkte nicht lesbar: %v", err)
	} else {
		mounts = string(b)
		install := d.ImageMarker == nil || !d.ImageMarker()
		// Auf install.sh-Systemen gibt es keinen Schreibschutz (Spec Abschnitt 9): nur ein Hinweis.
		warn := func(format string, a ...any) {
			if install {
				human = true
				lines = append(lines, "Warnung: "+fmt.Sprintf(format, a...)+" – erwartet auf einem System aus install.sh (kein Schreibschutz, keine Datenpartition)")
				return
			}
			bad(format, a...)
		}
		if overlayOnRoot(mounts) {
			lines = append(lines, "Overlay auf /: aktiv")
		} else {
			warn("Kein Overlay auf /, das System ist nicht schreibgeschützt")
		}
		switch ro, found := bootReadOnly(mounts); {
		case !found:
			warn("Boot-Partition nicht eingehängt gefunden (/boot/firmware, /boot)")
		case ro:
			lines = append(lines, "Boot-Partition: schreibgeschützt")
		default:
			warn("Boot-Partition beschreibbar")
		}
	}

	if d.Exec == nil {
		bad("Watchdog nicht prüfbar: kein Programmaufruf angebunden")
	} else if out, err := d.Exec(ctx, "systemctl", "show", "-p", "RuntimeWatchdogUSec"); err != nil {
		bad("Watchdog nicht prüfbar: systemctl: %v", err)
	} else if watchdogOn(out) {
		lines = append(lines, "Watchdog (RuntimeWatchdogUSec): eingestellt")
	} else {
		bad("Watchdog (RuntimeWatchdogUSec): nicht eingestellt")
	}

	if d.BootConfig == nil {
		bad("WLAN/Bluetooth nicht prüfbar: config.txt nicht angebunden")
	} else if text, err := d.BootConfig(); err != nil {
		bad("WLAN/Bluetooth nicht prüfbar: config.txt: %v", err)
	} else {
		wifiOff, btOff := radioOverlays(text)
		for _, r := range []struct {
			name         string
			overlay, set bool
			ovName       string
		}{
			{"WLAN", wifiOff, cfg.Network.WifiOff, "disable-wifi"},
			{"Bluetooth", btOff, cfg.Network.BluetoothOff, "disable-bt"},
		} {
			state := map[bool]string{true: "aus", false: "an"}[r.overlay]
			switch {
			case r.overlay == r.set:
				lines = append(lines, fmt.Sprintf("%s: %s, wie eingestellt", r.name, state))
			case r.set:
				bad("%s: soll aus sein, aber dtoverlay=%s fehlt in config.txt", r.name, r.ovName)
			default:
				bad("%s: soll an sein, aber dtoverlay=%s steht in config.txt", r.name, r.ovName)
			}
			human = human || r.overlay
		}
		if wifiOff || btOff {
			lines = append(lines, "Bitte am Gerät bestätigen (Pi 5): Die Overlays disable-wifi/disable-bt wirken nach einem Neustart; "+
				"mit „rfkill list“ oder „ip link“ prüfen, dass die Funkmodule fehlen")
		}
	}
	return StepResult{OK: ok, NeedsHuman: human, Detail: strings.Join(lines, "; ")}
}

// release räumt einen beendeten Client auf: Ordner im RAM weg, nicht mehr gehalten.
// Ein langer Dauertest sammelt sonst Tausende Ordner und Einträge.
func (r *rig) release(p *tracked) {
	if c, ok := r.owned[p]; ok {
		c()
		delete(r.owned, p)
	}
	for i, q := range r.procs {
		if q == p {
			r.procs = append(r.procs[:i], r.procs[i+1:]...)
			break
		}
	}
}

const disconnectGrace = 2 * time.Second

// waitDisconnected wartet, bis der Server die Verbindung nicht mehr zählt: Er
// bucht das Ende nebenläufig, kurz nach dem Prozessende steht sie noch.
func (r *rig) waitDisconnected(ctx context.Context) bool {
	deadline := time.Now().Add(disconnectGrace)
	for time.Now().Before(deadline) {
		if !r.connected() {
			return true
		}
		if !sleepCtx(ctx, min(r.d.Timing.Poll, time.Until(deadline))) {
			break
		}
	}
	return !r.connected()
}

// ImageMarkerFile wird vom Image-Bau geschrieben (packaging/image/stage-hmi/01-hmi/00-run.sh).
// Sie erkennt das SD-Abbild positiv; auf Systemen aus install.sh fehlt sie, und dort gibt
// es keinen Schreibschutz (Spec Abschnitt 9).
const ImageMarkerFile = "/etc/hmi-kiosk/image-release"
