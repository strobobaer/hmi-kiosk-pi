package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/rfb"
)

const probeLimit = 10 * time.Second

type option struct{ Value, Label string }

var iconLabels = map[string]string{
	"monitor": "Bildschirm", "press": "Presse", "tank": "Tank",
	"conveyor": "Förderband", "gear": "Zahnrad", "bolt": "Blitz",
}

var colorLabels = map[string]string{
	"blue": "Blau", "green": "Grün", "amber": "Gelb", "red": "Rot", "gray": "Grau", "violet": "Violett",
}

// iconOptions und colorOptions leiten sich aus den geschlossenen Mengen in
// local.go ab, damit die Kachelseite jede Auswahl darstellen kann.
func iconOptions() []option  { return closedOptions(icons, iconLabels) }
func colorOptions() []option { return closedOptions(colors, colorLabels) }

func closedOptions[V any](set map[string]V, labels map[string]string) []option {
	out := make([]option, 0, len(set))
	for k := range set {
		label := labels[k]
		if label == "" {
			label = k
		}
		out = append(out, option{k, label})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func validIcon(s string) bool  { _, ok := icons[s]; return ok }
func validColor(s string) bool { return colors[s] }

// hmiFormData enthält die Eingaben als Text: Eine ungültige Zahl soll im Formular
// stehen bleiben. Ein Passwortfeld gibt es hier bewusst nicht.
type hmiFormData struct {
	IsNew                bool
	ID                   string
	Name, Host, Port     string
	Icon, Color, Order   string
	Mode, Takeover       string
	ViewOnly, AllowWatch bool
	HasPassword          bool
	Icons, Colors        []option
}

func newHMIFormData() hmiFormData {
	return hmiFormData{IsNew: true, Port: "5900", Icon: "monitor", Color: "blue", Order: "0", Takeover: string(config.StyleConfirm), AllowWatch: true, Icons: iconOptions(), Colors: colorOptions()}
}

func hmiFormFrom(h config.HMI) hmiFormData {
	f := hmiFormData{
		ID: h.ID, Name: h.Name, Host: h.Host, Port: strconv.Itoa(h.Port),
		Icon: h.Icon, Color: h.Color, Order: strconv.Itoa(h.Order),
		Takeover: string(h.Takeover), ViewOnly: h.ViewOnly, AllowWatch: h.AllowWatch,
		HasPassword: h.PasswordEnc != "", Icons: iconOptions(), Colors: colorOptions(),
	}
	if h.Mode != nil {
		f.Mode = string(*h.Mode)
	}
	return f
}

func hmiFormFromRequest(r *http.Request, id string, isNew, hasPassword bool) hmiFormData {
	v := r.PostForm
	return hmiFormData{
		IsNew: isNew, ID: id, HasPassword: hasPassword,
		Name: strings.TrimSpace(v.Get("name")), Host: strings.TrimSpace(v.Get("host")), Port: strings.TrimSpace(v.Get("port")),
		Icon: v.Get("icon"), Color: v.Get("color"), Order: strings.TrimSpace(v.Get("order")),
		Mode: v.Get("mode"), Takeover: v.Get("takeover"),
		ViewOnly: v.Get("view_only") != "", AllowWatch: v.Get("allow_watch") != "",
		Icons: iconOptions(), Colors: colorOptions(),
	}
}

// apply überträgt das Formular auf h. Das Passwort behandelt applyPassword.
func (f hmiFormData) apply(h *config.HMI) error {
	port, err := strconv.Atoi(f.Port)
	if err != nil {
		return fmt.Errorf("Port muss eine Zahl sein: %q", f.Port)
	}
	order := 0
	if f.Order != "" {
		if order, err = strconv.Atoi(f.Order); err != nil {
			return fmt.Errorf("Reihenfolge muss eine Zahl sein: %q", f.Order)
		}
	}
	if !validIcon(f.Icon) {
		return errors.New("Unbekanntes Symbol.")
	}
	if !validColor(f.Color) {
		return errors.New("Unbekannte Farbe.")
	}
	h.Name, h.Host, h.Port = f.Name, f.Host, port
	h.Icon, h.Color, h.Order = f.Icon, f.Color, order
	h.ViewOnly, h.AllowWatch = f.ViewOnly, f.AllowWatch
	h.Takeover = config.TakeoverStyle(f.Takeover)
	h.Mode = nil
	if f.Mode != "" {
		m := config.Mode(f.Mode)
		h.Mode = &m
	}
	return nil
}

func (a *admin) applyPassword(h *config.HMI, r *http.Request) error {
	pw, remove := r.PostForm.Get("password"), r.PostForm.Get("remove_password") != ""
	switch {
	case remove && pw != "":
		return errors.New("Bitte entweder ein neues Passwort eingeben oder „Passwort entfernen“ wählen.")
	case remove:
		h.PasswordEnc = ""
	case pw != "":
		enc, err := a.d.Box.Seal(pw)
		if err != nil {
			return fmt.Errorf("Passwort konnte nicht verschlüsselt werden: %w", err)
		}
		h.PasswordEnc = enc
	}
	return nil
}

func newHMIID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// --- Liste ---

type hmiCard struct {
	ID, Name, Host        string
	Port, Order           int
	Icon, Color           string
	ViewOnly, HasPassword bool
	Probe                 *rfb.ProbeResult
}

type hmiListData struct{ HMIs []hmiCard }

func hmiCards(c config.Config, probeID string, probe *rfb.ProbeResult) []hmiCard {
	hs := slices.Clone(c.HMIs)
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].Order < hs[j].Order })
	cards := make([]hmiCard, 0, len(hs))
	for _, h := range hs {
		card := hmiCard{
			ID: h.ID, Name: h.Name, Host: h.Host, Port: h.Port, Order: h.Order,
			Icon: h.Icon, Color: h.Color, ViewOnly: h.ViewOnly, HasPassword: h.PasswordEnc != "",
		}
		if h.ID == probeID {
			card.Probe = probe
		}
		cards = append(cards, card)
	}
	return cards
}

