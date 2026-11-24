package release

import (
	"regexp"
	"runtime"
	"strings"
)

// Product beschreibt eines der Produkte, die aus diesem Repository entstehen.
type Product struct {
	ID          string // Kennung in Paketen und Dateinamen
	Name        string // Anzeigename
	Binary      string // Name der Programmdatei
	SystemLevel int    // Stand der Systemteile, den das Programm braucht
	// LegacyTags: Tags der Form v<x.y.z> (ohne Produktpräfix) gehören auch zu diesem Produkt.
	LegacyTags bool
}

var (
	HMIKiosk    = Product{ID: "hmi-kiosk", Name: "HMI-Kiosk", Binary: "hmi-kiosk", SystemLevel: SystemLevel, LegacyTags: true}
	PDHTerminal = Product{ID: "pdh-terminal", Name: "PDH-Terminal", Binary: "pdh-terminal", SystemLevel: 2}
)

// Products sind alle bekannten Produkte.
func Products() []Product { return []Product{HMIKiosk, PDHTerminal} }

// ProductByID liefert das Produkt zur Kennung; false bei unbekannter Kennung.
func ProductByID(id string) (Product, bool) {
	for _, p := range Products() {
		if p.ID == id {
			return p, true
		}
	}
	return Product{}, false
}

// PlatformWindowsAMD64 ist die Plattformkennung der Windows-Pakete (PAX-Datensatz HMIKIOSK.platform,
// Suffix des Paketnamens). Linux-Pakete tragen keine Kennung (leer).
const PlatformWindowsAMD64 = "windows-amd64"

// PlatformFor liefert die Plattformkennung für ein Betriebssystem und eine Architektur;
// leer heißt Linux (ohne Datensatz im Paket) oder keine Paketplattform.
func PlatformFor(goos, goarch string) string {
	if goos == "windows" && goarch == "amd64" {
		return PlatformWindowsAMD64
	}
	return ""
}

// CurrentPlatform ist die Plattformkennung des laufenden Programms: "windows-amd64" unter
// Windows, sonst leer.
func CurrentPlatform() string { return PlatformFor(runtime.GOOS, runtime.GOARCH) }

// PackageName ist der Name der Paketdatei im Release (Linux).
func (p Product) PackageName(version string) string { return p.PackageNameFor(version, "") }

// PackageNameFor ist der Name der Paketdatei für eine Plattform: ohne Plattform
// <ID>-<version>.pkg, sonst <ID>-<version>-<platform>.pkg.
func (p Product) PackageNameFor(version, platform string) string {
	if platform == "" {
		return p.ID + "-" + version + ".pkg"
	}
	return p.ID + "-" + version + "-" + platform + ".pkg"
}

// BinaryFor ist der Name der Programmdatei im Paket: unter Windows mit ".exe".
func (p Product) BinaryFor(platform string) string {
	if strings.HasPrefix(platform, "windows-") {
		return p.Binary + ".exe"
	}
	return p.Binary
}

var tagVersionRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// TagVersion liest die Version aus einem Release-Tag: <ID>-v<x.y.z>, bei LegacyTags auch
// v<x.y.z> und die bisher ebenfalls angenommene Schreibweise ohne v. Ob die Zahlen
// zulässig sind (Länge, führende Nullen), prüft der Aufrufer.
func (p Product) TagVersion(tag string) (string, bool) {
	var v string
	switch {
	case strings.HasPrefix(tag, p.ID+"-v"):
		v = strings.TrimPrefix(tag, p.ID+"-v")
	case p.LegacyTags:
		v = strings.TrimPrefix(tag, "v")
	default:
		return "", false
	}
	if !tagVersionRe.MatchString(v) {
		return "", false
	}
	return v, true
}
