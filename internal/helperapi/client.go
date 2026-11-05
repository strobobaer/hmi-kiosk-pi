// Package helperapi ist der Client des root-Helfers (erfüllt web.HelperAPI).
package helperapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"
)

const defaultTimeout = 45 * time.Second

type Client struct{ socket string }

func New(socket string) *Client { return &Client{socket: socket} }

// Call schickt einen Befehl und gibt den deutschen Fehlertext des Helfers zurück.
// Eine Verbindung pro Aufruf; die Frist von ctx gilt für den ganzen Aufruf.
func (c *Client) Call(ctx context.Context, cmd, arg string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.socket)
	if err != nil {
		return fmt.Errorf("Der Helfer ist nicht erreichbar (%v)", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	} else {
		conn.SetDeadline(time.Now().Add(defaultTimeout))
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	req, _ := json.Marshal(struct {
		Cmd string `json:"cmd"`
		Arg string `json:"arg"`
	}{cmd, arg})
	if _, err := conn.Write(append(req, '\n')); err != nil {
		return c.ioErr(ctx, err)
	}
	var resp struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(conn, 64<<10)).Decode(&resp); err != nil {
		return c.ioErr(ctx, err)
	}
	if resp.OK {
		return nil
	}
	if resp.Error == "" {
		return errors.New("Ungültige Antwort des Helfers")
	}
	return errors.New(resp.Error)
}

func (c *Client) ioErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("Der Helfer hat nicht rechtzeitig geantwortet (%v)", ctx.Err())
	}
	return fmt.Errorf("Verbindung zum Helfer gestört (%v)", err)
}
