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
	const lp = "S-1-5-21-1"
	if !OwnProfileSID("S-1-5-21-1-1001", Unmapped, "", "", "PDH-Kiosk", "HOST", lp) {
		t.Error("unauflösbare SID mit lokalem Präfix ist eine Waise")
	}
	if OwnProfileSID("S-1-5-21-9-1000", Unmapped, "", "", "PDH-Kiosk", "HOST", lp) {
		t.Error("unauflösbare Domänen-SID (DC weg) ist keine Waise")
	}
	if OwnProfileSID("S-1-5-21-1-1001", Unmapped, "", "", "PDH-Kiosk", "HOST", "") {
		t.Error("ohne bekanntes lokales Präfix keine Waise")
	}
	if OwnProfileSID("S-1-5-21-1-1001", LookupFailed, "", "", "PDH-Kiosk", "HOST", lp) {
		t.Error("sonstiger Auflösungsfehler heißt nicht unseres")
	}
	if !OwnProfileSID("S-1-5-21-1-1001", Resolved, "pdh-kiosk", "host", "PDH-Kiosk", "HOST", lp) {
		t.Error("lokales Konto muss unseres sein")
	}
	for _, c := range [][2]string{{"PDH-Kiosk", "CORP"}, {"Hans", "HOST"}} {
		if OwnProfileSID("S-1-5-21-1-1001", Resolved, c[0], c[1], "PDH-Kiosk", "HOST", lp) {
			t.Errorf("%v ist fremd", c)
		}
	}
	if SIDPrefix("S-1-5-21-1-2-3-1000") != "S-1-5-21-1-2-3" {
		t.Error("SIDPrefix")
	}
}

func TestUnresolvableDomainPrefixProfileNeverDeleted(t *testing.T) {
	f := newFake()
	f.profiles[foreignSID] = `C:\Users\PDH-Kiosk.CORP` // Domänen-SID, DC nicht erreichbar: unauflösbar
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	// Merker und Konto weg: nur der Scan bleibt. Die Domänen-SID darf nicht als Waise gelten.
	delete(f.users, KioskUser)
	delete(f.accounts, sidOf(KioskUser))
	delete(f.regs, f.k(HKLM, sidPath, sidName))
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.profiles[foreignSID]; !ok {
		t.Fatal("unauflösbares Domänenprofil wurde gelöscht")
	}
	f.settled(t)
}

func TestLookupFailureMeansNotOurs(t *testing.T) {
	f := newFake()
	f.lookupFails = map[string]bool{sidOf("PDH-Kiosk"): true}
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	delete(f.regs, f.k(HKLM, sidPath, sidName))
	delete(f.users, KioskUser)
	delete(f.accounts, sidOf(KioskUser))
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	if len(f.profiles) != 1 {
		t.Fatal("bei Auflösungsfehler darf nichts gelöscht werden")
	}
}

func TestUnresolvableLocalPrefixIsCleaned(t *testing.T) {
	f := newFake()
	if err := Run(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	delete(f.users, KioskUser)
	delete(f.accounts, sidOf(KioskUser)) // unauflösbar, lokales Präfix S-1-5-21-1
	delete(f.regs, f.k(HKLM, sidPath, sidName))
	if err := Undo(Plan(app), f); err != nil {
		t.Fatal(err)
	}
	f.settled(t)
}

func TestMarkerSIDWithForeignPrefixIsRefused(t *testing.T) {
	f := newFake()
	f.profiles[foreignSID] = `C:\Users\PDH-Kiosk.CORP`
	f.regs[f.k(HKLM, sidPath, sidName)] = "S:" + foreignSID // beschädigter oder untergeschobener Merker
	err := Undo(Plan(app), f)
	if err == nil || !strings.Contains(err.Error(), "abgelehnt") || !strings.Contains(err.Error(), foreignSID) {
		t.Fatalf("Ablehnung fehlt: %v", err)
	}
	if _, ok := f.profiles[foreignSID]; !ok {
		t.Fatal("fremdes Profil wurde gelöscht")
	}
	if slices.Contains(f.calls, "DeleteProfileBySID|"+foreignSID) {
		t.Error("Löschen versucht")
	}
}
