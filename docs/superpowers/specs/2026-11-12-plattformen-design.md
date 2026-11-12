# PDH-Terminal auf allen Plattformen: Betriebsarten, Installer, Updates (Übersicht)

Stand 2026-11-12. Übersicht und Nutzerentscheidungen für die Teilprojekte K1 und B1–B6. Jedes
Teilprojekt bekommt eine eigene Spec und einen eigenen Plan; diese Datei hält fest, was für alle gilt.

## Entscheidungen des Nutzers

- **Plattformen:** Raspberry Pi, Linux (Debian inkl. Raspberry Pi OS, Arch) und Windows 11, jeweils mit
  Installer.
- **Drei Betriebsarten, bei der Installation wählbar:**
  - **Kiosk:** eigenes Gerät, startet ins Terminal, kein Zugang zum System (heute: Pi-Abbild).
  - **Zusatz:** Terminal als Programm auf einem vorhandenen PC, das System bleibt normal nutzbar.
  - **Test:** Terminal ohne eigene Hardware zum Ausprobieren am PC.
- **Ansatz 1:** ein Programm `pdh-terminal`; die Betriebsart ist eine Einstellung. Was sich unterscheidet
  (Fenster/Sitzung, Root-Helfer ja/nein, Datenordner), liegt hinter Schnittstellen. Kopplung,
  Admin-Seite, Kartenleser und Selbstprüfung sind in allen Betriebsarten derselbe Code.
- **Kartenleser überall, soweit die Hardware es zulässt:** PN532 über I²C (Pi, Linux mit I²C-Bus),
  USB-Tastaturleser (Linux evdev mit Alleinbelegung; Windows Raw Input mit Abfangen der Tasten in der
  Sitzung), PC/SC-Leser, PN532 über USB-Seriell (HSU). Für Windows ist PC/SC die Empfehlung.
- **Ohne Leser per Tastatur** (K1), Anmeldeart je Terminal in PDH.
- **Updates auf zwei Wegen:** Paketverwaltung bzw. Setup (Grundversion am Ort des Systems) und eigener
  signierter Updater (Datenordner). Regel: **die höhere gültig signierte Version gewinnt** beim Start;
  keiner der Wege überschreibt die Dateien des anderen.
- **Der Server stößt an:** Software-Updates der Terminals, Playlist und Terminal-Einstellungen werden von
  PDH angestoßen (Ereigniskanal `/terminal/events`, eingeführt in K1; Updates zusätzlich über die
  Antwort auf die regelmäßige Statusabfrage des Geräts, damit auch ein Gerät ohne offene Terminalseite sie
  bekommt).
- **Kioskgerät unter Windows:** Windows 11 Pro, Enterprise oder Education (Zugewiesener Zugriff mit
  Edge-Kioskmodus); Home wird dafür nicht unterstützt.
- **Offline:** Die Info-Playlist läuft bei Verbindungsabbruch aus dem Zwischenspeicher auf dem Gerät
  weiter, mit Vermerk und/oder Zusatzseite „Server offline“ (TP7, gilt für alle Plattformen).

## Teilprojekte und Reihenfolge

| # | Teilprojekt | Ergebnis |
|---|---|---|
| K1 | Anmeldung ohne Leser | Anmeldeart je Terminal, PIN, Passwort-Knopf, Leserstatus, Ereigniskanal von PDH |
| B1 | Betriebsarten im Kern | Kiosk/Zusatz/Test als Einstellung, Plattformschnittstellen, Startregel „höhere Version gewinnt“, Updates von PDH angestoßen (Versionen je Gerät in PDH, „Aktualisieren“ einzeln/alle) |
| B2 | Windows-Kern | `pdh-terminal.exe` mit Edge; Testmodus nutzbar |
| B3 | Kartenleser überall | PC/SC, USB-Tastaturleser, PN532 USB-Seriell auf Linux und Windows |
| B4 | Linux-Installer | Debian (amd64/arm64, auch Raspberry Pi OS Desktop) und Arch, Betriebsart wählbar; `install.sh` wird der Kiosk-Weg |
| B5 | Windows-Installer | Setup mit Betriebsart, Dienst, Autostart, Updates |
| B6 | Windows-Kioskgerät | Zugewiesener Zugriff, Edge-Kiosk, Admin über die Ecke oben rechts, Sicherung |

Das Pi-Abbild bleibt das Kioskgerät für den Pi und ändert sich durch B1–B6 nur in der Struktur, nicht im
Verhalten.
