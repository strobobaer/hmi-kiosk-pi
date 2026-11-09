# Anlagenterminal Teilprojekt 1 (Grundgerüst) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** PDH bekommt die Terminalseite `/terminal` (Bildschirmschoner → Störungsinfo → „Neu“), Terminal-Einstellungen (Rückkehrzeit) und pro HMI-Verbindung die Felder „Ansehen/Bedienen am Terminal“. Weitere Terminal-Einstellungen der Spec (QR-Gültigkeit, Kartenpflicht, Playlist-Mischung) kommen mit ihren Teilprojekten, damit keine wirkungslosen Felder entstehen.

**Architecture:** Alles in PDH, server-gerendert wie die übrigen Seiten (Go `html/template`, eigenes Vollbild-Template ohne Navigation, wenig JS). Das Terminal ist der vorhandene System-Benutzer mit `users.terminal_infrastructure_id`; die Anlage samt Unteranlagen liefert `infraSubtreeIDs`. Die Pi-Seite ist das eigene Produkt PDH-Terminal (Teilprojekt 0); bis dahin lässt sich `/terminal` in jedem Browser im Vollbild testen. Dieser Plan ändert nichts an HMI-Kiosk.

**Tech Stack:** Go 1.24, chi, pgx/PostgreSQL, gohtml-Templates, Vanilla-JS.

**Spec:** `docs/superpowers/specs/2026-11-09-anlagenterminal-design.md` (Abschnitte „Ablauf am Terminal“, „Teil 1“, Umsetzung Schritt 1).

**Arbeitsort:** neuer Worktree `C:\workplace\pdh-server-terminal`, Zweig `feat/anlagenterminal`, abgezweigt von `feat/kiosk-terminal` (`git -C C:\workplace\pdh-server-kiosk worktree add -b feat/anlagenterminal ../pdh-server-terminal feat/kiosk-terminal`). Go per Docker: `MSYS_NO_PATHCONV=1 docker run --rm -v C:/workplace/pdh-server-terminal:/src -w /src -v pdh-kiosk-gomod:/go/pkg/mod -v pdh-kiosk-gobuild:/root/.cache/go-build golang:1.24-bookworm go …` (im Plan kurz `GO …`). Integrationstests wie in `internal/web/kiosk_integration_test.go` gegen einen Wegwerf-`postgres:16`.

## Global Constraints

- Regeln aus `AGENTS.md` gelten: Handbuch (`web/templates/help.gohtml`, `helpChapterIDs`), `CHANGELOG.md`-Eintrag, `VERSION` nächste Patch-Version **0.77.10**, Migration mit nächster freier Nummer (**156**, vor dem Merge gegen `origin/main` prüfen).
- UI, Kommentare, Commits deutsch; Commit-Präfixe `feat:`/`test:`/`docs:`; jede Nachricht endet mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Nicht pushen.
- Touch-Regeln der Terminalseite: Trefferflächen ≥ 64 px, keine Hover-Bedienung, wenig Text, Vollbild ohne PDH-Navigation.
- HMI-Werte exakt: Ansehen `everyone` · `card` · `never` (Standard `everyone`); Bedienen `never` · `card` · `everyone` (Standard `card`); `allow_control = false` ⇒ wirksam `never`.
- Rückkehrzeit: Standard **120 s**, erlaubt 30–3600 s.
- Rechte am Terminal immer mit Abteilungsbezug für die Anlage (vorhandene `hasPermRec`-Logik); der Terminal-Benutzer sieht nur seinen Teilbaum.

## Review Focus

