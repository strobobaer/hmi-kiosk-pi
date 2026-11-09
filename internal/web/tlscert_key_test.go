package web

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// spki liefert SubjectPublicKeyInfo des Blattzertifikats.
func spki(t *testing.T, c *tls.Certificate) []byte {
	t.Helper()
	return leafOf(t, c.Certificate).RawSubjectPublicKeyInfo
}

// Der Fingerabdruck, den PDH prüft, hängt nur am Schlüssel: Er muss jede Erneuerung überstehen.
func TestCertRenewalKeepsPrivateKey(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	c1, err := loadOrCreateCert(dir, now)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := loadOrCreateCert(dir, leafOf(t, c1.Certificate).NotAfter.Add(-29*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(c1.Certificate[0], c2.Certificate[0]) {
		t.Fatal("nicht erneuert")
	}
	if !bytes.Equal(spki(t, &c1), spki(t, &c2)) {
		t.Fatal("Erneuerung wegen Ablauf hat einen neuen Schlüssel erzeugt")
	}
}

func TestCertBrokenCertFileKeepsKey(t *testing.T) {
	dir := t.TempDir()
	c1, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, certFile), []byte("kaputt"), 0o644); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(spki(t, &c1), spki(t, &c2)) {
		t.Fatal("unlesbares Zertifikat: Schlüssel nicht weiterverwendet")
	}
}

func TestCertBrokenKeyFileCreatesNewKey(t *testing.T) {
	dir := t.TempDir()
	c1, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, certKeyFile), []byte("kaputt"), 0o600); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(spki(t, &c1), spki(t, &c2)) {
		t.Fatal("unlesbarer Schlüssel weiterverwendet")
	}
}

func TestCertSourceRenewalKeepsPrivateKey(t *testing.T) {
	dir := t.TempDir()
	cert, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	src := NewCertSource(dir, cert)
	before := src.PublicKeyFingerprint()
	got, err := src.GetCertificate(helloAt("192.0.2.91"))
	if err != nil {
		t.Fatal(err)
	}
	if !hasIP(leafIPs(t, got), "192.0.2.91") {
		t.Fatal("nicht erneuert")
	}
	if !bytes.Equal(spki(t, &cert), spki(t, got)) {
		t.Fatal("Erneuerung nach Adresswechsel hat einen neuen Schlüssel erzeugt")
	}
	reloaded, err := LoadOrCreateCert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(spki(t, &cert), spki(t, &reloaded)) {
		t.Fatal("nach Neustart anderer Schlüssel")
	}
	if after := src.PublicKeyFingerprint(); after != before || after == "" {
		t.Fatalf("Fingerabdruck %q → %q", before, after)
	}
}

func TestPublicKeyFingerprintFormat(t *testing.T) {
	cert, err := LoadOrCreateCert(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := PublicKeyFingerprint(cert)
	if !regexp.MustCompile(`^[0-9a-f]{2}(:[0-9a-f]{2}){31}$`).MatchString(got) {
		t.Fatalf("Format %q", got)
	}
	sum := sha256.Sum256(spki(t, &cert))
	if strings.ReplaceAll(got, ":", "") != hex.EncodeToString(sum[:]) {
		t.Fatalf("%q ist nicht SHA-256 über SubjectPublicKeyInfo", got)
	}
	if PublicKeyFingerprint(tls.Certificate{}) != "" {
		t.Fatal("leeres Zertifikat")
	}
}
