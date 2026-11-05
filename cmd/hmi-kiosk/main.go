//go:build linux

// hmi-kiosk ist der Kiosk-Dienst der HMI-Terminals.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"hmikiosk/internal/app"
	"hmikiosk/internal/client"
)

func main() {
	data := flag.String("data", "/data", "Datenordner")
	run := flag.String("run", "/run/hmi-kiosk", "Laufzeitordner im RAM")
	helperSocket := flag.String("helper-socket", app.DefaultHelperSocket, "Socket des root-Helfers")
	local := flag.String("local", app.DefaultLocalAddr, "Adresse der Kachelseite")
	admin := flag.String("admin", app.DefaultAdminAddr, "Adresse von Admin-Seite und Terminal-API")
	noChromium := flag.Bool("no-chromium", false, "Chromium nicht starten (für Tests)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := app.Run(ctx, app.Options{
		DataDir:         *data,
		RunDir:          *run,
		LocalAddr:       *local,
		AdminAddr:       *admin,
		HelperSocket:    *helperSocket,
		Runner:          client.NewExecRunner(),
		ChromiumEnabled: !*noChromium,
	})
	if err != nil {
		log.Fatal(err)
	}
}
