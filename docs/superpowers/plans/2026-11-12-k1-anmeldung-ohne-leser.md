# K1 Anmeldung ohne Kartenleser – Implementierungsplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Am PDH-Terminal ohne (funktionierenden) Kartenleser per Eingabe anmelden – Anmeldeart je Terminal in PDH, Terminal-PIN, Knopf „Mit Benutzer und Passwort“, Leserstatus vom Gerät, Ereigniskanal von PDH zur Terminalseite.

**Architecture:** PDH speichert Anmeldeart und PIN (Migration 161), prüft jede Eingabe-Anmeldung serverseitig über `POST /terminal/login` und legt dieselbe Kartensitzung an wie `/terminal/card`. Ein neuer EventSource-Kanal `GET /terminal/events` meldet Einstellungsänderungen sofort. Das Gerät ergänzt seinen Kartenstrom um das Ereignis `reader`; die Terminalseite entscheidet daraus, was sie anzeigt.

**Tech Stack:** Go 1.24, chi, pgx/PostgreSQL, bcrypt (`golang.org/x/crypto/bcrypt`), Vanilla-JS (`web/static/terminal.js`), Go-Templates.

**Spec:** `docs/superpowers/specs/2026-11-12-k1-anmeldung-ohne-leser-design.md` (Übersicht: `docs/superpowers/specs/2026-11-12-plattformen-design.md`)

## Arbeitsorte

- **PDH:** neuer Worktree `C:\workplace\pdh-server-k1`, Zweig `feat/anmeldung-ohne-leser` ab `feat/terminal-kopplung` (`27e7f6a`). Tasks 1–5.
- **Pi:** Worktree `C:\workplace\hmi-kiosk-pdhterm`, Zweig `feat/leserstatus` ab `feat/pdh-terminal`. Task 6.
- `C:\workplace\pdh-server` (Arbeitskopie des Nutzers) und die Container `pdh-gt-*` nie anfassen.
- PDH-Tests: Docker `golang:1.24-bookworm` mit Volumes `pdh-karten-gomod`/`pdh-karten-gobuild`, Netz `pdh-karten-test`, DB `postgres://postgres:pdh@pdh-pg-karten:5432/pdh_test?sslmode=disable` als `PDH_TEST_DSN`; Integration mit `-tags integration`. Pi-Tests: `scripts/dgo test -race ./...`.

## Global Constraints

- **Migrationen:** bestehende nie ändern; neue Datei `migrations/161_terminal_manual_login.up.sql`, Eintrag in `migrations/checksums.txt` über `PDH_MIGRATIONS_APPEND=1 go test ./pkg/database -run TestMigrationsFrozen`.
- `manual_login` ∈ `card_pin | password | card_only | none`, Standard `card_pin`; `password_button` Standard `true`.
- PIN: 4–8 Ziffern; abgelehnt: alle Ziffern gleich, lückenlos auf- oder absteigend (`1234`, `8765`, `0123`). Nur bcrypt-Hash speichern.
- PIN-Sperre: **5** Fehlversuche → **15 Minuten** gesperrt; richtige PIN setzt den Zähler zurück; in der DB, gilt an allen Terminals.
- Bremse je Terminal: vorhandener `cardFailLimit` (10/min, Schlüssel `"card:"+Terminal-ID`) zählt alle Anmeldewege.
- Meldetexte wörtlich: „Anmeldung fehlgeschlagen“, „Bitte kurz warten“ (429), „Kein Kartenleser – Anmeldung nicht möglich“, Knopf „Mit Benutzer und Passwort“, Warnung bei `card_only`: „Wer eine Kartennummer kennt, kann sich als diese Person anmelden“, Anzeige „PIN gesperrt bis HH:MM“.
- Anmelden dürfen nur aktive Benutzer, keine Systembenutzer.
- Leserstatus `reader`: `{"state":"ok"|"none"|"error"}`; ohne `reader` binnen **3 s** nach Verbindungsaufbau gilt `ok`.
- PIN und Passwort nie protokollieren (auch nicht maskiert); `authLog` nennt Art und Ergebnis.
- Routen `/terminal/login` und `/terminal/events` nur aus Gerätesitzungen (`cardTerminal`).
- Commits enden mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`; kein Push.

## Review Focus

1. Browser meldet „kein Leser“ an einem Terminal mit `none` → `/terminal/login` lehnt jede Eingabe-Art außer `password` (nur wenn Knopf an) mit 403 ab. → Test in Task 2.
2. Benutzer ohne PIN bei `card_pin` → „Anmeldung fehlgeschlagen“, zählt in die Bremse, löst keinen Fehler/500 aus. → Test in Task 2.
3. Gleichzeitige falsche PINs (zwei Terminals) → Zähler verliert keinen Fehlversuch (atomares `UPDATE … RETURNING`). → Test in Task 1.
4. Kartennummer mit Leerzeichen/Kleinbuchstaben bei `card_only`/`card_pin` → wie Karte erkannt. → Test in Task 2.
5. Gerät wird gesperrt, während `/terminal/events` offen ist → Strom endet, Neuverbindung bekommt 403. → Test in Task 3.

---

### Task 1: Migration, Anmeldeeinstellungen und Terminal-PIN (PDH)

**Files:**
- Create: `migrations/161_terminal_manual_login.up.sql`, Eintrag in `migrations/checksums.txt`
- Create: `internal/web/terminal_login_store.go`
- Test: `internal/web/terminal_login_store_test.go` (Unit), `internal/web/terminal_login_store_integration_test.go` (`//go:build integration`, Helfer `newATEnv` aus `anlagenterminal_authz_integration_test.go`)

