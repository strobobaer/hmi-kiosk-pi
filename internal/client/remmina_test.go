package client

import (
	"bytes"
	"crypto/cipher"
	"crypto/des"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSecret() (raw []byte, b64 string) {
	raw = make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i)
	}
	return raw, base64.StdEncoding.EncodeToString(raw)
}

func remminaDecrypt(t *testing.T, secretB64, enc string) string {
	t.Helper()
	raw, _ := base64.StdEncoding.DecodeString(secretB64)
	ct, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := des.NewTripleDESCipher(raw[:24])
	pt := make([]byte, len(ct))
	cipher.NewCBCDecrypter(blk, raw[24:32]).CryptBlocks(pt, ct)
	return string(bytes.TrimRight(pt, "\x00"))
}

func TestRemminaEncryptMatchesKnownVector(t *testing.T) {
	raw, b64 := testSecret()
	blk, err := des.NewTripleDESCipher(raw[:24])
	if err != nil {
		t.Fatal(err)
	}
	pt := append([]byte("abc"), make([]byte, 5)...)
	ct := make([]byte, 8)
	cipher.NewCBCEncrypter(blk, raw[24:32]).CryptBlocks(ct, pt)
	want := base64.StdEncoding.EncodeToString(ct)

	got, err := remminaEncrypt(b64, "abc")
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q, independent computation %q", got, want)
	}
	const pinned = "X4PauzOzjPs="
	if got != pinned {
		t.Fatalf("got %q, pinned %q", got, pinned)
	}
}

func TestRemminaEncryptPads8ByteInputWithExtraBlock(t *testing.T) {
	_, b64 := testSecret()
	enc, err := remminaEncrypt(b64, "12345678")
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := base64.StdEncoding.DecodeString(enc)
	if len(ct) != 16 {
		t.Fatalf("ciphertext len = %d, want 16", len(ct))
	}
	if remminaDecrypt(t, b64, enc) != "12345678" {
		t.Fatal("round trip failed")
	}
}

func TestRemminaEncryptUmlautRoundTrip(t *testing.T) {
	_, b64 := testSecret()
	enc, err := remminaEncrypt(b64, "Pässwörd-ß€")
	if err != nil {
		t.Fatal(err)
	}
	if got := remminaDecrypt(t, b64, enc); got != "Pässwörd-ß€" {
		t.Fatalf("got %q", got)
	}
}

func testSpec() Spec {
	return Spec{Host: "10.0.0.5", Port: 5900, Password: "geheim", ViewOnly: true,
		Quality: 2, ColorDepth: 32, Scale: true, SessionN: 7}
}

func prepareRemmina(t *testing.T, s Spec) (string, Command) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "sess")
	p := &remmina{rand: bytes.NewReader(func() []byte { r, _ := testSecret(); return r }())}
	cmd, err := p.Prepare(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	return dir, cmd
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRemminaProfileHasExpectedFields(t *testing.T) {
	dir, _ := prepareRemmina(t, testSpec())
	prof := readFile(t, filepath.Join(dir, "session.remmina"))
	_, b64 := testSecret()
	enc, _ := remminaEncrypt(b64, "geheim")
	for _, want := range []string{
		"[remmina]\n", "protocol=VNC\n", "server=10.0.0.5:5900\n", "password=" + enc + "\n",
		"viewonly=1\n", "quality=2\n", "colordepth=32\n", "disableclipboard=1\n",
		"closeonfailure=1\n", "disablepasswordstoring=0\n",
		"scale=1\n", "viewmode=1\n",
	} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile lacks %q:\n%s", want, prof)
		}
	}
	if strings.Contains(prof, "geheim") {
		t.Error("plaintext password in profile")
	}

	s := testSpec()
	s.Host, s.ViewOnly, s.Scale = "::1", false, false
	dir, _ = prepareRemmina(t, s)
	prof = readFile(t, filepath.Join(dir, "session.remmina"))
	for _, want := range []string{"server=[::1]:5900\n", "viewonly=0\n", "scale=0\n"} {
		if !strings.Contains(prof, want) {
			t.Errorf("profile lacks %q:\n%s", want, prof)
		}
	}
}

func TestRemminaProfileCannotBeInjected(t *testing.T) {
	for name, mod := range map[string]func(*Spec){
		"host newline": func(s *Spec) { s.Host = "a\nprotocol=RDP" },
		"host CR":      func(s *Spec) { s.Host = "a\rb" },
		"host dash":    func(s *Spec) { s.Host = "-evil" },
		"pw newline":   func(s *Spec) { s.Password = "x\n[remmina]" },
		"pw NUL":       func(s *Spec) { s.Password = "x\x00y" },
		"host empty":   func(s *Spec) { s.Host = "" },
		"port zero":    func(s *Spec) { s.Port = 0 },
	} {
		for _, p := range []Profile{&remmina{}, tigervnc{}} {
			s := testSpec()
			mod(&s)
			if _, err := p.Prepare(filepath.Join(t.TempDir(), "s"), s); err == nil {
				t.Errorf("%s/%s: no error", p.Name(), name)
			}
		}
	}
}

