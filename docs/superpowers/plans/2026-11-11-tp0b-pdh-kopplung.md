# TP0b: PDH-Terminal koppeln (PDH-Seite) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** PDH kann ein PDH-Terminal (Pi) per Einmalcode koppeln, meldet es über ein Geräte-Geheimnis als Terminal-Benutzer an und kann es jederzeit sperren.

**Architecture:** Neue Tabellen `terminal_devices` und `terminal_pairing_codes` (Migration 157). Eine Datei `internal/web/terminal_devices.go` enthält Code-Erzeugung, Kopplungs-API, Geräteanmeldung, Sperrprüfung mit 30-s-Zwischenspeicher und die Geräteliste. Sitzungen tragen den JWT-Claim `device`; `sessionUser` weist Tokens gesperrter Geräte ab.

**Tech Stack:** Go 1.24, chi, pgx/PostgreSQL, gohtml, golang-jwt.

**Spec:** `C:\workplace\hmi-kiosk\docs\superpowers\specs\2026-11-11-tp0-produktlinie-design.md` (Teil 2 „PDH“), Gesamtentwurf `…/2026-11-09-anlagenterminal-design.md`.

**Arbeitsort:** neuer Worktree `C:\workplace\pdh-server-pairing`, Zweig `feat/terminal-kopplung` ab `feat/anlagenterminal` (2f1776a). Nicht in `C:\workplace\pdh-server` arbeiten. Nicht pushen.

## Global Constraints

- Alle Texte deutsch; Handbuch, Changelog im selben Schritt (`AGENTS.md`).
- Migration `migrations/157_terminal_devices.up.sql`; vor dem Zusammenführen Kollision mit origin/main prüfen.
- VERSION bleibt `0.77.9`; Stichpunkte in den unveröffentlichten Eintrag `## [0.77.9]` (`### Neu`, `### Sicherheit`).
- Recht für Code erzeugen, Geräteliste, Sperren: `canManageUsers(r)` (`system.manage_users`) plus `h.outranksRole(r, targetRole)`; kein neues Recht.
- Code: 8 Zeichen aus `ABCDEFGHJKLMNPQRSTUVWXYZ23456789`, Anzeige `XXXX-XXXX`, gültig 10 min, einmal verwendbar; neuer Code löscht offene Codes desselben Benutzers. Gespeichert: `hex(sha256("pdh-terminal-code:"+CODE))`.
- Geheimnis: 32 Zufallsbytes, Base64url ohne Auffüllung, Präfix `pdht_`. Gespeichert: `hex(sha256("pdh-terminal-device:"+secret))`.
- Fehlversuchssperre für `/api/terminal/v1/pair`, `/api/terminal/v1/device` und `/terminal/geraet`: 5 Fehlversuche je Quelle in 5 min → 429; Quelle = `clientIP(r)`, IPv6 auf /64 gekürzt.
- JSON-Eingaben ≤ 4 KiB, unbekannte Felder → 422, Fehler `{"error":"…"}` deutsch, `Content-Type: application/json; charset=utf-8`.
- Gerätename: 1–120 Zeichen, keine Steuer-/Format-/Trennzeichen (Unicode Cc, Cf, Zl, Zp).
- Geräte-Cookie: `pdh_token` + `pdh_user_id` mit denselben Attributen wie in `LoginPost` (`Path: "/"`, `SameSite=Lax`), nur `MaxAge` 400 Tage (34560000).
- Sperr-Zwischenspeicher: höchstens 30 s.
- Tests: `docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm sh -c 'go vet ./... && go test ./...'`; Integration mit `-tags integration` und `PDH_TEST_DSN`. `gofmt -l` der geänderten Dateien (LF-normalisiert) leer.

## Review Focus

