// Package winkiosk richtet unter Windows den Kioskbetrieb ein und nimmt ihn zurück: lokaler Standardbenutzer
// PDH-Kiosk, Autologon (Passwort als LSA-Geheimnis), Shell und Richtlinien im Benutzer-Hive, Rechte am Datenordner.
// Die Schritte sind Daten (Plan) und arbeiten über die Schnittstelle Exec; plattformneutral testbar. Die
// Win32-Umsetzung steht in apply_windows.go und läuft nur auf dem Zielgerät.
package winkiosk

import (
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
)

const (
	// KioskUser ist der lokale Standardbenutzer des Kiosks.
	KioskUser = "PDH-Kiosk"
	// HiveKey ist der Name, unter dem der Benutzer-Hive vorübergehend unter HKEY_USERS geladen wird.
	HiveKey = "PDH-Kiosk-Hive"
	// SecretName ist das LSA-Geheimnis, aus dem Winlogon das Autologon-Passwort liest.
	SecretName = "DefaultPassword"
	// PasswordLen ist die Länge des zufälligen Passworts.
	PasswordLen = 32
)

// Wurzeln der Registry-Zugriffe.
const (
	HKLM  = "HKLM"  // HKEY_LOCAL_MACHINE
	Kiosk = "KIOSK" // der geladene Hive von PDH-Kiosk
)

const (
	winlogonKey  = `SOFTWARE\Microsoft\Windows NT\CurrentVersion\Winlogon`
	userWinlogon = `Software\Microsoft\Windows NT\CurrentVersion\Winlogon`
	policiesSys  = `Software\Microsoft\Windows\CurrentVersion\Policies\System`
	policiesExpl = `Software\Microsoft\Windows\CurrentVersion\Policies\Explorer`
	desktopKey   = `Control Panel\Desktop`
	pushKey      = `Software\Microsoft\Windows\CurrentVersion\PushNotifications`
)

// ErrNotFound meldet, dass das Ziel nicht (mehr) existiert. Exec-Umsetzungen liefern es für "gibt es nicht";
// beim Rückbau ist das kein Fehler.
var ErrNotFound = errors.New("nicht vorhanden")

// Exec sind die Systemzugriffe der Schritte. Das Passwort wird als Bytes übergeben, damit es danach überschrieben
// werden kann; Umsetzungen dürfen es nie protokollieren oder in Fehlertexte schreiben.
type Exec interface {
	CreateUser(name string, password []byte) error
	DeleteUser(name string) error // ErrNotFound, wenn es den Benutzer nicht gibt
	CreateProfile(user string) (profilePath string, err error)
	DeleteProfile(user string) error // ErrNotFound, wenn es Benutzer oder Profil nicht gibt
	ProfilePath(user string) (string, error)
	LoadHive(profilePath, key string) error
	UnloadHive(key string) error // ErrNotFound, wenn nicht geladen
	SetRegString(root, path, name, value string) error
	SetRegDWORD(root, path, name string, value uint32) error
	GetRegString(root, path, name string) (string, error)
	DeleteRegValue(root, path, name string) error // ErrNotFound, wenn Wert oder Schlüssel fehlt
	StoreLSASecret(name string, value []byte) error
	DeleteLSASecret(name string) error
	DataDir() string
	SetDataACL(dir, user string) error
	RestoreDataACL(dir string) error // ErrNotFound, wenn der Ordner fehlt
	Logf(format string, args ...any)
}

// Action ist ein Teilschritt.
type Action func(Exec) error

// Step ist ein Einrichtungsschritt mit seiner Rücknahme.
type Step struct {
	Name     string
	Do, Undo Action
}

// Schrittnamen.
const (
	StepUser    = "Benutzer anlegen"
	StepProfile = "Profil anlegen"
	StepLoad    = "Hive laden"
	StepShell   = "Shell setzen"
	StepPolicy  = "Richtlinien setzen"
	StepUnload  = "Hive entladen"
	StepLogon   = "Autologon einrichten"
	StepACL     = "Datenordner-Rechte"
)

