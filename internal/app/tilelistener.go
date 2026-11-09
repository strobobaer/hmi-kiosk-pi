package app

import (
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"sync"

	"hmikiosk/internal/config"
)

// tileListener ist der Port der Kachelseite. Switch bindet ihn im laufenden Betrieb
// an eine andere Adresse (Freigabe im Netz an oder aus); angenommene Verbindungen
// wie der Ereignisstrom von Chromium bleiben dabei bestehen.
type tileListener struct {
	mu     sync.Mutex
	cur    net.Listener
	addr   string // die zuletzt verlangte Adresse
	broken error  // weder neue noch alte Adresse ließ sich binden
	closed bool
}

func listenTiles(addr string) (*tileListener, error) {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &tileListener{cur: l, addr: addr}, nil
}

// tileAddr ist die Adresse der Kachelseite: lokal oder, bei Freigabe im Netz,
// derselbe Port auf allen Adressen (das schließt 127.0.0.1 für Chromium ein).
func tileAddr(local string, network bool) string {
	if !network {
		return local
	}
	_, port, err := net.SplitHostPort(local)
	if err != nil {
		return local
	}
	return net.JoinHostPort("", port)
}

func (t *tileListener) Accept() (net.Conn, error) {
	for {
		t.mu.Lock()
		l, closed, broken := t.cur, t.closed, t.broken
		t.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if broken != nil {
			return nil, broken
		}
		c, err := l.Accept()
		if err == nil {
			return c, nil
		}
		t.mu.Lock()
		swapped := t.cur != l || t.broken != nil
		closed = t.closed
		t.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if !swapped {
			return nil, err
		}
	}
}

// Switch bindet an addr; changed meldet, ob sich die Adresse geändert hat. Die alte
// Adresse wird zuerst geschlossen: Linux bindet 0.0.0.0:8080 nicht neben 127.0.0.1:8080.
// Scheitert das, gilt wieder die alte Adresse und err nennt den Grund.
func (t *tileListener) Switch(addr string) (changed bool, err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false, net.ErrClosed
	}
	if addr == t.addr && t.broken == nil {
		return false, nil
	}
	old := t.cur.Addr().String()
	t.cur.Close()
	l, err := net.Listen("tcp", addr)
	if err == nil {
		t.cur, t.addr = l, addr
		return true, nil
	}
	back, berr := net.Listen("tcp", old)
	if berr != nil {
		t.broken = fmt.Errorf("Kachelseite weder unter %s noch unter %s: %w", addr, old, errors.Join(err, berr))
		return true, t.broken
	}
	t.cur = back
	return false, err
}

func (t *tileListener) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.cur.Close()
}

func (t *tileListener) Addr() net.Addr {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.cur.Addr()
}

// syncTileListener folgt der Einstellung „Kachelseite im Netz“.
func (s *service) syncTileListener(c config.Config) {
	s.tileMu.Lock()
	defer s.tileMu.Unlock()
	if s.tileLn == nil {
		return // noch nicht gestartet: startServers liest die Einstellung selbst
	}
	want := tileAddr(s.opts.LocalAddr, c.Kiosk.NetworkTiles)
	changed, err := s.tileLn.Switch(want)
	switch {
	case err != nil && c.Kiosk.NetworkTiles:
		s.warn(fmt.Sprintf("Kachelseite nicht im Netz freigegeben, sie bleibt lokal: %v", err))
	case err != nil:
		s.warn(fmt.Sprintf("Kachelseite nicht auf %s zurückgestellt: %v. Bitte den Dienst neu starten (System).", want, err))
	case changed && c.Kiosk.NetworkTiles:
		log.Printf("Kachelseite im Netz freigegeben (nur für Tests): %s", s.tileLn.Addr())
	case changed:
		log.Printf("Kachelseite wieder nur lokal: %s", s.tileLn.Addr())
	}
}

// tileAddrNow ist die gebundene Adresse der Kachelseite (nil vor dem Start).
func (s *service) tileAddrNow() *net.TCPAddr {
	s.tileMu.Lock()
	defer s.tileMu.Unlock()
	if s.tileLn == nil {
		return nil
	}
	a, _ := s.tileLn.Addr().(*net.TCPAddr)
	return a
}

// tileURLs nennt die Adressen der Kachelseite im Netz; nil, solange sie nur lokal läuft.
func (s *service) tileURLs() []string {
	a := s.tileAddrNow()
	if a == nil || !a.IP.IsUnspecified() {
		return nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	return tileURLsFor(addrs, a.Port)
}

// tileURLsFor nimmt die IPv4-Adressen ohne Loopback und Link-Local.
func tileURLsFor(addrs []net.Addr, port int) []string {
	var urls []string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := n.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		urls = append(urls, "http://"+net.JoinHostPort(ip.String(), strconv.Itoa(port))+"/")
	}
	return urls
}
