package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/helperapi"
)

// Die Befehle der Ethernet-Einstellung. net-apply setzt eine neue Einstellung mit Rückfallfrist,
// net-confirm macht sie dauerhaft, net-revert stellt die alte sofort wieder her, net-status liest.
const (
	CmdNetApply   = helperapi.CmdNetApply
	CmdNetConfirm = helperapi.CmdNetConfirm
	CmdNetRevert  = helperapi.CmdNetRevert
	CmdNetStatus  = helperapi.CmdNetStatus
)

const (
	netPendingFile  = "pending.json"
	netPrevFile     = "prev.nmconnection"
	maxNetStateSize = 4096
	nmWaitSec       = "20"
)

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// NetConfig sind Orte und Zeiten der Ethernet-Einstellung; Leeres gilt als Voreinstellung.
type NetConfig struct {
	SysClassNet string // /sys/class/net
	ConnDir     string // /etc/NetworkManager/system-connections
	// StateDir hält die offene Frist (root, 0700, tmpfs): nach einem Neustart des Geräts ist sie weg.
	StateDir string // /run/hmi-kiosk-net
	// SavedPath ist die bestätigte Einstellung auf der Datenpartition (für net-boot).
	SavedPath     string        // /data/system/ethernet.json
	Deadline      time.Duration // Rückfallfrist, 2 Minuten
	ActivateDelay time.Duration // Pause vor dem Umschalten, damit die Antwort den Browser erreicht
	HasTool       func(name string) bool
	IfaceAddrs    func(iface string) ([]netip.Prefix, error)
	AfterFunc     func(d time.Duration, f func()) (stop func() bool)
	Now           func() time.Time
}

func (n NetConfig) or(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func (n NetConfig) sysClassNet() string { return n.or(n.SysClassNet, "/sys/class/net") }
func (n NetConfig) connDir() string {
	return n.or(n.ConnDir, "/etc/NetworkManager/system-connections")
}
func (n NetConfig) stateDir() string  { return n.or(n.StateDir, "/run/hmi-kiosk-net") }
func (n NetConfig) savedPath() string { return n.or(n.SavedPath, "/data/system/ethernet.json") }
func (n NetConfig) keyfile() string   { return filepath.Join(n.connDir(), NMConnFile) }
func (n NetConfig) pendingPath() string {
	return filepath.Join(n.stateDir(), netPendingFile)
}
func (n NetConfig) prevPath() string { return filepath.Join(n.stateDir(), netPrevFile) }

func (n NetConfig) deadline() time.Duration {
	if n.Deadline > 0 {
		return n.Deadline
	}
	return helperapi.NetDeadlineSec * time.Second
}

func (n NetConfig) activateDelay() time.Duration {
	if n.ActivateDelay > 0 {
		return n.ActivateDelay
	}
	return 2 * time.Second
}

func (n NetConfig) hasTool(name string) bool {
	if n.HasTool != nil {
		return n.HasTool(name)
	}
	return OSHasTool(name)
}

func (n NetConfig) now() time.Time {
	if n.Now != nil {
		return n.Now()
	}
	return time.Now()
}

func (n NetConfig) after(d time.Duration, f func()) func() bool {
	if n.AfterFunc != nil {
		return n.AfterFunc(d, f)
	}
	return time.AfterFunc(d, f).Stop
}

func (n NetConfig) ifaceAddrs(iface string) ([]netip.Prefix, error) {
	if n.IfaceAddrs != nil {
		return n.IfaceAddrs(iface)
	}
	ifc, err := net.InterfaceByName(iface)
	if err != nil {
		return nil, err
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	var out []netip.Prefix
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(ipn.IP); ok {
				ones, _ := ipn.Mask.Size()
				out = append(out, netip.PrefixFrom(ip.Unmap(), ones))
			}
		}
	}
	return out, nil
}

// netSaved ist die bestätigte Einstellung auf der Datenpartition.
type netSaved struct {
	Iface    string             `json:"iface"`
	Ethernet config.EthernetCfg `json:"ethernet"`
}

