package winkiosk

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

func TestRunRollsBackItselfAtEveryStep(t *testing.T) {
	for _, step := range []string{"CreateUser", "CreateProfile", "LoadHive", "SetRegString|Shell", "SetRegDWORD|ToastEnabled",
		"UnloadHive", "SetRegString|DefaultUserName", "SetRegString|DefaultDomainName", "StoreLSASecret", "SetRegString|AutoAdminLogon",
		"SetRegString|KioskSID", "SetRegString|B:DefaultUserName", "SetRegString|B:Saved", "SetDataACL"} {
		f := newFake()
		f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] = "S:Hans" // fremdes Autologon muss danach wieder da sein
		f.regs[f.k(HKLM, winlogonKey, "AutoAdminLogon")] = "S:1"
		f.failOn, f.failErr = step, errors.New("kaputt")
		err := Run(Plan(app), f)
		if err == nil || !strings.HasPrefix(err.Error(), "Schritt ") || !strings.Contains(err.Error(), "kaputt") {
			t.Errorf("%s: Fehler %v", step, err)
		}
		// Endzustand direkt nach Run, ohne weiteres Undo: der fremde Benutzername ist wieder da, sonst nichts.
		// AutoAdminLogon wird nie wiederhergestellt (R8: das Passwort ist nicht sicher zu sichern), sobald das
		// Autologon angefasst wurde; davor blieb es unberührt.
		wantAuto := "S:1"
		if slices.Contains(f.calls, "DeleteRegValue|AutoAdminLogon") {
			wantAuto = ""
		}
		if f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] != "S:Hans" || f.regs[f.k(HKLM, winlogonKey, "AutoAdminLogon")] != wantAuto {
			t.Errorf("%s: fremdes Autologon: %v", step, f.regs)
		}
		delete(f.regs, f.k(HKLM, winlogonKey, "DefaultUserName"))
		delete(f.regs, f.k(HKLM, winlogonKey, "AutoAdminLogon"))
		f.settled(t)
	}
}

func TestRunRollbackFailureIsReportedAndLaterUndoCompletes(t *testing.T) {
	f := newFake()
	f.failOn, f.failErr = "SetRegDWORD|NoRun", errors.New("kaputt")
	f.extraFail = map[string]error{"UnloadHive|" + HiveKey: errors.New("Hive klemmt")}
	err := Run(Plan(app), f)
	if err == nil || !strings.Contains(err.Error(), "kaputt") || !strings.Contains(err.Error(), "Rückbau unvollständig") || !strings.Contains(err.Error(), "Hive klemmt") {
		t.Fatalf("Fehler: %v", err)
	}
	if err := Undo(Plan(app), f); err != nil {
		t.Fatalf("späteres Undo: %v", err)
	}
	f.settled(t)
}

func TestLogonRollbackFailureIsNotSwallowed(t *testing.T) {
	f := newFake()
	f.failOn, f.failErr = "SetRegString|AutoAdminLogon", errors.New("kaputt")
	f.extraFail = map[string]error{"DeleteLSASecret|DefaultPassword": errors.New("LSA klemmt")}
	err := Run(Plan(app), f)
	if err == nil || !strings.Contains(err.Error(), "LSA klemmt") || !strings.Contains(err.Error(), "Rückbau unvollständig") {
		t.Fatalf("Rückbaufehler fehlt: %v", err)
	}
	if !strings.Contains(strings.Join(f.logs, "\n"), "LSA klemmt") {
		t.Errorf("Rückbaufehler nicht protokolliert")
	}
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.settled(t)
}

func TestUndoReportsRealErrorsButContinues(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.failOn, f.failErr = "RestoreDataACL", errors.New("ACL klemmt")
	err := Undo(Plan(app), f)
	if err == nil || !strings.Contains(err.Error(), "ACL klemmt") {
		t.Fatalf("Fehler fehlt: %v", err)
	}
	if f.users[KioskUser] || len(f.profiles) != 0 || len(f.secrets) != 0 {
		t.Errorf("Rückbau sollte trotz Fehler weiterlaufen")
	}
}

