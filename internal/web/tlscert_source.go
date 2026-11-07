package web

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"net"
	"path/filepath"
	"sync"
	"time"
)

// certRetry: Mindestabstand zwischen zwei Erneuerungen (insgesamt und je Adresse).
const certRetry = time.Minute

// certTriedMax begrenzt die Merkliste der zuletzt versuchten Adressen.
const certTriedMax = 64

// CertSource liefert das Zertifikat der Admin-Seite. Fehlt die IP-Adresse, unter der ein Browser
// das Terminal anspricht, im Zertifikat (etwa nach dem Wechsel auf eine feste Adresse), wird es
// mit den aktuellen Adressen und dieser Adresse neu erzeugt und auf der Datenpartition abgelegt.
// So meldet der Browser nur die bekannte Warnung für ein selbst signiertes Zertifikat und nicht
// zusätzlich eine falsche Adresse. Die Terminals untereinander prüfen das Zertifikat nicht.
type CertSource struct {
	dir    string
	mu     sync.Mutex
	cert   *tls.Certificate
	ips    []net.IP
	tried  map[string]time.Time
	last   time.Time // letzte Erneuerung (gleich welche Adresse)
	now    func() time.Time
	ifaces func() []net.IP
}

func NewCertSource(dir string, cert tls.Certificate) *CertSource {
	c := &CertSource{dir: dir, tried: map[string]time.Time{}, now: time.Now, ifaces: interfaceIPs}
	c.set(&cert)
	return c
}

func (c *CertSource) set(cert *tls.Certificate) {
	c.cert, c.ips = cert, nil
	if len(cert.Certificate) == 0 {
		return
	}
	if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
		c.ips = leaf.IPAddresses
	}
}

func (c *CertSource) covers(ip net.IP) bool {
	for _, x := range c.ips {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}

// GetCertificate ist für tls.Config.GetCertificate gedacht. Erneuert wird höchstens einmal je
// Minute insgesamt (und je Adresse); der Schlüssel entsteht außerhalb der Sperre, andere
// Handshakes bekommen solange das bisherige Zertifikat.
func (c *CertSource) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var ip net.IP
	if hello != nil && hello.Conn != nil {
		if a, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok {
			ip = a.IP
		}
	}
	c.mu.Lock()
	cur := c.cert
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || c.covers(ip) {
		c.mu.Unlock()
		return cur, nil
	}
	now := c.now()
	key := ip.String()
	if t, ok := c.tried[key]; (ok && now.Sub(t) < certRetry) || (!c.last.IsZero() && now.Sub(c.last) < certRetry) {
		c.mu.Unlock()
		return cur, nil
	}
	c.last = now
	for k, t := range c.tried {
		if now.Sub(t) >= certRetry {
			delete(c.tried, k)
		}
	}
	if len(c.tried) >= certTriedMax {
		clear(c.tried)
	}
	c.tried[key] = now
	c.mu.Unlock()

	ips := c.ifaces()
	if !containsIP(ips, ip) {
		ips = append(ips, ip)
	}
	certPEM, keyPEM, err := generateCert(now, ips)
	var cert tls.Certificate
	if err == nil {
		cert, err = tls.X509KeyPair(certPEM, keyPEM)
	}
	if err != nil {
		log.Printf("Zertifikat für %s nicht erneuert: %v", ip, err)
		return cur, nil
	}
	if err := mkdirCert(c.dir); err != nil {
		log.Printf("Neues Zertifikat für %s gilt nur bis zum Neustart: %v", ip, err)
	} else if err := saveCert(filepath.Join(c.dir, certFile), filepath.Join(c.dir, certKeyFile), certPEM, keyPEM); err != nil {
		log.Printf("Neues Zertifikat für %s gilt nur bis zum Neustart: %v", ip, err)
	}
	c.mu.Lock()
	c.set(&cert)
	c.mu.Unlock()
	return &cert, nil
}

func containsIP(ips []net.IP, ip net.IP) bool {
	for _, x := range ips {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}