**Interfaces:**
- Produces:
  - `type terminalLoginSettings struct { ManualLogin string; PasswordButton bool }` und Konstanten `loginCardPIN = "card_pin"`, `loginPassword = "password"`, `loginCardOnly = "card_only"`, `loginNone = "none"`.
  - `func (h *Handler) terminalLoginSettings(ctx context.Context, userID string) (terminalLoginSettings, error)` – ohne Zeile in `terminal_settings`: `{card_pin, true}`.
  - `func (h *Handler) saveTerminalLoginSettings(ctx context.Context, userID string, s terminalLoginSettings) error` – Upsert wie `saveTerminalIdle`; unbekannte Art → Fehler.
  - `func validTerminalPIN(pin string) error`
  - `func (h *Handler) setTerminalPIN(ctx context.Context, userID, pin string) error` (validiert, hasht, setzt Zähler und Sperre zurück)
  - `func (h *Handler) clearTerminalPIN(ctx context.Context, userID string) error` (Hash, Zähler, Sperre auf NULL/0)
  - `func (h *Handler) checkTerminalPIN(ctx context.Context, userID, pin string, now time.Time) (bool, error)` – keine PIN oder gesperrt → `false, nil`; falsch → Zähler +1, beim 5. Fehlversuch `locked_until = now + 15 min` und Zähler 0; richtig → Zähler 0.
  - `func (h *Handler) terminalPINState(ctx context.Context, userID string, now time.Time) (hasPIN bool, lockedUntil *time.Time, err error)`

- [ ] **Step 1: Migration schreiben.** `ALTER TABLE terminal_settings ADD COLUMN IF NOT EXISTS manual_login TEXT NOT NULL DEFAULT 'card_pin' CHECK (manual_login IN ('card_pin','password','card_only','none')), ADD COLUMN IF NOT EXISTS password_button BOOLEAN NOT NULL DEFAULT true;` und `ALTER TABLE users ADD COLUMN IF NOT EXISTS terminal_pin_hash TEXT, ADD COLUMN IF NOT EXISTS terminal_pin_failures INT NOT NULL DEFAULT 0, ADD COLUMN IF NOT EXISTS terminal_pin_locked_until TIMESTAMPTZ;` mit Kopfkommentar im Stil von 156/160. Dann `PDH_MIGRATIONS_APPEND=1 go test ./pkg/database -run TestMigrationsFrozen` und erneut ohne Variable → PASS.

- [ ] **Step 2: Unit-Test `TestValidTerminalPIN`** – gültig: `"4711"`, `"90210"`, `"13579246"`; ungültig: `""`, `"123"`, `"123456789"`, `"12a4"`, `"0000"`, `"1234"`, `"8765"`, `"0123"`, `"456789"`. Laufen lassen → FAIL (undefiniert).

- [ ] **Step 3: `validTerminalPIN` und Konstanten implementieren**, Test → PASS.

