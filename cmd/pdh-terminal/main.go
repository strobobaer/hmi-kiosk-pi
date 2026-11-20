// pdh-terminal ist der Dienst des PDH-Terminals: Startseite im Kiosk-Browser, Kopplung
// mit PDH und Admin-Seite.
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"hmikiosk/internal/product/pdhterminal"
)

func main() {
	data := flag.String("data", defaultData, "Datenordner")
	run := flag.String("run", defaultRun, "Laufzeitordner im RAM")
	helperSocket := flag.String("helper-socket", defaultHelperSocket, "Socket des root-Helfers (leer: kein Helfer)")
	local := flag.String("local", pdhterminal.DefaultLocalAddr, "Adresse der Startseite (nur Loopback)")
	admin := flag.String("admin", pdhterminal.DefaultAdminAddr, "Adresse der Admin-Seite (HTTPS)")
	localAdmin := flag.String("local-admin", pdhterminal.DefaultLocalAdminAddr, "Adresse der Admin-Seite am Terminal (nur Loopback)")
	noChromium := flag.Bool("no-chromium", false, "Chromium nicht starten (für Tests)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := pdhterminal.Run(ctx, pdhterminal.Options{
		DataDir:         *data,
		RunDir:          *run,
		LocalAddr:       *local,
		AdminAddr:       *admin,
		LocalAdminAddr:  *localAdmin,
		HelperSocket:    *helperSocket,
		ChromiumEnabled: !*noChromium,
		TrayEnabled:     true,
	})
	stop()
	switch {
	case errors.Is(err, pdhterminal.ErrQuit):
		os.Exit(pdhterminal.ExitQuit)
	case errors.Is(err, pdhterminal.ErrRestart):
		os.Exit(pdhterminal.ExitRestart)
	case err != nil:
		log.Fatal(err)
	}
}
