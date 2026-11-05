package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
)

const exportPW = "export-passwort"

func exportBlob(t *testing.T, b *browser) []byte {
	t.Helper()
	r := b.post("/admin/system/export", url.Values{"password": {exportPW}, "password2": {exportPW}})
	if r.Code != http.StatusOK {
		t.Fatalf("Export: %d %s", r.Code, r.Body.String())
	}
	return r.Body.Bytes()
}

func importBlob(t *testing.T, b *browser, blob []byte, pw string) string {
	t.Helper()
	tok := b.tokenFrom("/admin/system")
	r := b.upload("/admin/system/import", [][2]string{{"csrf", tok}, {"password", pw}}, blob)
	if r.Code == http.StatusSeeOther {
		return b.follow(r)
	}
	return r.Body.String()
}

func TestExportFileNameAndContentAreEncrypted(t *testing.T) {
	e, b := newLoggedIn(t)
	e.addHMI("x", "Presse", "vnc-geheim")
	e.mutate(func(c *config.Config) {
		c.Terminal.Name = `Halle "2"/ä`
		enc, _ := e.box.Seal("gemeinsam-geheim")
		c.SharedKeyEnc = enc
	})
	r := b.post("/admin/system/export", url.Values{"password": {exportPW}, "password2": {exportPW}})
	if r.Code != http.StatusOK {
		t.Fatalf("Status %d", r.Code)
	}
	cd := r.Header().Get("Content-Disposition")
	wantContains(t, cd, "attachment", ".hkx")
	wantNotContains(t, cd, "/", `"2"`, "\n")
	if s := r.Header().Get("Content-Type"); s != "application/octet-stream" {
		t.Errorf("Typ %q", s)
	}
	blob := r.Body.Bytes()
	wantNotContains(t, string(blob), "vnc-geheim", "gemeinsam-geheim", "Presse")

	plain, err := secrets.OpenExport(blob, exportPW)
	if err != nil {
		t.Fatal(err)
	}
	s := string(plain)
	wantContains(t, s, "vnc-geheim", "gemeinsam-geheim")
	wantNotContains(t, s, e.cfg().Admin.PasswordHash, e.hmi("x").PasswordEnc, e.cfg().SharedKeyEnc)
}

func TestExportFileNamePlainASCII(t *testing.T) {
	e, b := newLoggedIn(t)
	e.mutate(func(c *config.Config) { c.Terminal.Name = "Halle-2" })
	r := b.post("/admin/system/export", url.Values{"password": {exportPW}, "password2": {exportPW}})
	wantContains(t, r.Header().Get("Content-Disposition"), "hmi-kiosk-Halle-2.hkx")
}

func TestExportRequiresStrongRepeatedPassword(t *testing.T) {
	_, b := newLoggedIn(t)
	for _, f := range []url.Values{
		{"password": {"kurz"}, "password2": {"kurz"}},
		{"password": {exportPW}, "password2": {"anders-passwort"}},
	} {
		r := b.post("/admin/system/export", f)
		if r.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", f, r.Code)
		}
		wantNotContains(t, r.Header().Get("Content-Disposition"), "attachment")
	}
}

// Quelle und Ziel haben verschiedene Box-Schlüssel: Geheimnisse müssen neu versiegelt werden.
func TestExportImportKeepsDeviceIdentity(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.addHMI("x", "Presse", "vnc-geheim")
	src.addHMI("y", "Ohne Passwort", "")
	src.mutate(func(c *config.Config) {
		c.Terminal.Name = "Quelle"
		c.Terminal.Role = config.RolePrimary
		c.Client.Quality = 9
		c.Kiosk.Layout = "3"
		enc, _ := src.box.Seal("gemeinsam-geheim")
		c.SharedKeyEnc = enc
		c.Network.WifiOff = true
		c.Network.NTPServer = "quelle.ntp"
	})
	blob := exportBlob(t, sb)

	dst, db := newLoggedIn(t)
	dst.mutate(func(c *config.Config) {
		c.Terminal.Name = "Ziel"
		c.Terminal.Role = config.RoleNormal
		c.Admin.PasswordHash = "fast$ziel-passwort-xy"
		c.Network.BluetoothOff = true
		c.Network.NTPServer = "ziel.ntp"
	})
	before := dst.cfg()
	body := importBlob(t, db, blob, exportPW)
	wantContains(t, body, "importiert")

	c := dst.cfg()
	if c.Terminal != before.Terminal {
		t.Errorf("Identität geändert: %+v", c.Terminal)
	}
	if c.Admin.PasswordHash != "fast$ziel-passwort-xy" {
		t.Error("Admin-Passwort geändert")
	}
	if c.Network != before.Network {
		t.Errorf("Netzwerk ist Gerätezustand und bleibt: %+v", c.Network)
	}
	if c.Client.Quality != 9 || c.Kiosk.Layout != "3" || len(c.HMIs) != 2 {
		t.Errorf("Einstellungen nicht übernommen: %+v", c)
	}
	if got, err := dst.box.Open(dst.hmi("x").PasswordEnc); err != nil || got != "vnc-geheim" {
		t.Errorf("VNC-Passwort nicht neu versiegelt: %q %v", got, err)
	}
	if dst.hmi("y").PasswordEnc != "" {
		t.Error("leeres Passwort erfunden")
	}
	if got, err := dst.box.Open(c.SharedKeyEnc); err != nil || got != "gemeinsam-geheim" {
		t.Errorf("Schlüssel: %q %v", got, err)
	}
	if dst.appliedCount() != 1 {
		t.Errorf("Apply %d", dst.appliedCount())
	}
}

