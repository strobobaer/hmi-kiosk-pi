# Rückfall über das Tailnet – Bauplan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das PDH-Terminal weicht bei Netzwerkfehlern zur LAN-Adresse des PDH-Servers auf eine Tailnet-Adresse (`*.ts.net`) aus, kehrt selbst zurück, und Tailscale ist auf Pi und Windows mitgeliefert und über die Admin-Seite anmeldbar.

**Architecture:** Ein kleiner Zustand `Route` (Haupt/Ausweich) im Paket `pdhterminal` entscheidet die aktive Basisadresse; `Client` fragt ihn für jede Anfrage, meldet Fehler zurück und wiederholt eine Anfrage nach einem Wechsel einmal. Tailscale wird über ein Paket `internal/tailnet` gesteuert (Pi: über den Root-Helfer, Windows: direkt über `tailscale.exe`). PDH speichert den gemeldeten Weg je Gerät und zeigt ihn an.

**Tech Stack:** Go 1.24, html/template, Inno Setup 6 (Setup.exe), WiX (MSI), pi-gen-Stufen (Pi-Abbild, Debian trixie arm64), PostgreSQL-Migrationen (PDH).

**Spec:** `docs/superpowers/specs/2026-11-24-tailnet-rueckfall-design.md`

## Global Constraints

- Ohne `pdh.fallback_url` verhält sich das Terminal exakt wie vorher (gleiche Anfragen, gleiche Anzeigen, gleiche Richtliniendateien).
- `fallback_url`: reiner Ursprung `https://name[:port]`, nur https (auch bei `allowHttp`), kein Pfad, ≠ Hauptadresse.
- Wechsel nur bei Netzwerkfehlern; kein Wechsel bei `StateUntrusted`, 401/403 (`StateRevoked`), entkoppeltem Gerät oder HTTP-Antworten des Servers.
- Rückkehrprüfung der Hauptadresse alle **5 Minuten**, solange die Ausweichadresse aktiv ist.
- Kopplung bleibt an die Hauptadresse gebunden: Prüfung `Device.URL == cfg.PDH.URL` unverändert; Koppeln über die Ausweichadresse nicht möglich.
- Auth-Key: nie speichern, nie loggen, nie als Befehlszeilenargument; nur über eine 0600-Datei und `--auth-key=file:<pfad>`, Datei direkt danach löschen.
- Gerätename für Tailscale: `[a-z0-9-]{1,63}`, abgeleitet aus dem Gerätenamen (Kleinbuchstaben, andere Zeichen → `-`).
- Header an PDH: `X-Terminal-Route: lan` bzw. `tailnet` (nur hinzufügend).
- Tailscale-MSI wird nicht mitgeliefert: Setup.exe lädt `https://pkgs.tailscale.com/stable/tailscale-setup-<VERSION>-amd64.msi` und prüft SHA-256 (Version und Prüfsumme in `packaging/windows/tailscale.env`). Fehlschlag bricht die Terminal-Installation nicht ab. Deinstallation entfernt Tailscale nicht.
- Texte deutsch, im Stil des Repos; Terminal-Version **0.5.0**.
- PDH: Migration und Version jeweils die nächste freie über `origin/main` zum Zeitpunkt des PDH-Teils (bei Planerstellung main 0.82.53, Migration 217 ⇒ 218 / 0.82.54); Migration nur hinzufügend, IDs per `pdh_uuid_v7()`.
- Keine Kiosk-/Setup-/MSI-Programme auf dem Entwickler-PC ausführen; `.secrets/` nie anfassen; nicht pushen ohne Freigabe.
- Commit-Abschlusszeile: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. Hauptadresse liefert Zertifikatsfehler (z. B. abgelaufenes Zertifikat oder Angreifer im LAN) → Terminal darf NICHT still aufs Tailnet wechseln, Anzeige „Zertifikat nicht vertrauenswürdig“ bleibt. (Test in Task 2/3)
2. Ausweichadresse aktiv, Konfiguration wird geändert (Haupt- oder Ausweichadresse neu/leer) → Route übernimmt die neuen Adressen sofort, eine geleerte Ausweichadresse beendet den Ausweichbetrieb. (Test in Task 3)
3. Hauptadresse flattert (abwechselnd erreichbar/nicht) → höchstens ein Wechsel je Fehler, keine Wechselschleife innerhalb einer Anfrage, Rückkehr nur über die 5-Minuten-Prüfung. (Test in Task 2)
4. Auth-Key mit Sonderzeichen/Zeilenumbruch oder leer → leer wird abgelehnt, Inhalt landet nie in Protokoll, Fehlermeldung oder Befehlszeile. (Test in Task 6)
5. Windows: `tailscale.exe up` scheitert mangels Rechten (Kiosk-Benutzer ohne Admin) → klare Meldung auf der Admin-Seite („Tailscale-Anmeldung braucht Administratorrechte oder unbeaufsichtigten Modus“), kein Absturz. (Test in Task 6)

