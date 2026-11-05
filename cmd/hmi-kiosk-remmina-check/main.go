//go:build linux

// hmi-kiosk-remmina-check gleicht das echte Remmina unter Xvfb mit den Annahmen aus internal/client ab.
// Ausgabe je Punkt: "OK <punkt>: <detail>" oder "ABWEICHUNG <punkt>: <detail>".
// Der Exit-Code ist nur dann ungleich 0, wenn die Prüfumgebung selbst versagt.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"hmikiosk/internal/client"
	"hmikiosk/internal/rfb"
)

// Muss zur Fensterregel in openbox-rc.xml und zur Xvfb-Größe in run.sh passen.
var target = image.Rect(0, 48, 1280, 800)

const (
	serverW, serverH = 1024, 600
	connectTimeout   = 90 * time.Second
	settle           = 5 * time.Second
	dropLimit        = 10 * time.Second
	exitObserve      = 40 * time.Second
)

var (
	verbose   = flag.Bool("v", false, "Ausgaben von Remmina auf stderr zeigen")
	shotDir   = flag.String("shots", "", "Verzeichnis für Bildschirmfotos der Geometrieprüfung")
	only      = flag.String("only", "", "nur Punkte, deren Name diesen regulären Ausdruck trifft")
	wrongAuth atomic.Int64
	tmpRoot   string
)

func ok(point, format string, a ...any) {
	fmt.Printf("OK %s: %s\n", point, fmt.Sprintf(format, a...))
}

func dev(point, format string, a ...any) {
	fmt.Printf("ABWEICHUNG %s: %s\n", point, fmt.Sprintf(format, a...))
}

func report(good bool, point, format string, a ...any) {
	if good {
		ok(point, format, a...)
	} else {
		dev(point, format, a...)
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "Prüfumgebung fehlerhaft: "+format+"\n", a...)
	os.Exit(2)
}

// authLog zählt Fehlanmeldungen, die der Test-Server über das Standard-Log meldet.
type authLog struct{}

func (authLog) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("falschem Passwort")) {
		wrongAuth.Add(1)
	}
	if *verbose {
		os.Stderr.Write(p)
	}
	return len(p), nil
}

func startServer(password string) (*rfb.TestServer, int, func()) {
	return startServerSized(serverW, serverH, password)
}

func startServerSized(w, h int, password string) (*rfb.TestServer, int, func()) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal("Lauschen: %v", err)
	}
	s := rfb.NewTestServer(w, h, password)
	go s.Serve(l)
	return s, l.Addr().(*net.TCPAddr).Port, func() { l.Close(); s.DropClients() }
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if *verbose {
		os.Stderr.Write(p)
	}
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

type session struct {
	dir    string
	secret string
	cmd    *exec.Cmd
	done   chan struct{}
	waitE  error
	output *lockedBuffer
}

type opts struct {
	n        int
	password string
	port     int
	viewOnly bool
	scale    bool
	patch    map[string]string // überschreibt Profilfelder nach Prepare
	env      []string
}

var dirCounter atomic.Int64

func prepare(o opts) (string, client.Command) {
	p, err := client.ByName("remmina")
	if err != nil {
		fatal("%v", err)
	}
	dir := filepath.Join(tmpRoot, "s"+strconv.FormatInt(dirCounter.Add(1), 10))
	cmd, err := p.Prepare(dir, client.Spec{
		Host: "127.0.0.1", Port: o.port, Password: o.password, ViewOnly: o.viewOnly,
		Quality: 2, ColorDepth: 32, Scale: o.scale, SessionN: o.n,
	})
	if err != nil {
		fatal("Prepare: %v", err)
	}
	if len(o.patch) > 0 {
		pf := filepath.Join(dir, "session.remmina")
		b, err := os.ReadFile(pf)
		if err != nil {
			fatal("%v", err)
		}
		for k, v := range o.patch {
			re := regexp.MustCompile(`(?m)^` + k + `=.*$`)
			if re.Match(b) {
				b = re.ReplaceAll(b, []byte(k+"="+v))
			} else {
				b = append(b, []byte(k+"="+v+"\n")...)
			}
		}
		if err := os.WriteFile(pf, b, 0o600); err != nil {
			fatal("%v", err)
		}
	}
	cmd.Env = append(cmd.Env, o.env...)
	return dir, cmd
}

