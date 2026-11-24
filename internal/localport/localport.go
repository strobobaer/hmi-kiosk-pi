// Package localport hält den Port der lokalen Startseite des Terminals (Grundwert 8080). Die
// lokale Admin-Seite am Terminal liegt einen Port darüber (8081). Der Wert wird beim Start aus der
// Konfiguration gesetzt und gilt für den ganzen Prozess; eine Änderung wirkt erst nach dem Neustart.
// Das Paket hängt von nichts ab, damit Browser, Helfer und Seiten es gemeinsam nutzen können.
package localport

import (
	"strconv"
	"strings"
	"sync/atomic"
)

const (
	// Default ist der Grundwert: Startseite 127.0.0.1:8080, lokale Admin-Seite 127.0.0.1:8081.
	Default = 8080
	// Min und Max begrenzen den einstellbaren Port; Max lässt Platz für den Port darüber.
	Min = 1024
	Max = 65534
)

var port atomic.Int32

// Set setzt den Port der Startseite; 0 (oder ein Wert außerhalb von 1…Max) heißt Grundwert.
// Die Prüfung der Grenzen (Min, Max, Admin-Port) macht config.ValidateLocalPort.
func Set(p int) {
	if p <= 0 || p > Max {
		p = Default
	}
	port.Store(int32(p))
}

// Get ist der Port der Startseite.
func Get() int {
	if p := int(port.Load()); p != 0 {
		return p
	}
	return Default
}

// Admin ist der Port der lokalen Admin-Seite am Terminal.
func Admin() int { return Get() + 1 }

func hostPort(host string, p int) string { return host + ":" + strconv.Itoa(p) }

// Addr ist die Bindeadresse der Startseite, AdminAddr die der lokalen Admin-Seite (nur Loopback).
func Addr() string      { return hostPort("127.0.0.1", Get()) }
func AdminAddr() string { return hostPort("127.0.0.1", Admin()) }

// StartURL ist die Adresse der Startseite für den Browser, AdminURL die der lokalen Admin-Seite.
func StartURL() string { return "http://" + Addr() + "/" }
func AdminURL() string { return "http://" + AdminAddr() + "/admin/" }

func isLocal(hostport string, p int) bool {
	return hostport == hostPort("127.0.0.1", p) || hostport == hostPort("localhost", p)
}

// StartHost und AdminHost: Die Host-Kopfzeile der Anfrage ist genau einer der lokalen Namen mit dem
// Port (Schutz gegen DNS-Rebinding).
func StartHost(hostport string) bool { return isLocal(hostport, Get()) }
func AdminHost(hostport string) bool { return isLocal(hostport, Admin()) }

// StartOrigin und AdminOrigin: genau http://<lokaler Name>:<Port>.
func StartOrigin(o string) bool {
	rest, ok := strings.CutPrefix(o, "http://")
	return ok && isLocal(rest, Get())
}

func AdminOrigin(o string) bool {
	rest, ok := strings.CutPrefix(o, "http://")
	return ok && isLocal(rest, Admin())
}
