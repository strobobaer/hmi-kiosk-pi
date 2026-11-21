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
	aclSID   string // SID, für die SetDataACL zuletzt gesetzt wurde
	dataDir  bool   // gibt es den Datenordner?

	calls       []string
	logs        []string
	passwords   []string // alle je übergebenen Passwörter
	failOn      string   // Aufruf (Name oder Name|Argument), der scheitert
	failErr     error
	extraFail   map[string]error  // weitere einmalige Ausfälle (z. B. im Rückbau)
	loggedOn    bool              // Kiosk-Sitzung hält das Profil
	accounts    map[string]string // SID -> Domäne, für auflösbare Fremdkonten (z. B. CORP)
	lookupFails map[string]bool   // SIDs, deren Auflösung scheitert
	stuckLogon  bool              // Abmelden gelingt nicht
}

func newFake() *fake {
	return &fake{users: map[string]bool{}, profiles: map[string]string{}, loaded: map[string]string{},
		accounts: map[string]string{}, regs: map[string]string{}, secrets: map[string]string{}, acl: "zusatz", dataDir: true}
}

func (f *fake) rec(name, arg string) error {
	c := name
	if arg != "" {
		c += "|" + arg
	}
	f.calls = append(f.calls, c)
	if err, ok := f.extraFail[c]; ok {
		delete(f.extraFail, c)
		return err
	}
	if f.failOn != "" && (f.failOn == c || f.failOn == name) {
		f.failOn = "" // einmalig
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
	f.accounts[sidOf(name)] = "HOST"
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
	delete(f.accounts, sidOf(name)) // ab jetzt unauflösbar
	return nil
}
func sidOf(user string) string { return "S-1-5-21-1-1001" } // nur der Kiosk-Benutzer kommt vor

const foreignSID = "S-1-5-21-9-1000" // Domänenkonto CORP\PDH-Kiosk mit Profil PDH-Kiosk.CORP

func (f *fake) CreateProfile(user string) (string, error) {
	if err := f.rec("CreateProfile", user); err != nil {
		return "", err
	}
	p := `C:\Users\` + user
	f.profiles[sidOf(user)] = p
	return p, nil
}
func (f *fake) UserSID(user string) (string, error) {
	if !f.users[user] {
		return "", ErrNotFound
	}
	return sidOf(user), nil
}
func (f *fake) DeleteProfileBySID(sid string) error {
	if err := f.rec("DeleteProfileBySID", sid); err != nil {
		return err
	}
	if _, ok := f.profiles[sid]; !ok {
		return ErrNotFound
	}
	if f.loggedOn && sid == sidOf(KioskUser) {
		return errors.New("Profil in Benutzung")
	}
	delete(f.profiles, sid)
	if sid == sidOf(KioskUser) {
		for k := range f.regs {
			if strings.HasPrefix(k, Kiosk+"|") {
				delete(f.regs, k)
			}
		}
	}
	return nil
}
func (f *fake) ProfilePathBySID(sid string) (string, error) {
	if p, ok := f.profiles[sid]; ok {
		return p, nil
	}
	return "", ErrNotFound
}

// FindOrphanProfiles bildet die Auswahl der Windows-Umsetzung nach: Namenstreffer, aber nur eigene oder
// unauflösbare SIDs.
func (f *fake) FindOrphanProfiles(user string) ([]string, error) {
	f.calls = append(f.calls, "FindOrphanProfiles")
	var out []string
	for sid, p := range f.profiles {
		base := p[strings.LastIndex(p, `\`)+1:]
		if base != user && !strings.HasPrefix(base, user+".") {
			continue
		}
		if own, _ := f.OwnsProfileSID(sid, user); own {
			out = append(out, sid)
		}
	}
	return out, nil
}
func (f *fake) LoadHive(path, key string) error {
	if err := f.rec("LoadHive", key); err != nil {
		return err
	}
	if f.loggedOn {
		return errors.New("Sharing-Verletzung")
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
	if err := f.rec("SetRegString", f.argFor(path, name)); err != nil {
		return err
	}
	if !f.hiveOK(root) {
		return errors.New("Hive nicht geladen")
	}
	f.regs[f.k(root, path, name)] = "S:" + v
	return nil
}
func (f *fake) SetRegDWORD(root, path, name string, v uint32) error {
	if err := f.rec("SetRegDWORD", f.argFor(path, name)); err != nil {
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
	if !strings.HasPrefix(v, "S:") {
		return "", errors.New("falscher Typ")
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
func (f *fake) SetDataACL(dir, sid string) error {
	if err := f.rec("SetDataACL", sid); err != nil {
		return err
	}
	f.acl, f.aclSID = "kiosk", sid
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
	if len(f.users) != 0 || len(f.profiles) != f.foreignCount() || len(f.loaded) != 0 || len(f.regs) != 0 || len(f.secrets) != 0 || f.acl != "zusatz" {
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
	if f.aclSID != sidOf(KioskUser) {
		t.Errorf("Datenordner-Rechte für SID %q, erwartet %q", f.aclSID, sidOf(KioskUser))
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
	var got []string
	for _, c := range f.calls[i+1:] {
		if !strings.HasPrefix(c, "GetReg") && c != "FindOrphanProfiles" {
			got = append(got, c)
		}
	}
	want := []string{"DeleteRegValue|Shell", "UnloadHive|" + HiveKey, "DeleteProfileBySID|" + sidOf(KioskUser), "DeleteUser|" + KioskUser, "DeleteRegValue|" + sidName}
	if !slices.Equal(got, want) {
		t.Errorf("Rückbau: %v, erwartet %v", got, want)
	}
	f.settled(t)
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

func TestEditionSupported(t *testing.T) {
	for id, want := range map[string]bool{
		"Professional": true, "ProfessionalN": true, "ProfessionalWorkstation": true, "Enterprise": true,
		"EnterpriseS": true, "Education": true, "IoTEnterprise": true, "IoTEnterpriseS": true, "IoTEnterpriseSK": true, "IoTCore": false, "Core": false, "CoreSingleLanguage": false, "CoreN": false, "": false,
		"ServerStandard": false,
	} {
		if got := EditionSupported(id); got != want {
			t.Errorf("EditionSupported(%q) = %v", id, got)
		}
	}
}
func (f *fake) GetRegDWORD(root, path, name string) (uint32, error) {
	f.calls = append(f.calls, "GetRegDWORD|"+name)
	v, ok := f.regs[f.k(root, path, name)]
	if !ok || !f.hiveOK(root) || !strings.HasPrefix(v, "D:") {
		return 0, ErrNotFound
	}
	var n uint32
	fmt.Sscanf(strings.TrimPrefix(v, "D:"), "%d", &n)
	return n, nil
}
func (f *fake) DeleteRegKey(root, path string) error {
	if err := f.rec("DeleteRegKey", ""); err != nil {
		return err
	}
	found := false
	for k := range f.regs {
		if strings.HasPrefix(k, root+"|"+path+"|") {
			delete(f.regs, k)
			found = true
		}
	}
	if !found {
		return ErrNotFound
	}
	return nil
}
func (f *fake) LogoffUser(user string) error {
	if err := f.rec("LogoffUser", user); err != nil {
		return err
	}
	if f.stuckLogon {
		return errors.New("Sitzung besteht noch")
	}
	f.loggedOn = false
	return nil
}

// argFor unterscheidet Schreibzugriffe auf die Sicherung (B:) von denen auf Winlogon.
func (f *fake) argFor(path, name string) string {
	if path == backupKey {
		return "B:" + name
	}
	return name
}

func (f *fake) foreignCount() int {
	if _, ok := f.profiles[foreignSID]; ok {
		return 1
	}
	return 0
}

const localPrefix = "S-1-5-21-1" // lokale Kontodomäne des Fake-Rechners

// OwnsProfileSID bildet die Windows-Umsetzung nach: aufgelöste SIDs über accounts (Domäne), sonst Unmapped;
// f.lookupFails markiert SIDs, deren Auflösung scheitert (Domänencontroller weg).
func (f *fake) OwnsProfileSID(sid, user string) (bool, error) {
	dom, ok := f.accounts[sid]
	res := Unmapped
	switch {
	case f.lookupFails[sid]:
		res = LookupFailed
	case ok:
		res = Resolved
	}
	account := ""
	if res == Resolved {
		account = user // die Fake-Konten heißen alle wie der Benutzer
	}
	return OwnProfileSID(sid, res, account, dom, user, "HOST", localPrefix), nil
}
