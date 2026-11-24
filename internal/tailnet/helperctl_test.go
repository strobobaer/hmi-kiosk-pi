package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type fakeHelper struct {
	cmd, arg string
	data     string
	err      error
}

func (f *fakeHelper) Call(_ context.Context, cmd, arg string) error {
	f.cmd, f.arg = cmd, arg
	return f.err
}

func (f *fakeHelper) CallData(_ context.Context, cmd, arg string) (json.RawMessage, error) {
	f.cmd, f.arg = cmd, arg
	return json.RawMessage(f.data), f.err
}

func TestHelperCtlUp(t *testing.T) {
	h := &fakeHelper{}
	if err := NewHelper(h).Up(context.Background(), secretKey, "halle-3"); err != nil {
		t.Fatal(err)
	}
	if h.cmd != "tailnet-up" {
		t.Fatal(h.cmd)
	}
	var v map[string]string
	if err := json.Unmarshal([]byte(h.arg), &v); err != nil || v["key"] != secretKey || v["host"] != "halle-3" || len(v) != 2 {
		t.Fatalf("Wert: %q %v", h.arg, err)
	}
	h.err = errors.New("tailscale fehlgeschlagen: " + secretKey)
	if err := NewHelper(h).Up(context.Background(), secretKey, "x"); err == nil || strings.Contains(err.Error(), secretKey) {
		t.Fatalf("Fehler: %v", err)
	}
	for _, k := range []string{"", "a\nb"} {
		h.cmd = ""
		if err := NewHelper(h).Up(context.Background(), k, "x"); err == nil || h.cmd != "" {
			t.Errorf("Schlüssel %q ging zum Helfer", k)
		}
	}
}

func TestHelperCtlStatusAndDown(t *testing.T) {
	h := &fakeHelper{data: `{"installed":false}`}
	c := NewHelper(h)
	st, err := c.Status(context.Background())
	if err != nil || st.Installed || h.cmd != "tailnet-status" {
		t.Fatalf("%+v %v %s", st, err, h.cmd)
	}
	h.data = statusRunning
	if st, _ = c.Status(context.Background()); !st.LoggedIn {
		t.Fatalf("%+v", st)
	}
	if err := c.Down(context.Background()); err != nil || h.cmd != "tailnet-down" || h.arg != "" {
		t.Fatalf("%v %s %q", err, h.cmd, h.arg)
	}
}
