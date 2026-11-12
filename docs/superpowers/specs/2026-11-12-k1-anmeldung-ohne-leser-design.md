# Teilprojekt K1: Anmeldung am Terminal ohne Kartenleser

Stand 2026-11-12. Gehört zu `2026-11-09-anlagenterminal-design.md` (Teil 2, Kartensitzung).
Betrifft dieses Repository (Kartendienst des PDH-Terminals) und PDH (`pdh-server`, Folgezweig nach
`feat/terminal-kopplung`). Erstes Teilprojekt der Reihe K1, B1–B6 (Betriebsarten und Plattformen).

## Entscheidungen des Nutzers

- **Ohne Leser per Tastatur:** Ist kein Kartenleser eingestellt oder ist er gestört, meldet man sich per
  Eingabe an.
- **Anmeldeart je Terminal einstellbar** in PDH: Kartennummer + PIN, Benutzer + Passwort, nur
  Kartennummer, keine.
- **Mit Leser:** zusätzlich ein Knopf „Mit Benutzer und Passwort“; die Karte bleibt der normale Weg.
- Alle Anmeldewege enden in der vorhandenen Kartensitzung (2 Minuten, „Abmelden“, Ende mit dem
  Bildschirmschoner, nur gekoppelte Geräte).

## Teil 1: Verhalten am Terminal

**Leser vorhanden** (Gerät meldet `ok`): „Karte auflegen“ wie bisher, dazu der Knopf **„Mit Benutzer
und Passwort“** (abschaltbar je Terminal). Er öffnet einen Dialog Benutzername/Passwort.

**Kein Leser oder Leser gestört** (Gerät meldet `none` oder `error`): Es gilt die Anmeldeart des Terminals.

| Einstellung (`manual_login`) | Am Terminal |
|---|---|
| `card_pin` – Kartennummer + PIN (Standard) | Felder „Kartennummer“ und „PIN“ |
| `password` – Benutzer + Passwort | Dialog Benutzername/Passwort, direkt offen |
| `card_only` – nur Kartennummer | Feld „Kartennummer“ |
| `none` – keine | Hinweis „Kein Kartenleser – Anmeldung nicht möglich“ |

Der Knopf „Mit Benutzer und Passwort“ bleibt auch ohne Leser sichtbar, außer bei `none` oder wenn er
abgeschaltet ist.

- **Ziffernfeld auf der Seite** für Kartennummer und PIN, touchgerecht, mit den Tasten A–F für
  Kartennummern im Format `hex`/`hex-rev`. Benutzername und Passwort brauchen bis TP3 (Bildschirmtastatur
  auf PDH-Seiten) eine angeschlossene Tastatur.
- Anmelden dürfen nur aktive Benutzer, keine Systembenutzer (wie bei Karten).
- Jeder Fehlschlag meldet nur **„Anmeldung fehlgeschlagen“**, ohne zu sagen, ob Nummer, PIN, Benutzer
  oder Passwort falsch war.

## Teil 2: Daten in PDH

Neue Migration mit der nächsten freien Nummer (bei Abfassung 161); eingetragen in
`migrations/checksums.txt` (`TestMigrationsFrozen`). Bestehende Migrationen werden nicht geändert.

- `terminal_settings.manual_login TEXT NOT NULL DEFAULT 'card_pin'`, CHECK auf
  `card_pin | password | card_only | none`; gilt auch für vorhandene Terminals.
- `terminal_settings.password_button BOOLEAN NOT NULL DEFAULT true`.
- `users.terminal_pin_hash TEXT` (bcrypt; NULL = keine PIN).
- `users.terminal_pin_failures INT NOT NULL DEFAULT 0`, `users.terminal_pin_locked_until TIMESTAMPTZ`.

**PIN:** 4–8 Ziffern; abgelehnt werden alle gleichen Ziffern (`0000`) und auf- oder absteigende Folgen
(`1234`, `8765`). Gespeichert wird nur der bcrypt-Hash. Nach **5** falschen PINs ist die PIN **15 Minuten**
gesperrt; eine richtige PIN setzt den Zähler zurück. Die Sperre liegt in der Datenbank und gilt an allen
Terminals und über Neustarts.

**Oberfläche in PDH**
- Systembenutzer, Karte „Anlagenterminal“ (neben der Rückkehrzeit): Auswahl „Anmeldung ohne Kartenleser“
  und Schalter „Knopf ‚Mit Benutzer und Passwort‘“. Speichern wie die Rückkehrzeit (Recht „Benutzer
  verwalten“).
- Benutzer: Feld „Terminal-PIN“ mit „PIN setzen“ und „PIN entfernen“; die PIN wird nie angezeigt. Setzen
  dürfen Personen mit „Benutzer verwalten“ und jeder Benutzer für sich im Profil. Ist die PIN gesperrt,
  zeigt PDH „PIN gesperrt bis HH:MM“; „PIN entfernen“ oder neu setzen hebt die Sperre auf.

