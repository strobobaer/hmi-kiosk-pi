package helper

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/helperapi"
)

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

type fakeTimers struct {
	mu sync.Mutex
	ts []*fakeTimer
}

func (ft *fakeTimers) after(d time.Duration, f func()) func() bool {
	ft.mu.Lock()
	defer ft.mu.Unlock()
	t := &fakeTimer{d: d, f: f}
	ft.ts = append(ft.ts, t)
	return func() bool {
		ft.mu.Lock()
		defer ft.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

// fire löst den Zeitgeber mit Dauer d aus, sofern er nicht gestoppt ist.
func (ft *fakeTimers) fire(t *testing.T, d time.Duration) bool {
	t.Helper()
	ft.mu.Lock()
	var hit *fakeTimer
	for _, x := range ft.ts {
		if x.d == d && x.f != nil {
			hit = x
		}
	}
	if hit == nil {
		ft.mu.Unlock()
		t.Fatalf("kein Zeitgeber mit %v", d)
	}
	f, stopped := hit.f, hit.stopped
	hit.f = nil
	ft.mu.Unlock()
	if stopped {
		return false
	}
	f()
	return true
}

type netEnv struct {
	*env
	timers  *fakeTimers
	now     time.Time
	addrs   []netip.Prefix
	tools   map[string]bool
	prevOut string
	outs    []string
}

const (
	testActivate = 2 * time.Second
	testDeadline = 120 * time.Second
)

func newNetEnv(t *testing.T) *netEnv {
	t.Helper()
	ne := &netEnv{env: newEnv(t, ""), timers: &fakeTimers{}, now: time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC),
		tools: map[string]bool{"nmcli": true, "arping": true}, prevOut: "11111111-2222-4333-8444-555555555555\n",
		addrs: []netip.Prefix{netip.MustParsePrefix("192.168.1.20/24")}}
	sys := filepath.Join(ne.dir, "sys")
	mkNetIf(t, sys, "eth0", "1", false, true)
	ne.srv.Net = NetConfig{
		SysClassNet:   sys,
		ConnDir:       filepath.Join(ne.dir, "nm"),
		StateDir:      filepath.Join(ne.dir, "run-net"),
		SavedPath:     filepath.Join(ne.dir, "data", "system", "ethernet.json"),
		Deadline:      testDeadline,
		ActivateDelay: testActivate,
		HasTool:       func(n string) bool { return ne.tools[n] },
		IfaceAddrs:    func(string) ([]netip.Prefix, error) { return ne.addrs, nil },
		AfterFunc:     ne.timers.after,
		Now:           func() time.Time { return ne.now },
	}
	ne.srv.ExecOut = func(_ context.Context, name string, args ...string) (string, error) {
		ne.outs = append(ne.outs, strings.Join(append([]string{name}, args...), " "))
		return ne.prevOut, nil
	}
	return ne
}

func (ne *netEnv) keyfile() string { return filepath.Join(ne.srv.Net.ConnDir, NMConnFile) }

func (ne *netEnv) apply(t *testing.T, e config.EthernetCfg) error {
	t.Helper()
	b, _ := json.Marshal(e)
	return ne.srv.Dispatch(context.Background(), CmdNetApply, string(b))
}

func (ne *netEnv) status(t *testing.T) helperapi.NetStatus {
	t.Helper()
	d, err := ne.srv.DispatchData(context.Background(), CmdNetStatus, "")
	if err != nil {
		t.Fatal(err)
	}
	return d.(helperapi.NetStatus)
}

var staticCfg = config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.50", Prefix: 24, Gateway: "192.168.1.1", DNS: []string{"192.168.1.1"}}

func TestNetApplyStaticChecksConflictWritesKeyfileAndActivatesLater(t *testing.T) {
	ne := newNetEnv(t)
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	want := []string{"arping -D -q -I eth0 -c 2 -w 3 192.168.1.50", "nmcli connection reload"}
	if got := ne.fx.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Aufrufe %v, erwartet %v", got, want)
	}
	if !reflect.DeepEqual(ne.outs, []string{"nmcli -g GENERAL.CON-UUID device show eth0"}) {
		t.Fatalf("Abfragen %v", ne.outs)
	}
	wantKF, _ := NMKeyfile("eth0", staticCfg)
	if got := ne.read(t, ne.keyfile()); got != string(wantKF) {
		t.Fatalf("Keyfile:\n%s", got)
	}
	fi, err := os.Stat(ne.keyfile())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("Modus %v %v", fi.Mode(), err)
	}
	// Aktiviert wird erst nach kurzer Pause, damit die Antwort noch den Browser erreicht.
	if !ne.timers.fire(t, testActivate) {
		t.Fatal("Aktivierung gestoppt")
	}
	want = append(want, "nmcli -w 20 connection up "+NMConnID)
	if got := ne.fx.got(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Aufrufe %v", got)
	}
	st := ne.status(t)
	if !st.Pending || st.RemainingSec != 120 || st.PendingCfg.Address != "192.168.1.50" || st.Iface != "eth0" {
		t.Fatalf("Status %+v", st)
	}
	if !reflect.DeepEqual(st.Addresses, []string{"192.168.1.20/24"}) {
		t.Fatalf("Adressen %v", st.Addresses)
	}
	ne.now = ne.now.Add(100 * time.Second)
	if st := ne.status(t); st.RemainingSec != 20 {
		t.Fatalf("Restzeit %d", st.RemainingSec)
	}
}

