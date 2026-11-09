# Anlagenterminal: PDH-Terminal auf hmi-kiosk

Stand 2026-11-09. Betrifft hmi-kiosk (dieses Repository) und PDH (`pdh-server`, Arbeitszweig
`feat/kiosk-terminal` bzw. Folgezweige). Baut auf der PDH-Fernsteuerung auf
(`2026-11-09-pdh-integration.md`).

## Ziel

In jeder Anlage steht ein Raspberry Pi 5 mit hmi-kiosk, Touchscreen (Bedienung wie ein Bestellterminal)
und RFID-Leser. Er ist dauerhaft als PDH-Terminal-Benutzer angemeldet und dient als PDH-Terminal für
**seine Anlage und alle Unteranlagen**. Jede Person kann ohne Vorwissen Störungen, Tickets und
Stillstände erfassen; Bearbeitungsschritte verlangen je nach Einstellung die Karte der Instandhalter.
Im Ruhezustand zeigt das Terminal eine zentral gepflegte Info-Playlist.

## Entscheidungen des Nutzers

| Thema | Entscheidung |
|---|---|
| Aufbau | Oberfläche und Logik in **PDH**; hmi-kiosk ist die Hülle (Vollbild, Karten, Rückkehr) |
| Karten | Technik noch offen → Leser austauschbar: **PN532 über I²C** (Standard), **USB-Leser** als Alternative |
| Kartenweg | **Lokales Ereignis**: hmi-kiosk liest, meldet über `127.0.0.1` an die PDH-Seite, PDH prüft |
| Beseitigung | Erste Karte öffnet den Dialog, weitere Karten werden **Beteiligte** |
| Kartenpflicht | Für Übernehmen, Behoben melden und Bestätigen **einzeln einstellbar** |
| HMI-Zugriff | **Pro HMI-Verbindung einstellbar**: Ansehen und Bedienen am Terminal |
| Smartphone | Erreicht PDH **intern und über das Internet** |
| Playlist | Alle Folienarten (Text/Bild, Live-Status, Grafana, Webseite/PDF), erweiterbar |
| Stillstände | Neue Erfassung (Rüsten, Reinigung …) aus dem Anlagenstamm, **live und nachträglich**, auswertbar |

## Ablauf am Terminal

1. **Bildschirmschoner:** Playlist läuft.
2. **Berührung → Störungsinfo** der Anlage und Unteranlagen: offene Störungen (Status, Alter), laufende
   Stillstände mit Uhr, offene Tickets.
3. **„Neu“** → Seite mit großen Kacheln: **Störung**, **Ticket** und die **Stillstandsgründe** der Anlage.
4. Nach der eingestellten Zeit ohne Berührung zurück zur Playlist; offene Kartensitzung endet.

## Teil 1: Gesamtaufbau

### PDH
- **Anlagenterminal** = vorhandener Terminal-Benutzer (`users.is_system_user`, Terminal-Standort) plus
  Einstellungen: zugeordnete Anlage (Teilbaum), Rückkehrzeit zur Playlist (Standard 2 min),
  QR-Gültigkeit (Standard 10 min), Kartenpflicht je Schritt, Playlist-Mischung. Globale Standardwerte
  in den PDH-Einstellungen, Abweichungen pro Terminal.
- Seite **`/terminal`**: touchgerecht (große Trefferflächen ≥ 64 px, wenig Text, keine Hover-Bedienung),
  Vollbild, deutsch.
- Der Terminal-Benutzer sieht nur seinen Anlagen-Teilbaum.

### hmi-kiosk
- Web-Kachel „PDH-Terminal“ als **Daueranzeige ohne Leiste** (vorhanden).
- Neuer **Kartendienst** (Teil 2).
- Die Inaktivitäts-Rückkehr liegt in PDH (`/terminal`), nicht in hmi-kiosk; hmi-kiosk startet die Seite
  nach Absturz neu (vorhanden).

### HMIs am Terminal
- Über das **vorhandene noVNC von PDH** im selben Fenster (nicht Remmina), damit das Bild als Canvas
  direkt als **Bildschirmfoto** an eine Störung gehängt werden kann.