var secretLine = regexp.MustCompile(`(?m)^secret=.+$`)

func start(o opts) *session {
	dir, c := prepare(o)
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Env = append(os.Environ(), c.Env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGTERM}
	pref, err := os.ReadFile(filepath.Join(dir, "config", "remmina", "remmina.pref"))
	if err != nil {
		fatal("%v", err)
	}
	s := &session{dir: dir, secret: secretLine.FindString(string(pref)), cmd: cmd, done: make(chan struct{}), output: &lockedBuffer{}}
	cmd.Stdout, cmd.Stderr = s.output, s.output
	if err := cmd.Start(); err != nil {
		fatal("remmina startet nicht: %v", err)
	}
	go func() {
		s.waitE = cmd.Wait()
		close(s.done)
	}()
	return s
}

func (s *session) exited() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *session) waitExit(d time.Duration) (time.Duration, bool) {
	t0 := time.Now()
	select {
	case <-s.done:
		return time.Since(t0), true
	case <-time.After(d):
		return d, false
	}
}

func (s *session) kill() {
	if s.exited() {
		return
	}
	syscall.Kill(-s.cmd.Process.Pid, syscall.SIGKILL)
	<-s.done
}

// tail liefert die letzten Zeilen ohne den immer gleichen Hinweis von Remmina.
func (s *session) tail() string {
	var keep []string
	for _, l := range strings.Split(s.output.String(), "\n") {
		if l == "" || strings.Contains(l, "does not log all output") || strings.Contains(l, "More info available") || strings.Contains(l, "Usage/Remmina-debugging") {
			continue
		}
		keep = append(keep, l)
	}
	if len(keep) > 3 {
		keep = keep[len(keep)-3:]
	}
	return strings.Join(keep, " | ")
}

func waitFor(d time.Duration, f func() bool) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return f()
}

func waitClients(s *rfb.TestServer, n int) bool {
	return waitFor(connectTimeout, func() bool { return s.ClientCount() >= n })
}

type window struct {
	id    string
	title string
	rect  image.Rectangle
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

var xwinRe = regexp.MustCompile(`(?m)^\s*(Absolute upper-left X|Absolute upper-left Y|Width|Height|Map State):\s*(\S+)`)

func windowInfo(id string) (window, bool) {
	out, err := run("xwininfo", "-id", id)
	if err != nil {
		return window{}, false
	}
	v := map[string]string{}
	for _, m := range xwinRe.FindAllStringSubmatch(out, -1) {
		v[m[1]] = m[2]
	}
	if v["Map State"] != "IsViewable" {
		return window{}, false
	}
	atoi := func(k string) int { n, _ := strconv.Atoi(v[k]); return n }
	x, y := atoi("Absolute upper-left X"), atoi("Absolute upper-left Y")
	title, _ := run("xdotool", "getwindowname", id)
	return window{id: id, title: strings.TrimSpace(title),
		rect: image.Rect(x, y, x+atoi("Width"), y+atoi("Height"))}, true
}

// windowsOf liefert die sichtbaren Fenster eines Prozesses, ohne GTK-Hilfsfenster.
func windowsOf(pid int) []window {
	out, _ := run("xdotool", "search", "--onlyvisible", "--pid", strconv.Itoa(pid))
	var ws []window
	for _, id := range strings.Fields(out) {
		if w, good := windowInfo(id); good && w.rect.Dx() > 1 && w.rect.Dy() > 1 {
			ws = append(ws, w)
		}
	}
	return ws
}

func describe(ws []window) string {
	if len(ws) == 0 {
		return "keine Fenster"
	}
	var p []string
	for _, w := range ws {
		p = append(p, fmt.Sprintf("%q %dx%d+%d+%d", w.title, w.rect.Dx(), w.rect.Dy(), w.rect.Min.X, w.rect.Min.Y))
	}
	return strings.Join(p, ", ")
}

func captureRoot(save string) image.Image {
	path := filepath.Join(tmpRoot, "root.png")
	if out, err := exec.Command("import", "-window", "root", path).CombinedOutput(); err != nil {
		fatal("Bildschirmfoto: %v: %s", err, out)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		fatal("%v", err)
	}
	if save != "" {
		if err := os.WriteFile(save, b, 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "Bildschirmfoto nicht gespeichert: %v\n", err)
		}
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		fatal("Bildschirmfoto lesen: %v", err)
	}
	return img
}