**Bremse je Terminal:** Der vorhandene Zähler `cardFailLimit` (10 Fehlschläge pro Minute je Terminal)
zählt alle Anmeldewege zusammen: unbekannte Karte, falsche Nummer/PIN, falsches Passwort. Während der
Bremse antwortet PDH mit 429, das Terminal zeigt „Bitte kurz warten“.

## Teil 3: Gerät und Schnittstelle

**Leserstatus im Kartenstrom.** `GET 127.0.0.1:8080/api/card/events` sendet zusätzlich das Ereignis
`reader` mit `{"state":"ok"|"none"|"error"}`: sofort nach dem Verbinden und bei jeder Änderung. `none` =
Typ „aus“; `error` = Leser lässt sich nicht öffnen oder antwortet nicht (derselbe Zustand, den
Admin-Seite und Selbstprüfung zeigen). Origin-Prüfung und Grenzen des Kartenstroms bleiben unverändert.

**Ältere Geräte:** Kommt innerhalb von **3 Sekunden** nach dem Verbinden kein `reader`, nimmt die
PDH-Seite `ok` an (heutiges Verhalten).

**Neue Route in PDH:** `POST /terminal/login`, nur aus Gerätesitzungen (wie `/terminal/card`), mit
`{"kind":"card_pin","card":…,"pin":…}`, `{"kind":"card_only","card":…}` oder
`{"kind":"password","user":…,"password":…}`. PDH prüft serverseitig, ob die Art an diesem Terminal
erlaubt ist: `password` nur bei eingeschaltetem Knopf oder `manual_login = password`; `card_pin` und
`card_only` nur, wenn sie der Einstellung entsprechen. Eine Art, die die Einstellung nicht erlaubt,
wird mit 403 abgelehnt. Die Kartennummer wird wie bei Karten verglichen (ohne Groß-/Kleinschreibung,
Rand-Leerzeichen entfernt). Die Passwortprüfung ist die normale PDH-Anmeldung samt ihrer Sperren. Erfolg
legt eine Kartensitzung an wie `/terminal/card`.

## Fehlerfälle

| Fall | Am Terminal |
|---|---|
| Nummer, PIN, Benutzer oder Passwort falsch; Benutzer ohne PIN bei `card_pin`; Systembenutzer | „Anmeldung fehlgeschlagen“ |
| PIN gesperrt | „Anmeldung fehlgeschlagen“ (in PDH beim Benutzer „PIN gesperrt bis …“) |
| Bremse des Terminals | „Bitte kurz warten“ |
| Leser fällt während einer Sitzung aus | Sitzung läuft weiter; die Eingabe erscheint beim nächsten Anmelden |
| Kartenstrom nicht erreichbar | wie bisher; Eingabe nur nach `reader`-Ereignis `none`/`error` |

## Sicherheit

- Der vom Browser gemeldete Leserstatus ist nicht vertrauenswürdig, und PDH kennt ihn nicht. Ein Browser,
  der „kein Leser“ vortäuscht, erreicht deshalb höchstens die am Terminal eingestellte Anmeldeart, auch
  an einem Terminal mit Leser. Wer an einem Terminal mit Leser keine Eingabe will, stellt `none` ein.
- `card_only` ist nur so sicher wie die Geheimhaltung der Kartennummern; PDH zeigt bei dieser Auswahl
  einen Warnhinweis („Wer eine Kartennummer kennt, kann sich als diese Person anmelden“).
- PIN und Passwort werden nie protokolliert; Protokolle nennen nur Terminal, Art und Ergebnis.

## Tests

- PDH-Integration: jede Anmeldeart × Einstellung (erlaubt/abgelehnt), PIN-Sperre nach 5 Fehlversuchen und
  Rücksetzen nach Erfolg, Sperre über alle Terminals, Bremse je Terminal über alle Wege, Ablehnung aus
  Nicht-Gerätesitzungen, Systembenutzer abgelehnt, Kartennummer ohne Groß-/Kleinschreibung,
  PIN-Regeln (Länge, triviale PINs).
- Gerät: Go-Tests für `reader` beim Verbinden und bei Zustandswechseln.
- Terminalseite: Anzeige je Leserstatus und Einstellung, Rückfall auf `ok` nach 3 s.
- Gerätetest: Prüfliste um Teil „Anmeldung ohne Leser“ (Leser abziehen → Eingabe erscheint; Knopf
  „Mit Benutzer und Passwort“ mit Leser).

## Nicht Teil dieses Teilprojekts

- Bildschirmtastatur auf PDH-Seiten (TP3).
- Betriebsarten, Windows, weitere Leser und Installer (B1–B6).
- Info-Playlist mit Zwischenspeicher auf dem Gerät und Offline-Anzeige (TP7; ergänzt: je Terminal
  „Vermerk“, „Zusatzseite ‚Server offline‘“ oder beides).
