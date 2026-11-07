package app

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
)

// errPingUnavailable: ping fehlt oder lässt sich nicht starten. Die Kachel zeigt
// dann keinen Zustand (wie „aus“) statt jedes HMI als nicht erreichbar.
var errPingUnavailable = errors.New("ping nicht verfügbar")

// pinger schickt ein ICMP-Echo über das System-ping. Auf Raspberry Pi OS Trixie
// trägt /usr/bin/ping die Capability cap_net_raw, der Kiosk-Benutzer braucht also
// weder root noch eine Einstellung von net.ipv4.ping_group_range, und es kommt
// keine zusätzliche Abhängigkeit (golang.org/x/net) in die Auslieferung.
type pinger struct {
	lookup func(ctx context.Context, host string) ([]net.IP, error)
	run    func(ctx context.Context, args []string) error
	logf   func(format string, args ...any) // nil heißt log.Printf

	down atomic.Bool // ping zuletzt nicht verfügbar; protokolliert wird nur der Wechsel
}

func newPinger() *pinger {
	return &pinger{
		lookup: func(ctx context.Context, host string) ([]net.IP, error) {
			return net.DefaultResolver.LookupIP(ctx, "ip", host)
		},
		run: func(ctx context.Context, args []string) error {
			return exec.CommandContext(ctx, "ping", args...).Run()
		},
	}
}

// ping löst den Namen einmal auf und ruft ping mit der Adresse und festen
// Argumenten ohne Shell. Eine Adresse kann nie wie eine Option aussehen.
// Fehlt ping oder startet es nicht, kommt errPingUnavailable: kein Zustand.
func (p *pinger) ping(ctx context.Context, host string) (bool, error) {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := p.lookup(ctx, host)
		if err != nil || len(ips) == 0 {
			return false, nil
		}
		ip = preferIPv4(ips)
	}
	err := p.run(ctx, []string{"-n", "-c", "1", "-W", "1", ip.String()})
	if notStarted(err) {
		if p.down.CompareAndSwap(false, true) {
			p.log("ping nicht verfügbar – Erreichbarkeit per Ping ausgesetzt, Kacheln ohne Zustand "+
				"(ping unavailable, reachability by ping suspended): %v", err)
		}
		return false, errPingUnavailable
	}
	if p.down.CompareAndSwap(true, false) {
		p.log("ping wieder verfügbar – Erreichbarkeit per Ping läuft wieder (ping available again)")
	}
	return err == nil, nil
}

// notStarted trennt „ping lief nicht“ (fehlt im Suchpfad, nicht ausführbar) von
// „ping lief, das Ziel antwortete nicht“ (Exit-Status ungleich 0).
func notStarted(err error) bool {
	var ee *exec.Error
	var pe *fs.PathError
	return errors.As(err, &ee) || errors.As(err, &pe)
}

func (p *pinger) log(format string, args ...any) {
	if p.logf != nil {
		p.logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func preferIPv4(ips []net.IP) net.IP {
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip
		}
	}
	return ips[0]
}