type measurement struct {
	blobs  int
	sx, sy float64
	ox, oy float64 // Bildschirmposition von Serverpixel 0,0
}

func isMarker(img image.Image, x, y int) bool {
	r, g, b, _ := img.At(x, y).RGBA()
	return r>>8 > 200 && g>>8 > 140 && g>>8 < 230 && b>>8 < 80
}

// measure sucht die Marker des Testbilds (alle 100 Serverpixel) und leitet aus dem Abstand der
// äußersten Marker (spanX, spanY in Serverpixeln) den Maßstab ab.
func measure(img image.Image, spanX, spanY float64) measurement {
	area := img.Bounds()
	seen := make(map[image.Point]bool)
	type blob struct{ cx, cy float64 }
	var blobs []blob
	for y := area.Min.Y; y < area.Max.Y; y++ {
		for x := area.Min.X; x < area.Max.X; x++ {
			pt := image.Pt(x, y)
			if seen[pt] || !isMarker(img, x, y) {
				continue
			}
			stack := []image.Point{pt}
			seen[pt] = true
			var sx, sy, n float64
			for len(stack) > 0 {
				c := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				sx, sy, n = sx+float64(c.X), sy+float64(c.Y), n+1
				for _, d := range []image.Point{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					q := c.Add(d)
					if q.In(area) && !seen[q] && isMarker(img, q.X, q.Y) {
						seen[q] = true
						stack = append(stack, q)
					}
				}
			}
			blobs = append(blobs, blob{sx / n, sy / n})
		}
	}
	m := measurement{blobs: len(blobs)}
	if len(blobs) < 2 {
		return m
	}
	minX, maxX, minY, maxY := blobs[0].cx, blobs[0].cx, blobs[0].cy, blobs[0].cy
	for _, b := range blobs {
		minX, maxX = min(minX, b.cx), max(maxX, b.cx)
		minY, maxY = min(minY, b.cy), max(maxY, b.cy)
	}
	m.sx = (maxX - minX) / spanX
	m.sy = (maxY - minY) / spanY
	m.ox = minX - 100*m.sx
	m.oy = minY - 100*m.sy
	return m
}

func xprops(w window) string {
	props, _ := run("xprop", "-id", w.id, "WM_CLASS", "_NET_WM_STATE")
	return strings.Join(strings.Fields(props), " ")
}

// connected startet eine Sitzung und wartet auf den Client und ein stabiles Fenster.
func connected(point string, o opts, srv *rfb.TestServer) (*session, bool) {
	s := start(o)
	if !waitClients(srv, 1) {
		dev(point, "kein Client nach %s; Prozess beendet=%v; %s; Ausgabe: %s", connectTimeout, s.exited(), describe(windowsOf(s.cmd.Process.Pid)), s.tail())
		s.kill()
		return nil, false
	}
	waitFor(connectTimeout, func() bool { return len(windowsOf(s.cmd.Process.Pid)) > 0 })
	time.Sleep(settle)
	return s, true
}

func checkVersion() {
	ver, _ := exec.Command("remmina", "--version").CombinedOutput()
	m := regexp.MustCompile(`org\.remmina\.Remmina - \S+`).Find(ver)
	full, _ := run("dpkg-query", "-W", "-f", "${Package}=${Version} ${Architecture} ${db:Status-Abbrev}; ", "remmina", "remmina-plugin-vnc", "remmina-plugin-secret", "gnome-keyring")
	arch, _ := run("uname", "-m")
	report(m != nil, "version", "%s; Pakete: %s; Architektur %s", m, full, strings.TrimSpace(arch))
}