- [ ] **Step 4: Integrationstests** `TestTerminalLoginSettingsDefaultAndSave` (Standard ohne Zeile; Speichern/Lesen jeder Art; `"foo"` → Fehler; Rückkehrzeit bleibt beim Speichern unverändert) und `TestTerminalPINLifecycle`:
  - ohne PIN → `checkTerminalPIN` false;
  - `setTerminalPIN("4711")` → richtig true, falsch false;
  - 5× falsch → `terminalPINState` liefert `lockedUntil ≈ now+15min`, danach auch richtige PIN false;
  - mit `now+16min` → richtige PIN true, Zähler 0;
  - 4× falsch, 1× richtig, 4× falsch → nicht gesperrt;
  - `clearTerminalPIN` hebt Sperre und PIN auf;
  - Review Focus 3: 10 nebenläufige falsche Prüfungen (Goroutinen) → danach gesperrt, und `terminal_pin_failures` war nie > 5 (Zähler über `UPDATE users SET terminal_pin_failures = terminal_pin_failures + 1 … RETURNING`).
  Laufen lassen → FAIL.

- [ ] **Step 5: Store-Funktionen implementieren.** bcrypt mit `bcrypt.DefaultCost`; Vergleich erst nach Prüfung der Sperre; der Fehlversuch-Zähler wird atomar in einem `UPDATE … RETURNING` erhöht und bei ≥ 5 im selben Statement gesperrt und zurückgesetzt. Tests → PASS.

- [ ] **Step 6: Commit** `feat(terminal): Anmeldeart je Terminal und Terminal-PIN (Migration 161)`.

### Task 2: `POST /terminal/login` (PDH)

**Files:**
- Create: `internal/web/terminal_login.go`
- Modify: `internal/web/terminal_card.go` (Route in `cardRoutes`; Kommentar zu `cardFailLimit`: zählt alle Anmeldewege)
- Test: `internal/web/terminal_login_test.go` (Unit), `internal/web/terminal_login_integration_test.go` (Helfer `cardHTTP`, `newCardHandler` aus `terminal_card_integration_test.go`)

**Interfaces:**
- Consumes: Task 1 (`terminalLoginSettings`, `checkTerminalPIN`), `cardTerminal`, `cardFail`, `cardFailLimit`, `h.users.GetByRFID`, `h.users.Authenticate(ctx, identifier, password)`, `h.cardSessionStart`, `authLog`, `maskUID`.
- Produces: `func loginKindAllowed(s terminalLoginSettings, kind string) bool`; Handler `func (h *Handler) TerminalLogin(w http.ResponseWriter, r *http.Request)`; Antworten im Format von `/terminal/card`: Erfolg `200 {"ok":true,"name":…}`, Fehlschlag `200 {"ok":false,"message":"Anmeldung fehlgeschlagen"}`, Bremse `429 {"ok":false,"message":"Bitte kurz warten"}`, Art nicht erlaubt `403`, Formfehler `400`.

- [ ] **Step 1: Unit-Test `TestLoginKindAllowed`** als Tabelle: `password` erlaubt bei `PasswordButton=true` (jede Art außer `none`) oder `ManualLogin=password`; bei `ManualLogin=none` immer verboten (auch mit Knopf an); `card_pin` nur bei `ManualLogin=card_pin`; `card_only` nur bei `ManualLogin=card_only`; unbekannte Art verboten. → FAIL.

- [ ] **Step 2: `loginKindAllowed` implementieren** → PASS.

- [ ] **Step 3: Integrationstests `TestTerminalLoginHandler`** (Body `{"kind":…,"card":…,"pin":…}` bzw. `{"kind":"password","user":…,"password":…}`):
  - `card_pin` richtig → ok + Name, Kartensitzung angelegt (`cardRows`=1); falsche PIN, unbekannte Nummer, Benutzer ohne PIN (Review Focus 2), Systembenutzer, inaktiver Benutzer → alle `ok:false` mit identischem Text „Anmeldung fehlgeschlagen“;
  - Kartennummer `"  deadbeef "` passt zu gespeichertem `DEADBEEF` (Review Focus 4);
  - `card_only` richtig → ok;
  - `password` mit Benutzername und mit E-Mail → ok; falsches Passwort → fehlgeschlagen;
  - Einstellung `none` + `card_only`/`card_pin`/`password` → 403 (Review Focus 1); `card_pin` + `card_only` → 403;
  - Bremse: 10 Fehlschläge gemischt (Karte über `/terminal/card` und Eingabe) → 11. Versuch 429, auch mit richtigen Daten;
  - Nicht-Gerätesitzung → 403; falscher Content-Type → 400; Body > 1024 Byte → 400.
  → FAIL.

