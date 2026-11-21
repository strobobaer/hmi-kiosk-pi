// Package winkiosk richtet unter Windows den Kioskbetrieb ein und nimmt ihn zurück: lokaler Standardbenutzer
// PDH-Kiosk, Autologon (Passwort als LSA-Geheimnis), Shell und Richtlinien im Benutzer-Hive, Rechte am Datenordner.
// Die Schritte sind Daten (Plan) und arbeiten über die Schnittstelle Exec; plattformneutral testbar. Die
// Win32-Umsetzung steht in apply_windows.go und läuft nur auf dem Zielgerät.
package winkiosk

import (
	"crypto/rand"
	"errors"
	"fmt"
	"slices"
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
	// backupKey sichert ein fremdes Autologon, solange der Kiosk eingerichtet ist.
	backupKey = `SOFTWARE\PDH-Terminal\KioskBackup`
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
	// Profile werden über die SID angesprochen: Ein Domänenkonto CORP\PDH-Kiosk hat ebenfalls ein Profil
	// PDH-Kiosk.CORP und darf nie angefasst werden.
	UserSID(user string) (string, error)         // SID des lokalen Kontos; ErrNotFound, wenn es fehlt
	ProfilePathBySID(sid string) (string, error) // ErrNotFound, wenn es kein Profil gibt
	DeleteProfileBySID(sid string) error         // ErrNotFound, wenn es kein Profil gibt
	// FindOrphanProfiles sucht Profile des Namens ohne erinnerte SID: nur solche, deren SID zum lokalen Konto
	// PDH-Kiosk gehört oder gar nicht mehr aufgelöst werden kann; Domänen- und andere Konten bleiben außen vor.
	FindOrphanProfiles(user string) ([]string, error)
	// OwnsProfileSID prüft (auch für die gemerkte SID), ob die SID das lokale Konto user oder dessen Waise
	// mit lokalem SID-Präfix ist; nur dann darf das Profil gelöscht werden.
	OwnsProfileSID(sid, user string) (bool, error)
	LoadHive(profilePath, key string) error
	UnloadHive(key string) error // ErrNotFound, wenn nicht geladen
	SetRegString(root, path, name, value string) error
	SetRegDWORD(root, path, name string, value uint32) error
	GetRegString(root, path, name string) (string, error)
	GetRegDWORD(root, path, name string) (uint32, error)
	DeleteRegKey(root, path string) error // ErrNotFound, wenn der Schlüssel fehlt
	// LogoffUser meldet alle Sitzungen des Benutzers ab; Fehler, wenn danach noch eine besteht.
	LogoffUser(user string) error
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

const logonCount = "AutoLogonCount"

var logonStrings = []string{"AutoAdminLogon", "DefaultUserName", "DefaultDomainName"}

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

const (
	sidPath = `SOFTWARE\PDH-Terminal`
	sidName = "KioskSID"
)

// kioskSIDs sind die SIDs, deren Profile zu uns gehören: die beim Setup gemerkte SID. Fehlt sie (Setup brach vor
// dem Merken ab, oder ältere Einrichtung), gelten die SID des lokalen Kontos und die vom Exec geprüften Waisen.
func kioskSIDs(e Exec) ([]string, error) {
	rec, err := e.GetRegString(HKLM, sidPath, sidName)
	if err == nil && rec != "" {
		return []string{rec}, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	var out []string
	if sid, err := e.UserSID(KioskUser); err == nil {
		out = append(out, sid)
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	orphans, err := e.FindOrphanProfiles(KioskUser)
	if err != nil {
		return nil, err
	}
	for _, o := range orphans {
		if !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	return out, nil
}

// profilePath ist der Ordner des Kiosk-Profils; ErrNotFound, wenn es keins gibt.
func profilePath(e Exec) (string, error) {
	sids, err := kioskSIDs(e)
	if err != nil {
		return "", err
	}
	for _, sid := range sids {
		if p, err := e.ProfilePathBySID(sid); err == nil {
			return p, nil
		} else if !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}
	return "", ErrNotFound
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
	// Fremdes Autologon wird vor dem Setup gesichert (ohne Klartext-Passwort) und beim Rückbau wiederhergestellt.
	backupLogon := func(e Exec) error {
		if _, err := e.GetRegString(HKLM, backupKey, "Saved"); err == nil {
			return nil // schon gesichert (frühere Einrichtung): das Original nicht überschreiben
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if u, err := e.GetRegString(HKLM, winlogonKey, "DefaultUserName"); err == nil && strings.EqualFold(u, KioskUser) {
			return nil // ist schon unseres
		}
		for _, n := range logonStrings {
			v, err := e.GetRegString(HKLM, winlogonKey, n)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return fmt.Errorf("Autologon sichern: %w", err)
			}
			if err := e.SetRegString(HKLM, backupKey, n, v); err != nil {
				return fmt.Errorf("Autologon sichern: %w", err)
			}
		}
		if v, err := e.GetRegDWORD(HKLM, winlogonKey, logonCount); err == nil {
			if err := e.SetRegDWORD(HKLM, backupKey, logonCount, v); err != nil {
				return fmt.Errorf("Autologon sichern: %w", err)
			}
		} else if !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("Autologon sichern: %w", err)
		}
		return e.SetRegString(HKLM, backupKey, "Saved", "1") // zuletzt: erst damit gilt die Sicherung
	}
	restoreLogon := func(e Exec) error {
		if _, err := e.GetRegString(HKLM, backupKey, "Saved"); err != nil {
			return ignoreNotFound(err)
		}
		var errs []error
		for _, n := range logonStrings {
			v, err := e.GetRegString(HKLM, backupKey, n)
			if err == nil {
				errs = append(errs, e.SetRegString(HKLM, winlogonKey, n, v))
			} else if errors.Is(err, ErrNotFound) {
				errs = append(errs, ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, n)))
			} else {
				errs = append(errs, err)
			}
		}
		if v, err := e.GetRegDWORD(HKLM, backupKey, logonCount); err == nil {
			errs = append(errs, e.SetRegDWORD(HKLM, winlogonKey, logonCount, v))
		} else if errors.Is(err, ErrNotFound) {
			errs = append(errs, ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, logonCount)))
		} else {
			errs = append(errs, err)
		}
		if err := errors.Join(errs...); err != nil {
			return err // Sicherung bleibt für einen neuen Versuch
		}
		return ignoreNotFound(e.DeleteRegKey(HKLM, backupKey))
	}
	// Autologon gehört nur dann uns, wenn DefaultUserName PDH-Kiosk ist; fremde Einstellungen bleiben.
	delLogon := func(e Exec) error {
		u, err := e.GetRegString(HKLM, winlogonKey, "DefaultUserName")
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		var errs []error
		if err == nil && strings.EqualFold(u, KioskUser) {
			errs = append(errs, ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, "AutoAdminLogon")))
			errs = append(errs, ignoreNotFound(e.DeleteLSASecret(SecretName)))
			errs = append(errs, ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, "DefaultDomainName")))
			// DefaultUserName zuletzt und nur bei Erfolg: Es ist das Merkmal, an dem ein späteres -undo das Autologon als unseres erkennt.
			if err := errors.Join(errs...); err != nil {
				return err
			}
			errs = []error{ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, "DefaultUserName"))}
		}
		if len(errs) > 0 && errors.Join(errs...) != nil {
			return errors.Join(errs...)
		}
		return restoreLogon(e)
	}

	return []Step{
		{StepUser,
			func(e Exec) error {
				var err error
				if pw, err = NewPassword(); err != nil {
					return err
				}
				if err = e.CreateUser(KioskUser, pw); err != nil {
					return err
				}
				// Die SID merken: Nur sie gilt später für Profil und Rückbau.
				sid, err := e.UserSID(KioskUser)
				if err == nil {
					err = e.SetRegString(HKLM, sidPath, sidName, sid)
				}
				if err != nil { // das halb angelegte Konto nicht stehen lassen (der Rückbau erfasst diesen Schritt nicht)
					err = fmt.Errorf("SID von %s merken: %w", KioskUser, err)
					if derr := ignoreNotFound(e.DeleteUser(KioskUser)); derr != nil {
						e.Logf("Konto entfernen gescheitert: %v", derr)
						err = fmt.Errorf("%w (Rückbau unvollständig: %v)", err, derr)
					}
					_ = e.DeleteRegValue(HKLM, sidPath, sidName)
				}
				return err
			},
			func(e Exec) error {
				wipe()
				// Ist das Profil noch da (z. B. in Benutzung), bleibt das Konto: Nach Abmeldung erneut -undo.
				if _, err := profilePath(e); err == nil {
					return errors.New("Rückbau unvollständig: Profil noch vorhanden oder in Benutzung, Benutzer bleibt; nach Abmeldung erneut -undo")
				} else if !errors.Is(err, ErrNotFound) {
					return fmt.Errorf("Profil nicht prüfbar, Benutzer bleibt: %w", err)
				}
				if err := ignoreNotFound(e.DeleteUser(KioskUser)); err != nil {
					return err
				}
				return ignoreNotFound(e.DeleteRegValue(HKLM, sidPath, sidName)) // zuletzt
			}},
		{StepProfile,
			func(e Exec) error { _, err := e.CreateProfile(KioskUser); return err },
			func(e Exec) error {
				sids, err := kioskSIDs(e)
				if err != nil {
					return err
				}
				var errs []error
				for _, sid := range sids {
					if _, err := e.ProfilePathBySID(sid); errors.Is(err, ErrNotFound) {
						continue
					}
					own, err := e.OwnsProfileSID(sid, KioskUser)
					if err != nil || !own {
						if err == nil {
							err = errors.New("gehört nicht zum lokalen Konto, abgelehnt")
						}
						errs = append(errs, fmt.Errorf("Profil %s: %w", sid, err))
						continue
					}
					errs = append(errs, ignoreNotFound(e.DeleteProfileBySID(sid)))
				}
				return errors.Join(errs...)
			}},
		{StepLoad,
			func(e Exec) error {
				p, err := profilePath(e)
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
				p, err := profilePath(e)
				if err != nil {
					return ignoreNotFound(err)
				}
				return ignoreNotFound(e.LoadHive(p, HiveKey))
			}},
		{StepLogon,
			func(e Exec) (err error) {
				defer wipe()
				defer func() {
					if err != nil { // halb eingerichtetes Autologon nicht stehen lassen
						if rb := delLogon(e); rb != nil {
							e.Logf("Rückbau Autologon gescheitert: %v", rb)
							err = fmt.Errorf("%w (Rückbau unvollständig: %v)", err, rb)
						}
					}
				}()
				if err = backupLogon(e); err != nil {
					_ = e.DeleteRegKey(HKLM, backupKey) // halbe Sicherung (ohne Marke) nicht liegen lassen
					return err
				}
				// Ein Klartext-Passwort und ein Zähler würden das Autologon stören; das Passwort wird nicht gesichert.
				for _, n := range []string{"DefaultPassword", logonCount} {
					if err = ignoreNotFound(e.DeleteRegValue(HKLM, winlogonKey, n)); err != nil {
						return err
					}
				}
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
func Undo(steps []Step, e Exec) error {
	// Eine noch angemeldete Kiosk-Sitzung hält Profil und Hive fest.
	if err := e.LogoffUser(KioskUser); err != nil && !errors.Is(err, ErrNotFound) {
		e.Logf("Abmelden von %s: %v", KioskUser, err)
	}
	return undoAll(steps, e)
}

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

// LocalAccount sagt, ob domain\account das lokale Konto user dieses Rechners (host) ist; ein Domänenkonto
// gleichen Namens ist es nicht.
func LocalAccount(account, domain, user, host string) bool {
	return strings.EqualFold(account, user) && strings.EqualFold(domain, host)
}

// Resolution ist das Ergebnis der Namensauflösung einer SID.
type Resolution int

const (
	Resolved     Resolution = iota // Konto gefunden (account, domain gelten)
	Unmapped                       // Windows kennt die SID nicht (NONE_MAPPED)
	LookupFailed                   // sonstiger Fehler: Domänencontroller weg, Zugriff usw.
)

// SIDPrefix ist die SID ohne die letzte Zahl (RID): S-1-5-21-a-b-c-1000 -> S-1-5-21-a-b-c.
func SIDPrefix(sid string) string {
	if i := strings.LastIndex(sid, "-"); i > 0 {
		return sid[:i]
	}
	return ""
}

// OwnProfileSID entscheidet, ob die SID eines Profils unser lokales Konto user (oder dessen Waise) ist:
//   - aufgelöst: nur das lokale Konto user dieses Rechners (host);
//   - nicht aufgelöst (Unmapped): nur Waise, wenn das SID-Präfix das der lokalen Kontodomäne (localPrefix) ist.
//     Eine Domänen-SID, die ohne Domänencontroller nicht aufgelöst wird, ist keine Waise;
//   - jeder andere Fehler: nicht unseres.
func OwnProfileSID(sid string, res Resolution, account, domain, user, host, localPrefix string) bool {
	switch res {
	case Resolved:
		return LocalAccount(account, domain, user, host)
	case Unmapped:
		return localPrefix != "" && SIDPrefix(sid) == localPrefix
	}
	return false
}
