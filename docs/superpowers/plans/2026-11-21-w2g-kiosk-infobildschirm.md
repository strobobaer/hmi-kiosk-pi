# W2+G Windows-Kiosk und globales Terminal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:**
- Terminals lassen sich in PDH zum globalen Infobildschirm umschalten: Folienschau, Vorgänge nur lesen, offline aus dem Zwischenspeicher.
- Windows-PCs lassen sich per Setup als gesperrtes Kioskgerät einrichten.

**Architecture:**
- **PDH:** Schalter `global_screen`, Tabelle `terminal_slides`, Verwaltungsseite, Abspieler-Modus `global` der Terminalseite, Vorgang-Ansicht nur lesen.
- **Terminalprogramm:** Zwischenspeicher-Endpunkt und lokale Offline-Folienschau; ist der Zwischenspeicher da, ist der Leser aus.
- **Windows-Kiosk:** eigener Benutzer mit eigener Shell (Starter), Autologon über LSA, Benutzerrichtlinien. Alles richtet ein Kiosk-Helfer ein, den das Inno-Setup aufruft. Edge läuft im Kioskmodus, die Admin-Geste kommt über einen Maus-Hook.

**Tech Stack:**
- **Terminal:** Go 1.24 (`scripts/dgo`), `golang.org/x/sys/windows`, Inno Setup 6 (Docker).
- **PDH:** Go/chi, PostgreSQL, Vanilla-JS.

**Spec:** `docs/superpowers/specs/2026-11-21-w2g-kiosk-infobildschirm-design.md`

## Global Constraints

- **Rückwärtskompatibel:**
  - Nur hinzufügende Migrationen und optionale Felder; alte `config.json` lädt.
  - Alte Terminals an neuem PDH und neue Terminals an altem PDH arbeiten weiter.
  - Linux-Pakete bleiben byteweise gleich; das Pi-Verhalten von Anlagen-Terminals bleibt unverändert.
- **PDH-Migrationen:**
  - Nie ändern, nur hinzufügen.
  - `PDH_MIGRATIONS_APPEND=1 go test ./pkg/database -run TestMigrationsFrozen` trägt die neue ein.
  - Neue Tabellen: `id UUID PRIMARY KEY DEFAULT pdh_uuid_v7()`.
  - Vor dem Merge gegen `origin/main` abgleichen; bei Kollision die eigene Nummer umnummerieren.
- **PDH-Arbeitsort:**
  - Worktree `C:\workplace\pdh-server-w2g`, Zweig `feat/infobildschirm` von `origin/main`, nach `git fetch origin main`.
  - Nie den Hauptcheckout `C:\workplace\pdh-server`, nie die Container `pdh-gt-*`.
  - Labor-Datenbank: `PDH_TEST_DSN='postgres://pdh:lab-pdh-test@127.0.0.1:54330/pdh_test?sslmode=disable'` (Container `pdh-lab-mssql-postgres-1`).
  - AGENTS.md beachten: VERSION, CHANGELOG, Handbuch-Kapitel, Rechte per Migration.
