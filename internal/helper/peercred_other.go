//go:build !linux

package helper

import (
	"errors"
	"net"
)

func peerUID(net.Conn) (int, error) {
	return 0, errors.New("SO_PEERCRED nur unter Linux")
}
