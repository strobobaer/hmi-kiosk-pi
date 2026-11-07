package web

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type addrConn struct {
	net.Conn
	local net.Addr
}

func (c addrConn) LocalAddr() net.Addr { return c.local }

func helloAt(ip string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{Conn: addrConn{local: &net.TCPAddr{IP: net.ParseIP(ip), Port: 8443}}}
}

func leafIPs(t *testing.T, c *tls.Certificate) []string {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ip := range leaf.IPAddresses {
		out = append(out, ip.String())
	}
	return out
}

func hasIP(ips []string, ip string) bool {
	for _, x := range ips {
		if x == ip {
			return true
		}
	}
	return false
}

func TestCertSourceRenewsForNewLocalAddress(t *testing.T) {
	dir := t.TempDir()
	cert, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := NewCertSource(dir, cert)
	// Loopback und Adressen im Zertifikat: kein neues Zertifikat.
	got, err := src.GetCertificate(helloAt("127.0.0.1"))
	if err != nil || &got.Certificate[0][0] != &cert.Certificate[0][0] {
		t.Fatalf("Loopback: neues Zertifikat (%v)", err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, certFile))

	got, err = src.GetCertificate(helloAt("192.0.2.77"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasIP(leafIPs(t, got), "192.0.2.77") {
		t.Fatalf("neue Adresse fehlt: %v", leafIPs(t, got))
	}
	after, _ := os.ReadFile(filepath.Join(dir, certFile))
	if string(after) == string(before) {
		t.Fatal("Zertifikat nicht auf die Datenpartition geschrieben")
	}
	reloaded, err := LoadOrCreateCert(dir)
	if err != nil || !hasIP(leafIPs(t, &reloaded), "192.0.2.77") {
		t.Fatalf("nach Neustart fehlt die Adresse (%v)", err)
	}
	again, _ := src.GetCertificate(helloAt("192.0.2.77"))
	if &again.Certificate[0][0] != &got.Certificate[0][0] {
		t.Fatal("bei bekannter Adresse erneut erzeugt")
	}
}

func TestCertSourceKeepsCertWhenRenewFails(t *testing.T) {
	dir := t.TempDir()
	cert, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(dir, "datei")
	os.WriteFile(blocker, []byte("x"), 0o644)
	src := NewCertSource(filepath.Join(blocker, "tls"), cert) // Ordner nicht anlegbar
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	src.now = func() time.Time { return now }
	got, err := src.GetCertificate(helloAt("192.0.2.88"))
	if err != nil {
		t.Fatal(err)
	}
	// Das Zertifikat gilt im Speicher auch ohne beschreibbaren Ordner.
	if !hasIP(leafIPs(t, got), "192.0.2.88") {
		t.Fatalf("Adresse fehlt: %v", leafIPs(t, got))
	}
}