---

## Vorbedingung

- [ ] Port-Einstellung (Zweig `fix/startseite-port`, Terminal 0.4.6) ist in `feat/pdh-terminal` gemergt; danach `git merge origin/feat/pdh-terminal` in `feat/tailnet-rueckfall`. (Beide ändern Admin-Seite und Browser-Richtlinie.)

### Task 1: Konfiguration `fallback_url`

**Files:**
- Modify: `internal/config/pdh.go` (PDHCfg, ValidatePDH, PDHOrigins)
- Test: `internal/config/pdh_test.go`

**Interfaces:**
- Produces: `PDHCfg.FallbackURL string` (`json:"fallbackUrl,omitempty"`); `PDHOrigins(p)` enthält `FallbackURL` direkt nach der Hauptadresse, wenn gesetzt (damit landet sie in der Browser-Richtlinie, die heute schon aus `PDHOrigins` gespeist wird – in run.go prüfen).

- [ ] **Step 1: Tests** `TestValidatePDHFallback` (Tabelle): `""` ok; `https://pdh.tail1234.ts.net` ok; `https://pdh.tail1234.ts.net:8443` ok; `http://pdh.ts.net` mit `AllowHTTP:true` ⇒ Fehler; `https://pdh.ts.net/x` ⇒ Fehler; gleich der Hauptadresse ⇒ Fehler. `TestPDHOriginsFallback`: `{URL:"https://pdh.lan", FallbackURL:"https://pdh.t.ts.net", ExtraOrigins:["https://cdn.x"]}` ⇒ `["https://pdh.lan","https://pdh.t.ts.net","https://cdn.x"]`; ohne Fallback unverändert.
- [ ] **Step 2:** `go test ./internal/config -run 'PDH' ` ⇒ FAIL.
- [ ] **Step 3:** Implementieren; Fehlermeldung `Ausweichadresse ungültig: …` im Stil der vorhandenen Meldungen; Fallback zählt gegen `MaxWebOrigins`.
- [ ] **Step 4:** `go test ./internal/config` ⇒ PASS.
- [ ] **Step 5:** Commit „Konfiguration: Ausweichadresse (Tailnet) für PDH“.

### Task 2: Wegwahl `Route`

**Files:**
- Create: `internal/product/pdhterminal/route.go`
- Test: `internal/product/pdhterminal/route_test.go`

**Interfaces:**
- Produces:
  - `func IsNetworkErr(err error) bool` – true für `net.OpError`, `*net.DNSError`, Zeitüberschreitung (`os.ErrDeadlineExceeded`, `context.DeadlineExceeded`, `net.Error.Timeout()`), Verbindung abgelehnt/zurückgesetzt; false für x509/TLS-Fehler (gleiche Erkennung wie `transportErr`), `nil` und alles andere.
  - `type Route struct` (unexportierte Felder, `sync.Mutex`); `func NewRoute(primary, fallback string) *Route`
  - `func (r *Route) SetURLs(primary, fallback string)` – leere oder geänderte Ausweichadresse ⇒ zurück auf Haupt.
  - `func (r *Route) Base() string` – aktive Basisadresse (ohne abschließenden `/`).
  - `func (r *Route) Name() string` – `"lan"` oder `"tailnet"`.
  - `func (r *Route) Fail(base string, err error) (switched bool)` – nur wenn `base` die Hauptadresse, `IsNetworkErr(err)`, Ausweichadresse gesetzt und aktiv=Haupt ⇒ Wechsel, merkt Zeitpunkt + Fehlertext.
  - `func (r *Route) Run(ctx context.Context, probe func(ctx context.Context, base string) error)` – Ticker `routeProbeEvery` (var, 5 min); bei aktivem Ausweich `probe(primary)`; `nil` ⇒ zurück.
  - `type RouteStatus struct{ Name string; Since time.Time; LastErr string }`; `func (r *Route) Status() RouteStatus`
  - `now func() time.Time` als Feld für Tests.