1. Terminal-Benutzer **ohne zugeordnete Anlage** oder mit **ausgeblendeter/gelöschter** Anlage → klare Meldung „Diesem Terminal ist keine Anlage zugeordnet“, kein 500, keine leere Liste ohne Erklärung (Test in Task 3).
2. **Kein System-Benutzer** ruft `/terminal` auf → 403 mit Hinweis, keine Daten (Test in Task 3).
3. **Sehr viele offene Störungen** im Teilbaum → höchstens 50 neueste werden gezeigt plus Gesamtzahl (Test in Task 3).
4. **Ungültige Rückkehrzeit** (0, negativ, Text, > 3600) → Formular lehnt mit deutscher Meldung ab, gespeicherter Wert bleibt (Test in Task 2).
5. **HMI auf „Bedienen: jeder“ bei `allow_control = false`** → gespeichert, aber wirksam `never`; Formular zeigt das an (Test in Task 1).

---

### Task 1: HMI-Felder „Ansehen/Bedienen am Terminal“

**Files:**
- Create: `migrations/156_anlagenterminal.up.sql` (dieser Task legt die HMI-Spalten an; Task 2 ergänzt dieselbe Datei)
- Modify: `internal/web/hmi.go` (Struktur `hmiRow` o. ä. Zeile ~55, Liste ~122, Speichern ~162–210)
- Modify: `web/templates/infra_detail.gohtml` (HMI-Reiter, Formular)
- Create: `internal/web/hmi_terminal.go`, Test: `internal/web/hmi_terminal_test.go`; Integration: `internal/web/anlagenterminal_integration_test.go` (Build-Tag `integration`)

**Interfaces:**
- Produces: `const (hmiTermEveryone = "everyone"; hmiTermCard = "card"; hmiTermNever = "never")`; `func validHMITerminalView(s string) bool`; `func validHMITerminalControl(s string) bool`; `func effectiveHMITerminalControl(allowControl bool, control string) string`; JSON-Felder `terminal_view`, `terminal_control` an der HMI (Liste und Speichern).

- [ ] **Step 1: Failing unit tests** in `hmi_terminal_test.go`:
  `TestValidHMITerminalView` (`everyone`,`card`,`never` gültig; `""`,`all`,`Card` ungültig), `TestValidHMITerminalControl` (dieselben drei gültig), `TestEffectiveHMITerminalControl` (`(false,"everyone")→"never"`, `(false,"card")→"never"`, `(true,"card")→"card"`, `(true,"everyone")→"everyone"`, `(true,"never")→"never"`).
- [ ] **Step 2:** `GO test ./internal/web/ -run HMITerminal` → FAIL (undefined).
- [ ] **Step 3:** Implement in `hmi_terminal.go`.
- [ ] **Step 4:** Migration-Teil: `ALTER TABLE infrastructure_hmis ADD COLUMN IF NOT EXISTS terminal_view TEXT NOT NULL DEFAULT 'everyone' CHECK (terminal_view IN ('everyone','card','never')), ADD COLUMN IF NOT EXISTS terminal_control TEXT NOT NULL DEFAULT 'card' CHECK (terminal_control IN ('never','card','everyone'));`
- [ ] **Step 5:** `hmi.go`: Felder lesen/schreiben; Speichern lehnt ungültige Werte mit 422 „Ansehen am Terminal ungültig“ / „Bedienen am Terminal ungültig“ ab; fehlende Felder im JSON = Standardwerte (alte Clients). Liste liefert zusätzlich `terminal_control_effective`.
- [ ] **Step 6:** Formular im HMI-Reiter: zwei Auswahlfelder mit den deutschen Bezeichnungen aus der Spec; bei „Bedienen: jeder“ roter Hinweis „Jede Person am Terminal kann dieses HMI ohne Karte bedienen.“; bei `allow_control` aus Hinweis „Bedienung ist für dieses HMI ausgeschaltet – am Terminal nur Ansehen.“
- [ ] **Step 7: Integration test** `TestAnlagenterminalHMIFields`: neues HMI ohne Felder → `everyone`/`card`; Speichern `card`/`everyone` mit `allow_control=false` → gespeichert, Liste zeigt `terminal_control_effective = "never"`; ungültiger Wert → 422, Zeile unverändert.
- [ ] **Step 8:** `GO test ./...` und Integrationstest → PASS.
- [ ] **Step 9: Commit** `feat: HMI-Verbindung mit Ansehen/Bedienen am Terminal`.

