package web

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"hmikiosk/internal/config"
)

const (
	pathMaintenance = "/admin/maintenance"
	maintenanceFlow = "Gespeichert. Der Dienst startet neu; danach bitte erneut anmelden."
)

// maintenancePaths bleiben im Wartungsmodus erreichbar; alles andere führt zur
// Wartungsseite.
var maintenancePaths = map[string]bool{
	pathMaintenance:              true,
	pathMaintenance + "/replace": true,
	pathMaintenance + "/new":     true,
	"/admin/logout":              true,
	"/admin/system/log":          true,
}

var errNotInMaintenance = errors.New("Das Terminal ist nicht im Wartungsmodus.")

func (a *admin) hardwareID() string {
	if a.d.HardwareID == nil {
		return ""
	}
	return a.d.HardwareID()
}

// inMaintenance: Die Karte läuft auf einem anderen Gerät als eingerichtet.
func (a *admin) inMaintenance(c config.Config) bool {
	return config.HardwareChanged(c.Terminal.HardwareID, a.hardwareID())
}

type maintenanceData struct {
	OldName, OldRole string
	OldHW, NewHW     string
	Name, Role       string // Eingaben des Formulars „neues Terminal“
}

// shortHardwareID zeigt nur das Ende der Kennung; es reicht zum Wiedererkennen.
func shortHardwareID(id string) string {
	if len(id) > 8 {
		return "…" + id[len(id)-8:]
	}
	return id
}

func roleLabel(r config.Role) string {
	if r == config.RolePrimary {
		return "Primär"
	}
	return "Normal"
}

func (a *admin) renderMaintenance(w http.ResponseWriter, s sessionRef, status int, errMsg string, form *maintenanceData) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	d := maintenanceData{
		OldName: cfg.Terminal.Name, OldRole: roleLabel(cfg.Terminal.Role),
		OldHW: shortHardwareID(cfg.Terminal.HardwareID), NewHW: shortHardwareID(a.hardwareID()),
		Name: cfg.Terminal.Name, Role: string(cfg.Terminal.Role),
	}
	if form != nil {
		d.Name, d.Role = form.Name, form.Role
	}
	a.page(w, s, status, "maintenance.html", "maintenance", "Wartungsmodus", errMsg, d)
}

func (a *admin) handleMaintenancePage(w http.ResponseWriter, r *http.Request, s sessionRef) {
	cfg, _, err := a.d.Store.Load()
	if err != nil {
		http.Error(w, "Konfiguration nicht lesbar", http.StatusInternalServerError)
		return
	}
	if !a.inMaintenance(cfg) {
		redirect(w, r, "/admin/")
		return
	}
	a.renderMaintenance(w, s, http.StatusOK, "", nil)
}

func (a *admin) handleMaintenanceReplace(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if r.PostForm.Get("confirm") != "ja" {
		a.renderMaintenance(w, s, http.StatusUnprocessableEntity, "Bitte bestätigen, dass das alte Gerät außer Betrieb ist.", nil)
		return
	}
	a.finishMaintenance(w, r, s, func(*config.Config) error { return nil }, nil)
}

func (a *admin) handleMaintenanceNew(w http.ResponseWriter, r *http.Request, s sessionRef) {
	f := maintenanceData{Name: strings.TrimSpace(r.PostForm.Get("name")), Role: r.PostForm.Get("role")}
	a.finishMaintenance(w, r, s, func(c *config.Config) error {
		c.Terminal.ID = config.NewTerminalID()
		c.Terminal.Name = f.Name
		c.Terminal.Role = config.Role(f.Role)
		return nil
	}, &f)
}

// finishMaintenance übernimmt die neue Hardware-Kennung samt change, erneuert das
// Zertifikat und startet den Dienst neu. Der Wartungsmodus endet mit dem Speichern.
func (a *admin) finishMaintenance(w http.ResponseWriter, r *http.Request, s sessionRef, change func(*config.Config) error, form *maintenanceData) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	hw := a.hardwareID()
	err := a.updateLocked(func(c *config.Config) error {
		if !config.HardwareChanged(c.Terminal.HardwareID, hw) {
			return errNotInMaintenance
		}
		c.Terminal.HardwareID = hw
		return change(c)
	})
	switch {
	case errors.Is(err, errNotInMaintenance):
		redirect(w, r, "/admin/")
		return
	case err != nil:
		a.renderMaintenance(w, s, formStatus(err), err.Error(), form)
		return
	}
	msg := maintenanceFlow
	if err := a.renewCert(); err != nil {
		log.Printf("Wartungsmodus: Zertifikat nicht erneuert: %v", err)
		msg = "Gespeichert, aber das Zertifikat der Admin-Seite konnte nicht erneuert werden; der Browser warnt eventuell weiter vor dem alten."
	}
	ctx, cancel := context.WithTimeout(r.Context(), helperLimit)
	defer cancel()
	if err := a.d.Helper.Call(ctx, helperRestartService, ""); err != nil {
		log.Printf("Wartungsmodus: Dienst nicht neu gestartet: %v", err)
		msg = fmt.Sprintf("Gespeichert. Der Dienst konnte nicht neu gestartet werden (%v); bitte das Terminal neu starten.", err)
	}
	a.done(w, r, s, "/admin/hmis", msg)
}

// renewCert löscht das Zertifikat und legt ein neues an; der Dienst lädt es beim
// nächsten Start. Rechnername und Adressen des Geräts haben sich geändert.
func (a *admin) renewCert() error {
	dir := a.d.TLSDir
	if dir == "" {
		return nil
	}
	for _, name := range []string{certFile, certKeyFile} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	_, err := LoadOrCreateCert(dir)
	return err
}