func TestUndoKeepsUserWhileProfileInUse(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.loggedOn, f.stuckLogon = true, true
	err := Undo(Plan(app), f)
	if err == nil || !strings.Contains(err.Error(), "Benutzer bleibt") || !strings.Contains(err.Error(), "erneut -undo") {
		t.Fatalf("Meldung: %v", err)
	}
	if !f.users[KioskUser] || len(f.profiles) != 1 {
		t.Fatalf("Benutzer und Profil müssen für den nächsten Versuch bleiben: %v %v", f.users, f.profiles)
	}
	if !slices.Contains(f.calls, "LogoffUser|"+KioskUser) {
		t.Errorf("Abmelden nicht versucht")
	}
	// Abmeldung erfolgt: erneutes -undo räumt alles weg.
	f.loggedOn, f.stuckLogon = false, false
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.settled(t)
}

func TestUndoLogsOffThenCleans(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.loggedOn = true // LogoffUser gelingt und gibt das Profil frei
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.settled(t)
}

func TestUndoCleansOrphanProfileWithoutAccount(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	delete(f.users, KioskUser) // Konto weg, Profil blieb (ProfileList)
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.settled(t)
}

func TestForeignAutologonBackedUpAndRestored(t *testing.T) {
	f := newFake()
	set := func(n, v string) { f.regs[f.k(HKLM, winlogonKey, n)] = v }
	set("AutoAdminLogon", "S:1")
	set("DefaultUserName", "S:Hans")
	set("DefaultDomainName", "S:FIRMA")
	set("AutoLogonCount", "D:3")
	set("DefaultPassword", "S:klartext-geheim")
	orig := maps.Clone(f.regs)
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.regs[f.k(HKLM, winlogonKey, "DefaultPassword")]; ok {
		t.Errorf("Klartext-Passwort nicht gelöscht")
	}
	if _, ok := f.regs[f.k(HKLM, winlogonKey, "AutoLogonCount")]; ok {
		t.Errorf("AutoLogonCount nicht gelöscht")
	}
	if f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] != "S:"+KioskUser {
		t.Errorf("Autologon nicht eingerichtet")
	}
	for k, v := range f.regs {
		if strings.Contains(v, "klartext-geheim") {
			t.Errorf("Klartext-Passwort noch vorhanden/gesichert: %s", k)
		}
	}
	if f.regs[f.k(HKLM, backupKey, "DefaultUserName")] != "S:Hans" || f.regs[f.k(HKLM, backupKey, "AutoLogonCount")] != "D:3" {
		t.Errorf("Sicherung fehlt: %v", f.regs)
	}
	for i := 0; i < 2; i++ { // auch wiederholt
		if err := Undo(Plan(app), f); err != nil {
			t.Fatal(err)
		}
	}
	// Wiederhergestellt: Benutzername, Domäne und Zähler. Nicht wiederhergestellt (R8): AutoAdminLogon (das Autologon
	// bleibt aus, bis es mit seinem Passwort neu eingeschaltet wird) und das gelöschte Klartext-Passwort; Sicherung weg.
	delete(orig, f.k(HKLM, winlogonKey, "DefaultPassword"))
	delete(orig, f.k(HKLM, winlogonKey, "AutoAdminLogon"))
	if !maps.Equal(f.regs, orig) {
		t.Fatalf("Zustand: %v, erwartet %v", f.regs, orig)
	}
	if len(f.secrets) != 0 {
		t.Errorf("Geheimnis blieb")
	}
}

func TestSecondSetupKeepsOriginalBackup(t *testing.T) {
	f := newFake()
	f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] = "S:Hans"
	for i := 0; i < 2; i++ {
		if err := Run(Plan(app), f); err != nil { // zweites Setup ohne Undo dazwischen
			t.Fatal(err)
		}
	}
	if f.regs[f.k(HKLM, backupKey, "DefaultUserName")] != "S:Hans" {
		t.Errorf("Original überschrieben: %v", f.regs)
	}
}