- [ ] **Step 4: `TerminalLogin` implementieren.** Ablauf: `cardTerminal` → JSON prüfen wie `TerminalCard` → Einstellungen laden → `loginKindAllowed` sonst 403 → Bremse prüfen → je Art Person ermitteln (Kartennummer getrimmt, `GetByRFID`; Passwort über `Authenticate`) → aktiv und kein Systembenutzer prüfen → bei `card_pin` `checkTerminalPIN` → Fehlschlag: `cardFailLimit.fail`, `authLog(r,false,"terminal-"+kind, …)` ohne PIN/Passwort → Erfolg: `cardSessionStart`, `authLog(true…)`. Feldgrenzen: `card` ≤ `cardMaxUID`, `pin` ≤ 8, `user`/`password` ≤ 200 Zeichen. Tests → PASS.

- [ ] **Step 5: Commit** `feat(terminal): Anmeldung ohne Karte über /terminal/login`.

### Task 3: Ereigniskanal `GET /terminal/events` (PDH)

**Files:**
- Create: `internal/web/terminal_events.go`
- Modify: `internal/web/terminal_card.go` (Route), `internal/web/terminal_devices.go` (`TerminalDeviceRevokeWeb`: nach erfolgreichem Sperren Ereignis-Abonnenten des Benutzers beenden), `internal/web/user_detail.go` und `internal/web/terminal_settings.go` (nach Speichern veröffentlichen), Handler-Struktur (Feld für den Verteiler, im Konstruktor anlegen)
- Test: `internal/web/terminal_events_test.go` (Unit), `internal/web/terminal_events_integration_test.go`

**Interfaces:**
- Consumes: `cardTerminal`, Task 1 `terminalLoginSettings`, `h.terminalIdleSeconds`.
- Produces:
  - `type terminalEventHub struct` mit `func newTerminalEventHub() *terminalEventHub`, `func (b *terminalEventHub) subscribe(userID string) (<-chan string, func())`, `func (b *terminalEventHub) publish(userID, event string)` (`userID == ""` = an alle), `func (b *terminalEventHub) closeUser(userID string)` (beendet alle Abos des Benutzers; Kanal wird geschlossen). Puffer 1 je Abo, nicht blockierend; doppelte `settings` werden zusammengefasst.
  - `func (h *Handler) notifyTerminalSettings(userID string)` – `publish(userID,"settings")`.
  - Handler `TerminalEvents`: sendet beim Verbinden und bei jedem `settings` `event: settings\ndata: {"manual_login":…,"password_button":…,"idle":…}`; Ping-Kommentar alle 25 s; Ende bei Kontextende oder geschlossenem Kanal. Schreibfrist aufheben wie `serveEvents` im Gerät (`http.NewResponseController(w).SetWriteDeadline(time.Time{})`).

- [ ] **Step 1: Unit-Tests** `TestTerminalEventHub`: Abo erhält `publish` für den eigenen Benutzer und für `""`, nicht für fremde; volles Abo blockiert `publish` nicht; `closeUser` schließt nur die Kanäle dieses Benutzers; Abmelden entfernt das Abo. → FAIL.

- [ ] **Step 2: `terminalEventHub` implementieren** → PASS (mit `-race`).

- [ ] **Step 3: Integrationstests** `TestTerminalEventsStream` mit `httptest.NewServer` und Gerätesitzung: erstes Ereignis ist `settings` mit den gespeicherten Werten; nach `saveTerminalLoginSettings` + `notifyTerminalSettings` kommt ein neues `settings` mit neuer Art; Nicht-Gerätesitzung → 403; Sperren über `TerminalDeviceRevokeWeb` → Strom endet, neuer Aufruf → 403 (Review Focus 5). → FAIL.

- [ ] **Step 4: Handler und Anbindung implementieren.** `notifyTerminalSettings` nach erfolgreichem `saveTerminalIdle`/`saveTerminalLoginSettings` in `user_detail.go`; nach Speichern der globalen Rückkehrzeit in `TerminalSettingsWeb` `publish("", "settings")`. Tests → PASS.

- [ ] **Step 5: Commit** `feat(terminal): Ereigniskanal /terminal/events für Einstellungsänderungen`.

### Task 4: Oberfläche in PDH – Einstellungen und PIN (PDH)

