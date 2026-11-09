package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
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

func validIcon(s string) bool  { return slices.Contains(config.HMIIcons, s) }
func validColor(s string) bool { return slices.Contains(config.HMIColors, s) }

// hmiFormData enthält die Eingaben als Text: Eine ungültige Zahl soll im Formular
// stehen bleiben. Ein Passwortfeld gibt es hier bewusst nicht.
type hmiFormData struct {
	IsNew                bool
	ID                   string
	Kind                 string // "vnc" oder "web"
	URL                  string // nur Web-Kachel
	URLExample           string // Beispiel einer Grafana-Adresse für den Hinweis
	Name, Host, Port     string
	Icon, Color, Order   string
	Mode, Takeover       string
	ViewOnly, AllowWatch bool
	HasPassword          bool
	PasswordNote         string // "lost" oder "unreadable", sonst leer
	Reach                string
	Icons, Colors        []option
	ReachOptions         []option
}

func newHMIFormData(kind string) hmiFormData {
	return hmiFormData{IsNew: true, Kind: formKind(kind), Port: "5900", Icon: "monitor", Color: "blue", Order: "0", Takeover: string(config.StyleConfirm), AllowWatch: true, Reach: config.ReachPing, Icons: iconOptions(), Colors: colorOptions(), ReachOptions: reachOptions()}
}

func hmiFormFrom(h config.HMI) hmiFormData {
	f := hmiFormData{
		Kind: formKind(h.Kind), URL: h.URL,
		ID: h.ID, Name: h.Name, Host: h.Host, Port: strconv.Itoa(h.Port),
		Icon: h.Icon, Color: h.Color, Order: strconv.Itoa(h.Order),
		Takeover: string(h.Takeover), ViewOnly: h.ViewOnly, AllowWatch: h.AllowWatch,
		HasPassword: h.PasswordEnc != "", Reach: h.ReachMode(), Icons: iconOptions(), Colors: colorOptions(), ReachOptions: reachOptions(),
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
		Kind: formKind(v.Get("kind")), URL: strings.TrimSpace(v.Get("url")),
		Name: strings.TrimSpace(v.Get("name")), Host: strings.TrimSpace(v.Get("host")), Port: strings.TrimSpace(v.Get("port")),
		Icon: v.Get("icon"), Color: v.Get("color"), Order: strings.TrimSpace(v.Get("order")),
		Mode: v.Get("mode"), Takeover: v.Get("takeover"),
		ViewOnly: v.Get("view_only") != "", AllowWatch: v.Get("allow_watch") != "", Reach: v.Get("reach"),
		Icons: iconOptions(), Colors: colorOptions(), ReachOptions: reachOptions(),
	}
}

// apply überträgt das Formular auf h. Das Passwort behandelt applyPassword.
func (f hmiFormData) apply(h *config.HMI) error {
	if !validIcon(f.Icon) {
		return errors.New("Unbekanntes Symbol.")
	}
	if !validColor(f.Color) {
		return errors.New("Unbekannte Farbe.")
	}
	order, err := parseOrder(f.Order)
	if err != nil {
		return err
	}
	if f.Kind == config.KindWeb {
		return f.applyWeb(h, order)
	}
	port, err := strconv.Atoi(f.Port)
	if err != nil {
		return fmt.Errorf("Port muss eine Zahl sein: %q", f.Port)
	}
	h.Kind, h.URL = "", ""
	h.Name, h.Host, h.Port = f.Name, f.Host, port
	h.Icon, h.Color, h.Order = f.Icon, f.Color, order
	h.ViewOnly, h.AllowWatch = f.ViewOnly, f.AllowWatch
	h.Reach = f.Reach
	h.Takeover = config.TakeoverStyle(f.Takeover)
	h.Mode = nil
	if f.Mode != "" {
		m := config.Mode(f.Mode)
		h.Mode = &m
	}
	return nil
}