- [ ] **Step 1: Tests**
  - `TestIsNetworkErr` (Tabelle: `&net.OpError{Op:"dial", Err: syscall.ECONNREFUSED}` true; `&net.DNSError{IsNotFound:true}` true; `context.DeadlineExceeded` true; `x509.UnknownAuthorityError{}` false; `&tls.CertificateVerificationError{}` false; `errors.New("x")` false; nil false).
  - `TestRouteSwitchOnNetworkErr`: `NewRoute("https://a","https://b")`; `Fail("https://a", netErr)` ⇒ true, `Base()=="https://b"`, `Name()=="tailnet"`.
  - `TestRouteNoSwitchOnTLS`: `Fail("https://a", x509Err)` ⇒ false, Base bleibt `https://a`.
  - `TestRouteNoFallbackConfigured`: `NewRoute("https://a","")`, `Fail(a, netErr)` ⇒ false.
  - `TestRouteFailOnFallbackNoFlip`: nach Wechsel `Fail("https://b", netErr)` ⇒ false, bleibt `b` (keine Schleife).
  - `TestRouteRunReturns`: `routeProbeEvery=10ms`; probe liefert erst Fehler, dann nil ⇒ Base wird wieder `https://a`; probe wird nie aufgerufen, solange aktiv=Haupt.
  - `TestRouteSetURLs`: im Ausweich `SetURLs("https://a","")` ⇒ `a`; `SetURLs("https://a2","https://b")` ⇒ `a2`.
  - Alles mit `-race`.
- [ ] **Step 2:** `go test -race ./internal/product/pdhterminal -run 'Route|IsNetworkErr'` ⇒ FAIL.
- [ ] **Step 3:** Implementieren.
- [ ] **Step 4:** gleicher Befehl ⇒ PASS.
- [ ] **Step 5:** Commit „Terminal: Wegwahl Haupt-/Ausweichadresse“.

### Task 3: Client und Abgleich über die Wegwahl

**Files:**
- Modify: `internal/product/pdhterminal/pair.go` (Client, Check, setClientHeaders), `functions.go` (FetchRemote, PushFunctions, readEvents), `run.go` (Route anlegen, `Run` starten, `SetURLs` bei Konfigurationsänderung)
- Test: `internal/product/pdhterminal/route_client_test.go`

**Interfaces:**
- Consumes: `Route` aus Task 2; `PDHCfg.FallbackURL` aus Task 1.
- Produces:
  - `Client.Route *Route` (nil ⇒ Verhalten wie heute, Basis = `d.URL`).
  - `func (c Client) base(d Device) string` – `c.Route.Base()` wenn Route gesetzt und `d.URL` gleich der Hauptadresse der Route, sonst `d.URL`.
  - `func (c Client) send(ctx context.Context, d Device, build func(base string) (*http.Request, error)) (*http.Response, error)` – führt aus; bei Transportfehler `c.Route.Fail(base, err)`; bei `switched==true` genau eine Wiederholung mit neuer Basis.
  - `setClientHeaders(req *http.Request, route string)` – setzt zusätzlich `X-Terminal-Route` (`"lan"` wenn route leer).
  - Check, FetchRemote, PushFunctions nutzen `send`; `readEvents` nutzt `c.base(d)` und meldet Verbindungsfehler per `Fail`.

