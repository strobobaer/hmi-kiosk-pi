package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"hmikiosk/internal/config"
	"hmikiosk/internal/secrets"
)

const exportFormat = 1

// exportFile ist der Klartext innerhalb der verschlüsselten Exportdatei. Die Box
// hat auf jedem Gerät einen eigenen Schlüssel; versiegelte Werte wären anderswo
// nutzlos. Deshalb stehen die Geheimnisse hier entschlüsselt in eigenen Feldern,
// und die *Enc-Felder bleiben leer.
type exportFile struct {
	Format       int               `json:"format"`
	Config       config.Config     `json:"config"`
	SharedKey    string            `json:"sharedKey,omitempty"`
	HMIPasswords map[string]string `json:"hmiPasswords,omitempty"`
}

func (a *admin) buildExport(c config.Config) (exportFile, error) {
	ex := exportFile{Format: exportFormat, HMIPasswords: map[string]string{}}
	if c.SharedKeyEnc != "" {
		key, err := a.d.Box.Open(c.SharedKeyEnc)
		if err != nil {
			return ex, errors.New("Der gemeinsame Schlüssel ist nicht lesbar, der Export wurde abgebrochen.")
		}
		ex.SharedKey = key
	}
	c.SharedKeyEnc = ""
	c.Admin = config.AdminCfg{}
	hmis := make([]config.HMI, len(c.HMIs))
	copy(hmis, c.HMIs)
	for i := range hmis {
		if hmis[i].PasswordEnc == "" {
			continue
		}
		pw, err := a.d.Box.Open(hmis[i].PasswordEnc)
		if err != nil {
			return ex, fmt.Errorf("Das VNC-Passwort von „%s“ ist nicht lesbar, der Export wurde abgebrochen.", hmis[i].Name)
		}
		ex.HMIPasswords[hmis[i].ID] = pw
		hmis[i].PasswordEnc = ""
	}
	c.HMIs = hmis
	ex.Config = c
	return ex, nil
}

// exportFileName lässt aus dem Terminalnamen nur Buchstaben, Ziffern, Punkt,
// Minus und Unterstrich stehen.
func exportFileName(name string) string {
	clean := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, name)
	return "hmi-kiosk-" + strings.Trim(clean, ".") + ".hkx"
}

func (a *admin) handleExport(w http.ResponseWriter, r *http.Request, s sessionRef) {
	pw := r.PostForm.Get("password")
	if err := checkNewPassword(pw, r.PostForm.Get("password2")); err != nil {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Export: "+err.Error(), systemExtras{})
		return
	}
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, "Konfiguration nicht lesbar.", systemExtras{})
		return
	}
	ex, err := a.buildExport(cfg)
	if err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, err.Error(), systemExtras{})
		return
	}
	raw, err := json.Marshal(ex)
	if err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, "Export fehlgeschlagen.", systemExtras{})
		return
	}
	if !a.acquireAuth(r.Context()) {
		return
	}
	blob, err := secrets.SealExport(raw, pw)
	a.releaseAuth()
	if err != nil {
		a.renderSystem(w, s, http.StatusInternalServerError, "Export fehlgeschlagen.", systemExtras{})
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.Itoa(len(blob)))
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": exportFileName(cfg.Terminal.Name)}))
	_, _ = w.Write(blob)
}

// merge baut die neue Konfiguration: alles aus der Datei außer dem, was zu diesem
// Gerät gehört. Das Netzwerk bleibt lokal, weil WLAN, Bluetooth und NTP über den
// Helfer wirken und der Import ihn nicht ruft; die Anzeige müsste sonst lügen.
func (a *admin) merge(ex exportFile, cur config.Config) (config.Config, error) {
	next := ex.Config
	next.SchemaVersion = cur.SchemaVersion
	next.Terminal = cur.Terminal
	next.Admin = cur.Admin
	next.Network = cur.Network

	// Beim Übertragen auf ein Nachbarterminal steht dieses selbst in der Liste.
	peers := make([]config.Peer, 0, len(next.Peers))
	for _, p := range next.Peers {
		if p.Name != cur.Terminal.Name {
			peers = append(peers, p)
		}
	}
	next.Peers = peers

	next.SharedKeyEnc = ""
	if ex.SharedKey != "" {
		enc, err := a.d.Box.Seal(ex.SharedKey)
		if err != nil {
			return next, err
		}
		next.SharedKeyEnc = enc
	}
	for i := range next.HMIs {
		h := &next.HMIs[i]
		h.PasswordEnc = ""
		if pw := ex.HMIPasswords[h.ID]; pw != "" {
			enc, err := a.d.Box.Seal(pw)
			if err != nil {
				return next, err
			}
			h.PasswordEnc = enc
		}
		if h.Icon != "" && !validIcon(h.Icon) {
			return next, fmt.Errorf("HMI „%s“: unbekanntes Symbol", h.Name)
		}
		if h.Color != "" && !validColor(h.Color) {
			return next, fmt.Errorf("HMI „%s“: unbekannte Farbe", h.Name)
		}
	}
	for _, check := range []error{checkClient(next.Client), checkKiosk(next.Kiosk), checkDisplay(next.Display)} {
		if check != nil {
			return next, check
		}
	}
	return next, nil
}

func (a *admin) handleImport(w http.ResponseWriter, r *http.Request, s sessionRef, mr *multipart.Reader) {
	fields, blob, err := readUpload(mr)
	if err != nil {
		uploadError(w, err)
		return
	}
	fail := func(status int, msg string) {
		a.renderSystem(w, s, status, "Import fehlgeschlagen: "+msg, systemExtras{})
	}
	if len(blob) == 0 {
		fail(http.StatusUnprocessableEntity, "Bitte eine Exportdatei auswählen.")
		return
	}
	if !a.acquireAuth(r.Context()) {
		return
	}
	plain, err := secrets.OpenExport(blob, fields["password"])
	a.releaseAuth()
	if err != nil {
		fail(http.StatusUnprocessableEntity, err.Error()+".")
		return
	}
	var ex exportFile
	if err := json.Unmarshal(plain, &ex); err != nil {
		fail(http.StatusUnprocessableEntity, "Die Datei enthält keine gültige Konfiguration.")
		return
	}
	if ex.Format != exportFormat || ex.Config.SchemaVersion != config.SchemaVersion {
		fail(http.StatusUnprocessableEntity, "Die Datei stammt aus einer nicht passenden Programmversion.")
		return
	}
	err = a.update(func(c *config.Config) error {
		next, err := a.merge(ex, *c)
		if err != nil {
			return err
		}
		*c = next
		return nil
	})
	if err != nil {
		fail(formStatus(err), err.Error())
		return
	}
	a.done(w, r, s, "/admin/system", "Konfiguration importiert. Name, Kennung, Rolle, Admin-Passwort und Netzwerk blieben unverändert.")
}
