//go:build !linux

package platform

import (
	"context"
	"net"
)

func HasEstablished(procRoot string, ip net.IP, port int) (bool, error) { return false, nil }

func EthernetHasLink(sysRoot string) bool { return false }

func InterfaceForIP(ip net.IP) (name string, isWifi bool) { return "", false }

func WatchTouch(ctx context.Context, devDir string, activity func()) error {
	<-ctx.Done()
	return ctx.Err()
}
