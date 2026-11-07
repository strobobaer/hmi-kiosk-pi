package helperapi

import (
	"context"
	"encoding/json"
	"errors"

	"hmikiosk/internal/config"
)

// Befehle der Ethernet-Einstellung (Protokoll zum Helfer).
const (
	CmdNetApply   = "net-apply"
	CmdNetConfirm = "net-confirm"
	CmdNetRevert  = "net-revert"
	CmdNetStatus  = "net-status"
)

// NetDeadlineSec ist die Rückfallfrist nach net-apply.
const NetDeadlineSec = 120

// NetStatus ist der Stand der Ethernet-Schnittstelle und einer offenen Rückfallfrist.
type NetStatus struct {
	Iface     string   `json:"iface,omitempty"`
	Addresses []string `json:"addresses,omitempty"` // IPv4 mit Präfix, z. B. 192.168.1.10/24
	// Pending: Eine neue Einstellung ist aktiv und wartet auf Bestätigung.
	Pending      bool               `json:"pending,omitempty"`
	PendingCfg   config.EthernetCfg `json:"pendingCfg"`
	RemainingSec int                `json:"remainingSec,omitempty"`
	// Message ist die letzte Meldung des Helfers (z. B. nach einer Rücksetzung).
	Message       string `json:"message,omitempty"`
	NMMissing     bool   `json:"nmMissing,omitempty"`
	ArpingMissing bool   `json:"arpingMissing,omitempty"`
	Error         string `json:"error,omitempty"` // Schnittstelle oder Adressen nicht lesbar
}

// NetStatus fragt den Stand der Ethernet-Einstellung ab.
func (c *Client) NetStatus(ctx context.Context) (NetStatus, error) {
	var st NetStatus
	raw, err := c.callData(ctx, CmdNetStatus)
	if err != nil {
		return st, err
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return st, errors.New("Ungültige Antwort des Helfers")
	}
	return st, nil
}

// NetApply setzt eine neue Ethernet-Einstellung mit Rückfallfrist.
func (c *Client) NetApply(ctx context.Context, e config.EthernetCfg) error {
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = c.roundTrip(ctx, CmdNetApply, string(b))
	return err
}

// NetConfirm bestätigt die offene Einstellung und liefert sie zurück.
func (c *Client) NetConfirm(ctx context.Context) (config.EthernetCfg, error) {
	var e config.EthernetCfg
	raw, err := c.callData(ctx, CmdNetConfirm)
	if err != nil {
		return e, err
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, errors.New("Ungültige Antwort des Helfers")
	}
	return e, nil
}

// NetRevert stellt die alte Einstellung sofort wieder her.
func (c *Client) NetRevert(ctx context.Context) error {
	_, err := c.roundTrip(ctx, CmdNetRevert, "")
	return err
}