- **Pro HMI-Verbindung** zwei neue Felder in `infrastructure_hmis`:

  | Feld | Werte | Standard |
  |---|---|---|
  | Ansehen am Terminal | `everyone` · `card` (Recht `hmi.view` für die Anlage) · `never` | `everyone` |
  | Bedienen am Terminal | `never` · `card` (Recht `hmi.control` für die Anlage) · `everyone` | `card` |

  `allow_control = false` erzwingt „Bedienen: nie“. „Jeder darf bedienen“ zeigt beim Speichern einen
  deutlichen Warnhinweis. Per Karte entsperrtes Bedienen fällt nach Inaktivität (2 min) auf den Zustand
  ohne Karte zurück. Jeder Zugriff im HMI-Protokoll mit Terminal und ggf. Person.

## Teil 2: Kartendienst und Kartensitzung

### hmi-kiosk: Kartendienst
- Admin-Seite **„Kartenleser“**: Typ `aus` · `pn532-i2c` · `usb`.
  - **PN532/I²C:** Abbild schaltet I²C ein (`dtparam=i2c_arm=on`), Benutzer `hmikiosk` in Gruppe `i2c`;
    Abfrage ISO 14443A ca. 4×/s; Bus und Adresse einstellbar (Standard `/dev/i2c-1`, `0x24`).
  - **USB:** Gerät wählbar (Name/Vendor-ID); der Dienst übernimmt es exklusiv (`EVIOCGRAB`), sammelt
    Ziffern/Zeichen bis Enter. Die Nummer gelangt nie als Tastatureingabe in ein Feld.
- **Format** der UID: Hex groß ohne Trennzeichen (Standard), Hex mit umgekehrter Byte-Reihenfolge oder
  Dezimal – passend zu den in PDH (`users.rfid_uid`) vorhandenen Einträgen.
- **Entprellen:** gleiche Karte innerhalb von 2 s einmal; Ereignisse älter als 5 s werden nie geliefert.
- **Ereignisstrom** `GET http://127.0.0.1:8080/api/card/events` (Server-Sent Events): nur Loopback,
  nur `Origin` = eingetragene PDH-Adresse (CORS mit genau diesem Ursprung, Antwort auf den
  Private-Network-Access-Preflight `Access-Control-Allow-Private-Network: true`). Ereignis
  `{"uid":"…","at":"RFC3339"}`. Die Chromium-Richtlinie erlaubt die PDH-Adresse ohnehin (Web-Kachel).
- **Anlernen:** Admin-Seite zeigt die zuletzt gelesene UID (zum Eintragen beim PDH-Benutzer).
- **Selbstprüfung:** neuer Schritt „Kartenleser“ (Leser antwortet / Gerät vorhanden).

### PDH: Kartensitzung
- `/terminal` abonniert den Ereignisstrom; bei einer Karte `POST /terminal/card {uid}` (Sitzung des
  Terminal-Benutzers). PDH sucht die Person (`rfid_uid`, aktiv).
- Unbekannt/gesperrt: „Karte nicht registriert“, sonst nichts.
- Bekannt: Hinweis „Angemeldet: <Name>“ und **Kartensitzung**: gebunden an dieses Terminal, gilt für die
  laufende Aktion, endet nach Abschluss, nach 2 min ohne Eingabe, bei einer anderen Karte (außer im
  Beteiligten-Dialog) oder über „Abmelden“.
- Rechte werden **immer mit der Person** und ihrem Abteilungsbezug für die betroffene Anlage geprüft
  (`hasPermRec`-Logik), nie mit dem Terminal-Benutzer. Die Kartensitzung ist keine PDH-Anmeldung.

## Teil 3: Störungen und Tickets

### Melden (jeder, ohne Karte)
1. **Wo?** Anlage und Unteranlagen als große Kacheln mit Pfadanzeige.
2. **Was?** Störung oder Ticket; Kategorie und Priorität als große Knöpfe (vorhandene PDH-Listen);
   Kurzbeschreibung per Bildschirmtastatur mit Vorschlägen aus häufigen Meldungen der Anlage.
