package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/helperapi"
)

// NetAPI stellt die Ethernet-Schnittstelle über den Helfer um (erfüllt von helperapi.Client).
type NetAPI interface {
	NetStatus(ctx context.Context) (helperapi.NetStatus, error)
	NetApply(ctx context.Context, e config.EthernetCfg) error
	NetConfirm(ctx context.Context) (config.EthernetCfg, error)
	NetRevert(ctx context.Context) error
}

const (
	netStatusTimeout = 5 * time.Second
	ethRefreshSec    = 10
	adminPort        = "8443"
)

// ethForm sind die Werte des Formulars, so wie sie eingegeben wurden.
type ethForm struct {
	Method, Address, Prefix, Gateway, DNS1, DNS2 string
}

type ethView struct {
	Available  bool   // Helfer-Anbindung vorhanden
	StatusErr  string // Stand nicht abrufbar
	Iface      string
	Addresses  []string
	Saved      string // gespeicherte (bestätigte) Einstellung in Worten
	Form       ethForm
	Pending    bool
	PendingAs  string // neue Einstellung in Worten
	Remaining  string // m:ss
	ConfirmAt  string // Adresse, unter der zu bestätigen ist (leer bei DHCP)
	Message    string
	NMMissing  bool
	ArpMissing bool
}

func describeEthernet(e config.EthernetCfg) string {
	switch e.Method {
	case config.EthDHCP:
		return "Automatisch (DHCP)"
	case config.EthStatic:
		s := fmt.Sprintf("Feste Adresse %s/%d", e.Address, e.Prefix)
		if e.Gateway != "" {
			s += ", Gateway " + e.Gateway
		}
		if len(e.DNS) > 0 {
			s += ", DNS " + strings.Join(e.DNS, ", ")
		}
		return s
	}
	return "nicht von dieser Seite verwaltet (Einstellung des Systems)"
}

func formOf(e config.EthernetCfg) ethForm {
	f := ethForm{Method: e.Method, Address: e.Address, Gateway: e.Gateway, Prefix: strconv.Itoa(config.DefaultEthPrefix)}
	if f.Method == "" {
		f.Method = config.EthDHCP
	}
	if e.Prefix != 0 {
		f.Prefix = strconv.Itoa(e.Prefix)
	}
	if len(e.DNS) > 0 {
		f.DNS1 = e.DNS[0]
	}
	if len(e.DNS) > 1 {
		f.DNS2 = e.DNS[1]
	}
	return f
}

func (a *admin) ethernetView(saved config.EthernetCfg, override *ethForm) ethView {
	v := ethView{Saved: describeEthernet(saved), Form: formOf(saved)}
	if override != nil {
		v.Form = *override
	}
	if a.d.Net == nil {
		return v
	}
	v.Available = true
	ctx, cancel := context.WithTimeout(context.Background(), netStatusTimeout)
	defer cancel()
	st, err := a.d.Net.NetStatus(ctx)
	if err != nil {
		v.StatusErr = err.Error()
		return v
	}
	v.Iface, v.Addresses, v.Message, v.NMMissing, v.ArpMissing = st.Iface, st.Addresses, st.Message, st.NMMissing, st.ArpingMissing
	if st.Error != "" {
		v.StatusErr = st.Error
	}
	if st.Pending {
		v.Pending = true
		v.PendingAs = describeEthernet(st.PendingCfg)
		v.Remaining = fmt.Sprintf("%d:%02d", st.RemainingSec/60, st.RemainingSec%60)
		if st.PendingCfg.Method == config.EthStatic {
			v.ConfirmAt = st.PendingCfg.Address
		}
	}
	return v
}

// parseEthForm baut die Einstellung aus dem Formular. Bei DHCP zählen die übrigen Felder nicht.
func parseEthForm(f ethForm) (config.EthernetCfg, error) {
	switch f.Method {
	case config.EthDHCP:
		return config.EthernetCfg{Method: config.EthDHCP}, nil
	case config.EthStatic:
	default:
		return config.EthernetCfg{}, errors.New("Bitte DHCP oder eine feste Adresse wählen.")
	}
	prefix, err := config.ParsePrefix(f.Prefix)
	if err != nil {
		return config.EthernetCfg{}, err
	}
	e := config.EthernetCfg{Method: config.EthStatic, Address: f.Address, Prefix: prefix, Gateway: f.Gateway}
	for _, d := range []string{f.DNS1, f.DNS2} {
		if d != "" {
			e.DNS = append(e.DNS, d)
		}
	}
	return e, config.ValidateEthernet(e)
}

