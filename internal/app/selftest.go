package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"hmikiosk/internal/browser"
	"hmikiosk/internal/client"
	"hmikiosk/internal/config"
	"hmikiosk/internal/coord"
	"hmikiosk/internal/platform"
	"hmikiosk/internal/selftest"
	"hmikiosk/internal/session"
	"hmikiosk/internal/web"
)

const (
	selftestRunLimit = 8 * time.Minute
	bootConfigFile   = "config.txt"
	execLimit        = 10 * time.Second
	soakDirName      = "soak"
	soakLogName      = "soak.jsonl"
)

var bootDirs = []string{"/boot/firmware", "/boot"}

// selftestPage hält, was die Selbstprüfung auf dem Bildschirm zeigen lässt.
type selftestPage struct{ v atomic.Pointer[string] }

func (p *selftestPage) set(page string) { p.v.Store(&page) }

func (p *selftestPage) get() string {
	if s := p.v.Load(); s != nil {
		return *s
	}
	return ""
}

// busyMessage nennt, warum die Prüfung jetzt nicht laufen darf; leer heißt: frei.
func (s *service) busyMessage() string {
	if s.soak.running() {
		return "Eine Dauerprüfung läuft. Bitte zuerst in der Admin-Seite unter System stoppen."
	}
	return s.sessionBusyMessage()
}

// sessionBusyMessage nennt Sitzung oder Daueranzeige als Grund.
func (s *service) sessionBusyMessage() string {
	// Eine Daueranzeige startet nach jedem Ende von selbst neu, auch zwischen zwei Schritten.
	if s.config().Kiosk.PermanentHMI != "" {
		return "Eine Daueranzeige ist eingestellt und läuft oder würde während der Prüfung wieder starten. " +
			"Bitte zuerst die Daueranzeige in der Admin-Seite unter Kiosk abschalten."
	}
	if snap := s.mgr.Snapshot(); snap.State != session.StateTiles {
		return fmt.Sprintf("Es läuft eine Sitzung (%s). Bitte zuerst mit „Zurück“ beenden und dann die Prüfung neu starten.", snap.HMIName)
	}
	return ""
}

func runCommand(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, execLimit)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

func readBootConfig() (string, error) {
	var firstErr error
	for _, dir := range bootDirs {
		b, err := os.ReadFile(filepath.Join(dir, bootConfigFile))
		if err == nil {
			return string(b), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return "", firstErr
}

// baseDeps sind die Teile, die Selbstprüfung und Dauerprüfung gemeinsam brauchen.
func baseDeps(procRootDir string, runner client.Runner) selftest.Deps {
	return selftest.Deps{
		Runner: runner,
		Established: func(ip net.IP, port int) (bool, error) {
			return platform.HasEstablished(procRootDir, ip, port)
		},
		Mounts:   func() ([]byte, error) { return os.ReadFile(filepath.Join(procRootDir, "mounts")) },
		Exec:     runCommand,
		GroupRSS: func(pid int) (int64, error) { return selftest.GroupRSS(procRootDir, pid) },
	}
}

func (s *service) selftestDeps() selftest.Deps {
	d := baseDeps(procRoot, s.runner)
	d.Profile = s.profile
	d.Config = s.config
	d.Screen = s.screen
	d.RAMDir = s.ramDir
	d.BootConfig = readBootConfig
	d.Busy = s.busyMessage
	d.ImageMarker = func() bool { _, err := os.Stat(selftest.ImageMarkerFile); return err == nil }
	d.Show = s.showSelftestPage
	d.Touch = s.touch
	d.ServicePID = os.Getpid
	d.Peer = func(ctx context.Context, p config.Peer) coord.PeerState {
		st := coord.CheckPeer(ctx, s.peerKey, nil, p)
		if st.Reachable && len(s.peerKey()) == 0 {
			st.KeyOK, st.Err = false, errors.New("auf diesem Terminal ist kein gemeinsamer Schlüssel gesetzt")
		}
		return st
	}
	return d
}

func (s *service) showSelftestPage(page string) {
	s.selftestPage.set(page)
	select {
	case s.browser <- struct{}{}:
	default:
	}
}

// browserTarget wählt, was Chromium zeigt: während der Selbstprüfung deren Seiten.
func (s *service) browserTarget(snap session.Snapshot) browser.Target {
	hideBar := s.config().Kiosk.HideBar
	switch s.selftestPage.get() {
	case "touch":
		return browser.TargetTouchTest
	case "bar":
		if hideBar {
			return browser.TargetNone
		}
		return browser.TargetBar
	}
	return browser.TargetFor(snap, hideBar)
}

// runSelftest führt die Prüfung aus; es läuft höchstens eine zugleich.
func (s *service) runSelftest(ctx context.Context) []selftest.StepResult {
	if !s.selftestMu.TryLock() {
		if s.soak.running() {
			return []selftest.StepResult{{Name: selftest.StepPrecondition, Detail: s.busyMessage()}}
		}
		return []selftest.StepResult{{Name: selftest.StepPrecondition, Detail: "Die Selbstprüfung läuft bereits."}}
	}
	defer s.selftestMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, selftestRunLimit)
	defer cancel()
	return selftest.Run(ctx, s.selftestDeps())
}

// adminSelftest ist die Selbstprüfung für die Admin-Seite.
func (s *service) adminSelftest(ctx context.Context) []web.SelftestStep {
	res := s.runSelftest(ctx)
	out := make([]web.SelftestStep, len(res))
	for i, r := range res {
		out[i] = web.SelftestStep{Name: r.Name, OK: r.OK, Detail: r.Detail, NeedsHuman: r.NeedsHuman}
	}
	return out
}

// SelftestStatus beantwortet GET /api/selftest/status für `hmi-kiosk soak`.
type SelftestStatus struct {
	Busy string `json:"busy"`
	PID  int    `json:"pid"`
}

// selftestAPI sind die lokalen Routen für den Befehl `hmi-kiosk selftest`, damit er
// auf derselben Anzeige prüft wie der Dienst.
func (s *service) selftestAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/selftest/run", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, s.runSelftest(r.Context()))
	})
	mux.HandleFunc("GET /api/selftest/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, SelftestStatus{Busy: s.busyMessage(), PID: os.Getpid()})
	})
	return web.SecureLocal(mux)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(v)
}

