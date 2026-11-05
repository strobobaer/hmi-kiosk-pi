package client

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Bestätigt gegen Remmina 1.4.39 aus Trixie (Spec Abschnitt 14): scale=1 skaliert mit festem
// Seitenverhältnis, viewmode=1 ist das normale Fenster, das die Fensterregel platzieren kann.
// closeonfailure=1 wirkt nur bei Verbindungsfehlern; bei falschem Passwort bleibt Remminas
// Passwortdialog offen, und nur der Dienst kann den Prozess beenden.
const (
	remminaKeyScale    = "scale"
	remminaKeyViewMode = "viewmode"
	remminaScaleOn     = "1"
	remminaScaleOff    = "0"
	remminaViewMode    = "1"
)

// RemminaTitlePrefix beginnt den Fensternamen (Profilfeld name). Remmina nimmt ihn als Titel des
// einen Fensters (gemessen in Task 7: "hmi-kiosk session 1"); WM_CLASS ist dagegen für alle
// Sitzungen "org.remmina.Remmina" und taugt nicht zur Unterscheidung.
const RemminaTitlePrefix = "hmi-kiosk session"

type remmina struct {
	rand io.Reader // nil: crypto/rand
}

func (*remmina) Name() string { return "remmina" }

func (r *remmina) Prepare(dir string, s Spec) (Command, error) {
	if err := validateSpec(s); err != nil {
		return Command{}, err
	}
	rd := r.rand
	if rd == nil {
		rd = rand.Reader
	}
	secret := make([]byte, 32)
	if _, err := io.ReadFull(rd, secret); err != nil {
		return Command{}, fmt.Errorf("Zufall: %w", err)
	}
	secretB64 := base64.StdEncoding.EncodeToString(secret)
	pw, err := remminaEncrypt(secretB64, s.Password)
	if err != nil {
		return Command{}, err
	}

	cfgDir := filepath.Join(dir, "config")
	dataDir := filepath.Join(dir, "data")
	cacheDir := filepath.Join(dir, "cache")
	for _, d := range []string{dir, cfgDir, filepath.Join(cfgDir, "remmina"), dataDir, cacheDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return Command{}, err
		}
	}
	pref := "[remmina_pref]\nsecret=" + secretB64 + "\n"
	if err := os.WriteFile(filepath.Join(cfgDir, "remmina", "remmina.pref"), []byte(pref), 0o600); err != nil {
		return Command{}, err
	}

	scale := remminaScaleOff
	if s.Scale {
		scale = remminaScaleOn
	}
	var b strings.Builder
	b.WriteString("[remmina]\n")
	for _, kv := range [][2]string{
		{"name", RemminaTitlePrefix + " " + strconv.Itoa(s.SessionN)},
		{"protocol", "VNC"},
		{"server", net.JoinHostPort(s.Host, strconv.Itoa(s.Port))},
		{"password", pw},
		{"quality", strconv.Itoa(s.Quality)},
		{"colordepth", strconv.Itoa(s.ColorDepth)},
		{"viewonly", boolStr(s.ViewOnly)},
		{"disableclipboard", "1"},
		{"closeonfailure", "1"},
		{"disablepasswordstoring", "0"},
		{remminaKeyScale, scale},
		{remminaKeyViewMode, remminaViewMode},
	} {
		if err := noControl(kv[0], kv[1]); err != nil {
			return Command{}, err
		}
		b.WriteString(kv[0] + "=" + kv[1] + "\n")
	}
	profile := filepath.Join(dir, "session.remmina")
	if err := os.WriteFile(profile, []byte(b.String()), 0o600); err != nil {
		return Command{}, err
	}

	return Command{
		Path: "remmina",
		Args: []string{
			"--gapplication-app-id=de.hmikiosk.session" + strconv.Itoa(s.SessionN),
			"-c", profile,
			"--disable-toolbar", "--enable-extra-hardening", "--no-tray-icon",
		},
		Env: []string{
			"HOME=" + dir,
			"XDG_CONFIG_HOME=" + cfgDir,
			"XDG_DATA_HOME=" + dataDir,
			"XDG_CACHE_HOME=" + cacheDir,
		},
	}, nil
}
