package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func leafOf(t *testing.T, raw [][]byte) *x509.Certificate {
	t.Helper()
	c, err := x509.ParseCertificate(raw[0])
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCertCreatedOnceThenReused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "neu")
	c1, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(c1.Certificate[0]) != string(c2.Certificate[0]) {
		t.Error("Zertifikat neu erzeugt")
	}
	leaf := leafOf(t, c1.Certificate)
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Errorf("Schlüssel %T", leaf.PublicKey)
	}
	if years := leaf.NotAfter.Sub(leaf.NotBefore).Hours() / 24 / 365; years < 9.9 || years > 10.1 {
		t.Errorf("Laufzeit %.1f Jahre", years)
	}
	if leaf.IsCA {
		t.Error("CA-Zertifikat")
	}
	fi, err := os.Stat(filepath.Join(dir, certKeyFile))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("Schlüsselrechte %o", fi.Mode().Perm())
	}
}

func TestCertSANsHostnameLocalAndInterfaceIPs(t *testing.T) {
	c, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	leaf := leafOf(t, c.Certificate)
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range leaf.DNSNames {
		names[n] = true
	}
	if !names[host] || !names[host+".local"] {
		t.Errorf("Namen: %v", leaf.DNSNames)
	}
	addrs, _ := net.InterfaceAddrs()
	want := map[string]bool{}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() {
			want[n.IP.String()] = true
		}
	}
	got := map[string]bool{}
	for _, ip := range leaf.IPAddresses {
		if ip.IsLoopback() {
			t.Errorf("Loopback in SAN: %v", ip)
		}
		got[ip.String()] = true
	}
	for ip := range want {
		if !got[ip] {
			t.Errorf("Adresse %s fehlt: %v", ip, leaf.IPAddresses)
		}
	}
}

func TestCertRegeneratedWhenExpiringWithin30Days(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c1, err := loadOrCreateCert(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	notAfter := leafOf(t, c1.Certificate).NotAfter
	c2, err := loadOrCreateCert(dir, notAfter.Add(-31*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if string(c1.Certificate[0]) != string(c2.Certificate[0]) {
		t.Error("zu früh erneuert")
	}
	c3, err := loadOrCreateCert(dir, notAfter.Add(-29*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if string(c1.Certificate[0]) == string(c3.Certificate[0]) {
		t.Error("nicht erneuert")
	}
}

func TestCertBrokenFilesAreReplaced(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, certFile), []byte("kaputt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Fatal(err)
	}
}

// Auf schreibgeschütztem /data scheitert jedes chmod (EROFS). Als root lässt sich
// das nicht erzwingen, darum die Fälschung.
func TestCertLoadOnReadOnlyData(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Fatal(err)
	}
	calls := 0
	old := chmodFile
	chmodFile = func(string, os.FileMode) error { calls++; return errors.New("read-only file system") }
	defer func() { chmodFile = old }()
	p := filepath.Join(dir, certKeyFile)

	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Errorf("Rechte 0600: %v", err)
	}
	if calls != 0 {
		t.Errorf("chmod bei unveränderten Rechten: %d", calls)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("Rechte %o", fi.Mode().Perm())
	}

	// Strenger als nötig: Das gescheiterte Setzen ist kein Fehler.
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Errorf("Rechte 0400: %v", err)
	}

	// Für andere lesbar und nicht korrigierbar: nicht stillschweigend benutzen.
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCert(dir); err == nil {
		t.Error("Rechte 0644 ohne Fehler hingenommen")
	}
}

func TestCertKeyRightsTightenedOnLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, certKeyFile)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateCert(dir); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("Rechte %o", fi.Mode().Perm())
	}
}
