package release

import (
	"regexp"
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
	PDHTerminal = Product{ID: "pdh-terminal", Name: "PDH-Terminal", Binary: "pdh-terminal", SystemLevel: 1}
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

// PackageName ist der Name der Paketdatei im Release.
func (p Product) PackageName(version string) string { return p.ID + "-" + version + ".pkg" }

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