// netPending ist die offene Frist. Das vorherige Keyfile liegt daneben (prev.nmconnection);
// fehlt es, gab es vorher kein eigenes Profil.
type netPending struct {
	Iface    string             `json:"iface"`
	New      config.EthernetCfg `json:"new"`
	PrevUUID string             `json:"prevUuid,omitempty"`
	Deadline time.Time          `json:"deadline"`
}

// netRuntime ist der Zustand im Speicher. applyID wechselt bei jeder Anwendung und Rücksetzung,
// damit verspätete Zeitgeber ins Leere laufen.
type netRuntime struct {
	applyID      uint64
	stopDeadline func() bool
	msg          string
}

func decodeStrict(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("Daten nach dem Wert")
	}
	return nil
}

// prepareNetStateDir legt den Ordner der Frist an und prüft, dass er ein echter Ordner ist.
func (n NetConfig) prepareStateDir() error {
	d := n.stateDir()
	if err := os.MkdirAll(d, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(d)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s ist kein Ordner", d)
	}
	return os.Chmod(d, 0o700)
}

func readSmallFile(path string) ([]byte, error) {
	if fi, err := os.Lstat(path); err != nil {
		return nil, err
	} else if !fi.Mode().IsRegular() {
		return nil, errors.New("keine reguläre Datei")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("keine reguläre Datei")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxNetStateSize+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxNetStateSize {
		return nil, errors.New("Datei zu groß")
	}
	return b, nil
}

func (n NetConfig) readPending() (netPending, error) {
	var p netPending
	b, err := readSmallFile(n.pendingPath())
	if err != nil {
		return p, err
	}
	if err := decodeStrict(b, &p); err != nil {
		return p, fmt.Errorf("Frist-Zustand ungültig: %v", err)
	}
	return p, nil
}

func (n NetConfig) writeKeyfile(data []byte) error {
	if err := os.MkdirAll(n.connDir(), 0o700); err != nil {
		return err
	}
	if err := writeAtomic(n.keyfile(), data, 0o600); err != nil {
		return err
	}
	// NetworkManager liest nur Keyfiles, die niemand sonst lesen darf.
	return os.Chmod(n.keyfile(), 0o600)
}

func (n NetConfig) removeKeyfile() error {
	if err := os.Remove(n.keyfile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (n NetConfig) clearPending() {
	os.Remove(n.pendingPath())
	os.Remove(n.prevPath())
}

func exitCode(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

func hasAddr(prefixes []netip.Prefix, a netip.Addr) bool {
	for _, p := range prefixes {
		if p.Addr() == a {
			return true
		}
	}
	return false
}

// netApply prüft den Wert, prüft die Adresse auf Konflikte, sichert den bisherigen Zustand,
// schreibt das Keyfile und startet Aktivierung und Rückfallfrist.
func (s *Server) netApply(ctx context.Context, arg string) error {
	var e config.EthernetCfg
	if err := decodeStrict([]byte(arg), &e); err != nil {
		return errors.New("Ethernet-Einstellung ungültig")
	}
	if e.Method != config.EthDHCP && e.Method != config.EthStatic {
		return errors.New("Die Ethernet-Einstellung braucht DHCP oder eine feste Adresse.")
	}
	if err := config.ValidateEthernet(e); err != nil {
		return err
	}
	n := s.Net
	if _, err := n.readPending(); err == nil {
		return errors.New("Eine Netzwerkänderung wartet noch auf Bestätigung. Bitte zuerst bestätigen oder zurücksetzen.")
	}
	if !n.hasTool("nmcli") {
		return errors.New("NetworkManager (nmcli) ist nicht installiert; die Ethernet-Einstellung lässt sich hier nicht ändern.")
	}
	iface, err := FirstEthernet(n.sysClassNet())
	if err != nil {
		return err
	}
	if e.Method == config.EthStatic {
		addr := netip.MustParseAddr(e.Address)
		cur, _ := n.ifaceAddrs(iface)
		if !hasAddr(cur, addr) {
			if !n.hasTool("arping") {
				return errors.New("arping fehlt (Paket iputils-arping): Die Adresse lässt sich nicht auf Konflikte prüfen. Abbruch, es wurde nichts geändert.")
			}
			err := s.exec(ctx, "arping", "-D", "-q", "-I", iface, "-c", "2", "-w", "3", addr.String())
			switch {
			case err == nil:
			case exitCode(err) == 1:
				return fmt.Errorf("Die Adresse %s wird bereits von einem anderen Gerät benutzt.", addr)
			default:
				return fmt.Errorf("Adressprüfung mit arping fehlgeschlagen: %v", err)
			}
		}
	}
	keyfile, err := NMKeyfile(iface, e)
	if err != nil {
		return err
	}

	// Bisherigen Zustand sichern.
	if err := n.prepareStateDir(); err != nil {
		return fmt.Errorf("Zustand der Frist nicht speicherbar: %v", err)
	}
	prevUUID := ""
	if s.ExecOut != nil {
		if out, err := s.ExecOut(ctx, "nmcli", "-g", "GENERAL.CON-UUID", "device", "show", iface); err == nil {
			if u := strings.TrimSpace(out); uuidRe.MatchString(u) && u != NMConnUUID {
				prevUUID = u
			}
		}
	}
	old, err := readSmallFile(n.keyfile())
	switch {
	case err == nil:
		if err := writeAtomic(n.prevPath(), old, 0o600); err != nil {
			return fmt.Errorf("Bisheriges Profil nicht sicherbar: %v", err)
		}
	case errors.Is(err, os.ErrNotExist):
		os.Remove(n.prevPath())
	default:
		return fmt.Errorf("Bisheriges Profil nicht lesbar: %v", err)
	}
	p := netPending{Iface: iface, New: e, PrevUUID: prevUUID, Deadline: n.now().Add(n.deadline())}
	pj, _ := json.Marshal(p)
	if err := writeAtomic(n.pendingPath(), pj, 0o600); err != nil {
		n.clearPending()
		return fmt.Errorf("Zustand der Frist nicht speicherbar: %v", err)
	}
	if err := n.writeKeyfile(keyfile); err != nil {
		s.restoreKeyfile(old)
		n.clearPending()
		return fmt.Errorf("Netzwerkprofil nicht schreibbar: %v", err)
	}
	if err := s.run(ctx, "nmcli", "connection", "reload"); err != nil {
		s.restoreKeyfile(old)
		n.clearPending()
		_ = s.exec(ctx, "nmcli", "connection", "reload")
		return err
	}

	s.netRT.applyID++
	id := s.netRT.applyID
	s.netRT.msg = ""
	n.after(n.activateDelay(), func() { s.netActivate(id) })
	s.netRT.stopDeadline = n.after(n.deadline(), func() { s.netDeadline(id) })
	log.Printf("Ethernet: neue Einstellung (%s) auf %s, Frist %v", describeEth(e), iface, n.deadline())
	return nil
}

func describeEth(e config.EthernetCfg) string {
	if e.Method == config.EthDHCP {
		return "DHCP"
	}
	return fmt.Sprintf("%s/%d", e.Address, e.Prefix)
}

func (s *Server) restoreKeyfile(old []byte) {
	if old != nil {
		_ = s.Net.writeKeyfile(old)
	} else {
		_ = s.Net.removeKeyfile()
	}
}

func netCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), commandTimeout)
}

// netActivate schaltet nach der kurzen Pause auf das Profil um; scheitert das, gilt sofort die alte Einstellung.
func (s *Server) netActivate(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.netRT.applyID != id {
		return
	}
	ctx, cancel := netCtx()
	defer cancel()
	if err := s.run(ctx, "nmcli", "-w", nmWaitSec, "connection", "up", NMConnID); err != nil {
		if _, perr := s.Net.readPending(); perr != nil {
			log.Printf("Ethernet: bestätigtes Profil ließ sich nicht aktivieren: %v", err)
			return
		}
		s.revertNetLocked(ctx, fmt.Sprintf("Die neue Einstellung ließ sich nicht aktivieren (%v); die alte Einstellung ist wiederhergestellt.", err))
	}
}

func (s *Server) netDeadline(id uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.netRT.applyID != id {
		return
	}
	if _, err := s.Net.readPending(); err != nil {
		return
	}
	ctx, cancel := netCtx()
	defer cancel()
	s.revertNetLocked(ctx, "Die neue Einstellung wurde nicht rechtzeitig bestätigt; die alte Einstellung ist wiederhergestellt.")
}

// revertNetLocked stellt das vorherige Profil (oder keins) wieder her und fährt die vorherige
// Verbindung hoch. Der Frist-Zustand wird auch bei Fehlern entfernt: Ein zweiter Versuch hätte
// dieselben Daten.
func (s *Server) revertNetLocked(ctx context.Context, reason string) error {
	n := s.Net
	p, _ := n.readPending()
	old, err := readSmallFile(n.prevPath())
	hadProfile := err == nil
	var errs []string
	if hadProfile {
		if err := n.writeKeyfile(old); err != nil {
			errs = append(errs, fmt.Sprintf("Profil nicht wiederherstellbar: %v", err))
		}
	} else if err := n.removeKeyfile(); err != nil {
		errs = append(errs, fmt.Sprintf("Profil nicht entfernbar: %v", err))
	}
	if err := s.run(ctx, "nmcli", "connection", "reload"); err != nil {
		errs = append(errs, err.Error())
	}
	iface := p.Iface
	if !ValidIface(iface) {
		iface, _ = FirstEthernet(n.sysClassNet())
	}
	var upErr error
	switch {
	case hadProfile:
		upErr = s.run(ctx, "nmcli", "-w", nmWaitSec, "connection", "up", NMConnID)
	case uuidRe.MatchString(p.PrevUUID):
		upErr = s.run(ctx, "nmcli", "-w", nmWaitSec, "connection", "up", "uuid", p.PrevUUID)
	case iface != "":
		upErr = s.run(ctx, "nmcli", "-w", nmWaitSec, "device", "connect", iface)
	}
	if upErr != nil {
		errs = append(errs, upErr.Error())
	}
	n.clearPending()
	s.netRT.applyID++
	if s.netRT.stopDeadline != nil {
		s.netRT.stopDeadline()
		s.netRT.stopDeadline = nil
	}
	msg := reason
	if len(errs) > 0 {
		msg += " Fehler dabei: " + strings.Join(errs, "; ")
	}
	s.netRT.msg = msg
	log.Printf("Ethernet: %s", msg)
	if len(errs) > 0 {
		return errors.New(msg)
	}
	return nil
}

func (s *Server) netRevert(ctx context.Context, _ string) error {
	if _, err := s.Net.readPending(); err != nil {
		return errors.New("Es wartet keine Netzwerkänderung auf Bestätigung.")
	}
	return s.revertNetLocked(ctx, "Die alte Einstellung wurde auf Wunsch wiederhergestellt.")
}

// netConfirm schreibt die Einstellung auf die Datenpartition und beendet die Frist.
func (s *Server) netConfirm(_ context.Context, _ string) (any, error) {
	n := s.Net
	p, err := n.readPending()
	if err != nil {
		return nil, errors.New("Es wartet keine Netzwerkänderung auf Bestätigung.")
	}
	b, _ := json.Marshal(netSaved{Iface: p.Iface, Ethernet: p.New})
	if err := writeStateFile(n.savedPath(), append(b, '\n')); err != nil {
		return nil, fmt.Errorf("Die Einstellung ließ sich nicht dauerhaft speichern (%v); die Frist läuft weiter.", err)
	}
	if s.netRT.stopDeadline != nil {
		s.netRT.stopDeadline()
		s.netRT.stopDeadline = nil
	}
	n.clearPending()
	s.netRT.msg = ""
	log.Printf("Ethernet: Einstellung (%s) bestätigt", describeEth(p.New))
	return p.New, nil
}

func (s *Server) netStatus(_ context.Context, _ string) (any, error) {
	n := s.Net
	st := helperapi.NetStatus{
		Message:       s.netRT.msg,
		NMMissing:     !n.hasTool("nmcli"),
		ArpingMissing: !n.hasTool("arping"),
	}
	iface, err := FirstEthernet(n.sysClassNet())
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Iface = iface
		addrs, err := n.ifaceAddrs(iface)
		if err != nil {
			st.Error = fmt.Sprintf("Adressen nicht lesbar: %v", err)
		}
		for _, a := range addrs {
			if a.Addr().Is4() {
				st.Addresses = append(st.Addresses, a.String())
			}
		}
	}
	if p, err := n.readPending(); err == nil {
		st.Pending, st.PendingCfg = true, p.New
		left := p.Deadline.Sub(n.now())
		st.RemainingSec = max(0, int((left+time.Second-1)/time.Second))
	}
	return st, nil
}

