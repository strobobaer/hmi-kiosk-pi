// hmi-kiosk-helper ist der kleine root-Helfer des Kiosk-Dienstes.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"hmikiosk/internal/helper"
)

// pendingUpdate ersetzt bis Aufgabe 14 die Aktualisierung.
type pendingUpdate struct{}

var errNotYet = errors.New("Die Aktualisierung ist noch nicht verfügbar")

func (pendingUpdate) Activate(string) error { return errNotYet }
func (pendingUpdate) Rollback() error       { return errNotYet }

func main() {
	socket := flag.String("socket", "/run/hmi-kiosk/helper.sock", "Pfad des Unix-Sockets")
	uid := flag.Int("uid", -1, "Benutzerkennung des Kiosk-Dienstes (erforderlich)")
	gid := flag.Int("gid", -1, "Gruppe des Sockets, -1 für unverändert")
	bootCfg := flag.String("boot-config", "/boot/firmware/config.txt", "Pfad der config.txt")
	ntpState := flag.String("ntp-state", "/data/system/ntp-server", "Datei für den dauerhaften NTP-Server")
	flag.Parse()
	if *uid < 0 {
		log.Fatal("-uid fehlt")
	}

	e, err := helper.NewOSExec()
	if err != nil {
		log.Fatal(err)
	}
	srv := helper.NewServer(*socket, *uid, e, *bootCfg, pendingUpdate{})
	srv.SocketGID = *gid
	srv.NTPStatePath = *ntpState

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.ReapplyNTP(ctx); err != nil {
		log.Printf("NTP-Einstellung nicht wiederhergestellt: %v", err)
	}
	if err := srv.Serve(ctx); err != nil {
		log.Fatal(err)
	}
}