func TestImportDropsOwnEntryFromPeers(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.mutate(func(c *config.Config) {
		c.Terminal.Name = "A"
		c.Peers = []config.Peer{{Name: "B", Address: "10.0.0.2"}, {Name: "C", Address: "10.0.0.3"}}
	})
	blob := exportBlob(t, sb)
	dst, db := newLoggedIn(t)
	dst.mutate(func(c *config.Config) { c.Terminal.Name = "B" })
	wantContains(t, importBlob(t, db, blob, exportPW), "importiert")
	if p := dst.cfg().Peers; len(p) != 1 || p[0].Name != "C" {
		t.Errorf("Peers: %+v", p)
	}
}

func TestImportWrongPasswordLeavesConfigUntouched(t *testing.T) {
	src, sb := newLoggedIn(t)
	src.addHMI("x", "Presse", "")
	blob := exportBlob(t, sb)
	dst, db := newLoggedIn(t)
	dst.addHMI("z", "Eigen", "")
	body := importBlob(t, db, blob, "falsches-passwort")
	wantContains(t, body, "Passwort falsch")
	if hs := dst.cfg().HMIs; len(hs) != 1 || hs[0].ID != "z" || dst.appliedCount() != 0 {
		t.Errorf("geändert: %+v, Apply %d", hs, dst.appliedCount())
	}
}

func sealedExport(t *testing.T, ex exportFile) []byte {
	t.Helper()
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := secrets.SealExport(raw, exportPW)
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

func TestImportInvalidConfigLeavesConfigUntouched(t *testing.T) {
	dst, db := newLoggedIn(t)
	dst.addHMI("z", "Eigen", "")
	base := config.Default()
	base.Client = config.ClientCfg{Profile: "remmina", Quality: 2, ColorDepth: 32}

	badHost := base
	badHost.HMIs = []config.HMI{{ID: "a", Name: "A", Host: "bad host;", Port: 5900}}
	badClient := base
	badClient.Client.Profile = "rdp"
	wrongSchema := base
	wrongSchema.SchemaVersion = config.SchemaVersion + 1

	for name, ex := range map[string]exportFile{
		"Host":   {Format: exportFormat, Config: badHost},
		"Client": {Format: exportFormat, Config: badClient},
		"Schema": {Format: exportFormat, Config: wrongSchema},
		"Format": {Format: exportFormat + 1, Config: base},
	} {
		body := importBlob(t, db, sealedExport(t, ex), exportPW)
		wantNotContains(t, body, "importiert.")
		if hs := dst.cfg().HMIs; len(hs) != 1 || hs[0].ID != "z" || dst.appliedCount() != 0 {
			t.Errorf("%s: geändert: %+v", name, hs)
		}
	}
	notJSON, _ := secrets.SealExport([]byte("kein json"), exportPW)
	if body := importBlob(t, db, notJSON, exportPW); strings.Contains(body, "importiert.") {
		t.Error("kein JSON angenommen")
	}
}

// Fremde Werte aus PasswordEnc in der Datei dürfen nicht übernommen werden.
func TestImportIgnoresSealedFieldsInFile(t *testing.T) {
	dst, db := newLoggedIn(t)
	base := config.Default()
	base.Client = config.ClientCfg{Profile: "remmina", Quality: 2, ColorDepth: 32}
	base.SharedKeyEnc = "fremd"
	base.HMIs = []config.HMI{{ID: "a", Name: "A", Host: "10.0.0.1", Port: 5900, PasswordEnc: "fremd"}}
	wantContains(t, importBlob(t, db, sealedExport(t, exportFile{Format: exportFormat, Config: base}), exportPW), "importiert")
	c := dst.cfg()
	if c.HMIs[0].PasswordEnc != "" || c.SharedKeyEnc != "" {
		t.Errorf("übernommen: %q %q", c.HMIs[0].PasswordEnc, c.SharedKeyEnc)
	}
}