func (a *admin) applyPassword(h *config.HMI, r *http.Request) error {
	if h.IsWeb() {
		return nil // eine Webseite hat kein VNC-Passwort; die Anmeldung bleibt im Web-Profil
	}
	pw, remove := r.PostForm.Get("password"), r.PostForm.Get("remove_password") != ""
	switch {
	case remove && pw != "":
		return errors.New("Bitte entweder ein neues Passwort eingeben oder „Passwort entfernen“ wählen.")
	case remove:
		h.PasswordEnc, h.PasswordLost, h.AuthFailed = "", false, false
	case pw != "":
		enc, err := a.d.Box.Seal(pw)
		if err != nil {
			return fmt.Errorf("Passwort konnte nicht verschlüsselt werden: %w", err)
		}
		h.PasswordEnc, h.PasswordLost, h.AuthFailed = enc, false, false
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
	Web                   bool // Web-Kachel: URL statt Host und Port
	URL                   string
	Port, Order           int
	Icon, Color           string
	ViewOnly, HasPassword bool
	PasswordNote          string // "lost" oder "unreadable", sonst leer
	AuthFailed            bool   // der Server hat das Passwort abgelehnt
	LockoutHint           string // nur nach einer Prüfung, die in die Sperre des Servers lief
	Probe                 *rfb.ProbeResult
}

type hmiListData struct {
	HMIs       []hmiCard
	Unreadable bool // mindestens ein Passwort ist mit dem Geräteschlüssel nicht lesbar
}

// passwordNote nennt, warum ein Passwort nicht nutzbar ist: verloren (Reparatur) oder unlesbar (falscher Schlüssel).
func (a *admin) passwordNote(h config.HMI) string {
	switch {
	case h.PasswordEnc == "" && h.PasswordLost:
		return "lost"
	case h.PasswordEnc != "":
		if _, err := a.d.Box.Open(h.PasswordEnc); err != nil {
			return "unreadable"
		}
	}
	return ""
}

func hmiCards(c config.Config, probeID string, probe *rfb.ProbeResult) []hmiCard {
	hs := slices.Clone(c.HMIs)
	sort.SliceStable(hs, func(i, j int) bool { return hs[i].Order < hs[j].Order })
	cards := make([]hmiCard, 0, len(hs))
	for _, h := range hs {
		card := hmiCard{
			ID: h.ID, Name: h.Name, Host: h.Host, Port: h.Port, Order: h.Order,
			Icon: h.Icon, Color: h.Color, ViewOnly: h.ViewOnly, HasPassword: h.PasswordEnc != "",
		}
		card.Web, card.URL = h.IsWeb(), h.URL
		card.AuthFailed = h.AuthFailed
		if h.ID == probeID {
			card.Probe = probe
			if probe != nil && probe.Locked {
				card.LockoutHint = rfb.LockoutHint
			}
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
	data := hmiListData{HMIs: hmiCards(cfg, probeID, probe)}
	for i, card := range data.HMIs {
		if h, ok := findIn(cfg, card.ID); ok {
			data.HMIs[i].PasswordNote = a.passwordNote(h)
			data.Unreadable = data.Unreadable || data.HMIs[i].PasswordNote == "unreadable"
		}
	}
	a.page(w, s, status, "hmis.html", "hmis", "HMIs", errMsg, data)
}

func (a *admin) handleHMIList(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	a.renderHMIList(w, s, http.StatusOK, "", "", nil)
}

func findIn(cfg config.Config, id string) (config.HMI, bool) {
	for _, h := range cfg.HMIs {
		if h.ID == id {
			return h, true
		}
	}
	return config.HMI{}, false
}

func (a *admin) findHMI(id string) (config.HMI, bool) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		return config.HMI{}, false
	}
	return findIn(cfg, id)
}

// --- Anlegen und Bearbeiten ---

func (a *admin) renderHMIForm(w http.ResponseWriter, s sessionRef, status int, errMsg string, f hmiFormData) {
	f.URL = withoutUserinfo(f.URL)
	f.URLExample = grafanaExample
	title := "HMI bearbeiten"
	if f.IsNew {
		title = "HMI anlegen"
	} else if h, ok := a.findHMI(f.ID); ok {
		f.PasswordNote = a.passwordNote(h)
	}
	a.page(w, s, status, "hmi_edit.html", "hmis", title, errMsg, f)
}

func (a *admin) handleHMINewForm(w http.ResponseWriter, r *http.Request, s sessionRef) {
	a.renderHMIForm(w, s, http.StatusOK, "", newHMIFormData(r.URL.Query().Get("kind")))
}

func (a *admin) handleHMIEditForm(w http.ResponseWriter, r *http.Request, s sessionRef) {
	h, ok := a.findHMI(r.PathValue("id"))
	if !ok {
		notFound(w)
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
		notFound(w)
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
		notFound(w)
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
		notFound(w)
	case err != nil:
		a.renderHMIList(w, s, formStatus(err), err.Error(), "", nil)
	default:
		a.done(w, r, s, "/admin/hmis", "HMI gelöscht.")
	}
}

func (a *admin) handleHMIProbe(w http.ResponseWriter, r *http.Request, s sessionRef) {
	h, ok := a.findHMI(r.PathValue("id"))
	if !ok {
		notFound(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeLimit)
	defer cancel()
	res := a.d.Probe(ctx, h)
	var errMsg string
	if res.AuthOK && h.AuthFailed {
		// Das Passwort stimmt wieder: Antippen und Daueranzeige dürfen anmelden.
		if err := a.update(clearAuthFailed(h.ID)); err != nil {
			errMsg = "Die Markierung „Passwort abgelehnt“ konnte nicht aufgehoben werden: " + err.Error()
		}
	}
	a.renderHMIList(w, s, http.StatusOK, errMsg, h.ID, &res)
}

func clearAuthFailed(id string) func(*config.Config) error {
	return func(c *config.Config) error {
		if i := slices.IndexFunc(c.HMIs, func(h config.HMI) bool { return h.ID == id }); i >= 0 {
			c.HMIs[i].AuthFailed = false
		}
		return nil
	}
}

// reachOptions sind die Prüfarten der Erreichbarkeit; Ping ist der Standard.
func reachOptions() []option {
	return []option{
		{config.ReachPing, "Ping (Standard)"},
		{config.ReachPort, "Port (VNC-Port bzw. Port der Webseite)"},
		{config.ReachOff, "aus"},
	}
}

// formKind: alles außer "web" ist VNC (auch alte Formulare ohne das Feld).
func formKind(k string) string {
	if k == config.KindWeb {
		return config.KindWeb
	}
	return config.KindVNC
}

func parseOrder(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("Reihenfolge muss eine Zahl sein: %q", s)
	}
	return n, nil
}

// applyWeb setzt eine Web-Kachel: nur Name, Adresse, Symbol, Farbe, Reihenfolge und
// Erreichbarkeit. Die VNC-Felder (auch das Passwort) werden geleert.
func (f hmiFormData) applyWeb(h *config.HMI, order int) error {
	if _, err := config.ParseWebURL(f.URL); err != nil {
		return err
	}
	*h = config.HMI{
		ID: h.ID, Name: f.Name, Kind: config.KindWeb, URL: f.URL,
		Icon: f.Icon, Color: f.Color, Order: order, Reach: f.Reach,
	}
	return nil
}

const grafanaExample = "http://grafana:3000/d/abc/linie-1?orgId=1&kiosk"

// withoutUserinfo entfernt Benutzer und Passwort aus einer abgelehnten Adresse, damit sie
// nicht im Formular (und in der Antwort) stehen bleiben.
func withoutUserinfo(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	u.User = nil
	return u.String()
}
