package card

import (
	"encoding/json"
	"net/http"
	"time"
)

// pingInterval ist der Abstand der Keep-alive-Kommentare im Strom (Tests kürzen ihn).
var pingInterval = 25 * time.Second

// EventsHandler liefert Kartenereignisse als Server-Sent Events. origin liefert den
// einzigen erlaubten Anfrage-Ursprung (scheme://host[:port], klein, ohne Pfad); ist er
// leer oder weicht der Origin-Header ab, antwortet der Handler mit 403 ohne CORS-Kopfzeilen.
func EventsHandler(h *Hub, origin func() string) http.Handler {
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
			serveEvents(w, r, h)
		default:
			hd.Set("Allow", "GET, OPTIONS")
			http.Error(w, "Methode nicht erlaubt", http.StatusMethodNotAllowed)
		}
	})
}

func serveEvents(w http.ResponseWriter, r *http.Request, h *Hub) {
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
		case <-tick.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
