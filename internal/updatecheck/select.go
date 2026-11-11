// Package updatecheck sucht in den GitHub-Releases nach einer neueren Programmversion
// und lädt deren Paketdatei. Vertrauen entsteht erst bei der Installation (Signatur,
// Systemstand); GitHub und TLS sind hier nur der Transportweg.
package updatecheck

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"hmikiosk/internal/release"
	"hmikiosk/internal/update"
)

// MaxPackageBytes ist die Grenze wie beim Hochladen (Variable für den Test).
var MaxPackageBytes int64 = 200 << 20

const maxNotesRunes = 600

// ErrNoRelease: Die laufende Version ist kein veröffentlichter Stand (z. B. "dev").
var ErrNoRelease = errors.New("Die laufende Version ist ein Entwicklungsstand; Update-Angebote gibt es nur für veröffentlichte Versionen.")

// Release ist der hier gebrauchte Teil eines GitHub-Release.
type Release struct {
	TagName    string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	HTMLURL    string  `json:"html_url"`
	Body       string  `json:"body"`
	Assets     []Asset `json:"assets"`
}

type Asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

// Offer ist eine gefundene neuere Version.
type Offer struct {
	Version    string
	Prerelease bool
	Notes      string // Klartext, gekürzt
	PageURL    string // Release-Seite, leer wenn nicht https
	AssetURL   string
	AssetSize  int64
	// NoPackage nennt den Grund, warum sich die Version nicht per Klick installieren lässt.
	NoPackage string
}

func (o Offer) Installable() bool { return o.NoPackage == "" && o.AssetURL != "" }

// Newer vergleicht zwei gültige Versionen x.y.z Teil für Teil als Zahl.
func Newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range pa {
		if c := compareNumber(pa[i], pb[i]); c != 0 {
			return c > 0
		}
	}
	return false
}

// compareNumber vergleicht Ziffernfolgen beliebiger Länge ohne Überlauf.
func compareNumber(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

// Select wählt die höchste Version des Produkts p über current. nil heißt: nichts Neueres.
func Select(rels []Release, p release.Product, current string, prerelease bool) (*Offer, error) {
	if !update.ValidVersion(current) {
		return nil, ErrNoRelease
	}
	var best *Release
	bestVersion := current
	for i := range rels {
		r := &rels[i]
		if r.Draft || (r.Prerelease && !prerelease) {
			continue
		}
		v, ok := p.TagVersion(r.TagName)
		if !ok || !canonical(v) || !Newer(v, bestVersion) {
			continue
		}
		best, bestVersion = r, v
	}
	if best == nil {
		return nil, nil
	}
	o := &Offer{Version: bestVersion, Prerelease: best.Prerelease, Notes: plainNotes(best.Body)}
	if isHTTPS(best.HTMLURL) {
		o.PageURL = best.HTMLURL
	}
	o.NoPackage = fmt.Sprintf("Das Release enthält keine Paketdatei %s. Diese Version kommt nur mit einem neuen SD-Abbild oder dem Bundle.", p.PackageName(bestVersion))
	for _, a := range best.Assets {
		if a.Name != p.PackageName(bestVersion) {
			continue
		}
		switch {
		case a.Size <= 0 || a.Size > MaxPackageBytes:
			o.NoPackage = fmt.Sprintf("Die Paketdatei ist leer oder größer als %d MB.", MaxPackageBytes>>20)
		case !isHTTPS(a.URL):
			o.NoPackage = "Die Adresse der Paketdatei ist ungültig."
		default:
			o.NoPackage, o.AssetURL, o.AssetSize = "", a.URL, a.Size
		}
		break
	}
	return o, nil
}

func isHTTPS(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

// canonical: gültige Version ohne führende Nullen. Sonst hinge der Paketname an der
// Schreibweise des Tags (v0.10.01 gälte als neuer als 0.10.0).
func canonical(v string) bool {
	if !update.ValidVersion(v) {
		return false
	}
	for _, part := range strings.Split(v, ".") {
		if len(part) > 1 && part[0] == '0' {
			return false
		}
	}
	return true
}

// plainNotes macht aus der Release-Notiz kurzen Klartext ohne Steuer- und Formatzeichen
// (Cf, etwa Bidi-Umschaltungen, die Text vortäuschen könnten); das Escapen übernimmt die Vorlage.
func plainNotes(s string) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, "�"), "\r\n", "\n")
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !(unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			return r
		}
		return -1
	}, s)
	s = strings.TrimSpace(s)
	if rs := []rune(s); len(rs) > maxNotesRunes {
		s = strings.TrimSpace(string(rs[:maxNotesRunes])) + "…"
	}
	return s
}
