package app

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func listenDatagram(t *testing.T, name string) *net.UnixConn {
	t.Helper()
	c, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: name, Net: "unixgram"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readDatagram(t *testing.T, c *net.UnixConn, limit time.Duration) string {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(limit))
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("keine Meldung: %v", err)
	}
	return string(buf[:n])
}

func TestNotifyPathAndAbstractSocket(t *testing.T) {
	for _, name := range []string{
		filepath.Join(t.TempDir(), "notify.sock"),
		"@hmi-kiosk-test-" + strconv.FormatInt(time.Now().UnixNano(), 36),
	} {
		c := listenDatagram(t, name)
		if err := (notifier{socket: name}).send("READY=1"); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := readDatagram(t, c, 5*time.Second); got != "READY=1" {
			t.Fatalf("%s: %q", name, got)
		}
	}
}

func TestNotifyWithoutSocketDoesNothing(t *testing.T) {
	if err := (notifier{}).send("READY=1"); err != nil {
		t.Fatal(err)
	}
}

func TestWatchdogInterval(t *testing.T) {
	env := func(usec, pid string) func(string) string {
		return func(k string) string {
			switch k {
			case "WATCHDOG_USEC":
				return usec
			case "WATCHDOG_PID":
				return pid
			}
			return ""
		}
	}
	cases := []struct {
		usec, pid string
		want      time.Duration
	}{
		{"", "", 0},
		{"abc", "", 0},
		{"0", "", 0},
		{"-5", "", 0},
		{"20000000", "", 10 * time.Second},
		{"20000000", "42", 10 * time.Second},
		{"20000000", "43", 0},
	}
	for _, c := range cases {
		if got := watchdogInterval(env(c.usec, c.pid), 42); got != c.want {
			t.Errorf("usec=%q pid=%q: %v", c.usec, c.pid, got)
		}
	}
}

func TestWatchdogOnlyWhenProbeAnswers(t *testing.T) {
	name := filepath.Join(t.TempDir(), "notify.sock")
	c := listenDatagram(t, name)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	healthy := make(chan bool, 1)
	probed := make(chan struct{}, 16)
	current := false
	done := make(chan struct{})
	go func() {
		defer close(done)
		runWatchdog(ctx, notifier{socket: name}, 5*time.Millisecond, func(context.Context) error {
			select {
			case current = <-healthy:
			default:
			}
			select {
			case probed <- struct{}{}:
			default:
			}
			if !current {
				return errors.New("hängt")
			}
			return nil
		})
	}()
	for range 3 {
		<-probed
	}
	c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
	if n, err := c.Read(make([]byte, 64)); err == nil {
		t.Fatalf("Meldung trotz hängender Prüfung (%d Byte)", n)
	}
	healthy <- true
	if got := readDatagram(t, c, 5*time.Second); got != "WATCHDOG=1" {
		t.Fatalf("Meldung %q", got)
	}
	cancel()
	<-done
}

func TestWatchdogProbeHasTwoSecondLimit(t *testing.T) {
	if watchdogProbeLimit != 2*time.Second {
		t.Fatalf("Frist %v", watchdogProbeLimit)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	got := make(chan time.Duration, 1)
	go runWatchdog(ctx, notifier{}, time.Millisecond, func(ctx context.Context) error {
		dl, ok := ctx.Deadline()
		if !ok {
			dl = time.Time{}
		}
		select {
		case got <- time.Until(dl):
		default:
		}
		return nil
	})
	if d := <-got; d <= 0 || d > watchdogProbeLimit {
		t.Fatalf("Frist %v", d)
	}
}