- **Terminal-Arbeitsort:** Worktree `C:\workplace\hmi-kiosk-pdhterm`, Zweig `feat/w2g` von `feat/pdh-terminal`. Bau nur über `scripts/dgo`, `CGO_ENABLED=0`.
- **Windows-Systemeinstellungen nie auf dem Entwicklungsrechner anwenden:** keine Benutzer, kein Autologon, keine Shell, keine Richtlinien, kein Setup-Lauf. Kiosk-Code wird gebaut und mit `vet` geprüft; die Logik wird plattformneutral getestet.
- **Git-Regeln:**
  - Nie `git stash` ohne Namen; nie pushen.
  - `.secrets/` und `hardware/gehaeuse/fusion360/PDH-Terminal-Gehaeuse/.vscode/` nie committen.
  - Commit-Trailer `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- **Windows-Lehren aus W1:**
  - `syscall.NewCallback` nur einmal je Prozess.
  - In Hook- und Fenster-Threads nie blockieren, sperren oder protokollieren.
  - Programme als GUI bauen (`-H windowsgui`).
- **Werte:**
  - Folienarten `image`, `text`, `vorgaenge`.
  - Dauer 5–600 s, Vorgabe 15.
  - Text höchstens 2000 Zeichen.
  - Bild JPEG/PNG/WebP, höchstens 5 MB.
  - Zwischenspeicher je Folie höchstens 5 MB, gesamt höchstens 50 MB.
  - Höchstens 12 Vorgänge je Art.
  - Recht `terminal.slides.edit` „Infobildschirm bearbeiten“.
  - Kiosk-Benutzer `PDH-Kiosk`.
  - Admin-Geste: 80 × 80 px oben rechts, 5 s halten.
  - Setup-Schalter `/MODE=zusatz|kiosk`.
  - Kiosk auf den Editionen Professional*, Enterprise*, Education*.

## Review Focus

1. **Vorgang-Ansicht:** Ein globales Terminal öffnet per erratener ID einen Vorgang einer nicht freigegebenen Anlage → 404 (Test in Task 3).
2. **Gerätesitzung im Modus global:** Sie darf weder Karten melden noch sich anmelden (`/terminal/card` → 403) (Test in Task 3).
3. **Zwischenspeicher:** Ein halber oder zu großer Zwischenspeicher (abgebrochene Übertragung, 60 MB) wird abgelehnt, und der vorige bleibt erhalten (Test in Task 4).
4. **Kiosk-Rückbau:** Scheitert ein Einrichtungsschritt, werden die schon gemachten Schritte zurückgenommen; `-undo` ist wiederholbar und unschädlich, wenn nichts eingerichtet ist (Test in Task 5).
5. **Admin-Geste:** Kurzes Antippen oben rechts tut nichts; nur 5 s Halten öffnet die Admin-Seite. Ein Ziehen aus dem Feld hinaus bricht ab (Test in Task 6).

---

### Task 1: PDH: Datenmodell, Schalter und Recht

**Arbeitsort:** PDH-Worktree.
**Files:**
- Create: `migrations/<N>_terminal_global_screen.up.sql`
- Modify: `migrations/checksums.txt`; `internal/web/terminal_login_store.go` (`terminalLoginSettings.GlobalScreen bool`); `internal/web/terminal_events.go` (Feld `global_screen`); `internal/web/user_detail.go`, `web/templates/user_detail.gohtml` (Kästchen mit Marker `terminal_global_screen_sent=1`)
- Create: `internal/web/terminal_slides_store.go` mit Tests

**Interfaces (Produces):**
- **Migration:**
  - `terminal_settings.global_screen BOOLEAN NOT NULL DEFAULT false`.
  - Tabelle `terminal_slides` wie Spec 6.1, mit CHECKs.
  - Recht `terminal.slides.edit`.
- `type terminalSlide struct { ID, Kind, Title, Body, ImageType string; Image []byte; InfraIDs []string; ShowFaults, ShowTickets, Active bool; Position, DurationSeconds int; UpdatedAt time.Time }`
- Funktionen an `*Handler`:
  - `listTerminalSlides(ctx, activeOnly bool) ([]terminalSlide, error)` (ohne Bilddaten).
  - `getTerminalSlide(ctx, id) (terminalSlide, error)`.
  - `saveTerminalSlide(ctx, *terminalSlide) error`: neue Folie ans Ende.
  - `deleteTerminalSlide(ctx, id) error`.
  - `moveTerminalSlide(ctx, id string, delta int) error`: tauscht die Position mit der Nachbarfolie.
  - `terminalSlideImage(ctx, id) ([]byte, string, error)`.
  - `slidesVersion(ctx) (string, error)`: Hash über IDs, `updated_at` und Reihenfolge der aktiven Folien.
- `validateTerminalSlide(*terminalSlide) error` mit den Grenzen aus den Global Constraints. Die Bildart wird per `http.DetectContentType` geprüft, nicht nach dem Dateinamen.
- `notifyGlobalTerminals()` veröffentlicht das Ereignis `slides` an alle Terminal-Benutzer mit `global_screen`.

- [ ] **Step 1: Tests schreiben** (Integration auf der Labor-Datenbank):
  - Speichern, Liste, Reihenfolge, Verschieben an den Rändern (kein Fehler, keine Änderung) und Löschen.
  - Abgelehnt werden: Dauer 4 und 601, Text mit 2001 Zeichen, Bild mit 5 MB + 1 Byte, Bild mit PNG-Namen und Textinhalt.
  - `slidesVersion` ändert sich nach jeder Änderung.
  - `global_screen` erscheint im Ereignis `settings`.
  - Speichern ohne Marker lässt `global_screen` unverändert.
  - Ohne `canManageUsers` wird nichts gespeichert.
  - Migration mit `TestMigrationsFrozen` und `TestUUIDv7Policy`; die neue Tabelle hat keine Altlast.
- [ ] **Step 2:** Die Tests scheitern.
- [ ] **Step 3:** Umsetzen, VERSION und CHANGELOG pflegen.
- [ ] **Step 4:** Tests grün; `go vet ./...`.
- [ ] **Step 5:** Commit `feat(terminal): Schalter Globales Terminal und Folienspeicher`.

### Task 2: PDH: Verwaltungsseite „Infobildschirm“

**Arbeitsort:** PDH-Worktree.
**Files:** Create: `internal/web/terminal_slides.go`, `web/templates/terminal_slides.gohtml`. Modify: Router, Menü, `web/templates/help.gohtml` (Kapitel „Infobildschirm“ und Verweis im Anlagenterminal-Kapitel).
**Interfaces:**
- **Consumes:** Task 1.
- **Produces:**
  - **Routen** (alle mit `terminal.slides.edit`, sonst 403):
    - `GET /terminal-slides`
    - `GET /terminal-slides/new?kind=…`
    - `GET /terminal-slides/{id}`
    - `POST /terminal-slides` (anlegen)
    - `POST /terminal-slides/{id}` (ändern)
    - `POST /terminal-slides/{id}/delete`
    - `POST /terminal-slides/{id}/move?d=-1|1`
    - `GET /terminal-slides/{id}/image`
  - CSRF wie bei den anderen Formularen.
  - Bild-Upload als multipart; das Muster liefert `internal/web/uploads.go`.
  - Jede Änderung ruft `notifyGlobalTerminals()`.
- [ ] **Step 1: Tests schreiben**
  - Ohne Recht 403.
  - Anlegen jeder Art mit den Formularfeldern aus Spec 6.2.
  - Ungültige Eingaben zeigen die Fehlermeldung im Formular, ohne zu speichern.
  - Verschieben, Löschen.
  - `TestHelpCoversAllPages` grün.
  - Das Menü zeigt den Eintrag nur mit dem Recht.
- [ ] **Step 2:** Die Tests scheitern.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4:** Tests grün.
- [ ] **Step 5:** Commit `feat(terminal): Verwaltungsseite Infobildschirm`.

### Task 3: PDH: Abspieler, Folien-JSON und Vorgang-Ansicht

**Arbeitsort:** PDH-Worktree.
**Files:** Modify: `internal/web/terminal_page.go` (Modus `global`), `web/templates/terminal.gohtml`, `web/static/terminal.js`, `internal/web/terminal_card.go` (Sperre). Create: `internal/web/terminal_global.go`, `web/templates/terminal_vorgang.gohtml`.
**Interfaces:**
- **Consumes:** Task 1.
- **Produces:**
  - **Modus `global`:** Eine Gerätesitzung mit `global_screen` bekommt `/terminal` (auch `/terminal/info`) im Modus `global`, ohne `data-card-events` und ohne Anmeldeteil.
  - **Kartenwege gesperrt:** `/terminal/card`, `/terminal/card/ping` und `/terminal/login` antworten für globale Terminals mit 403 „Globales Terminal: keine Anmeldung“.
  - **`GET /terminal/slides.json`** (nur Gerätesitzung, nur global, sonst 403): `{version, idle, slides:[{id, kind, title, body, duration, image_url, faults:[…], tickets:[…], faults_total, tickets_total}]}`. Vorgänge wie in `loadTerminalInfo`, über mehrere Anlagen samt Unteranlagen (leer = alle), höchstens 12 je Art.
  - **`GET /terminal/slides/{id}/image`** (nur Gerätesitzung, nur global): mit ETag und `Cache-Control: private, max-age=300`.
  - **`GET /terminal/vorgang/{fault|ticket}/{id}`** (nur Gerätesitzung, nur global): nur lesen, wie Spec 6.3. Gehört der Vorgang zu keiner Anlage einer aktiven Vorgänge-Folie, oder fehlt das Recht → 404.
  - **JS, reine Funktionen** in `terminal.js`:
    - `nextSlideIndex(slides, i)`
    - `slideDurationMs(slide)`
    - `isSwipeLeft(start, end)` (≥ 60 px horizontal, < 40 px vertikal)
    - `cachePayload(json, images)`
  - **Ablauf:**
    - Folienwechsel nach Dauer, Tippen oder Wischen blättert weiter, Vorgang antippen öffnet die Ansicht.
    - Das Ereignis `slides` oder eine geänderte `version` lädt neu.
    - Ist `global_screen` aus, lädt die Seite neu.
  - **Zwischenspeicher:** Nach jedem Laden mit neuer `version` geht `POST http://127.0.0.1:8080/api/slides/cache` ab: `{version, saved_at, slides, images:{id: base64}}`, je Bild ≤ 5 MB, gesamt ≤ 50 MB, sonst ohne Bilder. Ein normales Terminal (nicht global) schickt einmal je Seitenaufruf `DELETE /api/slides/cache`. Fehler und 404 still ignorieren, wie bei `/api/card/result`.