### Task 2: Terminal-Einstellungen (Rückkehrzeit)

**Files:**
- Modify: `migrations/156_anlagenterminal.up.sql` (Tabelle anhängen)
- Create: `internal/web/terminal_settings.go`, Test: `internal/web/terminal_settings_test.go`
- Modify: `internal/web/user_detail.go` (Abschnitt für System-Benutzer, Speichern bei ~416), zugehöriges Template der Benutzerseite
- Modify: globale Einstellung im vorhandenen Einstellungsmuster (`server_settings`, Schlüssel `terminal.idle_seconds`) samt Formularfeld auf der passenden Einstellungsseite (dort, wo Easy-Mode-Einstellungen liegen, `EasyModeSettingsWeb`)

**Interfaces:**
- Produces: `const terminalIdleDefault = 120`, `terminalIdleMin = 30`, `terminalIdleMax = 3600`; `func parseTerminalIdle(s string) (int, error)`; `func (h *Handler) terminalIdleSeconds(ctx context.Context, userID string) int` (Terminalwert, sonst globaler Wert, sonst 120).

- [ ] **Step 1: Failing tests** `TestParseTerminalIdle`: `"120"→120`, `"30"→30`, `"3600"→3600`; Fehler für `""`, `"0"`, `"-5"`, `"29"`, `"3601"`, `"abc"` mit Meldung „Rückkehrzeit muss zwischen 30 und 3600 Sekunden liegen“.
- [ ] **Step 2:** `GO test ./internal/web/ -run TerminalIdle` → FAIL.
- [ ] **Step 3:** Implement `parseTerminalIdle`.
- [ ] **Step 4:** Migration: `CREATE TABLE IF NOT EXISTS terminal_settings (user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE, idle_seconds INT CHECK (idle_seconds BETWEEN 30 AND 3600), updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());` (`NULL` = globaler Wert).
- [ ] **Step 5:** `terminalIdleSeconds` und Speichern auf der Benutzerseite (nur System-Benutzer, nur `canManageUsers`; leeres Feld = globaler Wert); globale Einstellung lesen/schreiben (gleiche Prüfung).
- [ ] **Step 6: Integration test** `TestAnlagenterminalIdleSettings`: ohne Werte → 120; global 300 → 300; Terminal 45 → 45; ungültig 5 → Fehlermeldung, Wert bleibt 45; Nicht-System-Benutzer → Feld wird ignoriert.
- [ ] **Step 7:** Tests → PASS. **Commit** `feat: Terminal-Einstellung Rückkehrzeit (global und je Terminal)`.

### Task 3: Seite `/terminal` (Schoner, Störungsinfo, „Neu“)

**Files:**
- Create: `internal/web/terminal_page.go`, Test: `internal/web/terminal_page_test.go`
- Create: `web/templates/terminal.gohtml` (eigenes Vollbild-Layout, nicht `base.gohtml`), `web/static/terminal.js`, CSS im Template oder `web/static/terminal.css`
- Modify: `internal/web/handler.go` (Routen registrieren, in der authentifizierten Gruppe wie `kioskRoutes`)

**Interfaces:**
- Consumes: `terminalIdleSeconds` (Task 2), `infraSubtreeIDs`, `terminalInfraID`, `infraPath`.
- Produces: Routen `GET /terminal` (Schoner), `GET /terminal/info` (Störungsinfo), `GET /terminal/neu` („Neu“), `GET /terminal/info.json` (Daten für die Aktualisierung); `type terminalInfo struct { InfraID, InfraPath string; Faults []terminalItem; FaultsTotal int; Tickets []terminalItem; TicketsTotal int }`, `type terminalItem struct { ID, Title, Path, Status, StatusLabel string; Age string }`; `func (h *Handler) loadTerminalInfo(ctx context.Context, infraID string) (terminalInfo, error)`; `const terminalListLimit = 50`.

