package web

import (
	"log"
	"net/http"

	"hmikiosk/internal/config"
)

// handleRemoteKeyGenerate erzeugt den Schlüssel der Fernsteuerung (PDH) und zeigt ihn genau
// dieses eine Mal; gespeichert wird nur sein Hash. Ein früherer Schlüssel gilt nicht mehr.
func (a *admin) handleRemoteKeyGenerate(w http.ResponseWriter, _ *http.Request, s sessionRef) {
	key, hash, err := NewRemoteKey()
	if err != nil {
		a.renderNetwork(w, s, http.StatusInternalServerError, "Es konnte kein Schlüssel erzeugt werden.", nil)
		return
	}
	if err := a.update(func(c *config.Config) error {
		c.Admin.RemoteKeyHash = hash
		return nil
	}); err != nil {
		a.renderNetwork(w, s, formStatus(err), err.Error(), nil)
		return
	}
	log.Printf("Fernsteuerung (PDH): neuer Schlüssel erzeugt")
	a.renderNetworkFull(w, s, http.StatusOK, "", nil, nil, key)
}

func (a *admin) handleRemoteKeyOff(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if err := a.update(func(c *config.Config) error {
		c.Admin.RemoteKeyHash = ""
		return nil
	}); err != nil {
		a.renderNetwork(w, s, formStatus(err), err.Error(), nil)
		return
	}
	log.Printf("Fernsteuerung (PDH): ausgeschaltet, Schlüssel gelöscht")
	a.done(w, r, s, "/admin/network", "Die Fernsteuerung ist aus; der Schlüssel ist gelöscht.")
}