- Code mit Kleinbuchstaben, Leerzeichen oder ohne Bindestrich eingegeben → wird angenommen (Normalisierung).
- Zwei gleichzeitige Kopplungen mit demselben Code → genau eine gelingt (atomares `UPDATE … WHERE used_at IS NULL RETURNING`).
- Terminal in Override-Sitzung, Gerät wird gesperrt → nach Ende des Overrides abgewiesen; zurückgestelltes Token behält 400-Tage-Cookie.
- Wartungssperre (`sessionNotBefore`) → Gerätesitzungen bleiben gültig (das Gerät meldet sich ohnehin automatisch an), Personen-Sitzungen nicht.
- Benutzer deaktiviert oder `is_system_user` entfernt → Gerätesitzung und Kopplung abgewiesen.

---

### Task 1: Migration, Code und Geheimnis, „Gerät koppeln“

**Files:**
- Create: `migrations/157_terminal_devices.up.sql`, `internal/web/terminal_devices.go`, `internal/web/terminal_devices_test.go`, `internal/web/terminal_devices_integration_test.go` (`//go:build integration`), `web/templates/terminal_pair_code.gohtml`
- Modify: `internal/web/handler.go` (Route neben `/users/{id}/groups` ~736), `web/templates/user_detail.gohtml` (Knopf, siehe Task 4 für die Karte – hier nur Knopf in der Übersicht bei `{{if $u.IsSystemUser}}`, gezeigt bei `.CanDevices`), `internal/web/user_detail.go` (`CanDevices bool`)

**Interfaces:**
- Produces: `newPairingCode() (string, error)` (8 Zeichen, ohne Bindestrich); `normalizePairingCode(s string) string` (Großbuchstaben, Leerzeichen und `-` entfernt); `formatPairingCode(c string) string` (`XXXX-XXXX`); `hashPairingCode(c string) string`; `newDeviceSecret() (string, error)`; `hashDeviceSecret(s string) string`; Handler `(h *Handler) TerminalPairCodeWeb(w, r)` für `POST /users/{id}/terminal-code`.

Migration (Kopfkommentar deutsch, Stil wie 156):
```sql
CREATE TABLE IF NOT EXISTS terminal_devices (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  secret_sha256 TEXT NOT NULL UNIQUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  last_seen_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS terminal_devices_user ON terminal_devices(user_id);
CREATE TABLE IF NOT EXISTS terminal_pairing_codes (
  code_sha256 TEXT PRIMARY KEY,
  user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ
);
```
(Prüfe, ob `gen_random_uuid()` in früheren Migrationen genutzt wird; sonst die dort übliche UUID-Erzeugung.)

- [ ] **Step 1: Unit-Tests** `TestPairingCodeFormat` (Länge 8, nur Alphabet, 200 Codes ohne Duplikat), `TestNormalizePairingCode` (`"abcd-efgh"`, `" ABCD EFGH "` → `"ABCDEFGH"`), `TestFormatPairingCode` (`"ABCDEFGH"` → `"ABCD-EFGH"`), `TestDeviceSecretFormat` (Präfix `pdht_`, Base64url-Teil dekodiert zu 32 Bytes), `TestHashesDomainSeparated` (`hashPairingCode(x) != hashDeviceSecret(x)`).
- [ ] **Step 2:** Tests laufen lassen → FAIL (undefiniert).
- [ ] **Step 3:** Helfer implementieren (crypto/rand, Ablehnungsstichprobe nicht nötig: 32er-Alphabet, `b%32`).
- [ ] **Step 4: Integrationstest** `TestTerminalPairCode`: Admin erzeugt Code für System-Benutzer → 200, Seite enthält `XXXX-XXXX`-Muster und „10 Minuten“; genau eine offene Zeile in `terminal_pairing_codes`; zweiter Aufruf → wieder genau eine offene Zeile (alte gelöscht). Nicht-System-Benutzer → 422-Seite „Nur für Terminal-Benutzer“. Ohne `system.manage_users` → 403.
- [ ] **Step 5:** `TerminalPairCodeWeb` implementieren: Ziel laden, `IsSystemUser` und aktiv prüfen, in einer Transaktion offene Codes des Benutzers löschen und neuen speichern (`expires_at = NOW() + interval '10 minutes'`), Seite `terminal_pair_code` rendern (Code groß, Ablaufzeit, Zurück-Link `/users/{id}`). Code nie in URL oder Protokoll. Log: `componentLog("terminal")` „kopplungscode erzeugt“ mit Benutzer-ID.
- [ ] **Step 6:** Unit- und Integrationstests → PASS; `go vet ./...`.
- [ ] **Step 7:** Commit `feat(terminal): Kopplungscode für Terminal-Benutzer`.

