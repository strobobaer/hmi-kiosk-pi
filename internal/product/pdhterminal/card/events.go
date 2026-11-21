package card

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"hmikiosk/internal/product/pdhterminal/notify"
)

// maxResultBody ist die größte Rückmeldung der PDH-Seite (in Bytes).
const maxResultBody = 2048

// ResultHandler nimmt die Rückmeldung der PDH-Seite zur Kartenanmeldung an (POST /api/card/result,
// Antwort 204) und zeigt sie über sh, sobald das Browser-Fenster nicht vorn liegt (foreground) und PDH
// mindestens eine Art der Benachrichtigung gewünscht hat. Ursprung und CORS wie bei EventsHandler.
// Name und Meldung kommen aus dem Netz: Sie werden bereinigt und nur als Text gezeigt.
func ResultHandler(origin func() string, foreground func() bool, sh notify.Shower) http.Handler {
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
			hd.Set("Access-Control-Allow-Methods", "POST")
			hd.Set("Access-Control-Allow-Headers", "Content-Type")
			hd.Set("Access-Control-Max-Age", "600")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				hd.Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodPost:
			if r.ContentLength > maxResultBody {
				http.Error(w, "Inhalt zu groß", http.StatusRequestEntityTooLarge)
				return
			}
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxResultBody))
			if err != nil {
				var tooBig *http.MaxBytesError
				if errors.As(err, &tooBig) {
					http.Error(w, "Inhalt zu groß", http.StatusRequestEntityTooLarge)
				} else {
					http.Error(w, "Inhalt nicht lesbar", http.StatusBadRequest)
				}
				return
			}
			var res notify.Result
			if err := json.Unmarshal(body, &res); err != nil {
				http.Error(w, "Kein gültiges JSON", http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			kinds := notify.Kinds(res.Notify)
			if sh == nil || len(kinds) == 0 || foreground() {
				return
			}
			res.Name, res.Message = notify.Clean(res.Name), notify.Clean(res.Message)
			text, tone := notify.Text(res)
			sh.Show(kinds, text, tone)
		default:
			hd.Set("Allow", "POST, OPTIONS")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
		}
	})
}

// pingInterval ist der Abstand der Keep-alive-Kommentare im Strom (Tests kürzen ihn).
var pingInterval = 25 * time.Second

// StateSource liefert den Leserzustand und meldet Änderungen.
type StateSource interface {
	ReaderState() string
	WatchState() (<-chan struct{}, func())
}

// EventsHandler liefert Kartenereignisse (event: card) und den Leserzustand (event: reader)
// als Server-Sent Events; ältere PDH-Seiten ignorieren reader. origin liefert den
// einzigen erlaubten Anfrage-Ursprung (scheme://host[:port], klein, ohne Pfad); ist er
// leer oder weicht der Origin-Header ab, antwortet der Handler mit 403 ohne CORS-Kopfzeilen.
func EventsHandler(h *Hub, st StateSource, origin func() string) http.Handler {
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
			hd.Set("Access-Control-Allow-Methods", "GET")
			hd.Set("Access-Control-Max-Age", "600")
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				hd.Set("Access-Control-Allow-Private-Network", "true")
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodGet:
			serveEvents(w, r, h, st)
		default:
			hd.Set("Allow", "GET, OPTIONS")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
		}
	})
}

func serveEvents(w http.ResponseWriter, r *http.Request, h *Hub, st StateSource) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming nicht unterstützt", http.StatusInternalServerError)
		return
	}
	ch, cancel, err := h.Subscribe()
	if err != nil {
		http.Error(w, "Zu viele Abonnenten", http.StatusServiceUnavailable)
		return
	}
	// Der Kanal wird nie geschlossen; das Ende kommt über den Anfragekontext.
	defer cancel()

	// Eine WriteTimeout des Servers würde den langlebigen Strom sonst abbrechen.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})

	hd := w.Header()
	hd.Set("Content-Type", "text/event-stream")
	hd.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	// Zustand sofort melden und bei Wechsel erneut; nie doppelt.
	var stateCh <-chan struct{}
	last := ""
	sendState := func() bool {
		cur := st.ReaderState()
		if cur == last {
			return true
		}
		data, err := json.Marshal(struct {
			State string `json:"state"`
		}{cur})
		if err != nil {
			return true
		}
		if _, err := w.Write([]byte("event: reader\ndata: " + string(data) + "\n\n")); err != nil {
			return false
		}
		last = cur
		fl.Flush()
		return true
	}
	if st != nil {
		var stop func()
		stateCh, stop = st.WatchState()
		defer stop()
		if !sendState() {
			return
		}
	}

	tick := time.NewTicker(pingInterval)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			if !h.Fresh(e) {
				continue
			}
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			if _, err := w.Write([]byte("event: card\ndata: " + string(data) + "\n\n")); err != nil {
				return
			}
			fl.Flush()
		case <-stateCh:
			if !sendState() {
				return
			}
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
