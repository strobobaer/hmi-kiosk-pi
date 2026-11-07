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

// certRetry: So lange wird nach einer gescheiterten Erneuerung für dieselbe Adresse nicht neu versucht.
const certRetry = time.Minute

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

// GetCertificate ist für tls.Config.GetCertificate gedacht.
func (c *CertSource) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var ip net.IP
	if hello != nil && hello.Conn != nil {
		if a, ok := hello.Conn.LocalAddr().(*net.TCPAddr); ok {
			ip = a.IP
		}
	}
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() || c.covers(ip) {
		return c.cert, nil
	}
	key := ip.String()
	if t, ok := c.tried[key]; ok && c.now().Sub(t) < certRetry {
		return c.cert, nil
	}
	c.tried[key] = c.now()
	ips := c.ifaces()
	if !containsIP(ips, ip) {
		ips = append(ips, ip)
	}
	certPEM, keyPEM, err := generateCert(c.now(), ips)
	if err != nil {
		log.Printf("Zertifikat für %s nicht erneuert: %v", ip, err)
		return c.cert, nil
	}
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		log.Printf("Zertifikat für %s nicht erneuert: %v", ip, err)
		return c.cert, nil
	}
	if err := mkdirCert(c.dir); err != nil {
		log.Printf("Neues Zertifikat für %s gilt nur bis zum Neustart: %v", ip, err)
	} else if err := saveCert(filepath.Join(c.dir, certFile), filepath.Join(c.dir, certKeyFile), certPEM, keyPEM); err != nil {
		log.Printf("Neues Zertifikat für %s gilt nur bis zum Neustart: %v", ip, err)
	}
	c.set(&cert)
	return c.cert, nil
}

func containsIP(ips []net.IP, ip net.IP) bool {
	for _, x := range ips {
		if x.Equal(ip) {
			return true
		}
	}
	return false
}