// RecoverNet setzt eine offene Frist sofort zurück: Nach einem Neustart des Helfers fehlt der
// Zeitgeber, und niemand hat bestätigt.
func (s *Server) RecoverNet(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Lstat(s.Net.pendingPath()); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	return s.revertNetLocked(ctx, "Der Helfer wurde während der Frist neu gestartet; die alte Einstellung ist wiederhergestellt.")
}

// NetBoot erzeugt beim Start das Keyfile aus der bestätigten Einstellung auf der Datenpartition
// (das Wurzeldateisystem des Abbilds ist ein tmpfs-Overlay). Es blockiert das Netz nie: Bei
// ungültiger oder unlesbarer Datei bleibt alles, wie es ist. Fehlt die Datei, ist ein eigenes
// Keyfile ein Überbleibsel einer unbestätigten Änderung (System ohne Schreibschutz) und wird entfernt.
func NetBoot(n NetConfig, logf func(format string, args ...any)) {
	raw, err := readStateFile(n.savedPath(), maxNetStateSize)
	if errors.Is(err, os.ErrNotExist) {
		if _, serr := os.Lstat(n.keyfile()); serr == nil {
			if err := n.removeKeyfile(); err != nil {
				logf("Ethernet: unbestätigtes Profil nicht entfernbar: %v", err)
				return
			}
			logf("Ethernet: keine bestätigte Einstellung, unbestätigtes Profil %s entfernt", n.keyfile())
			return
		}
		logf("Ethernet: keine bestätigte Einstellung, System bleibt unverändert")
		return
	}
	if err != nil {
		logf("Ethernet: bestätigte Einstellung nicht lesbar, nichts geändert: %v", err)
		return
	}
	var saved netSaved
	if err := decodeStrict(raw, &saved); err != nil {
		logf("Ethernet: bestätigte Einstellung ungültig, nichts geändert: %v", err)
		return
	}
	if saved.Ethernet.Method == "" {
		logf("Ethernet: bestätigte Einstellung ohne Art, nichts geändert")
		return
	}
	data, err := NMKeyfile(saved.Iface, saved.Ethernet)
	if err != nil {
		logf("Ethernet: bestätigte Einstellung ungültig, nichts geändert: %v", err)
		return
	}
	if cur, err := readSmallFile(n.keyfile()); err == nil && bytes.Equal(cur, data) {
		logf("Ethernet: Profil %s ist aktuell (%s)", NMConnID, describeEth(saved.Ethernet))
		return
	}
	if err := n.writeKeyfile(data); err != nil {
		logf("Ethernet: Profil nicht schreibbar: %v", err)
		return
	}
	logf("Ethernet: Profil %s geschrieben (%s auf %s)", NMConnID, describeEth(saved.Ethernet), saved.Iface)
}
