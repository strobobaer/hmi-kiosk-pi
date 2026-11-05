// hmi-kiosk-testvnc ist ein kleiner VNC-Server mit Testbild für Selbsttests.
package main

import (
	"flag"
	"log"
	"net"

	"hmikiosk/internal/rfb"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:5999", "Adresse, auf der gelauscht wird")
	width := flag.Int("width", 1024, "Breite des Testbilds")
	height := flag.Int("height", 600, "Höhe des Testbilds")
	password := flag.String("password", "", "VNC-Passwort (leer: keine Anmeldung)")
	flag.Parse()

	l, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("Lauschen auf %s fehlgeschlagen: %v", *listen, err)
	}
	log.Printf("Test-VNC-Server läuft auf %s (%dx%d)", l.Addr(), *width, *height)
	log.Fatal(rfb.NewTestServer(*width, *height, *password).Serve(l))
}
