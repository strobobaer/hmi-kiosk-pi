//go:build linux

// hmi-kiosk ist der Kiosk-Dienst der HMI-Terminals. Unterbefehle: selftest, soak.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"hmikiosk/internal/app"
	"hmikiosk/internal/client"
	"hmikiosk/internal/selftest"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "selftest":
			os.Exit(runSelftest(os.Args[2:], os.Stdout, os.Stderr))
		case "soak":
			os.Exit(runSoak(os.Args[2:], os.Stdout, os.Stderr))
		}
	}
	serve()
}

func serve() {
	data := flag.String("data", "/data", "Datenordner")
	run := flag.String("run", "/run/hmi-kiosk", "Laufzeitordner im RAM")
	helperSocket := flag.String("helper-socket", app.DefaultHelperSocket, "Socket des root-Helfers")
	local := flag.String("local", app.DefaultLocalAddr, "Adresse der Kachelseite")
	admin := flag.String("admin", app.DefaultAdminAddr, "Adresse von Admin-Seite und Terminal-API")
	localAdmin := flag.String("local-admin", app.DefaultLocalAdminAddr, "Adresse der Admin-Seite am Terminal (nur Loopback)")
	noChromium := flag.Bool("no-chromium", false, "Chromium nicht starten (für Tests)")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := app.Run(ctx, app.Options{
		DataDir:         *data,
		RunDir:          *run,
		LocalAddr:       *local,
		AdminAddr:       *admin,
		LocalAdminAddr:  *localAdmin,
		HelperSocket:    *helperSocket,
		Runner:          client.NewExecRunner(),
		ChromiumEnabled: !*noChromium,
	})
	if err != nil {
		log.Fatal(err)
	}
}

// runSelftest lässt den laufenden Dienst die Selbstprüfung ausführen, damit sie auf
// derselben Anzeige läuft. Exit 0: kein Schritt gescheitert. 1: mindestens einer
// gescheitert. 2: Aufruf oder Dienst nicht erreichbar.
func runSelftest(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("selftest", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	fmt.Fprintln(stderr, "Selbstprüfung läuft am Gerät und dauert einige Minuten. Bitte am Bildschirm den Anweisungen folgen.")
	res, err := app.RunSelftestRemote(ctx, "")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	fmt.Fprint(stdout, selftest.Format(res))
	if selftest.Failed(res) {
		return 1
	}
	return 0
}

// runSoak: Exit 0 ohne Fehler und Leck, auch bei Abbruch mit Strg+C; 1 bei
// Fehlern oder Leck; 2 bei falschem Aufruf oder wenn die Prüfung nicht läuft.
func runSoak(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("soak", flag.ContinueOnError)
	fs.SetOutput(stderr)
	dur := fs.Duration("duration", 24*time.Hour, "Dauer der Prüfung")
	data := fs.String("data", "/data", "Datenordner")
	run := fs.String("run", "/run/hmi-kiosk", "Laufzeitordner im RAM")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *dur <= 0 {
		fmt.Fprintln(stderr, "Die Dauer muss größer als 0 sein.")
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	res, err := app.RunSoak(ctx, app.SoakOptions{
		DataDir: *data, RunDir: *run, Duration: *dur, Runner: client.NewExecRunner(),
		Out:  func(line string) { fmt.Fprintln(stdout, line) },
		Warn: func(msg string) { fmt.Fprintln(stderr, msg) },
	})
	aborted := errors.Is(err, context.Canceled)
	if err != nil && !aborted {
		fmt.Fprintln(stderr, err)
		return 2
	}
	state := "beendet"
	if aborted {
		state = "abgebrochen"
	}
	fmt.Fprintf(stderr, "Dauerprüfung %s: %d Durchgänge, %d Fehler", state, res.Cycles, res.Failures)
	if res.Leak {
		fmt.Fprintf(stderr, ", Speicher wächst um %.1f MB je Stunde", res.SlopeBytesPerHour/(1<<20))
	}
	fmt.Fprintln(stderr)
	if res.Failures > 0 || res.Leak {
		return 1
	}
	return 0
}