func checkFlags() {
	_, c := prepare(opts{n: 90, port: 1, password: "x"})
	help, err := exec.Command("remmina", "--help-all").CombinedOutput()
	if err != nil {
		dev("flags", "--help-all: %v", err)
		return
	}
	var missing []string
	for _, a := range c.Args {
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, _, _ := strings.Cut(a, "=")
		re := regexp.MustCompile(`(?m)^\s+` + regexp.QuoteMeta(name) + `\b`)
		if a == "-c" {
			re = regexp.MustCompile(`(?m)^\s+-c, --connect`)
		}
		if !re.Match(help) {
			missing = append(missing, a)
		}
	}
	report(len(missing) == 0, "flags", "Args %q; fehlend in --help-all: %v; die echte Annahme beim Start zeigen die passwort-Punkte", c.Args, missing)
}

func checkPassword(point, pw string) {
	srv, port, stop := startServer(pw)
	defer stop()
	before := wrongAuth.Load()
	s, good := connected(point, opts{n: 1, port: port, password: pw, scale: true}, srv)
	if !good {
		return
	}
	defer s.kill()
	time.Sleep(3 * time.Second)
	m := measure(captureRoot(""), 900, 400)
	fails := wrongAuth.Load() - before
	report(srv.ClientCount() == 1 && fails == 0 && m.blobs > 0 && !s.exited(), point,
		"ClientCount=%d, Fehlanmeldungen=%d, Testbild-Marker sichtbar=%d, Fenster: %s; Ausgabe: %s", srv.ClientCount(), fails, m.blobs, describe(windowsOf(s.cmd.Process.Pid)), s.tail())
	if len(pw) > 8 {
		checkLeftovers(s, pw)
	}
}

// checkLeftovers: Nach dem Lauf darf nirgends Klartext liegen, und Remmina muss das erzeugte secret behalten.
func checkLeftovers(s *session, pw string) {
	s.kill()
	var hits []string
	filepath.Walk(s.dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil || fi.IsDir() {
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && bytes.Contains(b, []byte(pw)) {
			hits = append(hits, p)
		}
		return nil
	})
	report(len(hits) == 0, "klartext", "Klartext-Passwort in %v", hits)
	pref, _ := os.ReadFile(filepath.Join(s.dir, "config", "remmina", "remmina.pref"))
	secrets := secretLine.FindAllString(string(pref), -1)
	same := len(secrets) == 1 && secrets[0] == s.secret
	report(same, "pref-secret", "secret-Zeilen in remmina.pref nach dem Lauf: %d, unverändert=%v; Remmina ergänzt die Datei auf %d Byte", len(secrets), same, len(pref))
}

func checkDrop() {
	srv, port, stop := startServer("geheim12")
	defer stop()
	s, good := connected("drop-clients", opts{n: 1, port: port, password: "geheim12", scale: true}, srv)
	if !good {
		return
	}
	defer s.kill()
	srv.DropClients()
	d, ended := s.waitExit(exitObserve)
	switch {
	case ended && d <= dropLimit:
		ok("drop-clients", "Prozess endet nach %.1f s; %v", d.Seconds(), s.waitE)
	case ended:
		dev("drop-clients", "Prozess endet erst nach %.1f s (Grenze %s); %v", d.Seconds(), dropLimit, s.waitE)
	default:
		dev("drop-clients", "Prozess läuft nach %s weiter, Fenster: %s, ClientCount=%d, Ausgabe: %s", exitObserve, describe(windowsOf(s.cmd.Process.Pid)), srv.ClientCount(), s.tail())
	}
}

func checkSigterm() {
	srv, port, stop := startServer("geheim12")
	defer stop()
	_, c := prepare(opts{n: 1, port: port, password: "geheim12", scale: true})
	p, err := client.NewExecRunner().Start(c)
	if err != nil {
		fatal("Runner: %v", err)
	}
	if !waitClients(srv, 1) {
		dev("sigterm", "kein Client nach %s", connectTimeout)
		p.Stop(time.Second)
		return
	}
	time.Sleep(settle)
	const grace = 20 * time.Second
	t0 := time.Now()
	p.Stop(grace)
	d := time.Since(t0)
	werr := <-p.Done()
	gone := waitFor(5*time.Second, func() bool { return srv.ClientCount() == 0 })
	report(d < grace && gone, "sigterm", "Runner.Stop nach %.1f s, Ende: %v, Verbindung geschlossen=%v", d.Seconds(), werr, gone)
}

