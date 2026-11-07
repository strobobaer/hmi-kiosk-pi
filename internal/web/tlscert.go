package web

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	certFile    = "admin-cert.pem"
	certKeyFile = "admin-key.pem"

	certLifetime  = 10 * 365 * 24 * time.Hour
	certRenewBy   = 30 * 24 * time.Hour
	certBackdated = 24 * time.Hour // Ein Pi ohne Echtzeituhr startet oft mit etwas falscher Zeit
)

// LoadOrCreateCert lädt das Zertifikat der Admin-Seite aus dir oder erzeugt es.
// Es wird erneuert, wenn es unlesbar ist oder binnen 30 Tagen abläuft.
func LoadOrCreateCert(dir string) (tls.Certificate, error) {
	return loadOrCreateCert(dir, time.Now())
}

func loadOrCreateCert(dir string, now time.Time) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(dir, certFile), filepath.Join(dir, certKeyFile)
	if cert, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		leaf, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil && leaf.NotAfter.Sub(now) > certRenewBy {
			if err := tightenKeyFile(keyPath); err != nil {
				return tls.Certificate{}, fmt.Errorf("Rechte des Zertifikatsschlüssels: %w", err)
			}
			return cert, nil
		}
	}
	return createCert(dir, certPath, keyPath, now)
}

var chmodFile = os.Chmod

// tightenKeyFile setzt 0600, ohne ein schreibgeschütztes /data zum Fehler zu
// machen: Ist die Datei für andere ohnehin unlesbar, darf das Setzen scheitern.
func tightenKeyFile(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	perm := fi.Mode().Perm()
	if perm == 0o600 {
		return nil
	}
	if err := chmodFile(path, 0o600); err != nil && perm&0o077 != 0 {
		return err
	}
	return nil
}

func createCert(dir, certPath, keyPath string, now time.Time) (tls.Certificate, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	certPEM, keyPEM, err := generateCert(now, interfaceIPs())
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := saveCert(certPath, keyPath, certPEM, keyPEM); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

// generateCert erzeugt ein selbst signiertes Zertifikat für den Rechnernamen und ips.
func generateCert(now time.Time, ips []net.IP) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, err
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "hmi-kiosk"
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-certBackdated),
		NotAfter:              now.Add(certLifetime - certBackdated),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{host, host + ".local"},
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

func saveCert(certPath, keyPath string, certPEM, keyPEM []byte) error {
	if err := writeFileAtomic(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	return writeFileAtomic(certPath, certPEM, 0o644)
}

func interfaceIPs() []net.IP {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var ips []net.IP
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() {
			ips = append(ips, n.IP)
		}
	}
	return ips
}

// writeFileAtomic setzt die Rechte vor dem Umbenennen, damit der Schlüssel nie
// unter seinem endgültigen Namen mit zu weiten Rechten liegt.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	if err = tmp.Chmod(perm); err != nil {
		return err
	}
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func mkdirCert(dir string) error { return os.MkdirAll(dir, 0o700) }
