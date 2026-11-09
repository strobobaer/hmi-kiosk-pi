// hmi-kiosk-helper ist der kleine root-Helfer des Kiosk-Dienstes.
//
// Ohne Unterbefehl bedient er den Socket. Mit "net-boot" schreibt er beim Start das
// Ethernet-Profil aus der Datenpartition. Mit "update-guard" prüft er nach einer
// Aktivierung, ob die neue Version gesund meldet, und schaltet sonst zurück.
// Mit "web-policy" schreibt er vor dem Kiosk-Dienst die Chromium-Richtlinie mit den
// gespeicherten Ursprüngen der Web-Kacheln (das Abbild beginnt mit der aus dem Paket).
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

	defaultNetSaved = "/data/system/ethernet.json"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "net-boot" {
		netBootMain(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "web-policy" {
		webPolicyMain(os.Args[2:])
		return
	}
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
	netSaved := flag.String("net-saved", defaultNetSaved, "Datei für die bestätigte Ethernet-Einstellung")
	webOrigins := flag.String("web-origins", "/data/system/web-origins", "Datei für die Ursprünge der Web-Kacheln (Chromium-Richtlinie)")
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
	srv.WebOriginsStatePath = *webOrigins
	srv.HealthyMarker = *marker
	srv.ReapplyNTPOnStart = true
	srv.ExecOut = out
	srv.Backup.HasTool = helper.OSHasTool
	srv.Net.SavedPath = *netSaved
	srv.RecoverNetOnStart = true

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv.CleanupStale(ctx)
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

// netBootMain schreibt vor NetworkManager das Profil aus der bestätigten Ethernet-Einstellung
// (hmi-kiosk-network.service). Es endet immer mit 0: Das Netz darf daran nie hängen.
func netBootMain(args []string) {
	fs := flag.NewFlagSet("net-boot", flag.ContinueOnError)
	saved := fs.String("saved", defaultNetSaved, "Datei mit der bestätigten Ethernet-Einstellung")
	if err := fs.Parse(args); err != nil {
		log.Printf("net-boot: %v", err)
		return
	}
	helper.NetBoot(helper.NetConfig{SavedPath: *saved}, log.Printf)
}

// webPolicyMain läuft als ExecStartPre des Helfers, also vor hmi-kiosk.service. Es endet
// immer mit 0: Der Helfer und damit der Kiosk dürfen nie daran hängen. Im Fehlerfall gilt
// die Richtlinie aus dem Paket oder nur die festen Einträge (Web-Kacheln gesperrt).
func webPolicyMain(args []string) {
	fs := flag.NewFlagSet("web-policy", flag.ContinueOnError)
	policy := fs.String("policy", "/etc/chromium/policies/managed/hmi-kiosk.json", "Chromium-Richtlinie")
	state := fs.String("state", "/data/system/web-origins", "Gespeicherte Ursprünge der Web-Kacheln")
	if err := fs.Parse(args); err != nil {
		log.Printf("web-policy: %v", err)
		return
	}
	if err := helper.ApplyWebPolicy(*policy, *state); err != nil {
		log.Printf("web-policy: %v", err)
	}
}
