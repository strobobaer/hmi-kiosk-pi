# Teilprojekt 0: Gemeinsamer Kern, Produkt PDH-Terminal, Kopplung per Code

Stand 2026-11-11. Gehört zu `2026-11-09-anlagenterminal-design.md` (Abschnitt „Produktlinie“).
Betrifft dieses Repository und PDH (`pdh-server`, Folgezweig nach `feat/anlagenterminal`).

## Entscheidungen des Nutzers

- **Zwei Programme, gemeinsame Bausteine:** `cmd/hmi-kiosk` und `cmd/pdh-terminal` in einem Repository.
- **Eigene Versionen, ein Schlüssel:** getrennte Versionsnummern und Releases, ein Signaturschlüssel.
- **Kopplung per Code:** Das PDH-Terminal wird über einen Einmalcode aus PDH verbunden.
- **Admin am Gerät (PDH-Terminal):** 5 Sekunden auf die obere rechte Ecke drücken, dann Admin-Passwort.
  Kein sichtbares Zahnrad.
- HMI-Kiosk funktioniert nach dem Umbau unverändert.

## Teil 1: Schnitt zwischen Kern und Produkten

**Gemeinsamer Kern**
- Abbild-Bau (pi-gen) mit Produktparameter; erster Start, Schreibschutz (tmpfs-Overlay), Datenpartition.
- root-Helfer mit fester Befehlsliste: Zeit, Netzwerk, feste IP, Sicherung, Neustart, Update-Aktivierung.
- Signierte Updates (Ed25519) mit Produktkennung, Systemstand, Rückfall-Wächter; Update-Prüfung über
  GitHub-Releases (nur Tags mit eigenem Präfix).
- Chromium-Steuerung (Vollbild, Neustart nach Absturz, Richtlinie), Bildschirmtastatur.
- Admin-Grundgerüst: Anmeldung, Sitzungen, Sperre nach Fehlversuchen, HTTPS-Zertifikat mit stabilem
  Schlüssel, Seiten Netzwerk, System (inkl. Updates), Sicherung, Wartung, Protokoll.
- Selbstprüfung als Rahmen; jedes Produkt hängt eigene Schritte ein.

**Nur HMI-Kiosk**
- Kacheln, VNC/Remmina/TigerVNC, Abstimmung zwischen Terminals (`coord`), Daueranzeige, Web-Kacheln,
  PDH-Fernsteuerung (`/api/remote/v1`), Kachelseite, Admin-Seiten HMIs, Kiosk, Terminals.

**Nur PDH-Terminal**
- Admin-Seite **„PDH“**: PDH-Adresse, Kopplung per Code, Verbindungszustand, zusätzlich erlaubte
  Adressen für Playlist-Folien (Grafana, Webseiten) in der Chromium-Richtlinie.
- Admin-Seite **„Kartenleser“**: Platzhalter in Teilprojekt 0; Inhalt in Teilprojekt 2.
- Chromium öffnet im Vollbild die lokale Startseite (Teil 2), danach `<PDH>/terminal`.
- Admin am Gerät über die Ecke (siehe oben): Der Dienst liest den Touchscreen direkt (evdev, unabhängig
  von der angezeigten PDH-Seite). Bleibt ein Finger 5 s im Feld 80 × 80 px oben rechts, zeigt der
  Browser-Supervisor (`browser.Supervisor.Show`) `http://127.0.0.1:8081` mit Passwortabfrage. Nach
  Abmelden oder 2 min Inaktivität kehrt er zur Startseite zurück.
- Kartendienst mit Ereignisstrom auf `127.0.0.1` folgt in Teilprojekt 2.

**Code-Aufteilung:** Kernpakete bleiben unter `internal/` und dürfen kein Produktpaket importieren
(Test). Das neue Produkt lebt in `internal/product/pdhterminal`. Die HMI-Kiosk-Teile bleiben in
TP0 an ihrem Ort und werden über Produktbeschreibungen abgeschaltet (Menü, Seiten, Routen,
Selbstprüfungsschritte); ein späterer Umzug nach `internal/product/hmikiosk` ist möglich, aber nicht
nötig. Plattformnamen (Benutzer `hmikiosk`, Einheiten `hmi-kiosk*.service`, `/etc/hmi-kiosk`) bleiben
für beide Produkte gleich; je Produkt verschieden sind Programm, Abbild, Pakete und Richtlinie.

## Teil 2: Kopplung per Code