3. **Anhänge:** „Foto per Smartphone“ (QR, Teil 5), „HMI-Bild“ (Teil 6); Vorschaubilder sofort.
4. **Absenden:** Nummer anzeigen. Optional Karte → Person als Melder.

Ein **Entwurf** entsteht beim ersten Anhang oder beim Absenden; nicht abgeschickte Entwürfe samt Bildern
löscht PDH nach 30 min. **Tickets** werden am Terminal nur gemeldet.

### Bearbeiten („Offene Störungen“)
- **Übernehmen** → Person zugewiesen, Status `in_progress`, Startzeit.
- **Behoben melden** → erste Karte öffnet den Dialog: Ursache, Maßnahme, Ausfallzeit (vorbelegt),
  Fotos/HMI-Bild; weitere Karten fügen **Beteiligte** hinzu (entfernbar); Status `resolved`.
  Neue Tabelle für Beteiligte einer Störung.
- **Bestätigen** → Status `closed`.
- **Kartenpflicht je Schritt** (Terminal, globaler Standard): `none` · `any_card` · `card_with_permission`
  (Standard; das zum Schritt passende Störungsrecht für die Anlage). Für Bestätigen zusätzlich
  „andere Person als der Beheber“ (Standard an).

## Teil 4: Stillstände (neu in PDH)

- **Anlagenstamm:** Stillstandsgründe je Anlage (Name, Art `planned`/`unplanned`, Farbe, Reihenfolge,
  aktiv). Unteranlagen erben die Gründe der übergeordneten Anlage und können eigene ergänzen.
- **Live:** Tipp auf einen Grund startet eine Uhr („Rüsten seit 0:12 h“, Knopf „Beenden“).
  Höchstens **ein laufender Stillstand je Anlage**; ein anderer Grund fragt „<A> beenden und <B>
  starten?“.
- **Nachträglich:** Grund, Beginn, Ende; keine Zukunft; Hinweis bei Überschneidung derselben Anlage.
- Kartenpflicht einstellbar (Standard: ohne Karte, Person optional per Karte).
- Korrekturen nur in PDH mit Recht, protokolliert.
- **Auswertung:** Summen und Häufigkeiten je Anlage/Grund/Zeitraum (inkl. Unteranlagen), Pareto,
  Zeitstrahl, CSV-Export, Bereitstellung für Grafana (wie vorhandene Statistik); Ausfallzeiten aus
  Störungen fließen als eigene Art ein.
- Neue Rechte für Pflege der Gründe, Korrektur und Auswertung (Namen nach PDH-Konvention).

## Teil 5: Foto per Kurzzeit-QR-Code

- „Foto per Smartphone“ erzeugt ein Kennzeichen (128 Bit Zufall) für **einen** Entwurf, gültig für die
  eingestellte Zeit (Standard 10 min). QR-Inhalt: einstellbare **Adresse für Smartphones** (PDH-Einstellung,
  intern oder öffentlich) + `/u/<kennzeichen>`.
- Upload-Seite: schlank, mobil, Kamera oder Galerie; zeigt keine PDH-Daten; nur JPEG/PNG/HEIC, je Bild
  ≤ 15 MB, ≤ 10 Bilder; EXIF-Standort wird entfernt; Drosselung gegen Massenanfragen.
- Abgelaufen/abgeschlossen: „Code abgelaufen, bitte am Terminal neu erzeugen“.
- Das Terminal zeigt eingegangene Bilder sofort (Abfrage alle 2 s, solange der Code gilt).

## Teil 6: HMI-Ansicht und Bildschirmfoto

- „HMI-Bild“ bzw. Kachel „HMIs“ zeigt die HMIs der gewählten Anlage (und Unteranlagen) gemäß der
  Einstellung pro HMI (Teil 1). noVNC von PDH im selben Fenster; Bedienen nach Karte, falls eingestellt.
- „Bild übernehmen“ → Canvas als PNG an den Entwurf/die Störung.
- HMI nicht erreichbar: Meldung, „Erneut versuchen“; Melden geht trotzdem.

## Teil 7: Info-Playlist (Bildschirmschoner)