func TestNetApplyConflictAborts(t *testing.T) {
	ne := newNetEnv(t)
	ne.fx.fail["arping -D -q -I eth0 -c 2 -w 3 192.168.1.50"] = exitErr(1)
	err := ne.apply(t, staticCfg)
	if err == nil || err.Error() != "Die Adresse 192.168.1.50 wird bereits von einem anderen Gerät benutzt." {
		t.Fatalf("Fehler %v", err)
	}
	if got := ne.fx.got(); len(got) != 1 {
		t.Fatalf("Aufrufe %v", got)
	}
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Keyfile geschrieben")
	}
	if ne.status(t).Pending || len(ne.timers.ts) != 0 {
		t.Fatal("Frist offen")
	}
}

func TestNetApplyArpingErrorAborts(t *testing.T) {
	ne := newNetEnv(t)
	ne.fx.fail["arping -D -q -I eth0 -c 2 -w 3 192.168.1.50"] = exitErr(2)
	err := ne.apply(t, staticCfg)
	if err == nil || !strings.Contains(err.Error(), "Adressprüfung") {
		t.Fatalf("Fehler %v", err)
	}
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Keyfile geschrieben")
	}
}

func TestNetApplyWithoutArpingAborts(t *testing.T) {
	ne := newNetEnv(t)
	ne.tools["arping"] = false
	err := ne.apply(t, staticCfg)
	if err == nil || !strings.Contains(err.Error(), "iputils-arping") {
		t.Fatalf("Fehler %v", err)
	}
	if len(ne.fx.got()) != 0 {
		t.Fatalf("Aufrufe %v", ne.fx.got())
	}
	if st := ne.status(t); !st.ArpingMissing {
		t.Fatal("Status meldet arping nicht als fehlend")
	}
}

func TestNetApplyWithoutNmcliAborts(t *testing.T) {
	ne := newNetEnv(t)
	ne.tools["nmcli"] = false
	err := ne.apply(t, config.EthernetCfg{Method: config.EthDHCP})
	if err == nil || !strings.Contains(err.Error(), "NetworkManager") || len(ne.fx.got()) != 0 {
		t.Fatalf("Fehler %v, Aufrufe %v", err, ne.fx.got())
	}
}

func TestNetApplyOwnAddressSkipsArping(t *testing.T) {
	ne := newNetEnv(t)
	cfg := staticCfg
	cfg.Address = "192.168.1.20"
	if err := ne.apply(t, cfg); err != nil {
		t.Fatal(err)
	}
	if got := ne.fx.got(); !reflect.DeepEqual(got, []string{"nmcli connection reload"}) {
		t.Fatalf("Aufrufe %v", got)
	}
}