### PDH
- Beim Terminal-Benutzer (System-Benutzer mit Terminal-Anlage) Knopf **„Gerät koppeln“**: zeigt einen
  Code aus 8 Zeichen (Alphabet ohne verwechselbare Zeichen, Anzeige `XXXX-XXXX`), einmal verwendbar,
  10 Minuten gültig. Ein neuer Code macht offene Codes desselben Benutzers ungültig. Gespeichert wird nur
  sein SHA-256. Recht: wie Benutzerverwaltung.
- Tabelle `terminal_devices`: id, user_id, name, secret_sha256, created_at, last_seen_at, revoked_at.
  Tabelle `terminal_pairing_codes`: user_id, code_sha256, expires_at, used_at.
- Beim Benutzer Liste **„Gekoppelte Geräte“** mit „Sperren“. Sperren wirkt spätestens nach 30 s.
- `POST /api/terminal/v1/pair` (JSON, ≤ 4 KiB, unbekannte Felder ablehnen):
  `{"code":"…","name":"…"}` → `200 {"device_id":"…","secret":"…","user":"<Anzeigename>"}`.
  Geheimnis: 32 Zufallsbytes, Base64url, Präfix `pdht_`, nur einmal ausgegeben.
  Fehler: `422` Code ungültig/abgelaufen/benutzt, `429` Sperre nach Fehlversuchen (5 je Quelladresse in
  5 Minuten, IPv6 je /64). Fehlertexte deutsch im Feld `error`.
- `GET /api/terminal/v1/device` mit `Authorization: Bearer <secret>` → `200 {"device_id","user","name"}`
  oder `401` (gesperrt/unbekannt); aktualisiert `last_seen_at`. Daraus zeigt der Pi „gesperrt“ und
  prüft vor der Anmeldung, ob PDH erreichbar ist.
- `POST /terminal/geraet` (Formular, Feld `secret`): prüft Geheimnis und Sperre, setzt die Sitzung des
  Terminal-Benutzers (Cookie wie System-Benutzer, lange Laufzeit) mit Claim `device` = Geräte-ID,
  aktualisiert `last_seen_at`, leitet auf `/terminal` um. Falsch/gesperrt: Fehlerseite „Gerät nicht
  gekoppelt oder gesperrt“, Sperre nach Fehlversuchen wie oben.
- Middleware: Tokens mit Claim `device` gelten nur, solange das Gerät nicht gesperrt und der Benutzer
  aktiv ist (Ergebnis höchstens 30 s zwischengespeichert). Karten-/Override-Sitzungen bleiben wie bisher;
  nach ihrem Ende ist wieder das Gerät angemeldet.
- Protokoll: Code erzeugt, gekoppelt (Gerät, Quelladresse), gesperrt, Anmeldung abgewiesen.
- Handbuch-Kapitel „Anlagenterminal“ ergänzt, Changelog, Version nach `AGENTS.md`.

### PDH-Terminal (Pi)
- Admin-Seite „PDH“: Adresse (`https://…`; `http://` nur mit ausdrücklichem Haken „unverschlüsselt
  erlauben“), Code, Gerätename (Standard: Hostname), Knopf „Koppeln“.
- Das Geheimnis liegt auf der Datenpartition, lesbar nur für den Dienst, wird nie angezeigt oder
  protokolliert und ist **nicht** Teil der Sicherung (nach Wiederherstellung neu koppeln).
  „Entkoppeln“ löscht es lokal (das Gerät in PDH sperrt der PDH-Admin).
- Zustand: nicht gekoppelt / gekoppelt als „<Benutzer>“, zuletzt verbunden / gesperrt / nicht erreichbar.
- Startseite `http://127.0.0.1:<lokal>/start`: sendet das Geheimnis per automatisch abgeschicktem
  POST-Formular an `<PDH>/terminal/geraet`; ohne Kopplung Hinweis „Gerät nicht gekoppelt“. Das
  Geheimnis steht nie in einer URL und geht nur an die eingetragene Adresse.
- Chromium-Richtlinie: erlaubt PDH-Adresse, lokale Startseite, `127.0.0.1:8081`, Zusatzadressen.

## Teil 3: Versionen, Releases, Abbilder

- Eigene Versionsnummern. Release-Tags: `hmi-kiosk-v<x.y.z>` und `pdh-terminal-v<x.y.z>`. Bestehende
  Tags `v<x.y.z>` gelten weiter als HMI-Kiosk.