// Jeder Lauf desselben Plans erzeugt ein frisches Passwort; der Plan hält das alte nach Schritt 7 nicht mehr.
func TestEveryRunUsesFreshPassword(t *testing.T) {
	f := newFake()
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

// R8: Nach dem Rückbau ist das Autologon aus (AutoAdminLogon fehlt), auch wenn vorher ein fremdes lief; Benutzername
// und Domäne sind wieder da.
func TestUndoDoesNotRestoreAutoAdminLogon(t *testing.T) {
	f := newFake()
	set := func(n, v string) { f.regs[f.k(HKLM, winlogonKey, n)] = v }
	set("AutoAdminLogon", "S:1")
	set("DefaultUserName", "S:Hans")
	set("DefaultDomainName", "S:FIRMA")
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if v, ok := f.regs[f.k(HKLM, winlogonKey, "AutoAdminLogon")]; ok {
		t.Errorf("AutoAdminLogon wurde wiederhergestellt: %q", v)
	}
	if f.regs[f.k(HKLM, winlogonKey, "DefaultUserName")] != "S:Hans" || f.regs[f.k(HKLM, winlogonKey, "DefaultDomainName")] != "S:FIRMA" {
		t.Errorf("Benutzername/Domäne nicht wiederhergestellt: %v", f.regs)
	}
	if _, ok := f.regs[f.k(HKLM, backupKey, "Saved")]; ok {
		t.Error("Sicherung blieb")
	}
}

// -acl: setzt die Datenordner-Rechte mit der gemerkten SID, ohne etwas anderes anzufassen.
func TestReapplyACLUsesRecordedSID(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.acl, f.aclSID, f.calls = "zusatz", "", nil // das Setup eines Updates hat "Benutzer: Ändern" gesetzt
	if err := ReapplyACL(f); err != nil {
		t.Fatal(err)
	}
	if f.acl != "kiosk" || f.aclSID != sidOf(KioskUser) {
		t.Errorf("acl=%s sid=%q", f.acl, f.aclSID)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "Set") && c != "SetDataACL|"+sidOf(KioskUser) || strings.HasPrefix(c, "Delete") || strings.HasPrefix(c, "Create") {
			t.Errorf("-acl änderte mehr als die Rechte: %s", c)
		}
	}
	// Eine gemerkte SID gilt vor der Auflösung des Namens.
	f.regs[f.k(HKLM, sidPath, sidName)] = "S:S-1-5-21-7-7-7"
	if err := ReapplyACL(f); err != nil || f.aclSID != "S-1-5-21-7-7-7" {
		t.Errorf("gemerkte SID nicht genutzt: %v %q", err, f.aclSID)
	}
	// Ohne Kiosk (weder SID noch Konto): Fehler, keine Rechte gesetzt.
	g := newFake()
	if err := ReapplyACL(g); err == nil || g.acl != "zusatz" {
		t.Errorf("ohne Kiosk: err=%v acl=%s", err, g.acl)
	}
}

// Ein vorhandenes lokales Konto PDH-Kiosk ohne gemerkte SID gehört nicht uns: Setup lehnt ab, nichts wird gelöscht.
func TestPreflightRefusesForeignAccount(t *testing.T) {
	f := newFake()
	f.users[KioskUser] = true
	f.profiles[sidOf(KioskUser)] = `C:\Users\PDH-Kiosk`
	err := PreflightSetup(f)
	if !errors.Is(err, ErrForeignAccount) {
		t.Fatalf("erwartet ErrForeignAccount, bekam %v", err)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "Delete") {
			t.Errorf("Preflight löschte: %s", c)
		}
	}
	if !f.users[KioskUser] || len(f.profiles) != 1 {
		t.Error("Konto oder Profil verändert")
	}
	// Eigenes Konto (SID gemerkt): erlaubt. Kein Konto: erlaubt.
	f.regs[f.k(HKLM, sidPath, sidName)] = "S:" + sidOf(KioskUser)
	if err := PreflightSetup(f); err != nil {
		t.Errorf("eigenes Konto: %v", err)
	}
	if err := PreflightSetup(newFake()); err != nil {
		t.Errorf("ohne Konto: %v", err)
	}
}
