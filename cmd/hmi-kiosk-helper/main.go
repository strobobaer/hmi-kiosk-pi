// hmi-kiosk-helper ist der kleine root-Helfer des Kiosk-Dienstes.
//
// Ohne Unterbefehl bedient er den Socket. Mit "update-guard" prüft er nach einer
// Aktivierung, ob die neue Version gesund meldet, und schaltet sonst zurück.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hmikiosk/internal/helper"
	"hmikiosk/internal/update"
)

const (
	defaultRoot   = "/data"
	defaultMarker = "/run/hmi-kiosk/healthy"
	guardTimeout  = 60 * time.Second
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "update-guard" {
		guardMain(os.Args[2:])
		return
	}
	serveMain()
}

func serveMain() {
	socket := flag.String("socket", "/run/hmi-kiosk-helper/helper.sock", "Pfad des Unix-Sockets")
	uid := flag.Int("uid", -1, "Benutzerkennung des Kiosk-Dienstes (erforderlich)")
	gid := flag.Int("gid", -1, "Gruppe des Sockets, -1 für unverändert")
	bootCfg := flag.String("boot-config", "/boot/firmware/config.txt", "Pfad der config.txt")
	ntpState := flag.String("ntp-state", "/data/system/ntp-server", "Datei für den dauerhaften NTP-Server")
	root := flag.String("root", defaultRoot, "Wurzel der Datenpartition mit app/")
	marker := flag.String("marker", defaultMarker, "Gesundmarke des Dienstes")
	flag.Parse()
	if *uid < 0 {
		log.Fatal("-uid fehlt")
	}

	e, err := helper.NewOSExec()
	if err != nil {
		log.Fatal(err)
	}
	out, err := helper.NewOSExecOut()
	if err != nil {
		log.Fatal(err)
	}
	srv := helper.NewServer(*socket, *uid, e, *bootCfg, update.Ops{Root: *root})
	srv.SocketGID = *gid
	srv.NTPStatePath = *ntpState
	srv.HealthyMarker = *marker
	srv.ReapplyNTPOnStart = true
	srv.ExecOut = out

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Serve(ctx); err != nil {
		log.Fatal(err)
	}
}

func guardMain(args []string) {
	fs := flag.NewFlagSet("update-guard", flag.ExitOnError)
	root := fs.String("root", defaultRoot, "Wurzel der Datenpartition mit app/")
	marker := fs.String("marker", defaultMarker, "Gesundmarke des Dienstes")
	fs.Parse(args)

	e, err := helper.NewOSExec()
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := runGuard(ctx, *root, *marker, guardTimeout, e); err != nil {
		log.Fatalf("Update-Wächter: %v", err)
	}
}

// runGuard wartet auf die Gesundmarke der aktivierten Version; bleibt sie aus,
// wird zurückgeschaltet und der Dienst neu gestartet.
func runGuard(ctx context.Context, root, marker string, timeout time.Duration, e helper.Exec) error {
	return update.Guard(ctx, root, marker, timeout, func(ctx context.Context) error {
		log.Print("Update-Wächter: keine Gesundmeldung, schalte auf die vorherige Version zurück")
		if err := update.Rollback(root); err != nil {
			return err
		}
		// Nach mehreren Fehlstarts hätte systemd den Dienst gesperrt; reset-failed hebt das auf.
		if err := e(ctx, "systemctl", "reset-failed", "hmi-kiosk.service"); err != nil {
			log.Printf("Update-Wächter: reset-failed: %v", err)
		}
		return e(ctx, "systemctl", "restart", "hmi-kiosk.service")
	})
}