- [ ] **Step 1: Tests** (zwei `httptest.NewTLSServer`, Client mit `RootCAs` beider Testserver – nur im Test)
  - `TestCheckFallsBack`: Haupt geschlossen (Listener zu), Ausweich antwortet 200 ⇒ `Check` ⇒ `StateOK`, Ausweich hat Header `X-Terminal-Route: tailnet` gesehen.
  - `TestCheckNoFallbackOnUntrusted`: Haupt ist TLS-Server mit fremdem Zertifikat ⇒ `StateUntrusted`, Ausweich nie angefragt.
  - `TestCheckNoFallbackOn401`: Haupt 401 ⇒ `StateRevoked`, Ausweich nie angefragt.
  - `TestNoRouteUnchanged`: `Client{}` ohne Route ⇒ Anfrage geht an `d.URL`, Header `X-Terminal-Route: lan`.
  - `TestRunSetURLsOnConfigChange`: Ausweichadresse in der Konfiguration geleert ⇒ `Route.Base()` == Hauptadresse (Review Focus 2; an der Stelle in run.go testen, die heute Konfigurationsänderungen übernimmt).
- [ ] **Step 2:** `go test -race ./internal/product/pdhterminal` ⇒ neue Tests FAIL.
- [ ] **Step 3:** Implementieren; Probe für `Route.Run` = GET `/api/terminal/v1/device` gegen die Hauptadresse mit Gerätegeheimnis (`nil` bei HTTP 200 oder 401 – Server erreichbar).
- [ ] **Step 4:** `go test -race ./internal/product/pdhterminal && GOOS=windows go build ./... && GOOS=linux GOARCH=arm64 go build ./...` ⇒ PASS.
- [ ] **Step 5:** Commit „Terminal: Anfragen an PDH über die Wegwahl, Rückkehr zur Hauptadresse“.

### Task 4: Startseite und Kartenleser über die aktive Adresse

**Files:**
- Modify: `internal/product/pdhterminal/start.go` (serveStart, pdhOnline, cardOrigin)
- Test: `internal/product/pdhterminal/start_test.go`

**Interfaces:**
- Consumes: `Client.Route`, `Client.base`.
- Produces: `startView.Action = client.base(*d) + "/terminal/geraet"`; CSP `form-action <haupt> [<ausweich>]`; `cardOrigin(p config.PDHCfg, base string) string` (Ursprung der aktiven Basis).

- [ ] **Step 1: Tests** `TestStartUsesFallbackAction` (Route im Ausweich ⇒ Formular-Action auf Ausweichadresse, CSP enthält beide Ursprünge); `TestStartGateUnchanged` (Device.URL ≠ cfg.URL ⇒ weiter „nicht gekoppelt“, auch wenn Device.URL der Ausweichadresse entspricht); `TestCardOriginFollowsRoute`.
- [ ] **Step 2:** FAIL. **Step 3:** Implementieren. **Step 4:** `go test ./internal/product/pdhterminal` PASS.
- [ ] **Step 5:** Commit „Terminal: Startseite und Kartenleser folgen der aktiven Adresse“.

### Task 5: Admin-Seite PDH – Ausweichadresse und Weg

**Files:**
- Modify: `internal/product/pdhterminal/admin.go` (pdhView, render, settings), `admin_pdh.html`
- Test: `internal/product/pdhterminal/admin_test.go`

**Interfaces:**
- Consumes: `PDHCfg.FallbackURL`, `Route.Status()`.
- Produces: `pdhView.FallbackURL string`, `pdhView.Route string` (`„verbunden über: LAN“` bzw. `„verbunden über: Tailnet seit <hh:mm>“`), `pdhView.RouteClass` (`ok` bzw. `warn`).