func TestNetApplyDHCPWithoutArping(t *testing.T) {
	ne := newNetEnv(t)
	ne.tools["arping"] = false
	if err := ne.apply(t, config.EthernetCfg{Method: config.EthDHCP}); err != nil {
		t.Fatal(err)
	}
	if got := ne.fx.got(); !reflect.DeepEqual(got, []string{"nmcli connection reload"}) {
		t.Fatalf("Aufrufe %v", got)
	}
	if !strings.Contains(ne.read(t, ne.keyfile()), "[ipv4]\nmethod=auto\n") {
		t.Fatal("Keyfile ohne DHCP")
	}
}

func TestNetApplyRejectsBadArgsWithoutAction(t *testing.T) {
	ne := newNetEnv(t)
	for _, arg := range []string{
		`{"method":"static","address":"192.168.1.0","prefix":24}`,
		`{"method":"static","address":"192.168.1.50","prefix":24,"extra":1}`,
		`{"method":""}`,
		`{"method":"dhcp"} {}`,
		`kein json`,
	} {
		if err := ne.srv.Dispatch(context.Background(), CmdNetApply, arg); err == nil {
			t.Errorf("%s angenommen", arg)
		}
	}
	if len(ne.fx.got()) != 0 || len(ne.timers.ts) != 0 {
		t.Fatalf("Aufrufe %v", ne.fx.got())
	}
}

func TestNetApplyRejectedWhilePending(t *testing.T) {
	ne := newNetEnv(t)
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	err := ne.apply(t, config.EthernetCfg{Method: config.EthDHCP})
	if err == nil || !strings.Contains(err.Error(), "Bestätigung") {
		t.Fatalf("Fehler %v", err)
	}
}

func TestNetApplyRejectedDuringBackup(t *testing.T) {
	ne := newNetEnv(t)
	run, err := ne.srv.backup.begin(context.Background(), "download", "Browser")
	if err != nil {
		t.Fatal(err)
	}
	defer run.end(nil, "")
	err = ne.apply(t, staticCfg)
	if err == nil || err.Error() != errBackupBusy || len(ne.fx.got()) != 0 {
		t.Fatalf("Fehler %v, Aufrufe %v", err, ne.fx.got())
	}
}

func TestNetDeadlineRevertsToPreviousConnection(t *testing.T) {
	ne := newNetEnv(t)
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	ne.timers.fire(t, testActivate)
	n := len(ne.fx.got())
	if !ne.timers.fire(t, testDeadline) {
		t.Fatal("Frist gestoppt")
	}
	got := ne.fx.got()[n:]
	want := []string{"nmcli connection reload", "nmcli -w 20 connection up uuid 11111111-2222-4333-8444-555555555555"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Aufrufe %v", got)
	}
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Keyfile bleibt")
	}
	st := ne.status(t)
	if st.Pending || !strings.Contains(st.Message, "wiederhergestellt") {
		t.Fatalf("Status %+v", st)
	}
	if _, err := os.Stat(ne.srv.Net.SavedPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("ohne Bestätigung dauerhaft gespeichert")
	}
}

func TestNetRevertWithoutPreviousConnectionReconnectsDevice(t *testing.T) {
	ne := newNetEnv(t)
	ne.prevOut = "\n"
	if err := ne.apply(t, config.EthernetCfg{Method: config.EthDHCP}); err != nil {
		t.Fatal(err)
	}
	n := len(ne.fx.got())
	if err := ne.srv.Dispatch(context.Background(), CmdNetRevert, ""); err != nil {
		t.Fatal(err)
	}
	got := ne.fx.got()[n:]
	if !reflect.DeepEqual(got, []string{"nmcli connection reload", "nmcli -w 20 device connect eth0"}) {
		t.Fatalf("Aufrufe %v", got)
	}
	// Die verspätete Aktivierung und die Frist laufen danach ins Leere.
	ne.timers.fire(t, testActivate)
	ne.timers.fire(t, testDeadline)
	if len(ne.fx.got()) != n+2 {
		t.Fatalf("Aufrufe nach der Rücksetzung: %v", ne.fx.got()[n:])
	}
}

