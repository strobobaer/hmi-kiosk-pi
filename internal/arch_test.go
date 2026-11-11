package internal_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const (
	modPath     = "hmikiosk/"
	productPkg  = modPath + "internal/product/"
	productDirs = "internal/product/"
)

// Produktpakete dürfen den HMI-Teil nicht kennen: Das PDH-Terminal hat keine Sitzungen,
// keinen VNC-Client und keine Abstimmung zwischen Terminals. Geprüft werden nur die
// unmittelbaren Importe; was Pakete wie web oder selftest weiterreichen, bleibt außen vor.
var hmiOnly = []string{
	modPath + "internal/coord",
	modPath + "internal/session",
	modPath + "internal/rfb",
	modPath + "internal/client",
}

// pdhOnly sind die Ordner (mit Schrägstrich am Ende), für die hmiOnly verboten ist.
var pdhOnly = []string{"internal/product/pdhterminal/", "cmd/pdh-terminal/"}

// violations prüft die Importe je Datei (Pfad mit Schrägstrichen ab der Wurzel des Moduls).
func violations(imports map[string][]string) []string {
	var out []string
	for file, imps := range imports {
		for _, imp := range imps {
			if rest, ok := strings.CutPrefix(imp, productPkg); ok {
				name, _, _ := strings.Cut(rest, "/")
				if !strings.HasPrefix(file, productDirs+name+"/") && !strings.HasPrefix(file, "cmd/") {
					out = append(out, file+" importiert "+imp)
				}
			}
			if slices.ContainsFunc(pdhOnly, func(d string) bool { return strings.HasPrefix(file, d) }) &&
				slices.ContainsFunc(hmiOnly, func(h string) bool { return imp == h || strings.HasPrefix(imp, h+"/") }) {
				out = append(out, file+" importiert "+imp)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestViolationsTable(t *testing.T) {
	imp := func(p string) string { return modPath + p }
	tests := []struct {
		name string
		in   map[string][]string
		want int
	}{
		{"sauber", map[string][]string{
			"internal/web/admin.go":                  {imp("internal/config")},
			"internal/product/pdhterminal/run.go":    {imp("internal/web"), imp("internal/browser")},
			"cmd/pdh-terminal/main.go":               {imp("internal/product/pdhterminal")},
			"internal/product/pdhterminal/status.go": {imp("internal/product/pdhterminal")},
		}, 0},
		{"Kern importiert Produkt", map[string][]string{"internal/web/admin.go": {imp("internal/product/pdhterminal")}}, 1},
		{"anderes Produkt", map[string][]string{"internal/product/hmi/x.go": {imp("internal/product/pdhterminal")}}, 1},
		{"Unterpaket eines Produkts", map[string][]string{"internal/app/app.go": {imp("internal/product/pdhterminal/sub")}}, 1},
		{"cmd darf Produkt", map[string][]string{"cmd/hmi-kiosk/main.go": {imp("internal/product/pdhterminal")}}, 0},
		{"PDH importiert Sitzung", map[string][]string{"internal/product/pdhterminal/run.go": {imp("internal/session")}}, 1},
		{"PDH-Programm importiert Client", map[string][]string{"cmd/pdh-terminal/main.go": {imp("internal/client")}}, 1},
		{"HMI-Programm darf Sitzung", map[string][]string{"cmd/hmi-kiosk/main.go": {imp("internal/session")}}, 0},
		{"ähnlicher Name ist kein Verbot", map[string][]string{"internal/product/pdhterminal/run.go": {imp("internal/clientx")}}, 0},
	}
	for _, tt := range tests {
		if got := violations(tt.in); len(got) != tt.want {
			t.Errorf("%s: %q, erwartet %d Verstöße", tt.name, got, tt.want)
		}
	}
}

// TestCoreDoesNotImportProducts liest die Importe aller Quelldateien (ohne Tests) unter
// internal/ und cmd/.
func TestCoreDoesNotImportProducts(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	imports := map[string][]string{}
	fset := token.NewFileSet()
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, s := range f.Imports {
				p, err := strconv.Unquote(s.Path.Value)
				if err != nil {
					return err
				}
				imports[rel] = append(imports[rel], p)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(imports) < 50 {
		t.Fatalf("nur %d Dateien gelesen, Pfad falsch?", len(imports))
	}
	for _, v := range violations(imports) {
		t.Error(v)
	}
}
