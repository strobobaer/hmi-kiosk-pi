package app

import (
	"context"
	"log"
	"net/http"
	"time"

	"hmikiosk/internal/release"
	"hmikiosk/internal/web"
)

// remoteBackupLimit begrenzt die Frage an den Helfer, ob eine Sicherung läuft.
const remoteBackupLimit = 2 * time.Second

// remoteAPI baut die Fernbefehle für PDH; sie gehören nur auf den HTTPS-Listener.
func (s *service) remoteAPI() http.Handler {
	return web.NewRemoteAPI(web.RemoteDeps{
		Config:   s.config,
		Session:  s.mgr,
		Version:  release.Version,
		NotReady: s.remoteNotReady,
	})
}

// remoteNotReady meldet eine laufende Sicherung. Ist der Helfer nicht erreichbar, läuft
// auch keine Sicherung: Das blockiert die Fernbefehle nicht.
func (s *service) remoteNotReady(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, remoteBackupLimit)
	defer cancel()
	st, err := s.helper.BackupStatus(ctx)
	if err != nil {
		log.Printf("Fernsteuerung (PDH): Stand der Sicherung nicht abrufbar: %v", err)
		return ""
	}
	if st.Running {
		return "Eine Sicherung läuft. Bitte nach ihrem Ende noch einmal versuchen."
	}
	return ""
}

// tlsFingerprint ist der Fingerabdruck des Zertifikats, das der HTTPS-Listener zeigt.
func (s *service) tlsFingerprint() string {
	if src := s.certSrc.Load(); src != nil {
		return src.PublicKeyFingerprint()
	}
	return ""
}