// NewPassword liefert ein zufälliges Passwort aus PasswordLen Zeichen mit Groß-, Kleinbuchstaben, Ziffern und
// Sonderzeichen (genügt der Windows-Kennwortrichtlinie).
func NewPassword() ([]byte, error) {
	const (
		upper  = "ABCDEFGHJKLMNPQRSTUVWXYZ"
		lower  = "abcdefghijkmnopqrstuvwxyz"
		digits = "23456789"
		syms   = "!#$%&*+-=?@_"
		all    = upper + lower + digits + syms
	)
	limit := byte(256 - 256%len(all))
	buf := make([]byte, 64)
	defer clear(buf)
	for {
		pw := make([]byte, 0, PasswordLen)
		for len(pw) < PasswordLen {
			if _, err := rand.Read(buf); err != nil {
				return nil, fmt.Errorf("Zufallszahlen: %w", err)
			}
			for _, b := range buf {
				if b < limit && len(pw) < PasswordLen {
					pw = append(pw, all[int(b)%len(all)])
				}
			}
		}
		s := string(pw)
		if strings.ContainsAny(s, upper) && strings.ContainsAny(s, lower) &&
			strings.ContainsAny(s, digits) && strings.ContainsAny(s, syms) {
			return pw, nil
		}
		clear(pw)
	}
}

// ShellValue ist der Wert von Winlogon\Shell für das Programmverzeichnis app.
func ShellValue(app string) string {
	return `"` + strings.TrimRight(app, `\/`) + `\pdh-terminal-start.exe" -kiosk`
}

type regValue struct {
	path, name string
}

// Die Richtlinien des Kiosk-Benutzers: DWORD 1, Zeichenfolge "0" und die Benachrichtigungen (DWORD 0).
var (
	policyDWORDs = []regValue{
		{policiesSys, "DisableTaskMgr"}, {policiesSys, "DisableLockWorkstation"}, {policiesSys, "DisableChangePassword"},
		{policiesExpl, "NoRun"}, {policiesExpl, "NoWinKeys"}, {policiesExpl, "NoViewContextMenu"},
	}
	policyStrings = []regValue{{desktopKey, "ScreenSaveActive"}, {desktopKey, "ScreenSaveIsSecure"}}
	toast         = regValue{pushKey, "ToastEnabled"}
)

