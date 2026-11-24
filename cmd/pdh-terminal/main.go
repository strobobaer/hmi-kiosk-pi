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
	local := flag.String("local", "", "Adresse der Startseite (nur Loopback); leer: 127.0.0.1 mit dem Port aus der Konfiguration, Grundwert "+pdhterminal.DefaultLocalAddr)
	admin := flag.String("admin", pdhterminal.DefaultAdminAddr, "Adresse der Admin-Seite (HTTPS)")
	localAdmin := flag.String("local-admin", "", "Adresse der Admin-Seite am Terminal (nur Loopback); leer: der Port der Startseite + 1, Grundwert "+pdhterminal.DefaultLocalAdminAddr)
	noChromium := flag.Bool("no-chromium", false, "Chromium nicht starten (für Tests)")
	kiosk := flag.Bool("kiosk", false, "Windows-Kiosk: Edge im Vollbild, kein Symbol im Infobereich, keine Benachrichtigungen, Admin-Geste oben rechts")
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
		TrayEnabled:     !*kiosk,
		Kiosk:           *kiosk,
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