- [ ] **Step 1: Tests schreiben**
  - Globale Gerätesitzung: `/terminal` ist im Modus `global` ohne `data-card-events`; `/terminal/card` antwortet 403.
  - Normale Sitzung: Anlagen-Terminal unverändert (bestehende Tests grün).
  - `slides.json`:
    - nur aktive Folien, in Reihenfolge;
    - Vorgänge gefiltert nach Anlagen und Rechten;
    - höchstens 12 je Art;
    - 403 für Nicht-Gerätesitzung und für Nicht-global.
  - Vorgang-Ansicht:
    - erlaubte Anlage → 200 ohne Formulare und Links außer „Zurück“;
    - fremde Anlage → 404;
    - Ticket ohne Ticket-Recht → 404.
  - Node-Tests der JS-Funktionen.
- [ ] **Step 2:** Die Tests scheitern.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4:** Tests grün.
- [ ] **Step 5:** Commit `feat(terminal): Folienschau und Vorgang-Ansicht für globale Terminals`.

### Task 4: Terminal: Zwischenspeicher, Offline-Folienschau, Leser aus

**Files:**
- Create: `internal/product/pdhterminal/slides/cache.go`, `internal/product/pdhterminal/slides/offline.go`, `internal/product/pdhterminal/slides/offline.html` mit Tests
- Modify: `card/events.go` (Routen unter `/api/slides/cache` wie `/api/card/result`: gleiche Herkunft, CORS, Private-Network)
- Modify: `start.go` (offline → `/slides`, wenn ein Zwischenspeicher da ist)
- Modify: `run.go` (Leser aus, solange ein Zwischenspeicher da ist)