func ignoreNotFound(err error) error {
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

// Plan liefert die Schritte in der Reihenfolge der Einrichtung. app ist das Programmverzeichnis.
func Plan(app string) []Step {
	var pw []byte // lebt nur zwischen Schritt 1 und 7
	wipe := func() { clear(pw); pw = nil }

	delPolicies := func(e Exec) error {
		var errs []error
		for _, v := range policyDWORDs {
			errs = append(errs, ignoreNotFound(e.DeleteRegValue(Kiosk, v.path, v.name)))
		}
		for _, v := range policyStrings {
			errs = append(errs, ignoreNotFound(e.DeleteRegValue(Kiosk, v.path, v.name)))
		}
		errs = append(errs, ignoreNotFound(e.DeleteRegValue(Kiosk, toast.path, toast.name)))
		return errors.Join(errs...)
	}
	// Autologon gehört nur dann uns, wenn DefaultUserName PDH-Kiosk ist; fremde Einstellungen bleiben.
	delLogon := func(e Exec) error {
		u, err := e.GetRegString(HKLM, winlogonKey, "DefaultUserName")
		if errors.Is(err, ErrNotFound) || (err == nil && !strings.EqualFold(u, KioskUser)) {
			return nil
		}
		if err != nil {
			return err
		}
		errs := []error{ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, "AutoAdminLogon"))}
		errs = append(errs, ignoreNotFound(e.DeleteLSASecret(SecretName)))
		errs = append(errs, ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, "DefaultDomainName")))
		errs = append(errs, ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, "DefaultUserName")))
		return errors.Join(errs...)
	}

	return []Step{
		{StepUser,
			func(e Exec) error {
				var err error
				if pw, err = NewPassword(); err != nil {
					return err
				}
				return e.CreateUser(KioskUser, pw)
			},
			func(e Exec) error { wipe(); return ignoreNotFound(e.DeleteUser(KioskUser)) }},
		{StepProfile,
			func(e Exec) error { _, err := e.CreateProfile(KioskUser); return err },
			func(e Exec) error { return ignoreNotFound(e.DeleteProfile(KioskUser)) }},
		{StepLoad,
			func(e Exec) error {
				p, err := e.ProfilePath(KioskUser)
				if err != nil {
					return err
				}
				return e.LoadHive(p, HiveKey)
			},
			func(e Exec) error { return ignoreNotFound(e.UnloadHive(HiveKey)) }},
		{StepShell,
			func(e Exec) error { return e.SetRegString(Kiosk, userWinlogon, "Shell", ShellValue(app)) },
			func(e Exec) error { return ignoreNotFound(e.DeleteRegValue(Kiosk, userWinlogon, "Shell")) }},
		{StepPolicy,
			func(e Exec) error {
				for _, v := range policyDWORDs {
					if err := e.SetRegDWORD(Kiosk, v.path, v.name, 1); err != nil {
						return fmt.Errorf("%s: %w", v.name, err)
					}
				}
				for _, v := range policyStrings {
					if err := e.SetRegString(Kiosk, v.path, v.name, "0"); err != nil {
						return fmt.Errorf("%s: %w", v.name, err)
					}
				}
				if err := e.SetRegDWORD(Kiosk, toast.path, toast.name, 0); err != nil {
					return fmt.Errorf("%s: %w", toast.name, err)
				}
				return nil
			},
			delPolicies},
		{StepUnload,
			func(e Exec) error { return e.UnloadHive(HiveKey) },
			// Rücknahme: Hive wieder laden, damit die Schritte davor ihre Werte entfernen können.
			func(e Exec) error {
				p, err := e.ProfilePath(KioskUser)
				if err != nil {
					return ignoreNotFound(err)
				}
				return ignoreNotFound(e.LoadHive(p, HiveKey))
			}},
		{StepLogon,
			func(e Exec) (err error) {
				defer wipe()
				defer func() {
					if err != nil {
						_ = delLogon(e) // halb eingerichtetes Autologon nicht stehen lassen
					}
				}()
				if err = e.SetRegString(HKLM, winlogonKey, "DefaultUserName", KioskUser); err != nil {
					return err
				}
				if err = e.SetRegString(HKLM, winlogonKey, "DefaultDomainName", "."); err != nil {
					return err
				}
				if err = e.StoreLSASecret(SecretName, pw); err != nil {
					return err
				}
				return e.SetRegString(HKLM, winlogonKey, "AutoAdminLogon", "1")
			},
			delLogon},
		{StepACL,
			func(e Exec) error { return e.SetDataACL(e.DataDir(), KioskUser) },
			func(e Exec) error { return ignoreNotFound(e.RestoreDataACL(e.DataDir())) }},
	}
}

// Run führt die Schritte aus. Scheitert einer, werden die schon gemachten rückwärts zurückgenommen; der Fehler
// lautet "Schritt <Name>: <Fehler>" (mit Hinweis, wenn auch der Rückbau scheiterte).
func Run(steps []Step, e Exec) error {
	for i, s := range steps {
		e.Logf("Schritt %s", s.Name)
		if err := s.Do(e); err != nil {
			e.Logf("Schritt %s gescheitert: %v", s.Name, err)
			if rb := undoAll(steps[:i], e); rb != nil {
				return fmt.Errorf("Schritt %s: %w (Rückbau unvollständig: %v)", s.Name, err, rb)
			}
			return fmt.Errorf("Schritt %s: %w", s.Name, err)
		}
	}
	return nil
}

// Undo nimmt alle Schritte rückwärts zurück. Fehlendes ist kein Fehler, daher ist Undo wiederholbar; andere
// Fehler stoppen den Rückbau nicht, werden aber gesammelt gemeldet.
func Undo(steps []Step, e Exec) error { return undoAll(steps, e) }

func undoAll(steps []Step, e Exec) error {
	var errs []error
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		e.Logf("Zurück: %s", s.Name)
		if err := ignoreNotFound(s.Undo(e)); err != nil {
			e.Logf("Zurück %s gescheitert: %v", s.Name, err)
			errs = append(errs, fmt.Errorf("Schritt %s: %w", s.Name, err))
		}
	}
	return errors.Join(errs...)
}

// EditionSupported sagt, ob der Kiosk auf der Edition (EditionID) läuft: Professional*, Enterprise*, Education*.
// Core* (Home) und alles Unbekannte ist nein.
func EditionSupported(editionID string) bool {
	for _, p := range []string{"Professional", "Enterprise", "Education"} {
		if strings.HasPrefix(editionID, p) {
			return true
		}
	}
	return false
}