- Folien in PDH: Art `text_image`, `live_status`, `grafana`, `web`, `pdf` (erweiterbar), Anzeigedauer,
  gültig von/bis, Reihenfolge, aktiv.
- **Global** (alle Terminals) und **pro Terminal**; Mischung je Terminal: globale zuerst · abwechselnd ·
  nur eigene.
- Terminal fragt jede Minute nach Änderungen; ohne PDH-Verbindung läuft die zuletzt geladene Playlist
  weiter, Symbol „offline“.
- `web`/`grafana` nur mit Adressen, die die Chromium-Richtlinie des Terminals erlaubt (Web-Kachel-Ursprünge).

## Fehlerfälle

- **PDH nicht erreichbar:** Playlist aus Zwischenspeicher, „offline“; Störungsinfo/Melden: „Keine
  Verbindung zu PDH, bitte am PC melden“. Keine heimliche Zwischenspeicherung von Meldungen.
- **Kartenleser fehlt/defekt:** Admin-Seite und Selbstprüfung melden es; am Terminal „Kartenleser nicht
  bereit“; Schritte ohne Kartenpflicht gehen weiter.
- **Zwei Personen:** andere Karte beendet die Kartensitzung (außer im Beteiligten-Dialog).

## Sicherheit

- Kartensitzung: ein Terminal, eine Aktion, 2 min; Rechte der Person mit Abteilung für die Anlage.
- Karten-UIDs gelten als kopierbar: Kartensitzung ist keine vollwertige Anmeldung.
- QR-Upload: 128-Bit-Kennzeichen, Ablauf, nur ein Entwurf, Dateigrenzen, EXIF-Standort entfernt,
  Drosselung, keine Datenausgabe.
- Kartenereignis nur auf dem Gerät und nur an die eingetragene PDH-Adresse; keine alten Ereignisse.
- Terminal-Benutzer sieht nur seinen Teilbaum; HMI-Bedienung immer über Einstellung pro HMI.
- Protokoll jeder Aktion mit Terminal und ggf. Person.

## Tests

- **PDH:** Unit-Tests für Rechte, Kartenpflicht, Kartensitzung, QR-Kennzeichen; Integrationstests gegen
  PostgreSQL für Melden, Übernehmen, Behoben (Beteiligte), Bestätigen (andere Person), Stillstände
  (ein laufender je Anlage, Überschneidung), Abteilungsgrenzen, QR-Ablauf und Upload-Grenzen;
  Handbuch, Changelog, Version nach `AGENTS.md`.
- **hmi-kiosk:** PN532 über nachgebildeten I²C-Bus, USB über nachgebildete evdev-Ereignisse,
  Ereignisstrom (Ursprung, Loopback, Entprellen, keine alten Ereignisse), Admin-Seite, Selbstprüfung.
- **Ende-zu-Ende:** nachgebildeter Leser → Ereignis → PDH-Seite → Kartensitzung (aufbauend auf
  `test/pdh-e2e`).
- **Am Gerät:** echter Leser, Touch, noVNC-Bildschirmfoto, QR-Upload vom Smartphone (intern und extern).

## Umsetzung (je Teilprojekt eigener Plan)

1. Grundgerüst: `/terminal`, Störungsinfo, „Neu“-Seite, Rückkehr zur Playlist (zunächst leer),
   Terminal-Einstellungen, HMI-Felder pro Verbindung.
2. Kartendienst (hmi-kiosk) und Kartensitzung (PDH).
3. Störung/Ticket melden und bearbeiten mit Kartenpflicht und Beteiligten.
4. Stillstände mit Auswertung.
5. Foto per QR-Code.
6. HMI-Ansicht mit Bildschirmfoto.
7. Info-Playlist.

## Nicht Teil dieses Entwurfs

- Offline-Erfassung am Terminal ohne PDH.
- Bearbeitung von Tickets am Terminal (nur Melden).
- Anlernen von Karten direkt am Terminal (UID über die hmi-kiosk-Admin-Seite).
- Kartentechnik jenseits PN532/USB (LEGIC, HID iCLASS) – folgt, wenn die Ausweise feststehen.