- [ ] **Step 1: Tests** Speichern mit `fallback=https://pdh.t.ts.net` ⇒ in Konfiguration; ungültig (`http://…`) ⇒ 400 mit „Ausweichadresse ungültig“ und Formular wieder befüllt; Anzeige „verbunden über: Tailnet“ bei Route im Ausweich.
- [ ] **Step 2:** FAIL. **Step 3:** Feld „Ausweichadresse (Tailnet)“ mit Hinweis „Zweiter Weg zum selben PDH-Server, z. B. https://pdh.<tailnet>.ts.net – wird nur bei Netzwerkfehlern benutzt.“ Speichern ändert die Kopplung nicht (kein Neu-Koppeln, wenn sich nur die Ausweichadresse ändert). **Step 4:** PASS.
- [ ] **Step 5:** Commit „Admin-Seite: Ausweichadresse und Verbindungsweg“.

### Task 6: Tailscale-Steuerung und Admin-Bereich „Tailnet“

**Files:**
- Create: `internal/tailnet/tailnet.go`, `internal/tailnet/cli.go`, `internal/tailnet/helperctl.go`, Tests `internal/tailnet/*_test.go`
- Modify: `internal/helper/commands.go`, `internal/helper/osexec.go` (`tailscale` in `optionalBinaryNames`), neue Datei `internal/helper/tailnet.go`, Test `internal/helper/tailnet_test.go`
- Create: `internal/product/pdhterminal/admin_tailnet.go`, `admin_tailnet.html`; Modify: `admin.go` (Nav „Tailnet“, Pages, Routes POST `/admin/tailnet/up`, `/admin/tailnet/down`)

**Interfaces:**
- Produces:
  - `type Status struct{ Installed, LoggedIn bool; Name, DNSName, IP, State string }`
  - `func ParseStatus(b []byte) (Status, error)` – aus `tailscale status --json`: `BackendState` (`Running` ⇒ LoggedIn), `Self.HostName`, `Self.DNSName` (ohne Schlusspunkt), erstes IPv4 aus `Self.TailscaleIPs`.
  - `type Ctl interface{ Status(ctx context.Context) (Status, error); Up(ctx context.Context, authKey, hostname string) error; Down(ctx context.Context) error }`
  - `func HostnameFrom(deviceName string) string` (Regel aus Global Constraints; leer ⇒ `pdh-terminal`).
  - `func NewCLI(bin string, run ExecOut) Ctl` mit `type ExecOut func(ctx context.Context, name string, args ...string) (string, error)` – Windows; `bin` Grundwert `%ProgramFiles%\Tailscale\tailscale.exe`; fehlt die Datei ⇒ `Status{Installed:false}`. Up: Key in `os.CreateTemp` (Rechte 0600), `up --auth-key=file:<pfad> --hostname=<h> --unattended`, Datei per `defer os.Remove`.
  - `func NewHelper(h HelperCaller) Ctl` mit `type HelperCaller interface{ Call(ctx, cmd, arg string) error; CallData(ctx, cmd, arg string) (json.RawMessage, error) }` – Pi (vorhandene Data-Variante des helperapi-Clients nutzen bzw. nachrüsten).
  - Helfer-Befehle: `CmdTailnetStatus = "tailnet-status"` (runData, liefert Ausgabe von `tailscale status --json`; fehlt das Programm ⇒ `{"installed":false}`), `CmdTailnetUp = "tailnet-up"` (takesArg, JSON `{"key":"…","host":"…"}`; Key nach `/run/hmi-kiosk/tailnet-key` 0600, `tailscale up --auth-key=file:/run/hmi-kiosk/tailnet-key --hostname=<h>`, Datei immer löschen), `CmdTailnetDown = "tailnet-down"` (`tailscale logout`).
  - Admin-Seite „Tailnet“: Status (nicht installiert / nicht angemeldet / angemeldet als `<Name>` · `<DNSName>` · `<IP>`), Feld „Auth-Key“ (type=password, autocomplete=off) + „Anmelden“, Knopf „Abmelden“; Audit per `f.Logf("tailnet: angemeldet als %s", name)` ohne Key.