**Interfaces (Produces):**
- `slides.Store{Dir string}`:
  - `Save(payload []byte) error`: Grenzen, JSON-Prüfung, atomar, alte Version erst nach Erfolg weg.
  - `Load() (*Snapshot, error)`
  - `Clear() error`
  - `Present() bool`
- `POST /api/slides/cache`: Inhalt ≤ 60 MB lesen, danach Grenzen prüfen, 204 oder 413/400. `DELETE /api/slides/cache` → 204.
- `GET /slides` auf dem lokalen Server: Offline-Folienschau aus dem Zwischenspeicher, gleiche JS-Funktionen wie PDH (Kopie, eingebettet), Bilder unter `/slides/img/{id}`, Vermerk „Server offline“, bei Vorgängen „Stand <Zeit>“, Vorgänge nicht antippbar. Rückkehr wie die Offline-Startseite heute (alle 30 s prüfen).
- Kartendienst: `Present()` → Leser aus. Statusdetail „Globales Terminal: Kartenleser aus“. Wechsel ohne Neustart.

- [ ] **Step 1: Tests schreiben**
  - Gültiger Zwischenspeicher wird gespeichert und geladen.
  - Abgelehnt, alter bleibt (Review Focus 3): 60 MB; ein Bild über 5 MB; kaputtes JSON; abgebrochener Schreibvorgang (Fehler beim Umbenennen simuliert).
  - `DELETE` leert.
  - Herkunft und CORS wie bei `/api/card/result`.
  - Offline-Startseite: mit Zwischenspeicher → Weiterleitung `/slides`, ohne → wie bisher.
  - Leser: Zwischenspeicher da → Kartendienst aus mit Detail; gelöscht → wieder an.