func checkTwoSessions() {
	srvA, portA, stopA := startServer("geheim12")
	defer stopA()
	srvB, portB, stopB := startServer("geheim12")
	defer stopB()
	a, good := connected("zwei-sitzungen", opts{n: 1, port: portA, password: "geheim12", scale: true}, srvA)
	if !good {
		return
	}
	defer a.kill()
	b, good := connected("zwei-sitzungen", opts{n: 2, port: portB, password: "geheim12", scale: true}, srvB)
	if !good {
		return
	}
	defer b.kill()
	both := srvA.ClientCount() == 1 && srvB.ClientCount() == 1 && !a.exited() && !b.exited()
	wa, wb := windowsOf(a.cmd.Process.Pid), windowsOf(b.cmd.Process.Pid)
	b.kill()
	gone := waitFor(5*time.Second, func() bool { return srvB.ClientCount() == 0 })
	alive := srvA.ClientCount() == 1 && !a.exited()
	report(both && gone && alive, "zwei-sitzungen", "App-IDs session1/session2: beide verbunden=%v, A: %s, B: %s; B beendet: B getrennt=%v, A läuft weiter=%v",
		both, describe(wa), describe(wb), gone, alive)
}

func checkSameAppID() {
	srvA, portA, stopA := startServer("geheim12")
	defer stopA()
	srvB, portB, stopB := startServer("geheim12")
	defer stopB()
	a, good := connected("gleiche-app-id", opts{n: 1, port: portA, password: "geheim12", scale: true}, srvA)
	if !good {
		return
	}
	defer a.kill()
	b := start(opts{n: 1, port: portB, password: "geheim12", scale: true})
	defer b.kill()
	d, ended := b.waitExit(30 * time.Second)
	forwarded := waitFor(15*time.Second, func() bool { return srvB.ClientCount() > 0 })
	ok("gleiche-app-id", "zweiter Aufruf mit gleicher ID: beendet=%v nach %.1f s, Verbindung über die erste Instanz=%v; bestätigt die Pflicht zu eigenen IDs", ended, d.Seconds(), forwarded)
}

func checkNoBus() {
	noBus := []string{"DBUS_SESSION_BUS_ADDRESS=unix:path=/nonexistent/bus"}
	srvA, portA, stopA := startServer("geheim12")
	defer stopA()
	srvB, portB, stopB := startServer("geheim12")
	defer stopB()
	a, good := connected("ohne-sitzungsbus", opts{n: 1, port: portA, password: "geheim12", scale: true, env: noBus}, srvA)
	if !good {
		return
	}
	defer a.kill()
	b, good := connected("ohne-sitzungsbus", opts{n: 2, port: portB, password: "geheim12", scale: true, env: noBus}, srvB)
	if !good {
		return
	}
	defer b.kill()
	both := srvA.ClientCount() == 1 && srvB.ClientCount() == 1
	b.kill()
	srvA.DropClients()
	d, ended := a.waitExit(exitObserve)
	report(both && ended, "ohne-sitzungsbus", "zwei Sitzungen verbunden=%v; nach DropClients Ende=%v nach %.1f s; Ausgabe: %s", both, ended, d.Seconds(), a.tail())
}

func checkWrongPassword() {
	srv, port, stop := startServer("richtig1")
	defer stop()
	before := wrongAuth.Load()
	s := start(opts{n: 1, port: port, password: "falsch99", scale: true})
	defer s.kill()
	d, ended := s.waitExit(exitObserve)
	attempts := wrongAuth.Load() - before
	if ended {
		ok("falsches-passwort", "closeonfailure=1: Prozess endet nach %.1f s; %v; Fehlanmeldungen am Server=%d; Ausgabe: %s", d.Seconds(), s.waitE, attempts, s.tail())
		return
	}
	save := ""
	if *shotDir != "" {
		save = filepath.Join(*shotDir, "falsches-passwort.png")
		captureRoot(save)
	}
	ws := describe(windowsOf(s.cmd.Process.Pid))
	clients := srv.ClientCount()
	syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
	stopped, gone := s.waitExit(10 * time.Second)
	dev("falsches-passwort", "closeonfailure=1 greift nicht: Prozess läuft nach %s weiter; Fenster: %s; Fehlanmeldungen am Server=%d; ClientCount=%d; SIGTERM beendet=%v nach %.1f s",
		exitObserve, ws, attempts, clients, gone, stopped.Seconds())
}

