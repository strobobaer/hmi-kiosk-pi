package app

import (
	"context"
	"net"
	"os/exec"
	"strings"
)

// pinger schickt ein ICMP-Echo über das System-ping. Auf Raspberry Pi OS Trixie
// trägt /usr/bin/ping die Capability cap_net_raw, der Kiosk-Benutzer braucht also
// weder root noch eine Einstellung von net.ipv4.ping_group_range, und es kommt
// keine zusätzliche Abhängigkeit (golang.org/x/net) in die Auslieferung.
type pinger struct {
	lookup func(ctx context.Context, host string) ([]net.IP, error)
	run    func(ctx context.Context, args []string) error
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
func (p *pinger) ping(ctx context.Context, host string) bool {
	host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	ip := net.ParseIP(host)
	if ip == nil {
		ips, err := p.lookup(ctx, host)
		if err != nil || len(ips) == 0 {
			return false
		}
		ip = preferIPv4(ips)
	}
	return p.run(ctx, []string{"-n", "-c", "1", "-W", "1", ip.String()}) == nil
}

func preferIPv4(ips []net.IP) net.IP {
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip
		}
	}
	return ips[0]
}
