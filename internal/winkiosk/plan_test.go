package winkiosk

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// fake ist ein Speicher-Abbild des Systems; es berührt nichts außerhalb des Tests.
type fake struct {
	users    map[string]bool
	profiles map[string]string
	loaded   map[string]string // Hive-Schlüssel -> Profilpfad
	regs     map[string]string // "root|pfad|name" -> Wert (mit Typ-Präfix)
	secrets  map[string]string
	acl      string // "", "kiosk" oder "zusatz"
	dataDir  bool   // gibt es den Datenordner?

	calls     []string
	logs      []string
	passwords []string // alle je übergebenen Passwörter
	failOn    string   // Aufruf (Name oder Name|Argument), der scheitert
	failErr   error
}

func newFake() *fake {
	return &fake{users: map[string]bool{}, profiles: map[string]string{}, loaded: map[string]string{},
		regs: map[string]string{}, secrets: map[string]string{}, acl: "zusatz", dataDir: true}
}

func (f *fake) rec(name, arg string) error {
	c := name
	if arg != "" {
		c += "|" + arg
	}
	f.calls = append(f.calls, c)
	if f.failOn != "" && (f.failOn == c || f.failOn == name) {
		return f.failErr
	}
	return nil
}

func (f *fake) CreateUser(name string, pw []byte) error {
	f.passwords = append(f.passwords, string(pw))
	if err := f.rec("CreateUser", name); err != nil {
		return err
	}
	f.users[name] = true
	return nil
}
func (f *fake) DeleteUser(name string) error {
	if err := f.rec("DeleteUser", name); err != nil {
		return err
	}
	if !f.users[name] {
		return ErrNotFound
	}
	delete(f.users, name)
	return nil
}
func (f *fake) CreateProfile(user string) (string, error) {
	if err := f.rec("CreateProfile", user); err != nil {
		return "", err
	}
	p := `C:\Users\` + user
	f.profiles[user] = p
	return p, nil
}
func (f *fake) DeleteProfile(user string) error {
	if err := f.rec("DeleteProfile", user); err != nil {
		return err
	}
	if _, ok := f.profiles[user]; !ok {
		return ErrNotFound
	}
	delete(f.profiles, user)
	for k := range f.regs {
		if strings.HasPrefix(k, Kiosk+"|") {
			delete(f.regs, k)
		}
	}
	return nil
}
func (f *fake) ProfilePath(user string) (string, error) {
	if p, ok := f.profiles[user]; ok {
		return p, nil
	}
	return "", ErrNotFound
}
func (f *fake) LoadHive(path, key string) error {
	if err := f.rec("LoadHive", key); err != nil {
		return err
	}
	f.loaded[key] = path
	return nil
}
func (f *fake) UnloadHive(key string) error {
	if err := f.rec("UnloadHive", key); err != nil {
		return err
	}
	if _, ok := f.loaded[key]; !ok {
		return ErrNotFound
	}
	delete(f.loaded, key)
	return nil
}

func (f *fake) k(root, path, name string) string { return root + "|" + path + "|" + name }

// hiveOK: Werte im Kiosk-Hive gehen nur, solange er geladen ist (und es das Profil noch gibt).
func (f *fake) hiveOK(root string) bool {
	if root != Kiosk {
		return true
	}
	_, ok := f.loaded[HiveKey]
	return ok
}
func (f *fake) SetRegString(root, path, name, v string) error {
	if err := f.rec("SetRegString", name); err != nil {
		return err
	}
	if !f.hiveOK(root) {
		return errors.New("Hive nicht geladen")
	}
	f.regs[f.k(root, path, name)] = "S:" + v
	return nil
}
func (f *fake) SetRegDWORD(root, path, name string, v uint32) error {
	if err := f.rec("SetRegDWORD", name); err != nil {
		return err
	}
	if !f.hiveOK(root) {
		return errors.New("Hive nicht geladen")
	}
	f.regs[f.k(root, path, name)] = fmt.Sprintf("D:%d", v)
	return nil
}
func (f *fake) GetRegString(root, path, name string) (string, error) {
	f.calls = append(f.calls, "GetRegString|"+name)
	v, ok := f.regs[f.k(root, path, name)]
	if !ok || !f.hiveOK(root) {
		return "", ErrNotFound
	}
	return strings.TrimPrefix(v, "S:"), nil
}
func (f *fake) DeleteRegValue(root, path, name string) error {
	if err := f.rec("DeleteRegValue", name); err != nil {
		return err
	}
	if !f.hiveOK(root) {
		return ErrNotFound
	}
	if _, ok := f.regs[f.k(root, path, name)]; !ok {
		return ErrNotFound
	}
	delete(f.regs, f.k(root, path, name))
	return nil
}
func (f *fake) StoreLSASecret(name string, v []byte) error {
	f.passwords = append(f.passwords, string(v))
	if err := f.rec("StoreLSASecret", name); err != nil {
		return err
	}
	f.secrets[name] = string(v)
	return nil
}
func (f *fake) DeleteLSASecret(name string) error {
	if err := f.rec("DeleteLSASecret", name); err != nil {
		return err
	}
	if _, ok := f.secrets[name]; !ok {
		return ErrNotFound
	}
	delete(f.secrets, name)
	return nil
}
func (f *fake) DataDir() string { return `C:\ProgramData\PDH-Terminal` }
func (f *fake) SetDataACL(dir, user string) error {
	if err := f.rec("SetDataACL", user); err != nil {
		return err
	}
	f.acl = "kiosk"
	return nil
}
func (f *fake) RestoreDataACL(dir string) error {
	if err := f.rec("RestoreDataACL", ""); err != nil {
		return err
	}
	if !f.dataDir {
		return ErrNotFound
	}
	f.acl = "zusatz"
	return nil
}
func (f *fake) Logf(format string, args ...any) {
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

// settled: nach Rückbau ist alles wie vorher.
func (f *fake) settled(t *testing.T) {
	t.Helper()
	if len(f.users) != 0 || len(f.profiles) != 0 || len(f.loaded) != 0 || len(f.regs) != 0 || len(f.secrets) != 0 || f.acl != "zusatz" {
		t.Errorf("Rückbau unvollständig: users=%v profiles=%v loaded=%v regs=%v secrets=%d acl=%s",
			f.users, f.profiles, f.loaded, f.regs, len(f.secrets), f.acl)
	}
}

const app = `C:\Program Files\PDH-Terminal`

func TestPlanOrderAndValues(t *testing.T) {
	steps := Plan(app)
	var names []string
	for _, s := range steps {
		names = append(names, s.Name)
	}
	want := []string{StepUser, StepProfile, StepLoad, StepShell, StepPolicy, StepUnload, StepLogon, StepACL}
	if !slices.Equal(names, want) {
		t.Fatalf("Reihenfolge: %v", names)
	}
	f := newFake()
	if err := Run(steps, f); err != nil {
		t.Fatal(err)
	}
	k := func(root, path, name string) string { return f.regs[f.k(root, path, name)] }
	checks := map[string]string{
		k(HKLM, winlogonKey, "AutoAdminLogon"):          "S:1",
		k(HKLM, winlogonKey, "DefaultUserName"):         "S:PDH-Kiosk",
		k(HKLM, winlogonKey, "DefaultDomainName"):       "S:.",
		k(Kiosk, userWinlogon, "Shell"):                 `S:"C:\Program Files\PDH-Terminal\pdh-terminal-start.exe" -kiosk`,
		k(Kiosk, policiesSys, "DisableTaskMgr"):         "D:1",
		k(Kiosk, policiesSys, "DisableLockWorkstation"): "D:1",
		k(Kiosk, policiesSys, "DisableChangePassword"):  "D:1",
		k(Kiosk, policiesExpl, "NoRun"):                 "D:1",
		k(Kiosk, policiesExpl, "NoWinKeys"):             "D:1",
		k(Kiosk, policiesExpl, "NoViewContextMenu"):     "D:1",
		k(Kiosk, desktopKey, "ScreenSaveActive"):        "S:0",
		k(Kiosk, desktopKey, "ScreenSaveIsSecure"):      "S:0",
		k(Kiosk, pushKey, "ToastEnabled"):               "D:0",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("Wert %q, erwartet %q", got, want)
		}
	}
	if !f.users[KioskUser] || f.acl != "kiosk" || len(f.loaded) != 0 {
		t.Errorf("Endzustand: users=%v acl=%s loaded=%v", f.users, f.acl, f.loaded)
	}
	if len(f.secrets[SecretName]) != PasswordLen {
		t.Errorf("LSA-Geheimnis fehlt oder falsche Länge")
	}
	if f.passwords[0] != f.secrets[SecretName] {
		t.Errorf("Benutzer und Autologon müssen dasselbe Passwort haben")
	}
}

func TestFailureRollsBackInReverse(t *testing.T) {
	f := newFake()
	f.failOn, f.failErr = "SetRegDWORD|DisableLockWorkstation", errors.New("Zugriff verweigert")
	err := Run(Plan(app), f)
	if err == nil || err.Error() != "Schritt Richtlinien setzen: DisableLockWorkstation: Zugriff verweigert" {
		t.Fatalf("Fehler: %v", err)
	}
	// Nach dem Fehlschlag: 4, 3, 2, 1 zurück.
	i := slices.Index(f.calls, "SetRegDWORD|DisableLockWorkstation")
	got := f.calls[i+1:]
	want := []string{"DeleteRegValue|Shell", "UnloadHive|" + HiveKey, "DeleteProfile|" + KioskUser, "DeleteUser|" + KioskUser}
	if !slices.Equal(got, want) {
		t.Errorf("Rückbau: %v, erwartet %v", got, want)
	}
	f.settled(t)
}

func TestFailureAtEveryStepRestores(t *testing.T) {
	for _, step := range []string{"CreateUser", "CreateProfile", "LoadHive", "SetRegString|Shell", "SetRegDWORD|ToastEnabled",
		"UnloadHive", "SetRegString|DefaultUserName", "SetRegString|DefaultDomainName", "StoreLSASecret", "SetRegString|AutoAdminLogon", "SetDataACL"} {
		f := newFake()
		f.failOn, f.failErr = step, errors.New("kaputt")
		err := Run(Plan(app), f)
		if err == nil || !strings.HasPrefix(err.Error(), "Schritt ") || !strings.Contains(err.Error(), "kaputt") {
			t.Errorf("%s: Fehler %v", step, err)
		}
		// Der Rückbau läuft ohne den ausgefallenen Aufruf; ein klemmender Rückbau-Aufruf zählt hier nicht.
		f.failOn = ""
		if uerr := Undo(Plan(app), f); uerr != nil {
			t.Errorf("%s: Undo danach: %v", step, uerr)
		}
		f.settled(t)
	}
}

func TestUndoWithoutKioskIsHarmless(t *testing.T) {
	f := newFake()
	// Fremdes Autologon (anderer Benutzer) und fremdes Geheimnis bleiben unberührt.
	f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] = "S:Hans"
	f.regs[f.k(HKLM, winlogonKey, "AutoAdminLogon")] = "S:1"
	f.secrets[SecretName] = "fremd"
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] != "S:Hans" || f.regs[f.k(HKLM, winlogonKey, "AutoAdminLogon")] != "S:1" || f.secrets[SecretName] != "fremd" {
		t.Errorf("fremdes Autologon wurde angefasst: %v %v", f.regs, f.secrets)
	}
	if err := Undo(Plan(app), newFake()); err != nil {
		t.Fatalf("leeres System: %v", err)
	}
}

func TestUndoTwiceAfterSetup(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := Undo(Plan(app), f); err != nil {
			t.Fatalf("Undo %d: %v", i, err)
		}
		f.settled(t)
	}
}

func TestSetupRepeatableAfterUndo(t *testing.T) {
	f := newFake()
	for i := 0; i < 2; i++ {
		if err := Run(Plan(app), f); err != nil {
			t.Fatalf("Lauf %d: %v", i, err)
		}
		if err := Undo(Plan(app), f); err != nil {
			t.Fatal(err)
		}
	}
	f.settled(t)
}

func TestUndoReportsRealErrorsButContinues(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.failOn, f.failErr = "DeleteProfile", errors.New("Profil in Benutzung")
	err := Undo(Plan(app), f)
	if err == nil || !strings.Contains(err.Error(), "Profil in Benutzung") {
		t.Fatalf("Fehler fehlt: %v", err)
	}
	if f.users[KioskUser] {
		t.Errorf("Rückbau sollte trotz Fehler weiterlaufen")
	}
}

func TestPasswordNeverLeaks(t *testing.T) {
	for _, fail := range []string{"", "CreateUser", "CreateProfile", "StoreLSASecret", "SetRegString|AutoAdminLogon", "SetDataACL"} {
		f := newFake()
		f.failOn, f.failErr = fail, errors.New("kaputt")
		err := Run(Plan(app), f)
		if err == nil && fail == "" {
			err = Undo(Plan(app), f)
		}
		text := strings.Join(f.logs, "\n")
		if err != nil {
			text += "\n" + err.Error()
		}
		for _, pw := range f.passwords {
			if pw != "" && strings.Contains(text, pw) {
				t.Errorf("%q: Passwort in Protokoll oder Fehler", fail)
			}
		}
		if len(f.passwords) == 0 {
			t.Errorf("%q: Test prüft nichts (kein Passwort gesehen)", fail)
		}
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw, err := NewPassword()
		if err != nil {
			t.Fatal(err)
		}
		s := string(pw)
		if len(s) != PasswordLen {
			t.Fatalf("Länge %d", len(s))
		}
		for _, set := range []string{"ABCDEFGHJKLMNPQRSTUVWXYZ", "abcdefghijkmnopqrstuvwxyz", "23456789", "!#$%&*+-=?@_"} {
			if !strings.ContainsAny(s, set) {
				t.Fatalf("Zeichenklasse %q fehlt in %q", set, s)
			}
		}
		if seen[s] {
			t.Fatal("Passwort wiederholt sich")
		}
		seen[s] = true
	}
}

func TestPasswordWipedAfterSetup(t *testing.T) {
	f := newFake()
	// Das Passwort, das das Fake sah, ist eine Kopie; der Plan selbst darf nach Schritt 7 nichts mehr halten.
	// Geprüft wird indirekt: Ein zweiter Lauf desselben Plans erzeugt ein neues Passwort.
	steps := Plan(app)
	if err := Run(steps, f); err != nil {
		t.Fatal(err)
	}
	first := f.passwords[0]
	if err := Undo(steps, f); err != nil {
		t.Fatal(err)
	}
	if err := Run(steps, f); err != nil {
		t.Fatal(err)
	}
	if f.passwords[2] == first {
		t.Errorf("Passwort wurde wiederverwendet")
	}
}

func TestEditionSupported(t *testing.T) {
	for id, want := range map[string]bool{
		"Professional": true, "ProfessionalN": true, "ProfessionalWorkstation": true, "Enterprise": true,
		"EnterpriseS": true, "Education": true, "Core": false, "CoreSingleLanguage": false, "CoreN": false, "": false,
		"ServerStandard": false,
	} {
		if got := EditionSupported(id); got != want {
			t.Errorf("EditionSupported(%q) = %v", id, got)
		}
	}
}