- [ ] **Step 2:** Die Tests scheitern.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4:** Prüfen.
  - Tests grün; Windows-Bau und `vet` grün; `bash packaging/test-products.sh`.
  - Probelauf: `dist\win\pdh-terminal.exe -no-chromium` mit Scratch-Ordnern, Zwischenspeicher per `Invoke-WebRequest` (mit Origin) hochladen, `/slides` liefert die Folien.
- [ ] **Step 5:** Commit `feat(pdh-terminal): Zwischenspeicher und Offline-Folienschau für globale Terminals`.

### Task 5: Windows: Kiosk-Helfer

**Files:**
- Create: `cmd/pdh-terminal-kiosk/main.go` (Build-Tag windows)
- Create: `internal/winkiosk/plan.go` (plattformneutral: Schritte und Registry-Werte als Daten)
- Create: `internal/winkiosk/apply_windows.go`
- Create: `internal/winkiosk/plan_test.go`

**Interfaces (Produces):**
- `winkiosk.Plan(app string) []Step`, mit `Step{Name string; Do, Undo Action}`. Die Schritte in dieser Reihenfolge:
  1. Benutzer `PDH-Kiosk` anlegen (Standardbenutzer, zufälliges 32-Zeichen-Passwort aus `crypto/rand`).
  2. Profil anlegen.
  3. Hive laden.
  4. Shell `"<app>\pdh-terminal-start.exe" -kiosk`.
  5. Richtlinien `DisableTaskMgr`, `DisableLockWorkstation`, `DisableChangePassword`, `NoRun`, `NoWinKeys`, `NoViewContextMenu`, `ScreenSaveActive=0`, `ScreenSaveIsSecure=0`, Benachrichtigungen aus.
  6. Hive entladen.
  7. Autologon: `AutoAdminLogon=1`, `DefaultUserName=PDH-Kiosk`, `DefaultDomainName=.`, Passwort als LSA-Geheimnis `DefaultPassword`.
  8. ACL des Datenordners: nur Administratoren, SYSTEM, `PDH-Kiosk`.
