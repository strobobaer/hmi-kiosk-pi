package app

import (
	"context"
	"log"
	"net"
	"os"
	"strconv"
	"time"
)

const watchdogProbeLimit = 2 * time.Second

// notifier spricht das sd_notify-Protokoll ohne Bibliothek. Ein Name mit
// führendem „@“ ist eine abstrakte Adresse; net.UnixAddr setzt sie um.
type notifier struct{ socket string }

func notifierFromEnv() notifier { return notifier{socket: os.Getenv("NOTIFY_SOCKET")} }

func (n notifier) send(state string) error {
	if n.socket == "" {
		return nil
	}
	c, err := net.DialUnix("unixgram", nil, &net.UnixAddr{Name: n.socket, Net: "unixgram"})
	if err != nil {
		return err
	}
	defer c.Close()
	if err := c.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	_, err = c.Write([]byte(state))
	return err
}

// watchdogInterval liefert den halben WATCHDOG_USEC-Takt oder 0, wenn der
// Watchdog nicht aktiv ist oder einem anderen Prozess gilt.
func watchdogInterval(getenv func(string) string, pid int) time.Duration {
	usec, err := strconv.ParseInt(getenv("WATCHDOG_USEC"), 10, 64)
	if err != nil || usec <= 0 {
		return 0
	}
	if p := getenv("WATCHDOG_PID"); p != "" && p != strconv.Itoa(pid) {
		return 0
	}
	return time.Duration(usec) * time.Microsecond / 2
}

// runWatchdog meldet sich nur, wenn probe rechtzeitig antwortet: Hängt der
// Sitzungsautomat, soll systemd den Dienst neu starten.
func runWatchdog(ctx context.Context, n notifier, every time.Duration, probe func(context.Context) error) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		pctx, cancel := context.WithTimeout(ctx, watchdogProbeLimit)
		err := probe(pctx)
		cancel()
		if err != nil {
			if ctx.Err() == nil {
				log.Printf("Watchdog: Sitzungsverwaltung antwortet nicht: %v", err)
			}
			continue
		}
		if err := n.send("WATCHDOG=1"); err != nil {
			log.Printf("Watchdog: Meldung nicht zugestellt: %v", err)
		}
	}
}