func (a *admin) handleEthernetApply(w http.ResponseWriter, r *http.Request, s sessionRef) {
	v := r.PostForm
	f := ethForm{
		Method: v.Get("method"), Address: strings.TrimSpace(v.Get("address")), Prefix: strings.TrimSpace(v.Get("prefix")),
		Gateway: strings.TrimSpace(v.Get("gateway")), DNS1: strings.TrimSpace(v.Get("dns1")), DNS2: strings.TrimSpace(v.Get("dns2")),
	}
	e, err := parseEthForm(f)
	if err != nil {
		a.renderNetworkEth(w, s, http.StatusUnprocessableEntity, err.Error(), nil, &f)
		return
	}
	if a.d.Net == nil {
		a.renderNetworkEth(w, s, http.StatusServiceUnavailable, "Die Ethernet-Einstellung ist hier nicht verfügbar.", nil, &f)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), helperLimit)
	defer cancel()
	if err := a.d.Net.NetApply(ctx, e); err != nil {
		a.renderNetworkEth(w, s, http.StatusConflict, "Die Einstellung wurde nicht übernommen: "+err.Error(), nil, &f)
		return
	}
	mins := helperapi.NetDeadlineSec / 60
	msg := fmt.Sprintf("Neue Einstellung aktiv. Bitte innerhalb von %d Minuten unter https://%s:%s/admin/network anmelden und bestätigen, sonst wird die alte Einstellung wiederhergestellt.", mins, e.Address, adminPort)
	if e.Method == config.EthDHCP {
		msg = fmt.Sprintf("Neue Einstellung (DHCP) aktiv. Die Adresse vergibt der DHCP-Server (dort nachsehen); bitte innerhalb von %d Minuten unter https://<neue Adresse>:%s/admin/network anmelden und bestätigen, sonst wird die alte Einstellung wiederhergestellt. Bleibt die Adresse gleich, genügt das Bestätigen hier.", mins, adminPort)
	}
	// Direkt antworten statt umzuleiten: Die alte Adresse verschwindet in wenigen Sekunden.
	a.sessions.setFlash(s, msg)
	a.renderNetworkEth(w, s, http.StatusOK, "", nil, nil)
}

func (a *admin) handleEthernetConfirm(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Net == nil {
		a.renderNetwork(w, s, http.StatusServiceUnavailable, "Die Ethernet-Einstellung ist hier nicht verfügbar.", nil)
		return
	}
	a.cfgMu.Lock()
	ctx, cancel := context.WithTimeout(r.Context(), helperLimit)
	e, err := a.d.Net.NetConfirm(ctx)
	cancel()
	if err == nil {
		err = a.updateLocked(func(c *config.Config) error {
			c.Network.Ethernet = e
			return nil
		})
		if err != nil {
			err = fmt.Errorf("Die Einstellung ist am Gerät bestätigt und bleibt nach dem Neustart, wurde aber nicht in der Konfiguration gespeichert (%v). Diese Seite zeigt deshalb den alten Stand.", unwrapSave(err))
		}
	} else {
		err = fmt.Errorf("Bestätigen fehlgeschlagen: %v", err)
	}
	a.cfgMu.Unlock()
	if err != nil {
		a.renderNetwork(w, s, http.StatusConflict, err.Error(), nil)
		return
	}
	a.done(w, r, s, "/admin/network", "Netzwerkeinstellung bestätigt und gespeichert: "+describeEthernet(e)+".")
}

func unwrapSave(err error) error {
	var sf saveFailed
	if errors.As(err, &sf) {
		return sf.err
	}
	if errors.Is(err, errConfigUnreadable) {
		return errors.New("Konfiguration nicht lesbar")
	}
	return err
}

func (a *admin) handleEthernetRevert(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.Net == nil {
		a.renderNetwork(w, s, http.StatusServiceUnavailable, "Die Ethernet-Einstellung ist hier nicht verfügbar.", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), helperLimit)
	defer cancel()
	if err := a.d.Net.NetRevert(ctx); err != nil {
		a.renderNetwork(w, s, http.StatusConflict, "Zurücksetzen fehlgeschlagen: "+err.Error(), nil)
		return
	}
	a.done(w, r, s, "/admin/network", "Die alte Netzwerkeinstellung wurde wiederhergestellt.")
}