func TestRemminaArgsIncludeUniqueAppIDAndNoKiosk(t *testing.T) {
	dir, cmd := prepareRemmina(t, testSpec())
	if cmd.Path != "remmina" {
		t.Fatalf("path %q", cmd.Path)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--gapplication-app-id=de.hmikiosk.session7", "-c " + filepath.Join(dir, "session.remmina"), "--no-tray-icon"} {
		if !strings.Contains(args, want) {
			t.Errorf("args lack %q: %s", want, args)
		}
	}
	for _, bad := range []string{"--kiosk", "--enable-fullscreen"} {
		if strings.Contains(args, bad) {
			t.Errorf("args contain %q", bad)
		}
	}
	s := testSpec()
	s.SessionN = 3
	_, cmd = prepareRemmina(t, s)
	if !strings.Contains(strings.Join(cmd.Args, " "), "de.hmikiosk.session3") {
		t.Error("app id not per session")
	}
}

func TestRemminaEnvRedirectsXDGIntoSessionDir(t *testing.T) {
	dir, cmd := prepareRemmina(t, testSpec())
	want := []string{
		"HOME=" + dir,
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"XDG_DATA_HOME=" + filepath.Join(dir, "data"),
		"XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
	}
	if strings.Join(cmd.Env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("env = %v, want %v", cmd.Env, want)
	}
}

func TestRemminaWritesPrefSecretMode0600(t *testing.T) {
	dir, _ := prepareRemmina(t, testSpec())
	pref := filepath.Join(dir, "config", "remmina", "remmina.pref")
	_, b64 := testSecret()
	if got := readFile(t, pref); got != "[remmina_pref]\nsecret="+b64+"\n" {
		t.Fatalf("pref = %q", got)
	}
	for p, mode := range map[string]os.FileMode{
		pref:                                  0o600,
		filepath.Join(dir, "session.remmina"): 0o600,
		dir:                                   0o700,
		filepath.Join(dir, "config"):          0o700,
		filepath.Join(dir, "data"):            0o700,
		filepath.Join(dir, "cache"):           0o700,
	} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != mode {
			t.Errorf("%s mode %v, want %v", p, fi.Mode().Perm(), mode)
		}
	}
}

func TestRemminaEmptyPasswordWritesEmptyField(t *testing.T) {
	s := testSpec()
	s.Password = ""
	dir, _ := prepareRemmina(t, s)
	if prof := readFile(t, filepath.Join(dir, "session.remmina")); !strings.Contains(prof, "\npassword=\n") {
		t.Fatalf("profile:\n%s", prof)
	}
}

func TestByName(t *testing.T) {
	for _, n := range []string{"remmina", "tigervnc"} {
		p, err := ByName(n)
		if err != nil || p.Name() != n {
			t.Errorf("%s: %v %v", n, p, err)
		}
	}
	if _, err := ByName("x"); err == nil {
		t.Error("unknown name accepted")
	}
}

func TestRemminaDefaultRandomIsFresh(t *testing.T) {
	p := &remmina{}
	a := filepath.Join(t.TempDir(), "a")
	b := filepath.Join(t.TempDir(), "b")
	if _, err := p.Prepare(a, testSpec()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Prepare(b, testSpec()); err != nil {
		t.Fatal(err)
	}
	pa := readFile(t, filepath.Join(a, "config", "remmina", "remmina.pref"))
	pb := readFile(t, filepath.Join(b, "config", "remmina", "remmina.pref"))
	if pa == pb {
		t.Fatal("secret repeated")
	}
}

// Ohne Leiste trägt das Fenster einen eigenen Titel; die labwc-Regel dafür schaltet auf
// Vollbild (gemessen: Titel steht unter labwc beim ersten Anzeigen schon fest).
func TestRemminaFullScreenUsesOwnTitle(t *testing.T) {
	s := testSpec()
	s.SessionN = 3
	dir, _ := prepareRemmina(t, s)
	if p := readFile(t, filepath.Join(dir, "session.remmina")); !strings.Contains(p, "\nname="+RemminaTitlePrefix+" 3\n") {
		t.Fatalf("Profil:\n%s", p)
	}
	s.FullScreen = true
	dir, _ = prepareRemmina(t, s)
	p := readFile(t, filepath.Join(dir, "session.remmina"))
	if !strings.Contains(p, "\nname="+RemminaFullTitlePrefix+" 3\n") || strings.Contains(p, RemminaTitlePrefix) {
		t.Fatalf("Profil:\n%s", p)
	}
}