**Files:**
- Modify: `web/templates/user_detail.gohtml` (Karte „Anlagenterminal“ neben `terminal_idle_seconds`, Zeile ~167; Benutzer-Stammdaten: Feld „Terminal-PIN“), `internal/web/user_detail.go` (Formularfelder `terminal_manual_login`, `terminal_password_button`; PIN-Aktionen), `web/templates/account.gohtml` und `internal/web/handler.go` (Route `POST /account/terminal-pin`), Daten für die Vorlage
- Test: `internal/web/terminal_login_ui_integration_test.go`

**Interfaces:**
- Consumes: Task 1 (alle Store-Funktionen), Task 3 `notifyTerminalSettings`.
- Produces: Formularfelder `terminal_manual_login` (Select mit den vier Werten, Beschriftungen „Kartennummer + PIN“, „Benutzer + Passwort“, „Nur Kartennummer“, „Keine“), `terminal_password_button` (Checkbox, Beschriftung „Knopf ‚Mit Benutzer und Passwort‘“); PIN-Formulare `POST /users/{id}/terminal-pin` (`action=set|clear`, Feld `pin`) und `POST /account/terminal-pin` (gleiche Felder, nur eigener Benutzer).

- [ ] **Step 1: Integrationstests:** Speichern der Anmeldeart über das Stammdatenformular nur mit „Benutzer verwalten“ und nur bei Systembenutzern (sonst unverändert); Warntext bei `card_only` erscheint in der gerenderten Seite; PIN setzen/entfernen über `/users/{id}/terminal-pin` nur mit „Benutzer verwalten“ (sonst 403); `/account/terminal-pin` setzt nur die eigene PIN; ungültige PIN → Fehlermeldung, nichts gespeichert; gesperrte PIN zeigt „PIN gesperrt bis HH:MM“; die PIN erscheint nie im HTML. → FAIL.

- [ ] **Step 2: Implementieren** im Stil der vorhandenen Rückkehrzeit (Prüfung `h.canManageUsers(r)` und `h.isSystemUser`; Fehler über `userRedirect`). → PASS.

- [ ] **Step 3: Commit** `feat(terminal): Anmeldeart und Terminal-PIN in der Benutzerverwaltung`.

### Task 5: Terminalseite, Hilfe, Changelog (PDH)

**Files:**
- Modify: `web/templates/terminal.gohtml`, `web/static/terminal.js`, `internal/web/terminal_page.go` (Vorlagendaten: Anfangswerte `manual_login`, `password_button`), `web/templates/help.gohtml` (Abschnitt „Kartenleser und Kartensitzung“, Zeile ~717), `CHANGELOG.md` (unter `## [0.77.9]`, „Neu“)
- Test: `internal/web/terminal_page_test.go`

**Interfaces:**
- Consumes: `/terminal/login` (Task 2), `/terminal/events` (Task 3), Geräte-Ereignis `reader` (Task 6; älteres Gerät sendet es nicht).
- Produces: in `terminal.js` eine reine Funktion `loginView(readerState, settings)` → `"card" | "card_pin" | "password" | "card_only" | "none"` plus `showPasswordButton(readerState, settings) bool`; Elemente mit IDs `login-panel`, `login-keypad`, `login-password-btn`, `login-dialog`.

- [ ] **Step 1: Vorlagentest** in `terminal_page_test.go`: Seite enthält `login-panel`, `login-password-btn` mit Text „Mit Benutzer und Passwort“, `data-manual-login` und `data-password-button` mit den Werten des Terminals; im Bildschirmschoner-Modus kein Anmeldeteil. → FAIL.

- [ ] **Step 2: Umsetzen.**
  - Zustand `reader = 'ok'`; der Kartenstrom setzt ihn bei Ereignis `reader`; ist nach 3 s kein `reader` gekommen, bleibt `ok`.
  - `loginView`: `ok` → `card`; `none`/`error` → `settings.manual_login`.
  - `showPasswordButton`: `settings.password_button && settings.manual_login !== 'none'`.
  - Ziffernfeld 0–9, A–F, Löschen, OK, Tasten mindestens 64 px; PIN-Feld `type=password`.
  - Dialog Benutzer/Passwort; bei `password` ohne Leser sofort offen.
  - `/terminal/events` per EventSource abonnieren und `settings` übernehmen (inkl. `idle` für den Rückkehr-Timer); Generationszähler wie beim Abmelden beachten.
  - Antworten wie bei Karten: Erfolg → Leiste „Angemeldet: Name“; `ok:false` → Meldung 4 s; 429 → „Bitte kurz warten“.
  - Eingaben nach jedem Versuch leeren.
  → Test PASS; zusätzlich `node --check web/static/terminal.js` falls Node verfügbar, sonst Syntaxprüfung durch den Reviewer.