func (a *admin) renderHMIList(w http.ResponseWriter, s sessionRef, status int, errMsg, probeID string, probe *rfb.ProbeResult) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	a.page(w, s, status, "hmis.html", "hmis", "HMIs", errMsg, hmiListData{HMIs: hmiCards(cfg, probeID, probe)})
}

func (a *admin) handleHMIList(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderHMIList(w, s, http.StatusOK, "", "", nil)
}

func (a *admin) findHMI(id string) (config.HMI, bool) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		return config.HMI{}, false
	}
	for _, h := range cfg.HMIs {
		if h.ID == id {
			return h, true
		}
	}
	return config.HMI{}, false
}

// --- Anlegen und Bearbeiten ---

func (a *admin) renderHMIForm(w http.ResponseWriter, s sessionRef, status int, errMsg string, f hmiFormData) {
	title := "HMI bearbeiten"
	if f.IsNew {
		title = "HMI anlegen"
	}
	a.page(w, s, status, "hmi_edit.html", "hmis", title, errMsg, f)
}

func (a *admin) handleHMINewForm(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderHMIForm(w, s, http.StatusOK, "", newHMIFormData())
}

func (a *admin) handleHMIEditForm(w http.ResponseWriter, r *http.Request, s sessionRef) {
	h, ok := a.findHMI(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	a.renderHMIForm(w, s, http.StatusOK, "", hmiFormFrom(h))
}

func (a *admin) handleHMICreate(w http.ResponseWriter, r *http.Request, s sessionRef) {
	f := hmiFormFromRequest(r, "", true, false)
	id, err := newHMIID()
	if err == nil {
		err = a.update(func(c *config.Config) error {
			var h config.HMI
			h.ID = id
			if err := f.apply(&h); err != nil {
				return err
			}
			if err := a.applyPassword(&h, r); err != nil {
				return err
			}
			c.HMIs = append(c.HMIs, h)
			return nil
		})
	}
	if err != nil {
		a.renderHMIForm(w, s, formStatus(err), err.Error(), f)
		return
	}
	a.done(w, r, s, "/admin/hmis", "HMI angelegt.")
}

var errNoSuchHMI = errors.New("HMI nicht gefunden")

func (a *admin) handleHMISave(w http.ResponseWriter, r *http.Request, s sessionRef) {
	id := r.PathValue("id")
	cur, ok := a.findHMI(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	f := hmiFormFromRequest(r, id, false, cur.PasswordEnc != "")
	err := a.update(func(c *config.Config) error {
		i := slices.IndexFunc(c.HMIs, func(h config.HMI) bool { return h.ID == id })
		if i < 0 {
			return errNoSuchHMI
		}
		if err := f.apply(&c.HMIs[i]); err != nil {
			return err
		}
		return a.applyPassword(&c.HMIs[i], r)
	})
	switch {
	case errors.Is(err, errNoSuchHMI):
		http.NotFound(w, r)
	case err != nil:
		a.renderHMIForm(w, s, formStatus(err), err.Error(), f)
	default:
		a.done(w, r, s, "/admin/hmis", "HMI gespeichert.")
	}
}

// --- Löschen und Prüfen ---

func (a *admin) handleHMIDelete(w http.ResponseWriter, r *http.Request, s sessionRef) {
	id := r.PathValue("id")
	err := a.update(func(c *config.Config) error {
		i := slices.IndexFunc(c.HMIs, func(h config.HMI) bool { return h.ID == id })
		if i < 0 {
			return errNoSuchHMI
		}
		if c.Kiosk.PermanentHMI == id {
			return fmt.Errorf("„%s“ ist als Daueranzeige eingestellt und kann nicht gelöscht werden. Bitte zuerst unter Kiosk die Daueranzeige ändern.", c.HMIs[i].Name)
		}
		c.HMIs = slices.Delete(c.HMIs, i, i+1)
		return nil
	})
	switch {
	case errors.Is(err, errNoSuchHMI):
		http.NotFound(w, r)
	case err != nil:
		a.renderHMIList(w, s, formStatus(err), err.Error(), "", nil)
	default:
		a.done(w, r, s, "/admin/hmis", "HMI gelöscht.")
	}
}

func (a *admin) handleHMIProbe(w http.ResponseWriter, r *http.Request, s sessionRef) {
	h, ok := a.findHMI(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeLimit)
	defer cancel()
	res := a.d.Probe(ctx, h)
	a.renderHMIList(w, s, http.StatusOK, "", h.ID, &res)
}
