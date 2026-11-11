package web

import (
	"context"
	"html/template"
	"io/fs"
	"log"
	"mime/multipart"
	"net/http"
	"strings"

	"hmikiosk/internal/config"
)

// NavItem ist ein Eintrag im Menü der Admin-Seite.
type NavItem struct{ Section, Label, Path string }

// AdminFrame ist, was ein Produkt vom Admin-Rahmen braucht: Seiten im gemeinsamen
// Layout, Konfiguration ändern und laden, den root-Helfer und das Protokoll.
type AdminFrame interface {
	Render(w http.ResponseWriter, status int, page, section, title, errMsg string, body any)
	Update(change func(*config.Config) error) error
	Load() (config.Config, error)
	Helper() HelperAPI
	Logf(format string, args ...any)
}

// ProductRoute ist eine Route des Produkts. Alle sind angemeldete Sitzungsrouten
// mit CSRF-Prüfung wie bei den Kernseiten; bei Upload liegt der CSRF-Wert als
// erstes multipart-Feld, die weiteren Felder liefert ProductUpload.
type ProductRoute struct {
	Method, Path string
	Upload       bool
	Handle       func(f AdminFrame, w http.ResponseWriter, r *http.Request)
}

// AdminProduct beschreibt ein Produkt auf dem gemeinsamen Admin-Rahmen.
type AdminProduct struct {
	ID, Name, Home string
	Nav            []NavItem
	// Pages: Seitenname → Template-Text (muss "content" definieren), geparst mit layout.html.
	Pages  map[string]string
	Routes []ProductRoute
	// IdleSeconds > 0: Abmeldung nach so vielen Sekunden Leerlauf (0: wie bisher).
	IdleSeconds int
	// HMI schaltet die HMI-Seiten, -Routen und -Teile der Kernseiten ein.
	HMI bool
}

// HMIKioskAdmin ist das Produkt HMI-Kiosk mit dem bisherigen Menü.
func HMIKioskAdmin() *AdminProduct {
	return &AdminProduct{
		ID: "hmi-kiosk", Name: "HMI-Kiosk", Home: "/admin/hmis", HMI: true,
		Nav: []NavItem{
			{"hmis", "HMIs", "/admin/hmis"},
			{"client", "Client", "/admin/client"},
			{"kiosk", "Kiosk", "/admin/kiosk"},
			{"terminals", "Terminals", "/admin/terminals"},
			{"network", "Netzwerk", "/admin/network"},
			{"system", "System", "/admin/system"},
		},
	}
}

const productUploadLimit = 1 << 20

type uploadKey struct{}

// ProductUpload liefert in einer Upload-Route den Leser hinter dem CSRF-Feld.
func ProductUpload(r *http.Request) *multipart.Reader {
	mr, _ := r.Context().Value(uploadKey{}).(*multipart.Reader)
	return mr
}

// hmiOnlyPages sind die Kernseiten, die es nur mit HMI gibt.
var hmiOnlyPages = map[string]bool{
	"hmis.html": true, "hmi_edit.html": true, "client.html": true, "kiosk.html": true, "terminals.html": true,
}

// productFrame ist der AdminFrame auf dem laufenden admin.
type productFrame struct {
	a *admin
	s sessionRef
}

func (f productFrame) Render(w http.ResponseWriter, status int, page, section, title, errMsg string, body any) {
	f.a.page(w, f.s, status, page, section, title, errMsg, body)
}

func (f productFrame) Update(change func(*config.Config) error) error { return f.a.update(change) }

func (f productFrame) Load() (config.Config, error) {
	c, info, err := f.a.d.Store.Load()
	if err != nil || setupClosedByFallback(c, info) {
		return config.Config{}, errConfigUnreadable
	}
	return c, nil
}

func (f productFrame) Helper() HelperAPI { return f.a.d.Helper }

func (f productFrame) Logf(format string, args ...any) { log.Printf(format, args...) }

// productRoutes übersetzt die Routen des Produkts in Rahmenrouten.
func (a *admin) productRoutes() []route {
	var out []route
	for _, pr := range a.product.Routes {
		pr := pr
		rt := route{method: pr.Method, path: pr.Path, kind: routeSession, limit: formBodyLimit}
		if pr.Upload {
			rt.kind, rt.limit = routeUpload, productUploadLimit
			rt.upload = func(w http.ResponseWriter, r *http.Request, s sessionRef, mr *multipart.Reader) {
				r = r.WithContext(context.WithValue(r.Context(), uploadKey{}, mr))
				pr.Handle(productFrame{a, s}, w, r)
			}
		} else {
			rt.handle = func(w http.ResponseWriter, r *http.Request, s sessionRef) {
				pr.Handle(productFrame{a, s}, w, r)
			}
		}
		out = append(out, rt)
	}
	return out
}

// parseProductPages hängt die Seiten des Produkts an; ein Name, den es als Kernseite
// schon gibt, ist ein Programmierfehler.
func parseProductPages(pages map[string]*template.Template, sub fs.FS, p *AdminProduct) {
	for name, text := range p.Pages {
		base := strings.TrimSuffix(name, ".html")
		if _, dup := pages[name]; dup {
			panic("Produktseite kollidiert mit Kernseite: " + name)
		}
		if _, dup := pages[base+".html"]; dup {
			panic("Produktseite kollidiert mit Kernseite: " + name)
		}
		t := template.Must(template.New("layout.html").Funcs(adminFuncs).ParseFS(sub, "layout.html"))
		pages[name] = template.Must(t.Parse(text))
	}
}
