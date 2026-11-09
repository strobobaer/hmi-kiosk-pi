package web

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"hmikiosk/internal/config"
	"hmikiosk/internal/updatecheck"
)

// UpdateCheckAPI sucht nach neueren Versionen und lädt nur die zuletzt gefundene.
type UpdateCheckAPI interface {
	Check(ctx context.Context) updatecheck.Result
	Last() (updatecheck.Result, bool)
	Download(ctx context.Context, version string) ([]byte, error)
}

// updatesView ist der Abschnitt „Updates“ der System-Seite.
type updatesView struct {
	Repo       string
	Prerelease bool
	Checked    bool
	At         string
	Summary    string
	Dev        bool
	Offer      *updatecheck.Offer
}

func (a *admin) updatesView(cfg config.Config, repoInput *string) *updatesView {
	if a.d.UpdateCheck == nil {
		return nil
	}
	v := &updatesView{Repo: cfg.Update.Repo, Prerelease: cfg.Update.Prerelease}
	if repoInput != nil {
		v.Repo = *repoInput
	}
	res, ok := a.d.UpdateCheck.Last()
	if !ok {
		return v
	}
	v.Checked, v.At, v.Summary, v.Dev = true, res.At.Local().Format("02.01.2006 15:04"), checkSummary(res), res.Dev
	// Ein Angebot aus einer anderen Quelle als der eingestellten gilt nicht mehr.
	if res.Repo == cfg.Update.Repo && res.Prerelease == cfg.Update.Prerelease {
		v.Offer = res.Offer
	} else {
		v.Summary += " (vor der Änderung der Quelle, bitte erneut suchen)"
	}
	return v
}

func checkSummary(res updatecheck.Result) string {
	switch {
	case res.Err != "":
		return "Fehler: " + res.Err
	case res.Dev:
		return "Entwicklungsstand, keine Update-Angebote"
	case res.Offer != nil:
		return "Version " + res.Offer.Version + " verfügbar"
	}
	return "Die laufende Version ist aktuell."
}

func (a *admin) handleUpdateCheck(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.UpdateCheck == nil {
		notFound(w)
		return
	}
	res := a.d.UpdateCheck.Check(r.Context())
	if res.Err != "" {
		a.renderSystem(w, s, http.StatusBadGateway, "Update-Prüfung fehlgeschlagen: "+res.Err, systemExtras{})
		return
	}
	a.done(w, r, s, "/admin/system", "Update-Prüfung: "+checkSummary(res))
}

func (a *admin) handleUpdateSettings(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.UpdateCheck == nil {
		notFound(w)
		return
	}
	repo := strings.TrimSpace(r.PostForm.Get("repo"))
	next := config.UpdateCfg{Repo: repo, Prerelease: r.PostForm.Get("prerelease") == "ja"}
	err := a.update(func(c *config.Config) error {
		if !config.ValidUpdateRepo(repo) {
			return errors.New("Die Quelle muss die Form Besitzer/Repository haben (wie bei GitHub) oder leer sein.")
		}
		c.Update = next
		return nil
	})
	if err != nil {
		a.renderSystem(w, s, formStatus(err), err.Error(), systemExtras{UpdateRepo: &repo})
		return
	}
	msg := "Gespeichert."
	if repo == "" {
		msg = "Gespeichert. Die Update-Prüfung ist aus."
	}
	a.done(w, r, s, "/admin/system", msg)
}

// handleUpdateInstall lädt die zuletzt gefundene Version und installiert sie auf
// demselben Weg wie ein hochgeladenes Paket.
func (a *admin) handleUpdateInstall(w http.ResponseWriter, r *http.Request, s sessionRef) {
	if a.d.UpdateCheck == nil {
		notFound(w)
		return
	}
	if !a.updateMu.TryLock() {
		a.renderSystem(w, s, http.StatusConflict, "Es läuft bereits ein Update.", systemExtras{})
		return
	}
	defer a.updateMu.Unlock()
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Now().Add(uploadDeadline))

	blob, err := a.d.UpdateCheck.Download(r.Context(), r.PostForm.Get("version"))
	if err != nil {
		a.renderSystem(w, s, http.StatusUnprocessableEntity, "Update fehlgeschlagen: "+err.Error(), systemExtras{})
		return
	}
	a.installUpdate(w, r, s, blob)
}