### Task 2: Kopplungs-API und Gerätezustand

**Files:**
- Modify: `internal/web/terminal_devices.go`, `cmd/server/main.go` (vor `r.Mount("/", …)` ~482), Tests wie Task 1

**Interfaces:**
- Consumes: Task 1 Helfer.
- Produces: `type failLimiter struct{…}`; `var terminalFailLimit = newFailLimiter(5, 5*time.Minute)`; `(l *failLimiter) blocked(key string, now time.Time) bool`; `(l *failLimiter) fail(key string, now time.Time)`; `limiterKey(ip string) string` (IPv6 → /64-Präfix-String); `(h *Handler) TerminalPairAPI(w, r)` für `POST /api/terminal/v1/pair`; `(h *Handler) TerminalDeviceAPI(w, r)` für `GET /api/terminal/v1/device`; `(h *Handler) deviceByID(ctx, id string) (userID string, active bool, err error)`.

API-Verträge:
- `POST /api/terminal/v1/pair` `{"code":"…","name":"…"}` → `200 {"device_id":"<uuid>","secret":"pdht_…","user":"<Vorname Nachname oder Benutzername>"}`; `422` Code ungültig/abgelaufen/benutzt („Kopplungscode ungültig oder abgelaufen“) oder Eingabe ungültig; `429` gesperrt („Zu viele Fehlversuche, bitte später erneut versuchen“).
- `GET /api/terminal/v1/device` mit `Authorization: Bearer pdht_…` → `200 {"device_id":"…","user":"…","name":"…"}`; `401 {"error":"Gerät nicht gekoppelt oder gesperrt"}`; aktualisiert `last_seen_at`. (Ergänzung zur Spezifikation: daraus zeigt die Pi-Admin-Seite „gesperrt“.)
- Kopplung nur, wenn der Benutzer aktiv und `is_system_user` ist; sonst 422 wie Code ungültig.
- Code-Verbrauch atomar: `UPDATE terminal_pairing_codes SET used_at=NOW() WHERE code_sha256=$1 AND used_at IS NULL AND expires_at > NOW() RETURNING user_id`, im selben Tx das Geräte-INSERT.

- [ ] **Step 1: Unit-Tests** `TestFailLimiter` (4 Fehler → nicht gesperrt, 5. → gesperrt, nach 5 min frei), `TestLimiterKey` (`"2001:db8:1:2:3::4"` und `"2001:db8:1:2:ffff::1"` gleicher Schlüssel; IPv4 unverändert), `TestPairAPIRejectsInput` ohne DB: >4 KiB, unbekanntes Feld, leerer Name, Name mit `\u200b` (Cf) → 422 JSON.
- [ ] **Step 2: Integrationstests** `TestTerminalPairAPI`: gültiger Code (in Kleinbuchstaben mit Bindestrich eingegeben) → 200, Geheimnis-Präfix, Zeile mit `hashDeviceSecret`; gleicher Code erneut → 422; abgelaufener Code (expires_at in Vergangenheit gesetzt) → 422; zwei parallele Anfragen mit neuem Code → genau eine 200; 5 Fehlversuche → 6. Anfrage auch mit gültigem Code 429. `TestTerminalDeviceAPI`: gültig → 200 + `last_seen_at` gesetzt; gesperrt (`revoked_at`) → 401; Benutzer deaktiviert → 401; falsches Geheimnis → 401 und zählt als Fehlversuch.
- [ ] **Step 3:** Tests → FAIL.
- [ ] **Step 4:** Implementieren, Routen in `cmd/server/main.go` öffentlich registrieren: `r.Post("/api/terminal/v1/pair", webHandler.TerminalPairAPI)`, `r.Get("/api/terminal/v1/device", webHandler.TerminalDeviceAPI)`. `authLog(r, ok, "terminal-kopplung", …)` bei Erfolg/Fehler (ohne Code/Geheimnis), `addHistory(ctx, "user", userID, "terminal_paired", "", "", name, "", "")` bei Erfolg (prüfe den refType, den der Historie-Reiter der Benutzerseite nutzt).
- [ ] **Step 5:** Tests → PASS, vet.
- [ ] **Step 6:** Commit `feat(terminal): Kopplungs-API und Gerätezustand`.