func TestNetRevertRestoresPreviousKeyfile(t *testing.T) {
	ne := newNetEnv(t)
	os.MkdirAll(ne.srv.Net.ConnDir, 0o700)
	old, _ := NMKeyfile("eth0", config.EthernetCfg{Method: config.EthStatic, Address: "192.168.1.20", Prefix: 24})
	os.WriteFile(ne.keyfile(), old, 0o600)
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	n := len(ne.fx.got())
	if err := ne.srv.Dispatch(context.Background(), CmdNetRevert, ""); err != nil {
		t.Fatal(err)
	}
	if got := ne.read(t, ne.keyfile()); got != string(old) {
		t.Fatalf("Keyfile:\n%s", got)
	}
	if got := ne.fx.got()[n:]; !reflect.DeepEqual(got, []string{"nmcli connection reload", "nmcli -w 20 connection up " + NMConnID}) {
		t.Fatalf("Aufrufe %v", got)
	}
}

func TestNetActivationFailureRevertsAtOnce(t *testing.T) {
	ne := newNetEnv(t)
	ne.fx.fail["nmcli -w 20 connection up "+NMConnID] = errors.New("kaputt")
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	ne.timers.fire(t, testActivate)
	st := ne.status(t)
	if st.Pending || !strings.Contains(st.Message, "ließ sich nicht aktivieren") {
		t.Fatalf("Status %+v", st)
	}
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Keyfile bleibt")
	}
}

func TestNetConfirmPersistsAndStopsDeadline(t *testing.T) {
	ne := newNetEnv(t)
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	ne.timers.fire(t, testActivate)
	d, err := ne.srv.DispatchData(context.Background(), CmdNetConfirm, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := d.(config.EthernetCfg); !reflect.DeepEqual(got, staticCfg) {
		t.Fatalf("bestätigt %+v", got)
	}
	var saved netSaved
	if err := json.Unmarshal([]byte(ne.read(t, ne.srv.Net.SavedPath)), &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Iface != "eth0" || !reflect.DeepEqual(saved.Ethernet, staticCfg) {
		t.Fatalf("gespeichert %+v", saved)
	}
	n := len(ne.fx.got())
	if ne.timers.fire(t, testDeadline) {
		t.Fatal("Frist nicht gestoppt")
	}
	if len(ne.fx.got()) != n {
		t.Fatal("trotz Bestätigung zurückgesetzt")
	}
	if ne.status(t).Pending {
		t.Fatal("noch offen")
	}
	if _, err := os.Stat(ne.keyfile()); err != nil {
		t.Fatal("Keyfile fehlt")
	}
	if err := ne.srv.Dispatch(context.Background(), CmdNetConfirm, ""); err == nil {
		t.Fatal("zweite Bestätigung angenommen")
	}
}

func TestNetConfirmAndRevertWithoutPending(t *testing.T) {
	ne := newNetEnv(t)
	for _, cmd := range []string{CmdNetConfirm, CmdNetRevert} {
		if err := ne.srv.Dispatch(context.Background(), cmd, ""); err == nil || !strings.Contains(err.Error(), "wartet keine") {
			t.Errorf("%s: %v", cmd, err)
		}
	}
}

func TestNetRestartWithPendingStateReverts(t *testing.T) {
	ne := newNetEnv(t)
	if err := ne.apply(t, staticCfg); err != nil {
		t.Fatal(err)
	}
	// Neuer Helfer mit denselben Ordnern, ohne Zeitgeber im Speicher.
	ne2 := newNetEnv(t)
	ne2.srv.Net.SysClassNet, ne2.srv.Net.ConnDir, ne2.srv.Net.StateDir, ne2.srv.Net.SavedPath =
		ne.srv.Net.SysClassNet, ne.srv.Net.ConnDir, ne.srv.Net.StateDir, ne.srv.Net.SavedPath
	if err := ne2.srv.RecoverNet(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := ne2.fx.got(); !reflect.DeepEqual(got, []string{"nmcli connection reload", "nmcli -w 20 connection up uuid 11111111-2222-4333-8444-555555555555"}) {
		t.Fatalf("Aufrufe %v", got)
	}
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Keyfile bleibt")
	}
	if ne2.status(t).Pending {
		t.Fatal("noch offen")
	}
	// Ohne offene Frist tut der Start nichts.
	if err := ne2.srv.RecoverNet(context.Background()); err != nil || len(ne2.fx.got()) != 2 {
		t.Fatalf("%v %v", err, ne2.fx.got())
	}
}

func TestNetBoot(t *testing.T) {
	ne := newNetEnv(t)
	var logs []string
	logf := func(f string, a ...any) { logs = append(logs, f) }
	// Ohne Datei und ohne Keyfile: nichts.
	NetBoot(ne.srv.Net, logf)
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("Keyfile ohne Datei")
	}
	saved, _ := json.Marshal(netSaved{Iface: "eth0", Ethernet: staticCfg})
	if err := writeStateFile(ne.srv.Net.SavedPath, saved); err != nil {
		t.Fatal(err)
	}
	NetBoot(ne.srv.Net, logf)
	want, _ := NMKeyfile("eth0", staticCfg)
	if got := ne.read(t, ne.keyfile()); got != string(want) {
		t.Fatalf("Keyfile:\n%s", got)
	}
	if fi, _ := os.Stat(ne.keyfile()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("Modus %v", fi.Mode())
	}
	fi1, _ := os.Stat(ne.keyfile())
	NetBoot(ne.srv.Net, logf) // gleich: nicht neu schreiben
	if fi2, _ := os.Stat(ne.keyfile()); !os.SameFile(fi1, fi2) {
		t.Fatal("unverändertes Keyfile neu geschrieben")
	}
	// Ungültige Datei: nichts ändern.
	bad, _ := json.Marshal(netSaved{Iface: "eth0;x", Ethernet: staticCfg})
	writeStateFile(ne.srv.Net.SavedPath, bad)
	NetBoot(ne.srv.Net, logf)
	if got := ne.read(t, ne.keyfile()); got != string(want) {
		t.Fatal("bei ungültiger Datei verändert")
	}
	writeStateFile(ne.srv.Net.SavedPath, []byte(`{"iface":"eth0","ethernet":{"method":"static","address":"192.168.1.0","prefix":24}}`))
	NetBoot(ne.srv.Net, logf)
	if got := ne.read(t, ne.keyfile()); got != string(want) {
		t.Fatal("bei ungültiger Adresse verändert")
	}
	// Datei fehlt, Keyfile bleibt von einer unbestätigten Änderung übrig: entfernen.
	os.Remove(ne.srv.Net.SavedPath)
	NetBoot(ne.srv.Net, logf)
	if _, err := os.Stat(ne.keyfile()); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("übrig gebliebenes Keyfile bleibt")
	}
	if len(logs) < 4 {
		t.Fatalf("zu wenige Meldungen: %v", logs)
	}
}