func checkRefused() {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fatal("%v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	s := start(opts{n: 1, port: port, password: "geheim12", scale: true})
	defer s.kill()
	d, ended := s.waitExit(exitObserve)
	report(ended, "nicht-erreichbar", "closeonfailure=1, Port geschlossen: Ende=%v nach %.1f s; Fenster: %s; Ausgabe: %s", ended, d.Seconds(), describe(windowsOf(s.cmd.Process.Pid)), s.tail())
}

func checkGeometry(scale, viewmode int) {
	point := fmt.Sprintf("fenster-scale%d-viewmode%d", scale, viewmode)
	srv, port, stop := startServer("geheim12")
	defer stop()
	s, good := connected(point, opts{n: 1, port: port, password: "geheim12", scale: scale != 0,
		patch: map[string]string{"viewmode": strconv.Itoa(viewmode), "scale": strconv.Itoa(scale)}}, srv)
	if !good {
		return
	}
	defer s.kill()
	ws := windowsOf(s.cmd.Process.Pid)
	if len(ws) == 0 {
		dev(point, "kein sichtbares Fenster; ClientCount=%d", srv.ClientCount())
		return
	}
	save := ""
	if *shotDir != "" {
		save = filepath.Join(*shotDir, point+".png")
	}
	w := ws[0]
	m := measure(captureRoot(save), 900, 400)
	expSX, expSY := 1.0, 1.0
	if scale != 0 {
		// Remmina behält das Seitenverhältnis (GtkAspectFrame), solange scaler_expand nicht gesetzt ist.
		expSX = min(float64(target.Dx())/serverW, float64(target.Dy())/serverH)
		expSY = expSX
	}
	fits := w.rect == target
	scaled := m.blobs == 50 && abs(m.sx-expSX) < 0.005 && abs(m.sy-expSY) < 0.005
	report(fits && (scale == 0 && m.blobs > 0 || scaled), point,
		"Fenster %dx%d+%d+%d (Regel %dx%d+%d+%d), Titel %q, %d Fenster; Marker=%d, Maßstab %.3f x %.3f (erwartet %.3f x %.3f), Bild-Ursprung %.0f,%.0f; %s",
		w.rect.Dx(), w.rect.Dy(), w.rect.Min.X, w.rect.Min.Y, target.Dx(), target.Dy(), target.Min.X, target.Min.Y,
		w.title, len(ws), m.blobs, m.sx, m.sy, expSX, expSY, m.ox, m.oy, xprops(w))
}

// checkAspect zeigt das Verhalten bei einem HMI, dessen Seitenverhältnis nicht zur Fläche passt.
func checkAspect(point string, patch map[string]string) {
	srv, port, stop := startServerSized(800, 600, "geheim12")
	defer stop()
	s, good := connected(point, opts{n: 1, port: port, password: "geheim12", scale: true, patch: patch}, srv)
	if !good {
		return
	}
	defer s.kill()
	save := ""
	if *shotDir != "" {
		save = filepath.Join(*shotDir, point+".png")
	}
	m := measure(captureRoot(save), 600, 400)
	const wantX, wantY = 400, 300
	x, y := int(m.ox+wantX*m.sx), int(m.oy+wantY*m.sy)
	if out, err := exec.Command("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "1").CombinedOutput(); err != nil {
		fatal("xdotool: %v: %s", err, out)
	}
	time.Sleep(3 * time.Second)
	got := "kein Ereignis"
	inputOK := false
	if pts := srv.Pointers(); len(pts) > 0 {
		p := pts[len(pts)-1]
		got = fmt.Sprintf("%d,%d", p.X, p.Y)
		inputOK = abs(float64(p.X-wantX)) <= 3 && abs(float64(p.Y-wantY)) <= 3
	}
	report(m.blobs == 35 && inputOK, point, "Server 800x600 in Fläche %dx%d: Marker=%d, Maßstab %.3f x %.3f, Bild-Ursprung %.0f,%.0f; Klick auf Bildschirm %d,%d kommt am Server bei %s an (erwartet %d,%d)",
		target.Dx(), target.Dy(), m.blobs, m.sx, m.sy, m.ox, m.oy, x, y, got, wantX, wantY)
}

func checkPointer(viewOnly bool) {
	point := "zeiger-scale1"
	if viewOnly {
		point = "viewonly"
	}
	srv, port, stop := startServer("geheim12")
	defer stop()
	s, good := connected(point, opts{n: 1, port: port, password: "geheim12", scale: true, viewOnly: viewOnly}, srv)
	if !good {
		return
	}
	defer s.kill()
	m := measure(captureRoot(""), 900, 400)
	if m.blobs == 0 {
		dev(point, "Testbild nicht sichtbar")
		return
	}
	const wantX, wantY = 512, 300
	x := int(m.ox + wantX*m.sx)
	y := int(m.oy + wantY*m.sy)
	if out, err := exec.Command("xdotool", "mousemove", strconv.Itoa(x), strconv.Itoa(y), "click", "1").CombinedOutput(); err != nil {
		fatal("xdotool: %v: %s", err, out)
	}
	time.Sleep(3 * time.Second)
	pts := srv.Pointers()
	if viewOnly {
		report(len(pts) == 0, point, "Klick bei viewonly=1: %d Zeigerereignisse am Server", len(pts))
		return
	}
	if len(pts) == 0 {
		dev(point, "Klick auf Bildschirm %d,%d erreicht den Server nicht", x, y)
		return
	}
	got := pts[len(pts)-1]
	report(abs(float64(got.X-wantX)) <= 3 && abs(float64(got.Y-wantY)) <= 3, point,
		"Klick auf Bildschirm %d,%d kommt am Server bei %d,%d an (erwartet %d,%d)", x, y, got.X, got.Y, wantX, wantY)
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func main() {
	flag.Parse()
	log.SetOutput(authLog{})
	if os.Getenv("DISPLAY") == "" {
		fatal("DISPLAY ist nicht gesetzt")
	}
	for _, tool := range []string{"remmina", "xdotool", "xwininfo", "xprop", "import"} {
		if _, err := exec.LookPath(tool); err != nil {
			fatal("%s fehlt", tool)
		}
	}
	var err error
	if tmpRoot, err = os.MkdirTemp("", "remmina-check"); err != nil {
		fatal("%v", err)
	}
	defer os.RemoveAll(tmpRoot)

	filter, err := regexp.Compile(*only)
	if err != nil {
		fatal("-only: %v", err)
	}
	step := func(name string, f func()) {
		if filter.MatchString(name) {
			f()
		}
	}
	step("version", checkVersion)
	step("flags", checkFlags)
	for _, c := range []struct{ point, pw string }{
		{"passwort-5-zeichen", "aZ3$k"},
		{"passwort-8-zeichen", "aZ3$kL9!"},
		{"passwort-12-zeichen", "aZ3$kL9!x7Qm"},
		{"passwort-umlaut", "Pässwö"},
	} {
		step(c.point, func() { checkPassword(c.point, c.pw) })
	}
	step("drop-clients", checkDrop)
	step("sigterm", checkSigterm)
	step("zwei-sitzungen", checkTwoSessions)
	step("gleiche-app-id", checkSameAppID)
	step("ohne-sitzungsbus", checkNoBus)
	step("falsches-passwort", checkWrongPassword)
	step("nicht-erreichbar", checkRefused)
	for _, scale := range []int{1, 0} {
		for viewmode := 0; viewmode <= 4; viewmode++ {
			step(fmt.Sprintf("fenster-scale%d-viewmode%d", scale, viewmode), func() { checkGeometry(scale, viewmode) })
		}
	}
	step("seitenverhaeltnis", func() { checkAspect("seitenverhaeltnis", nil) })
	step("scaler-expand", func() { checkAspect("scaler-expand", map[string]string{"scaler_expand": "1"}) })
	step("zeiger-scale1", func() { checkPointer(false) })
	step("viewonly", func() { checkPointer(true) })
}
