package slides

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
)

//go:embed offline.html
var offlinePage []byte

// offlineCSP: nur die eingebettete Seite, Bilder und Abfragen vom eigenen Ursprung.
const offlineCSP = "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; img-src 'self'; connect-src 'self'; form-action 'none'"

// CacheHandler nimmt den Zwischenspeicher von der PDH-Seite an: POST /api/slides/cache (Inhalt bis
// MaxBody, Antwort 204, 413 oder 400) und DELETE (leert, 204). Ursprung und CORS wie bei /api/card/result
// (nur die PDH-Adresse, Preflight samt Private-Network). changed wird nach jeder Änderung aufgerufen,
// damit der Kartenleser ohne Neustart umschaltet.
func CacheHandler(st *Store, origin func() string, changed func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := origin()
		if got := r.Header.Get("Origin"); allowed == "" || got != allowed {
			http.Error(w, "Ursprung nicht erlaubt", http.StatusForbidden)
			return
		}
		hd := w.Header()
		hd.Set("Access-Control-Allow-Origin", allowed)
		hd.Set("Vary", "Origin")

		switch r.Method {
		case http.MethodOptions:
			hd.Set("Access-Control-Allow-Methods", "POST, DELETE")
			hd.Set("Access-Control-Allow-Headers", "Content-Type")
			hd.Set("Access-Control-Max-Age", "600")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				hd.Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			if r.ContentLength > MaxBody {
				http.Error(w, "Inhalt zu groß", http.StatusRequestEntityTooLarge)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBody))
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					http.Error(w, "Inhalt zu groß", http.StatusRequestEntityTooLarge)
				} else {
					http.Error(w, "Inhalt nicht lesbar", http.StatusBadRequest)
				}
				return
			}
			switch err := st.Save(body); {
			case err == nil:
			case errors.Is(err, ErrTooLarge):
				http.Error(w, "Zwischenspeicher zu groß", http.StatusRequestEntityTooLarge)
				return
			case errors.Is(err, ErrInvalid):
				http.Error(w, "Zwischenspeicher ungültig", http.StatusBadRequest)
				return
			default:
				http.Error(w, "Zwischenspeicher nicht gespeichert", http.StatusInternalServerError)
				return
			}
			if changed != nil {
				changed()
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := st.Clear(); err != nil {
				http.Error(w, "Zwischenspeicher nicht gelöscht", http.StatusInternalServerError)
				return
			}
			if changed != nil {
				changed()
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			hd.Set("Allow", "POST, DELETE, OPTIONS")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
		}
	})
}

// Register hängt die Offline-Folienschau an mux (nur GET/HEAD kommen hier an):
//
//	/slides            die Seite (ohne Zwischenspeicher: zurück zu /start)
//	/slides/data.json  Folien aus dem Zwischenspeicher
//	/slides/img/{id}   Bild einer Folie
//	/slides/online     {"online":bool}: ist PDH wieder erreichbar (die Seite kehrt dann zu /start zurück)
func Register(mux *http.ServeMux, st *Store, online func(context.Context) bool) {
	RegisterThemed(mux, st, nil, online)
}

// RegisterThemed wie Register; theme (darf nil sein) liefert Farbvariablen als CSS-Regel, die die Grundfarben der
// Seite ersetzen (Hintergrund wie PDH).
func RegisterThemed(mux *http.ServeMux, st *Store, theme func() string, online func(context.Context) bool) {
	mux.HandleFunc("/slides", func(w http.ResponseWriter, r *http.Request) {
		if !st.Present() {
			http.Redirect(w, r, "/start", http.StatusFound)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", offlineCSP)
		page := offlinePage
		if theme != nil {
			page = bytes.Replace(page, []byte("/*THEME*/"), []byte(theme()), 1)
		}
		_, _ = w.Write(page)
	})
	mux.HandleFunc("/slides/data.json", func(w http.ResponseWriter, r *http.Request) {
		snap, err := st.Load()
		if err != nil {
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(snap)
	})
	mux.HandleFunc("/slides/img/{id}", func(w http.ResponseWriter, r *http.Request) {
		b, ctype, err := st.Image(r.PathValue("id"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(w, r)
			} else {
				http.Error(w, "Bild nicht lesbar", http.StatusInternalServerError)
			}
			return
		}
		h := w.Header()
		h.Set("Content-Type", ctype)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Length", strconv.Itoa(len(b)))
		_, _ = w.Write(b)
	})
	mux.HandleFunc("/slides/online", func(w http.ResponseWriter, r *http.Request) {
		ok := online != nil && online(r.Context())
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("Content-Type", "application/json; charset=utf-8")
		if ok {
			_, _ = w.Write([]byte(`{"online":true}`))
		} else {
			_, _ = w.Write([]byte(`{"online":false}`))
		}
	})
}