- [ ] **Step 3: Hilfe und Changelog.** Hilfe: Absatz zu Anmeldung ohne Leser, Anmeldearten, Terminal-PIN, Knopf. CHANGELOG „Neu“: ein Eintrag „Anlagenterminal: Anmeldung ohne Kartenleser …“ mit Anmeldearten, PIN-Sperre 5/15 min und sofortiger Übernahme geänderter Einstellungen.

- [ ] **Step 4: Gesamtlauf PDH:** `go vet ./...`, `go test ./...`, `go test -tags integration ./internal/web/ ./internal/core/users/ ./pkg/database/` → alles PASS.

- [ ] **Step 5: Commit** `feat(terminal): Anmeldung ohne Karte auf der Terminalseite`.

### Task 6: Leserstatus im Kartenstrom (Pi-Repo)

**Files:**
- Modify: `internal/product/pdhterminal/card/service.go`, `internal/product/pdhterminal/card/events.go`, `internal/product/pdhterminal/run.go:305` (Aufruf von `EventsHandler`), `docs/betrieb.md` (Abschnitt „Kartenleser“), `dist/geraetetest-tp2.md` (neuer Teil J)
- Test: `internal/product/pdhterminal/card/service_test.go`, `internal/product/pdhterminal/card/events_test.go`

**Interfaces:**
- Produces:
  - Konstanten `StateOK = "ok"`, `StateNone = "none"`, `StateError = "error"`.
  - `func (s *Service) ReaderState() string` – Typ aus → `none`; `Status.Active` → `ok`; Detail „Leser startet“ → `ok` (kein Aufblitzen der Eingabe beim Start); sonst `error`.
  - `func (s *Service) WatchState() (<-chan struct{}, func())` – Signal (Puffer 1, nicht blockierend) bei jeder Änderung von `ReaderState()`.
  - `func EventsHandler(h *Hub, st StateSource, origin func() string) http.Handler` mit `type StateSource interface { ReaderState() string; WatchState() (<-chan struct{}, func()) }`; `serveEvents` sendet nach dem Header sofort `event: reader\ndata: {"state":"…"}` und bei jedem Signal erneut, aber nur, wenn sich der Zustand gegenüber dem zuletzt gesendeten geändert hat.

- [ ] **Step 1: Tests:** `TestServiceReaderState` (aus → `none`; Start → `ok`; Öffnen scheitert → `error`; stabil → `ok`; Wechsel löst `WatchState`-Signal aus), `TestEventsReaderEvent` (erstes Ereignis ist `reader` mit aktuellem Zustand; Zustandswechsel → neues `reader`; gleicher Zustand → kein doppeltes; `card`-Ereignisse unverändert; Origin-Prüfung unverändert). → FAIL.

- [ ] **Step 2: Implementieren**; Statuswechsel in `setStatus`/`Apply` lösen das Signal aus. Tests → PASS mit `scripts/dgo test -race ./internal/product/pdhterminal/...`.

- [ ] **Step 3: Doku.** `docs/betrieb.md`: Satz zum Ereignis `reader` und dass die PDH-Seite ohne Leser die in PDH eingestellte Anmeldeart zeigt. `dist/geraetetest-tp2.md` Teil J „Anmeldung ohne Leser“: J1 Typ „aus“ → Terminalseite zeigt Eingabe der eingestellten Art; J2 Kartennummer + PIN anmelden; J3 5× falsche PIN → „Anmeldung fehlgeschlagen“, in PDH „PIN gesperrt bis …“; J4 Anmeldeart in PDH ändern → Terminal schaltet ohne Neuladen um; J5 Leser wieder an → „Karte auflegen“ und Knopf „Mit Benutzer und Passwort“.

- [ ] **Step 4: Gesamtlauf Pi:** `scripts/dgo test -race ./...`, `scripts/dgo vet ./...` (arm64-Vet wie bisher) → PASS.

- [ ] **Step 5: Commit** `feat(pdh-terminal): Leserstatus im Kartenstrom (Ereignis reader)`.