- [ ] **Step 1: Tests**
  - `TestParseStatus` mit fester JSON-Probe (Running, NeedsLogin, Stopped).
  - `TestHostnameFrom`: `"Halle 3 / Säge"` ⇒ `"halle-3---s-ge"` (jedes Zeichen außerhalb `[a-z0-9-]` nach Kleinschreibung einzeln → `-`, keine Zusammenfassung); `""` ⇒ `"pdh-terminal"`; 80 Zeichen ⇒ 63.
  - `TestCLIUpKeyNotInArgs`: Fake-ExecOut sammelt args ⇒ kein Arg enthält den Key, ein Arg beginnt mit `--auth-key=file:`; Datei existiert nach Rückkehr nicht mehr; Dateiinhalt während des Aufrufs = Key.
  - `TestCLIUpPermissionError`: Fake liefert Fehler „Access is denied“ ⇒ Fehlermeldung enthält „Administratorrechte oder unbeaufsichtigten Modus“ (Review Focus 5).
  - `TestHelperTailnetUp` (internal/helper): Fake-Exec; Key nicht in args, Datei gelöscht, ungültiger Hostname ⇒ Fehler; leerer Key ⇒ Fehler; Key mit Zeilenumbruch ⇒ Fehler (Review Focus 4).
  - `TestAdminTailnetNoKeyInLogOrPage`: POST mit Key ⇒ Logf-Aufzeichnung und Antwort-HTML enthalten den Key nicht.
- [ ] **Step 2:** `go test ./internal/tailnet ./internal/helper ./internal/product/pdhterminal` ⇒ FAIL.
- [ ] **Step 3:** Implementieren; Auswahl der Steuerung in run.go: Helfer-Socket gesetzt ⇒ `NewHelper`, sonst (Windows) `NewCLI`.
- [ ] **Step 4:** Tests PASS; `GOOS=windows go build ./... && GOOS=linux GOARCH=arm64 go build ./...`.
- [ ] **Step 5:** Commit „Tailnet: Status, Anmelden und Abmelden auf der Admin-Seite (Pi über den Helfer, Windows über tailscale.exe)“.

### Task 7: Pi-Abbild mit Tailscale

**Files:**
- Create: `packaging/image/stage-hmi/00-tailscale/00-run-chroot.sh`, `packaging/image/stage-hmi/00-tailscale/files/tailscale-archive-keyring.gpg` (einmalig von `https://pkgs.tailscale.com/stable/debian/trixie.noarmor.gpg` geladen; Fingerabdruck im Commit-Text notieren)
- Modify: `packaging/image/verify-image.sh`

**Interfaces:** Stufe läuft nach `00-packages` (alphabetisch) und installiert selbst.

- [ ] **Step 1:** verify-image.sh: `tailscale` in `REQUIRED_PACKAGES`; `enabled_in multi-user.target tailscaled.service`; Quelle `/etc/apt/sources.list.d/tailscale.list` mit `signed-by=/usr/share/keyrings/tailscale-archive-keyring.gpg` vorhanden (`ok`/`bad`-Muster).
- [ ] **Step 2:** `00-run-chroot.sh`: Keyring nach `/usr/share/keyrings/`, Liste `deb [signed-by=…] https://pkgs.tailscale.com/stable/debian trixie main`, `apt-get update`, `apt-get install -y --no-install-recommends tailscale`, `systemctl enable tailscaled`. Kein `tailscale up`.
- [ ] **Step 3:** `bash -n` auf beide Skripte; `shellcheck`, falls vorhanden. (Abbildbau selbst im Release-Schritt.)
- [ ] **Step 4:** Commit „Pi-Abbild: Tailscale aus der signierten Paketquelle, nicht angemeldet“.

### Task 8: Windows-Setup lädt Tailscale nach

**Files:**
- Create: `packaging/windows/tailscale.env` (`TAILSCALE_VERSION=…`, `TAILSCALE_MSI_SHA256=…` – aktuelle stabile Version, Prüfsumme von pkgs.tailscale.com geladen und notiert)
- Modify: `packaging/windows/build-setup.sh` (liest tailscale.env, übergibt `/DTailscaleVersion=… /DTailscaleSha256=…`), `packaging/windows/pdh-terminal.iss`
- Modify: Handbuch-Abschnitt Windows-Installation (MSI-Weg: Tailscale separat durch die IT verteilen)