- `winkiosk.Run(steps, exec) error`: führt aus. Scheitert ein Schritt, nimmt Run die schon gemachten Schritte rückwärts zurück und meldet „Schritt <Name>: <Fehler>“.
- `winkiosk.Undo(steps, exec) error`: alle Schritte rückwärts. Was fehlt, ist kein Fehler; deshalb ist Undo wiederholbar.
- **CLI:**
  - `pdh-terminal-kiosk.exe -setup -app <dir>` und `-undo`.
  - Rückgabewert 0 bei Erfolg, 2 bei Edition nicht unterstützt, 1 sonst.
  - Protokoll nach `%ProgramData%\PDH-Terminal\log\kiosk.log`, ohne Passwort.
- **Edition:** `winkiosk.EditionSupported(editionID string) bool`, wahr für Präfixe `Professional`, `Enterprise`, `Education`. `Core*` ist Home und damit falsch.

- [ ] **Step 1: Tests schreiben** (plattformneutral, mit Fake-`exec`):
  - Reihenfolge und Werte der Schritte.
  - Scheitert Schritt 5, werden 4, 3, 2 und 1 zurückgenommen, in dieser Reihenfolge.
  - Undo ohne eingerichteten Kiosk: kein Fehler.
  - Undo zweimal: kein Fehler.
  - Das Passwort kommt in keinem Protokoll und keiner Fehlermeldung vor.
  - `EditionSupported`: `Professional`, `ProfessionalN`, `Enterprise`, `Education` → wahr; `Core`, `CoreSingleLanguage` → falsch.
- [ ] **Step 2:** Die Tests scheitern.
- [ ] **Step 3:** Umsetzen.
  - Win32: `NetUserAdd`, `CreateProfile`, `RegLoadKey`/`RegUnLoadKey`, `LsaStorePrivateData`, `SetNamedSecurityInfo` über `x/sys/windows`.
  - **Nie** auf dem Entwicklungsrechner ausführen.
- [ ] **Step 4:** Tests grün; `GOOS=windows vet` und Bau von `./cmd/pdh-terminal-kiosk` grün (GUI-Subsystem nicht nötig, weil das Setup das Programm unsichtbar startet).
- [ ] **Step 5:** Commit `feat(windows): Kiosk-Helfer mit Rückbau`.

### Task 6: Windows: Terminal im Kiosk

**Files:** Modify: `cmd/pdh-terminal-start` und `internal/winstart` (Schalter `-kiosk` weitergeben), `cmd/pdh-terminal` (`-kiosk`), `internal/browser/edge.go` (`EdgeKioskArgs`), `internal/product/pdhterminal` (Kiosk: kein Minimieren, kein Tray, keine Benachrichtigung, Admin-Geste, Admin-Abschnitt „Kiosk“). Create: `internal/platform/corner_windows.go` (Maus-Hook).
**Interfaces (Produces):**
- `browser.EdgeKioskArgs(startURL, profileDir string) []string`: `--kiosk <url> --edge-kiosk-type=fullscreen --no-first-run --user-data-dir=…` sowie die Hintergrund-Schalter aus W1.
- `platform.WatchCorner` unter Windows über `WH_MOUSE_LL`. Gleiche `CornerCfg` und gleiche Erkennungslogik wie unter Linux, zerlegt in eine plattformneutrale Funktion `cornerGesture` mit Down/Move/Up-Ereignissen; die Linux-Fassung nutzt sie mit.
- **Kiosk-Modus:**
  - Edge startet nach einem Ende sofort neu, sichtbar, ohne `OnRestart`-Minimieren.
  - Kein Tray, `notify` ist aus.
  - Die Admin-Geste ruft `sup.Show(TargetAdmin)`.
  - Admin-Abschnitt „Kiosk“ mit „Kiosk-Benutzer abmelden“ (`ExitWindowsEx(EWX_LOGOFF)`) und dem Hinweis aus Spec 7.2.