// localHandler legt die Selbstprüfungs-API neben die Kachelseite.
func (s *service) localHandler() http.Handler {
	mux := http.NewServeMux()
	api := s.selftestAPI()
	mux.Handle("POST /api/selftest/run", api)
	mux.Handle("GET /api/selftest/status", api)
	mux.Handle("/", web.NewLocalWith(s.mgr, s.tiles, func() string { return s.config().Kiosk.Layout },
		web.LocalExtras{TouchHit: s.touch.Hit}))
	return mux
}

// --- Aufrufe aus dem Befehl ---

// localBase ist die lokale Seite des laufenden Dienstes.
const localBase = "http://" + DefaultLocalAddr

func localRequest(ctx context.Context, base, method, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, base+path, nil)
	if err != nil {
		return nil, err
	}
	// Die lokale Seite verlangt bei POST einen erlaubten Origin.
	req.Header.Set("Origin", "http://"+DefaultLocalAddr)
	return req, nil
}

func getJSON(client *http.Client, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Dienst antwortet mit Status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// RunSelftestRemote lässt den laufenden Dienst die Selbstprüfung ausführen und
// liefert das Ergebnis. base ist die Adresse der lokalen Seite (leer: Standard).
func RunSelftestRemote(ctx context.Context, base string) ([]selftest.StepResult, error) {
	if base == "" {
		base = localBase
	}
	req, err := localRequest(ctx, base, http.MethodPost, "/api/selftest/run")
	if err != nil {
		return nil, err
	}
	var res []selftest.StepResult
	hc := &http.Client{Timeout: selftestRunLimit + time.Minute}
	if err := getJSON(hc, req, &res); err != nil {
		return nil, fmt.Errorf("Der Dienst ist nicht erreichbar oder die Prüfung scheiterte: %w", err)
	}
	return res, nil
}

// --- Dauerprüfung ---

// SoakOptions sind die Angaben des Befehls `hmi-kiosk soak`.
type SoakOptions struct {
	DataDir, RunDir string
	Duration        time.Duration
	Runner          client.Runner
	// Base ist die lokale Seite des Dienstes; leer ist der Standard.
	Base string
	// Out bekommt die JSON-Zeilen, Warn die Hinweise.
	Out, Warn func(string)
}

// RunSoak führt die Dauerprüfung aus. Sie fragt den laufenden Dienst, ob eine
// Sitzung läuft, und misst dessen Speicher mit. Ohne erreichbaren Dienst wird nur
// der Client gemessen.
func RunSoak(ctx context.Context, o SoakOptions) (selftest.SoakResult, error) {
	var res selftest.SoakResult
	cfg, _, err := config.Open(o.DataDir).Load()
	if err != nil {
		return res, fmt.Errorf("Konfiguration nicht lesbar: %w", err)
	}
	base := o.Base
	if base == "" {
		base = localBase
	}
	d := baseDeps(procRoot, o.Runner)
	d.Config = func() config.Config { return cfg }
	d.Profile = func() client.Profile {
		p, err := client.ByName(cfg.Client.Profile)
		if err != nil {
			p, _ = client.ByName("remmina")
		}
		return p
	}
	d.Screen = func() (int, int) {
		w, h, found := platform.DisplaySize(sysRoot)
		return platform.Resolve(cfg.Display.OverrideW, cfg.Display.OverrideH, w, h, found)
	}
	soakRoot := filepath.Join(o.RunDir, soakDirName)
	d.RAMDir = func() (string, func(), error) {
		if err := os.MkdirAll(soakRoot, 0o700); err != nil {
			return "", nil, err
		}
		dir, err := os.MkdirTemp(soakRoot, "s-")
		if err != nil {
			return "", nil, err
		}
		return dir, func() { _ = os.RemoveAll(dir) }, nil
	}

	var st SelftestStatus
	req, err := localRequest(ctx, base, http.MethodGet, "/api/selftest/status")
	if err == nil {
		err = getJSON(&http.Client{Timeout: 5 * time.Second}, req, &st)
	}
	if err != nil {
		o.Warn(fmt.Sprintf("Hinweis: Der Dienst ist nicht erreichbar (%v). Es wird nur der Speicher des Clients gemessen, und ob eine Sitzung läuft, ist unbekannt.", err))
	} else {
		d.Busy = func() string { return st.Busy }
		d.ServicePID = func() int { return st.PID }
	}

	logPath := filepath.Join(o.DataDir, logDirName, soakLogName)
	var logFile *os.File
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err == nil {
		logFile, err = os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			o.Warn(fmt.Sprintf("Hinweis: %s nicht beschreibbar: %v", logPath, err))
		}
	} else {
		o.Warn(fmt.Sprintf("Hinweis: %s nicht beschreibbar: %v", logPath, err))
	}
	if logFile != nil {
		defer logFile.Close()
	}

	err = selftest.Soak(ctx, d, o.Duration, func(s selftest.SoakSample) {
		b, _ := json.Marshal(s)
		o.Out(string(b))
		if logFile != nil {
			_, _ = logFile.Write(append(b, '\n'))
		}
		res.Add(s)
	})
	return res, err
}
