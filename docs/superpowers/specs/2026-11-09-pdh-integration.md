# PDH-Integration: HMI aus PDH am Terminal zeigen, PDH als Kachel

Stand 2026-11-09. Gilt für hmi-kiosk (dieses Repository) und pdh-server (`C:\workplace\pdh-server`).

## Entscheidungen des Nutzers

- Das VNC-Bild erscheint auf dem **Bildschirm des hmi-kiosk-Terminals** (nicht im Browser des PC).
- **Feste Zuordnung pro Maschine:** Jede Anlage (PDH-Infrastrukturknoten) hat höchstens ein Kiosk-Terminal.
- In PDH ein **zusätzlicher Knopf** „Am Terminal zeigen“; die Browser-Ansicht (noVNC) bleibt.
- Ist das Terminal belegt (Sitzung offen, auch Daueranzeige), **lehnt es ab**; PDH zeigt die Meldung.
- PDH auf dem Kiosk im **PDH-Terminalmodus**: Web-Kachel mit dauerhaft angemeldetem PDH-System-Benutzer;
  Personen melden sich für Aktionen per Karte/Override an (vorhandenes PDH-Konzept, keine PDH-Änderung).

## Schnittstelle hmi-kiosk (Fernbefehle, Version 1)

Erreichbar über den vorhandenen HTTPS-Admin-Listener (`https://<terminal>:8443`). Alle Antworten JSON,
`Content-Type: application/json; charset=utf-8`. Fehlertexte deutsch im Feld `error`.

### Anmeldung
- Header `Authorization: Bearer <schlüssel>`. Der Schlüssel (32 Zufallsbytes, Base64url ohne Auffüllung,
  Präfix `hmik_`) wird in der Admin-Seite unter **Netzwerk → Fernsteuerung (PDH)** erzeugt und **einmal**
  angezeigt; gespeichert wird nur sein SHA-256. Neu erzeugen macht den alten ungültig; „Aus“ löscht ihn.
- Ohne gültigen Schlüssel: `401 {"error":"…"}`. Fehlversuche bremsen wie die Admin-Anmeldung
  (gleiche Sperre je Quelladresse).
- Dieselbe Seite zeigt den **Fingerabdruck des öffentlichen Schlüssels** des HTTPS-Zertifikats:
  SHA-256 über das DER von SubjectPublicKeyInfo, Hex klein, mit Doppelpunkten oder ohne (PDH akzeptiert
  beides). Der private TLS-Schlüssel bleibt bei Erneuerung des Zertifikats (IP-Wechsel) **gleich**,
  damit der Fingerabdruck stabil ist.

### `GET /api/remote/v1/status`
`200 {"version":"0.1.7","terminal":"<Name>","busy":false}` oder
`200 {"version":…,"terminal":…,"busy":true,"hmi":"<Name>","since":"<RFC3339>","source":"local|pdh"}`.

### `POST /api/remote/v1/open`
Body (höchstens 4 KiB, unbekannte Felder ablehnen):
```json
{"host":"10.1.2.3","port":5900,"password":"…","mode":"view","label":"Linie 3 / Presse 2","user":"M. Muster"}
```
- `host` wie die Host-Regeln der HMI-Konfiguration (`config.checkHost`), `port` 1–65535, `password`
  höchstens 64 Bytes (darf leer sein), `mode` `view` oder `control`, `label` und `user` Pflicht,
  Namensregeln wie `checkName` (≤120 Zeichen, keine Steuerzeichen).
- Ergebnis: `200 {"status":"opened"}` — Sitzung gestartet (Remmina startet asynchron).
  `409 {"status":"busy","hmi":"…","since":"…"}` — Terminal belegt (jede offene Sitzung einschließlich
  Daueranzeige oder ein anderes Terminal hält die passende HMI gesperrt). `422` Eingaben ungültig.
  `503` Terminal nicht bereit (Wartungsmodus, Sitzung startet gerade, Sicherung läuft).
- Ablauf am Terminal: Gibt es eine konfigurierte Kachel mit gleichem Host und Port (VNC), wird
  **diese** geöffnet (inklusive Abstimmung mit anderen Terminals); sonst eine nur lokale Sitzung ohne
  Abstimmung. Passwort und Modus kommen aus dem Befehl (`view` → nur ansehen). Die Leiste zeigt
  „<label> – von PDH: <user>“. Rückkehr bei Inaktivität und „Zurück“ gelten wie sonst. Das Passwort
  wird nie gespeichert oder protokolliert; das Protokoll nennt label, user, mode, Quelladresse.

### `POST /api/remote/v1/close`
Beendet eine **von PDH** geöffnete Sitzung (`200 {"status":"closed"}`), sonst `409 {"error":"…"}`.

### Ergänzungen nach der Umsetzung
- `429 {"error":"…"}`, solange die Quelladresse nach Fehlversuchen gesperrt ist (5 Fehlversuche, 5 Minuten;
  auch der richtige Schlüssel wird dann abgewiesen).
- Bei `409 busy` ist `since` **optional**: Hält ein anderes Terminal die HMI, fehlt es, und `hmi` lautet
  „<Kachel> (an <Terminal>)“.
- `404`/`405` kommen als JSON erst nach gültigem Schlüssel (sonst `401`); jede ungültige Eingabe
  (auch zu groß oder kein JSON) ist `422`.
- PDH verdrängt nie: Hält ein anderes Terminal die HMI, gibt es immer `409`, auch im Übernahme-Modus.
  Ausnahme: Ansehen (`view`) einer laufenden Daueranzeige.
- `503` zusätzlich bei Selbst-/Dauerprüfung und bei offener Admin-Seite am Terminal.

## PDH-Seite

- Migration: Tabelle `kiosk_terminals` (id, name, base_url `https://host:8443`, api_key_enc,
  pubkey_sha256, active, Zeitstempel), Spalte `infrastructure.kiosk_terminal_id` (nullable, FK),
  Berechtigung `hmi.kiosk` (Rollen admin, manager, technician), Sitzungsprotokoll um das Ziel
  „Terminal“ erweitern.
- Verwaltung „Kiosk-Terminals“ (Core): anlegen/ändern/löschen, Schlüssel und Fingerabdruck eintragen
  (Schlüssel verschlüsselt wie andere Geheimnisse), Knopf „Verbindung prüfen“ (status).
- Anlage bearbeiten: Auswahl „Kiosk-Terminal“.
- HMI-Reiter: Knopf „Am Terminal zeigen“ (Ansicht; „Am Terminal bedienen“ nur bei `allow_control` und
  `hmi.control`) bei zugeordnetem Terminal und `hmi.kiosk`. Server ruft `open` mit dem entschlüsselten
  HMI-Passwort; TLS mit Prüfung **nur** des Public-Key-Fingerabdrucks (kein CA-Vertrauen nötig).
  Meldungen: geöffnet / belegt (HMI, seit) / nicht erreichbar / Schlüssel falsch.
- PDH-Terminalmodus auf dem Kiosk: in hmi-kiosk Web-Kachel mit der PDH-Adresse; einmal als
  PDH-System-Benutzer anmelden (Anmeldung bleibt im Profil). Doku auf beiden Seiten.