- [ ] **Step 1: Tests schreiben**
  - `EdgeKioskArgs` in fester Reihenfolge.
  - `cornerGesture` (Review Focus 5): Halten 5 s im Feld → auslösen; 4,9 s → nichts; Ziehen hinaus → Abbruch; Antippen → nichts; Mehrfinger wie unter Linux.
  - Bestehende Linux-Corner-Tests unverändert grün.
  - Kiosk-Optionen: kein Tray, kein Minimieren.
- [ ] **Step 2:** Die Tests scheitern.
- [ ] **Step 3:** Umsetzen (Lehren aus W1: Callback einmal, Hook-Thread blockiert nicht).
- [ ] **Step 4:** Prüfen.
  - Tests grün; Windows-Bau, `vet` und native Testbinaries grün.
  - Probelauf: nur mit `-no-chromium -kiosk`, um zu sehen, dass der Hook installiert wird und die Geste per eingespielter Maus (SendInput) auslöst. Keine Abmeldung auslösen.
- [ ] **Step 5:** Commit `feat(windows): Terminal im Kiosk mit Admin-Geste`.

### Task 7: Setup: Betriebsart und Doku

**Files:** Modify: `packaging/windows/pdh-terminal.iss`, `packaging/windows/build-setup.sh` (auch `pdh-terminal-kiosk.exe` bauen und packen), `packaging/test-products.sh`, `docs/betrieb.md`, `README.md` (de/en).
**Interfaces:**
- **Consumes:** Task 5 CLI, Task 6 `-kiosk`.
- **Produces:**
  - **Seite „Betriebsart“** mit Zusatz (Vorgabe) und Kiosk. Kiosk ist auf nicht unterstützter Edition gesperrt, mit Meldung aus Spec 7.1.
  - **Schalter:** `/MODE=zusatz|kiosk` und `/RESTART=yes`.
  - **Kiosk:**
    - `pdh-terminal-kiosk.exe -setup -app "{app}"`.
    - Scheitert das, meldet das Setup den Fehler und bleibt bei Zusatz.
    - Der Run-Eintrag für alle Benutzer entfällt im Kiosk, weil die Shell startet.
    - Neustart anbieten.
  - **Zusatz nach Kiosk:** `-undo`.
  - **Deinstallation:** `-undo` vor dem Löschen.
  - **ACL:** Bei Zusatz bleibt `users-modify`, im Kiosk setzt der Helfer sie.
- [ ] **Step 1:** Prüfungen in `test-products.sh` ergänzen; sie scheitern zunächst.
  - `[Code]` prüft die Edition.
  - `/MODE` wird ausgewertet.
  - Die Deinstallation ruft `-undo`.
  - `pdh-terminal-kiosk.exe` ist in `[Files]`.
- [ ] **Step 2:** Umsetzen, Doku schreiben: Kiosk einrichten, Admin-Geste, an Windows kommen (Strg+Alt+Entf → Benutzer wechseln), Rückbau, Grenzen.
- [ ] **Step 3:** Prüfen.
  - `bash packaging/test-products.sh` grün.
  - `build-setup.sh --version 0.5.0` mit Wegwerf-Schlüssel baut. **Nicht ausführen.**
- [ ] **Step 4:** Commit `feat(windows): Setup mit Betriebsart Kiosk`.

### Task 8: Gesamtprobe und Geräteprüfliste

**Files:** `dist/geraetetest-w2g.md` (git-ignoriert)
- [ ] **Step 1:** Prüfliste nach Spec 8 schreiben, jeder Punkt mit „Erwartet:“.
- [ ] **Step 2:** Volle Suiten beider Repos.
  - **Terminal:** `scripts/dgo test -race ./...`, `vet` für linux/arm64, linux/amd64 und Windows, `test-products.sh`.
  - **PDH:** `go vet`, `go test`, Integrationstests mit der Labor-Datenbank.
- [ ] **Step 3:** Nichts committen. Ergebnis ins Ledger.