func TestNetCommandsListedAndApplyBlockedDuringBackup(t *testing.T) {
	for _, c := range []string{CmdNetApply, CmdNetConfirm, CmdNetRevert, CmdNetStatus} {
		if _, ok := commands[c]; !ok {
			t.Errorf("%s fehlt", c)
		}
	}
	if !blockedDuringBackup[CmdNetApply] {
		t.Error("net-apply nicht gesperrt")
	}
	if blockedDuringBackup[CmdNetRevert] || blockedDuringBackup[CmdNetStatus] {
		t.Error("Rücksetzen oder Status gesperrt")
	}
}

func TestNetOverSocketWithClient(t *testing.T) {
	linuxOnly(t)
	ne := newNetEnv(t)
	sock := startServer(t, ne.env)
	cl := helperapi.New(sock)
	ctx := context.Background()
	if err := cl.NetApply(ctx, staticCfg); err != nil {
		t.Fatal(err)
	}
	st, err := cl.NetStatus(ctx)
	if err != nil || !st.Pending || st.PendingCfg.Address != "192.168.1.50" || st.RemainingSec != 120 {
		t.Fatalf("Status %+v %v", st, err)
	}
	got, err := cl.NetConfirm(ctx)
	if err != nil || !reflect.DeepEqual(got, staticCfg) {
		t.Fatalf("bestätigt %+v %v", got, err)
	}
	if err := cl.NetRevert(ctx); err == nil {
		t.Fatal("Rücksetzen ohne Frist angenommen")
	}
}
