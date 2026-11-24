package tailnet

import (
	"context"
	"encoding/json"
	"errors"

	"hmikiosk/internal/helperapi"
)

// HelperCaller ist der Teil des Helfer-Clients (helperapi.Client), den die Steuerung braucht.
type HelperCaller interface {
	Call(ctx context.Context, cmd, arg string) error
	CallData(ctx context.Context, cmd, arg string) (json.RawMessage, error)
}

type helperCtl struct{ h HelperCaller }

// NewHelper steuert Tailscale über den root-Helfer (Pi).
func NewHelper(h HelperCaller) Ctl { return &helperCtl{h: h} }

func (c *helperCtl) Status(ctx context.Context) (Status, error) {
	raw, err := c.h.CallData(ctx, helperapi.CmdTailnetStatus, "")
	if err != nil {
		return Status{}, err
	}
	return ParseStatus(raw)
}

// Up schickt Key und Hostname als JSON an den Helfer. Die Prüfung läuft hier und noch einmal im Helfer.
func (c *helperCtl) Up(ctx context.Context, authKey, hostname string) error {
	if err := ValidateKey(authKey); err != nil {
		return err
	}
	if !ValidHostname(hostname) {
		return errors.New("Der Hostname ist ungültig.")
	}
	arg, err := json.Marshal(struct {
		Key  string `json:"key"`
		Host string `json:"host"`
	}{authKey, hostname})
	if err != nil {
		return errors.New("Anmeldedaten nicht verpackbar")
	}
	return redact(c.h.Call(ctx, helperapi.CmdTailnetUp, string(arg)), authKey)
}

func (c *helperCtl) Down(ctx context.Context) error {
	return c.h.Call(ctx, helperapi.CmdTailnetDown, "")
}