### Task 3: Geräteanmeldung und Sperrprüfung in der Sitzung

**Files:**
- Modify: `internal/web/terminal_devices.go`, `internal/web/terminal_page.go` (Route in `terminalRoutes`), `internal/web/handler.go` (`sessionClaims`, `sessionUser`, authMiddleware-Ausnahme ~2190, Override-Rückkehr ~3734), Template `web/templates/terminal_device_error.gohtml`, Tests

**Interfaces:**
- Consumes: Task 2 `terminalFailLimit`, `deviceByID`.
- Produces: `(h *Handler) TerminalDeviceLogin(w, r)` für `POST /terminal/geraet` (Formularfeld `secret`); `(h *Handler) sessionToken(r *http.Request) (sub string, iat int64, device string)` – `sessionClaims` wird zur Hülle darum; `type deviceCache` mit `(c *deviceCache) active(ctx context.Context, id, userID string, load func(ctx, id string) (string, bool, error)) bool` und `(c *deviceCache) forget(id string)`; `var terminalDevices = newDeviceCache(30 * time.Second)`; `const deviceCookieMaxAge = 34560000`.

Verhalten:
- `POST /terminal/geraet`: gesperrt (Limiter) → 429-Seite; Geheimnis unbekannt/gesperrt/Benutzer inaktiv oder kein System-Benutzer → Fehler zählen, 403-Seite `terminal_device_error` („Gerät nicht gekoppelt oder gesperrt. Bitte in der Admin-Seite des Terminals neu koppeln.“); sonst Token `h.users.IssueToken(u, 10*365*24*time.Hour, map[string]interface{}{"device": id})`, Cookies mit `deviceCookieMaxAge`, `pdh_return_token` löschen, `last_seen_at` setzen, 303 auf `/terminal`.
- `authMiddleware` lässt `/terminal/geraet` ohne Sitzung durch.
- `sessionUser`: hat das Token `device`, dann (a) `terminalDevices.active(...)` muss wahr sein und die Geräte-`user_id` gleich `sub`, (b) `sessionRevoked` (Wartungssperre) wird für Geräte-Tokens **nicht** angewandt. Sonst unverändert.
- Override-Rückkehr: Trägt das zurückgestellte Token `device`, Cookie-MaxAge `deviceCookieMaxAge`, sonst wie bisher 86400.
- Sperren ruft `terminalDevices.forget(id)`.

- [ ] **Step 1: Unit-Tests** `TestDeviceCacheTTL` (Lader-Aufrufe: 1. Aufruf lädt, 2. innerhalb 30 s nicht, nach 31 s wieder; `forget` erzwingt Neuladen; Fehler des Laders → inaktiv und nicht gespeichert), `TestSessionTokenDeviceClaim` (Token mit/ohne `device`).
- [ ] **Step 2: Integrationstests** `TestTerminalDeviceLogin`: gültiges Geheimnis → 303 `/terminal`, Cookie MaxAge 34560000, anschließender `GET /terminal` über `h.Routes()` mit dem Cookie → 200; nach `revoked_at` + `forget` → Weiterleitung auf `/global/` (keine Sitzung); falsches Geheimnis → 403-Seite mit Text; `sessionNotBefore` auf jetzt+1 s setzen → Gerätesitzung weiter 200, normale Admin-Sitzung abgewiesen. Override-Rückkehr mit Geräte-Token → Cookie-MaxAge 34560000 (Test gegen den Rückkehr-Handler).
- [ ] **Step 3:** FAIL → implementieren → PASS (inkl. bestehender Tests `go test ./internal/web/`).
- [ ] **Step 4:** Commit `feat(terminal): Geräteanmeldung und Sperrprüfung`.