- [ ] **Step 1: Failing tests** (Unit, mit vorhandenen Handler-Testhilfen bzw. `-tags integration` wo DB nötig):
  - `TestTerminalRequiresSystemUser`: normaler Benutzer → 403, Text enthält „nur für Terminal-Benutzer“.
  - `TestTerminalWithoutInfra`: System-Benutzer ohne Anlage → 200, Text „Diesem Terminal ist keine Anlage zugeordnet“.
  - `TestTerminalIdleSecondsInPage`: Seite enthält `data-idle="45"` bei Terminalwert 45.
  - Integration `TestAnlagenterminalInfo`: Anlage A mit Unteranlage B und fremder Anlage C; offene Störungen in A und B erscheinen, `resolved`/`closed`/archivierte nicht, C nicht; 60 offene Störungen → 50 Einträge, `FaultsTotal = 60`; offene Tickets analog (Status `open`, `in_progress`, `pending`); ausgeblendete Anlage (`hidden_at`) → Meldung wie ohne Anlage.
- [ ] **Step 2:** Tests → FAIL.
- [ ] **Step 3:** `loadTerminalInfo`: Teilbaum per `infraSubtreeIDs`; Störungen `status NOT IN ('resolved','closed') AND archived_at IS NULL`, neueste zuerst, `LIMIT 50` + `COUNT(*)`; Tickets entsprechend (Spalten in `001_core_schema` prüfen); `Path` relativ zur Terminal-Anlage; `Age` deutsch kurz („vor 12 min“, „vor 3 h“, „vor 2 Tagen“).
- [ ] **Step 4:** Handler und Template:
  - `/terminal`: Schoner (Platzhalter bis Teilprojekt 7): Anlagenname groß, Uhrzeit, „Zum Melden berühren“; Berührung → `/terminal/info`.
  - `/terminal/info`: Pfad oben, Kacheln „Offene Störungen (n)“ und „Offene Tickets (n)“ mit Liste (Titel, Unteranlage, Status, Alter), Knopf „Neu“ groß; Aktualisierung alle 30 s über `info.json`.
  - `/terminal/neu`: Kacheln **Störung** und **Ticket** (bis Teilprojekt 3 Verweis auf die vorhandene Melde-Seite des Easy-Mode `/e/{infraID}`), Bereich „Stillstand“ mit Hinweis „Stillstandsgründe folgen“ (wird in Teilprojekt 4 befüllt), „Zurück“.
  - `terminal.js`: Inaktivität = keine `pointerdown`/`keydown` für `data-idle` Sekunden → `location.replace('/terminal')`; auf `/terminal` selbst kein Timer.
- [ ] **Step 5:** Tests → PASS; Seiten im Browser (Docker-Stack aus `compose.yaml` oder `make run`) bei 1280×800 und 1920×1080 prüfen, Screenshot in den Bericht.
- [ ] **Step 6: Commit** `feat: Terminalseite mit Schoner, Störungsinfo und Neu`.

### Task 4: Handbuch, Changelog, Version

**Files:**
- Modify: `web/templates/help.gohtml` (neues Kapitel `terminal`, Abschnitt HMI-Felder im Kapitel `kiosk` oder `infrastructure`), `internal/web/help.go` (`helpChapterIDs` um `"terminal"`), `CHANGELOG.md` (`## [0.77.10] – 2026-11-09`, `### Neu`), `VERSION` (`0.77.10`)

- [ ] **Step 1:** `GO test ./internal/web/ -run 'Help|Changelog'` → nach Änderungen PASS (`TestHelpCoversAllPages` verlangt das Kapitel, falls `/terminal` `baseData(r, "terminal")` nutzt).
- [ ] **Step 2:** `GO test ./...`, `GO vet ./...`, `gofmt -l` auf geänderten Dateien leer; Integrationstests `-run Anlagenterminal` → PASS.
- [ ] **Step 3: Commit** PDH `docs: Anlagenterminal im Handbuch, Version 0.77.10`.