**Interfaces:** Inno-Pascal: Ist `{commonpf64}\Tailscale\tailscale.exe` vorhanden ⇒ nichts tun. Sonst `DownloadTemporaryFile(<url>, 'tailscale.msi', '{#TailscaleSha256}', nil)`, dann `msiexec /i "<tmp>\tailscale.msi" /qn TS_UNATTENDEDMODE=always`. Jeder Fehler ⇒ Protokollzeile + Weiterinstallieren (kein Abbruch, keine Meldungsbox im stillen Modus). Deinstallation fasst Tailscale nicht an.

- [ ] **Step 1:** Implementieren; `/DTailscaleVersion` ohne Wert ⇒ Abschnitt übersprungen (Bau ohne tailscale.env bleibt möglich).
- [ ] **Step 2:** Prüfung ohne Ausführung: `build-setup.sh` im Docker-Bau erst im Release-Schritt; jetzt `bash -n packaging/windows/build-setup.sh` und Inno-Syntax per Sichtprüfung im Review.
- [ ] **Step 3:** Commit „Windows-Setup: Tailscale bei Bedarf laden (Prüfsumme geprüft), MSI-Weg über die IT“.

### Task 9: Dokumentation und Änderungsprotokoll

**Files:**
- Modify: Handbuch (Kapitel Admin-Seite/PDH; neuer Abschnitt „Tailnet“), CHANGELOG (0.5.0)
- Create: `docs/tailnet-server-einrichtung.md` – `tailscale serve --bg --https=443 https+insecure://localhost:<PDH-Port>` bzw. `http://localhost:<port>` je nach PDH-Betrieb; ACL-Ausschnitt mit `tagOwners` für `tag:pdh-terminal` und Regel nur zum PDH-Server Port 443; Auth-Key erzeugen (Tag, vorab freigegeben, wiederverwendbar nach Wahl).

- [ ] **Step 1:** Schreiben. **Step 2:** Commit „Doku: Tailnet-Rückfall, Server-Einrichtung, 0.5.0“.

### Task 10: PDH – Weg je Terminal speichern und anzeigen

**Repo:** `pdh-server`, Worktree `C:\workplace\pdh-server-tailnet`, Zweig `feat/terminal-tailnet-weg` von aktuellem `origin/main`.

**Files:**
- Create: `migrations/<nächste>_terminal_route.up.sql` (Spalte `route TEXT` an der Tabelle, in der Migration 209 die Admin-Adresse speichert; `CHECK (route IN ('lan','tailnet'))`, NULL erlaubt), `checksums.txt` per `PDH_MIGRATIONS_APPEND=1 go test ./pkg/database -run TestMigrationsFrozen`
- Modify: die Stelle, die `X-Terminal-Admin-URL` übernimmt (gleiches Muster für `X-Terminal-Route`, nur `lan`/`tailnet`, sonst ignorieren), Kiosk-Terminals-Liste (Spalte „Weg“: „LAN“, „Tailnet“; leer ⇒ „LAN“), i18n, CHANGELOG, VERSION
- Test: Handler-Test mit `Host: pdh.t.ts.net` für `/api/terminal/v1/device`, `/terminal/geraet`, `/terminal/events` (funktionieren wie mit LAN-Namen); Test Header-Übernahme inkl. ungültiger Werte.

- [ ] **Step 1:** Tests ⇒ FAIL. **Step 2:** Implementieren. **Step 3:** `go test ./internal/web ./pkg/database` mit `PDH_TEST_DSN` (eigene frische Testdatenbank) ⇒ PASS.
- [ ] **Step 4:** Commit „Terminal: Verbindungsweg (LAN/Tailnet) je Gerät“.

## Abschluss

- Schlussreview (opus) über beide Zweige.
- Nach Freigabe: Terminal-Release 0.5.0 (dgo-Bau, 13 Dateien) und PDH-Merge; der Server-Schritt (`tailscale serve`, ACL) laut `docs/tailnet-server-einrichtung.md` durch den Nutzer.