- Ein Signaturschlüssel. Jedes Paket trägt die Produktkennung als PAX-Record `HMIKIOSK.product`
  (wie `HMIKIOSK.system-level`); fehlt sie, gilt `hmi-kiosk`. Das Gerät lehnt fremde Produkte ab.
  Programmdatei im Paket je Produkt (`hmi-kiosk` bzw. `pdh-terminal`).
- Update-Prüfung sucht nur Tags mit eigenem Präfix (HMI-Kiosk zusätzlich `v…`); Asset
  `<produkt>-<version>.pkg`.
- `packaging/build-image.sh --product hmi-kiosk|pdh-terminal`, ebenso `bundle.sh` und
  `hmi-kiosk-pack -product`. Standard ist `hmi-kiosk` (bisherige Aufrufe bleiben gültig).
- Eigener Systemstand-Marker je Produkt; PDH-Terminal beginnt bei 1.
- Grenze: Geräte mit 0.1.6/0.1.7 kennen die Produktkennung nicht und würden ein PDH-Terminal-Paket bei
  manuellem Hochladen annehmen; Schutz ab der ersten HMI-Kiosk-Version mit diesem Teilprojekt.
  Dokumentiert in `docs/betrieb.md`.

## Teil 4: Reihenfolge

1. Konfiguration in Kern- und HMI-Kiosk-Teil trennen; Dateiformat und Schema bleiben gleich, alte
   Sicherungen lassen sich einspielen.
2. Admin-Grundgerüst und Selbstprüfung in den Kern; HMI-Kiosk-Seiten registrieren sich.
3. Produktkennung in Paketen, Update-Prüfung mit Präfix, Abbild-/Bundle-Bau mit Produktparameter.
4. `cmd/pdh-terminal`: Admin-Seite „PDH“, Startseite, Eckgeste, eigene Abbild-Prüfung.
5. PDH: Kopplungscode, Geräte, Sperren, `/api/terminal/v1/pair`, `/terminal/geraet`.

Nach 1–3 sind alle HMI-Kiosk-Tests, `verify-image.sh` und `test/pdh-e2e` unverändert grün; erst dann
folgt 4. Schritt 5 ist unabhängig vom Pi und kann parallel in PDH entstehen.

## Fehlerfälle

- PDH nicht erreichbar beim Start: lokale Startseite zeigt „PDH nicht erreichbar“ und versucht es alle
  30 s erneut.
- Gerät gesperrt: PDH-Fehlerseite; die Admin-Seite am Pi zeigt „gesperrt“.
- Code falsch/abgelaufen: Meldung auf der Admin-Seite, nichts gespeichert.
- Fremdes Produktpaket: „Paket gehört zu <produkt>“, keine Änderung.

## Sicherheit

- Geheimnis und Code nur als SHA-256 in PDH; Geheimnis nie in URL, Protokoll oder Sicherung.
- Kopplung und Geräteanmeldung gebremst nach Fehlversuchen.
- Ein gesperrtes Gerät verliert die Sitzung spätestens nach 30 s.
- Die Eckgeste öffnet nur die passwortgeschützte Admin-Seite; Admin-Passwort wie bisher.
- Kernpakete importieren keine Produktpakete (Test).

## Tests

- **Kern/HMI-Kiosk:** alle bestehenden Tests, `verify-image.sh`, `test/install/check.sh`,
  `test/pdh-e2e`; neu: Importrichtung, Produktkennung (fremd/fehlend/eigen), Tag-Präfixe.
- **PDH-Terminal:** Admin-Seite PDH (Koppeln gegen httptest, Fehler, Entkoppeln), Startseite (Formular,
  kein Geheimnis in URL), Geheimnis nicht in Sicherung, Richtlinie, Selbstprüfung, Abbild-Prüfung.
- **PDH:** Code (Ablauf, Einmaligkeit, Ersetzen), Pair-Endpunkt (Grenzen, Sperre), `/terminal/geraet`,
  Sperren wirkt (Middleware), Rechte; Integration gegen PostgreSQL.
- **Ende-zu-Ende:** Koppeln → Start → `/terminal` angemeldet → Sperren → abgewiesen.

## Nicht Teil dieses Teilprojekts

- Kartendienst und Kartenleser-Seite (Teilprojekt 2).
- Inhalte von `/terminal` über Teilprojekt 1 hinaus.
- Fernverwaltung der PDH-Terminals aus PDH (Updates anstoßen, Zustand sammeln).
