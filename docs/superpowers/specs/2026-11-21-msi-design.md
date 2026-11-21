# MSI für die verwaltete Verteilung des PDH-Terminals (Design)

Stand 2026-11-21. Ergänzt das Inno-Setup (`2026-11-20-w1-windows-zusatz-design.md`, `2026-11-21-w2g-kiosk-infobildschirm-design.md`) um ein natives MSI für Intune und Gruppenrichtlinie.

## Ziel
`msiexec /i PDH-Terminal-x.y.z.msi MODE=zusatz|kiosk /qn`, je Rechner, Inhalt wie das Inno-Setup, ohne Oberfläche.

## Entscheidungen
- WiX 3.14 unter Wine im Docker-Abbild `packaging/windows/msi/Dockerfile` (Basisabbild-Digest, WiX-ZIP und wine-mono über SHA-256 festgelegt); auf dem Rechner wird nichts installiert. `packaging/windows/build-msi.sh --version x.y.z` nimmt die Programme aus `dist/win` (fehlen sie: Abbruch) und schreibt `dist/PDH-Terminal-<version>.msi`.
- Je Rechner (`InstallScope=perMachine`, x64), feste UpgradeCode, `MajorUpgrade`. ProductCode je Version fest (aus der Version abgeleitet), für die Intune-Erkennung.
- `MODE` zusatz (Vorgabe) oder kiosk; ohne Angabe bleibt die bisherige Betriebsart (Marker `HKLM\SOFTWARE\PDH-Terminal\Betriebsart`, wie Inno). Unbekannter Wert: Abbruch.
- Zusatz: HKLM-Run-Eintrag und Datenordner `ProgramData\PDH-Terminal` mit Benutzer: Ändern. Kiosk: beides entfällt, der Helfer setzt die Rechte.
- Kiosk-Helfer als verzögerte Aktionen ohne Identitätswechsel, verborgen: `-setup -app` beim Wechsel nach Kiosk, `-acl` bei Kiosk nach Kiosk, `-undo` beim Wechsel nach Zusatz und bei der Deinstallation (dort `Return=ignore`: die Deinstallation läuft weiter, der Fehler steht im MSI-Protokoll und in `kiosk-setup.log`). Scheitert `-setup`/`-acl`/`-undo` beim Wechsel, rollt der Installer zurück (anders als Inno, das bei Zusatz bleibt).
- Kiosk plant einen Neustart über `ScheduleReboot` (Standard-Eigenschaft `REBOOT`; Rückgabe 3010, `REBOOT=ReallySuppress` unterdrückt ihn).
- Startbedingung bei `MODE=kiosk`: EditionID beginnt mit Professional, Enterprise, IoTEnterprise oder Education.
- Firewall TCP 8443 (Domäne, Privat) über WixFirewallExtension (zwei Regeln, WiX erlaubt je Regel ein Profil); Edge `HideFirstRunExperience=1`; Startmenü-Verknüpfung mit AppUserModelID `PDH.Terminal` (MsiShortcutProperty) und Admin-URL.
- MSI und Inno-Setup schließen sich aus: der MSI prüft den Inno-Uninstall-Schlüssel, das Setup die UpgradeCode unter `HKLM\SOFTWARE\Classes\Installer\UpgradeCodes`; beide mit deutscher Meldung.

## Grenzen
- Nicht signiert; ICE-Prüfung wird unter Wine übersprungen (`-sval`).
- Ein Wechsel der Betriebsart bei gleicher Version: `msiexec /i … REINSTALL=ALL REINSTALLMODE=vamus MODE=…` oder Deinstallieren und neu installieren; bei höherer Version genügt `/i` mit `MODE`.
- Der MSI wurde nur gebaut und per msitools geprüft (Tabellen), nie ausgeführt: Test auf einem Windows-Gerät steht aus.
