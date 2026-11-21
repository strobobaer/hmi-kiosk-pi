package winkiosk

import (
	"slices"
	"strings"
	"testing"
)

// addForeign legt das Profil eines Domänenkontos CORP\PDH-Kiosk an.
func addForeign(f *fake) {
	f.profiles[foreignSID] = `C:\Users\PDH-Kiosk.CORP`
	f.accounts[foreignSID] = "CORP"
}

func TestForeignDomainProfileNeverTouched(t *testing.T) {
	f := newFake()
	addForeign(f)
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if err := Undo(Plan(app), f); err != nil {
		t.Fatalf("Undo trotz fremdem Profil: %v", err)
	}
	if _, ok := f.profiles[foreignSID]; !ok {
		t.Fatal("fremdes Domänenprofil wurde gelöscht")
	}
	if slices.Contains(f.calls, "DeleteProfileBySID|"+foreignSID) {
		t.Error("Löschen des fremden Profils versucht")
	}
	f.settled(t)
}

func TestRecordedSIDIsUsed(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if f.regs[f.k(HKLM, sidPath, sidName)] != "S:"+sidOf(KioskUser) {
		t.Fatalf("SID nicht gemerkt: %v", f.regs)
	}
	addForeign(f)
	f.calls = nil
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.calls, "FindOrphanProfiles") {
		t.Error("mit gemerkter SID darf nicht gesucht werden")
	}
	if _, ok := f.regs[f.k(HKLM, sidPath, sidName)]; ok {
		t.Error("SID-Merker blieb")
	}
	f.settled(t)
}

func TestNoRecordedSIDOrphanIsCleaned(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	// Konto und Merker weg, Profil blieb: Waise ohne erinnerte SID.
	delete(f.users, KioskUser)
	delete(f.accounts, sidOf(KioskUser))
	delete(f.regs, f.k(HKLM, sidPath, sidName))
	addForeign(f)
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.profiles[foreignSID]; !ok {
		t.Fatal("fremdes Domänenprofil wurde gelöscht")
	}
	f.settled(t)
}

func TestNoRecordedSIDForeignOnlyDoesNotBlockUndo(t *testing.T) {
	f := newFake()
	addForeign(f) // nur das Domänenprofil; kein Kiosk eingerichtet
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if len(f.profiles) != 1 {
		t.Fatal("Domänenprofil angefasst")
	}
}

func TestLocalAccountAndOwnSID(t *testing.T) {
	if !LocalAccount("PDH-Kiosk", "HOST", "PDH-Kiosk", "host") {
		t.Error("lokales Konto nicht erkannt")
	}
	if LocalAccount("PDH-Kiosk", "CORP", "PDH-Kiosk", "HOST") {
		t.Error("Domänenkonto darf nicht als lokales Konto gelten (keine Abmeldung)")
	}
	if LocalAccount("Hans", "HOST", "PDH-Kiosk", "HOST") {
		t.Error("anderer Benutzer")
	}
	if !OwnProfileSID(false, "", "", "PDH-Kiosk", "HOST") {
		t.Error("unauflösbare SID ist eine Waise")
	}
	if !OwnProfileSID(true, "pdh-kiosk", "host", "PDH-Kiosk", "HOST") {
		t.Error("lokales Konto muss unseres sein")
	}
	for _, c := range [][2]string{{"PDH-Kiosk", "CORP"}, {"Hans", "HOST"}} {
		if OwnProfileSID(true, c[0], c[1], "PDH-Kiosk", "HOST") {
			t.Errorf("%v ist fremd", c)
		}
	}
	if !strings.HasPrefix(foreignSID, "S-1-5-21-") {
		t.Error("Testdaten")
	}
}
