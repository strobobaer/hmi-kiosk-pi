package app

import (
	"net"
	"testing"
	"time"
)

func listenDatagram(t *testing.T, name string) *net.UnixConn {
	t.Helper()
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readDatagram(t *testing.T, c *net.UnixConn, limit time.Duration) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(limit))
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("keine Meldung: %v", err)
	}
	return string(buf[:n])
}