### Task 4: Gekoppelte Geräte und Sperren auf der Benutzerseite

**Files:**
- Modify: `internal/web/user_detail.go` (`Devices []terminalDeviceRow`, `CanDevices bool`), `internal/web/terminal_devices.go`, `internal/web/handler.go` (Route), `web/templates/user_detail.gohtml`, `internal/web/user_detail_test.go`, Integrationstest

**Interfaces:**
- Produces: `type terminalDeviceRow struct{ID, Name string; Created, LastSeen, Revoked string}` (deutsch formatierte Zeiten, leer = nie/nein); `(h *Handler) loadTerminalDevices(ctx, userID string) ([]terminalDeviceRow, error)`; `(h *Handler) TerminalDeviceRevokeWeb(w, r)` für `POST /users/{id}/devices/{dev}/revoke`.

- Karte „Gekoppelte Geräte“ in der Übersicht nur bei `.CanDevices` (System-Benutzer und `canManageUsers`), **außerhalb** des Stammdaten-Formulars; je Gerät Zeile wie das Zertifikat-Muster (`rec-row`, Badge „gesperrt“ rot, sonst „aktiv“), Knopf „Sperren“ (eigenes Formular, `confirm('Gerät sperren? Das Terminal wird innerhalb von 30 Sekunden abgemeldet.')`), darüber Knopf „Gerät koppeln“ (Formular auf `/users/{id}/terminal-code`). CSS nur mit vorhandenen Variablen (Fiori/Fluent, hell/dunkel).
- Sperren: Gerät muss zum Benutzer gehören (sonst 404), `revoked_at = NOW()` nur wenn NULL, `forget`, `addHistory(…"terminal_revoked"…)`, `userRedirect(w, r, id, "", "Gerät gesperrt", nil)`.

- [ ] **Step 1: Tests** Render-Test: `CanDevices` mit zwei Geräten (eins gesperrt) → beide Namen, „gesperrt“, genau ein Sperren-Knopf, kein `<form` innerhalb des Stammdaten-Formulars (Template-Ausgabe zwischen `rec-form`-Start und `</form>` enthält kein `devices/`); ohne `CanDevices` → keine Karte; `checkTabs` weiter grün. Integration: Sperren durch Admin → `revoked_at` gesetzt; Gerät eines anderen Benutzers → 404; ohne Recht → 403.
- [ ] **Step 2:** FAIL → implementieren → PASS.
- [ ] **Step 3:** Screenshot nicht nötig; Commit `feat(terminal): gekoppelte Geräte sperren`.

### Task 5: Handbuch und Changelog

**Files:**
- Modify: `web/templates/help.gohtml` (Kapitel `terminal`, l.692-724), `CHANGELOG.md` (Eintrag 0.77.9)

- Abschnitt „So richtest du ein Terminal ein“ neu: 1. System-Benutzer anlegen, Terminal-Standort setzen; 2. „Gerät koppeln“ → Code (10 Minuten gültig); 3. am PDH-Terminal Admin-Seite öffnen (5 s obere rechte Ecke, Admin-Passwort) → „PDH“: Adresse und Code eintragen, „Koppeln“; 4. das Terminal startet `/terminal` selbst. Abschnitt „Gerät sperren“ (Liste „Gekoppelte Geräte“, Wirkung ≤ 30 s, neu koppeln mit neuem Code). Hinweis: Wartungssperre meldet gekoppelte Geräte nicht ab.
- CHANGELOG `### Neu`: Kopplung per Code, Geräteanmeldung, Geräteliste. `### Sicherheit`: Geheimnisse nur als SHA-256, Sperre nach Fehlversuchen, Sperren wirkt in 30 s.
- [ ] **Step 1:** Ändern; `go test ./internal/web/ -run 'TestHelp|TestChangelog'` → PASS.
- [ ] **Step 2:** Vollständiger Lauf `go vet ./... && go test ./...` (+ Integration mit DSN, falls verfügbar), gofmt der geänderten Dateien.
- [ ] **Step 3:** Commit `docs(terminal): Kopplung im Handbuch und Changelog`.
