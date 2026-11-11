# TP0a: Gemeinsamer Kern und Produkt PDH-Terminal (Pi-Seite) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Aus dem Repository entstehen zwei Produkte – HMI-Kiosk (unverändert) und PDH-Terminal (`cmd/pdh-terminal`), das sich per Code mit PDH koppelt und `/terminal` im Vollbild zeigt.

**Architecture:** Kein Umzug der HMI-Dateien. Der Kern wird dort, wo heute HMI-Annahmen fest verdrahtet sind, über Produktbeschreibungen parametrisiert: `release.Product` (Name, Programm, Tag-Präfix, Paketname), `web.AdminProduct` (Menü, Startseite, Zusatzseiten), Selbstprüfungsschritte je Produkt, Paketbau mit `packaging/products/<id>/`. Gemeinsame Laufzeit-Bausteine aus `internal/app` wandern nach `internal/appcore`. Das neue Produkt lebt in `internal/product/pdhterminal` und `cmd/pdh-terminal`; ein Test verbietet Kernpaketen den Import von `internal/product/...`.

**Tech Stack:** Go 1.24 (Modul `hmikiosk`, nur `x/crypto`), pi-gen, systemd, Chromium, labwc; Tests über `scripts/dgo`.

**Spec:** `docs/superpowers/specs/2026-11-11-tp0-produktlinie-design.md`; PDH-Gegenstück: `docs/superpowers/plans/2026-11-11-tp0b-pdh-kopplung.md` (API-Verträge dort, Task 2/3).

**Arbeitsort:** Zweig `feat/pdh-terminal` ab `feat/hmi-kiosk` (7349bab) in `C:\workplace\hmi-kiosk` (oder Worktree). Nicht pushen, `.secrets/release.key` nie committen.

## Global Constraints

- HMI-Kiosk bleibt funktional und im Dateiformat unverändert: `config.json` (SchemaVersion 1), `.hkx`-Export, Update-Pakete ohne Produktkennung, Tags `v<x.y.z>`, Einheitennamen, Pfade `/etc/hmi-kiosk`, `/data`.
- Plattformnamen bleiben für **beide** Produkte gleich (Entscheidung dieses Plans, um Pakete/Abbild-Prüfung nicht zu verdoppeln): Benutzer `hmikiosk`, Einheiten `hmi-kiosk*.service`, Helfer `hmi-kiosk-helper`, `/etc/hmi-kiosk`, Datenpartition `HMIDATA` auf `/data`. Je Produkt verschieden: Programmname (`hmi-kiosk` | `pdh-terminal`), `ExecStart` der Haupteinheit, Abbild-/Bundle-/Paketname, Hostname-Vorgabe, Systemstand-Marker-Inhalt, Chromium-Grundrichtlinie, Datei `/etc/hmi-kiosk/product` (Inhalt = Produkt-ID).
- Produkt-IDs exakt `hmi-kiosk` und `pdh-terminal`; Anzeigenamen „HMI-Kiosk“ und „PDH-Terminal“.
- Release-Tags `hmi-kiosk-v<x.y.z>` / `pdh-terminal-v<x.y.z>`; HMI-Kiosk akzeptiert zusätzlich `v<x.y.z>`. Assets `<id>-<version>.pkg`.
- PAX-Record `HMIKIOSK.product` am Programmeintrag; fehlt er, gilt `hmi-kiosk`.
- PDH-Terminal Systemstand beginnt bei 1 (`release.PDHTerminal.SystemLevel = 1`).
- Admin-Geste: 5 s Berührung im Feld 80 × 80 px oben rechts; Admin-Leerlauf PDH-Terminal 120 s.
- Geheimnis-Datei `<data>/pdh/device.json` (0600, Dienstbenutzer), nie angezeigt, nie protokolliert, nicht in Sicherungen.
- PDH-Adresse: nur Ursprung (`https://host[:port]`), `http://` nur mit `AllowHTTP`; Vertrauen über den Systemspeicher des Geräts (kein eigenes CA-Feld in TP0, siehe Review Focus).
- Alle Texte deutsch. Tests: `scripts/dgo vet ./...`, `scripts/dgo test -race -count=1 ./...`; Paketierung: `bash test/install/run.sh`, `packaging/image/test-*.sh`.
- Commits deutsch mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

- PDH mit internem Zertifikat (PDH-eigene PKI) → Kopplung scheitert mit verständlicher Meldung „Zertifikat von PDH wird nicht vertraut“ statt rohem Go-Fehler (Test in Task 8).
- Touchscreen ohne `ABS_MT_*` (nur `ABS_X/Y`) oder nur mit `ABS_MT_*` → Geste funktioniert mit beiden Achsenarten (Test in Task 7); gedrehte Bildschirme sind nicht Teil von TP0.
- Export eines anderen Terminals eingespielt → lokaler PDH-Abschnitt (Adresse, Gerätename) bleibt erhalten (Test in Task 1).
- HMI-Kiosk-Paket auf PDH-Terminal hochgeladen (und umgekehrt) → abgelehnt mit „Paket gehört zu …“ (Test in Task 4).
- PDH nicht erreichbar beim Start → Startseite mit Hinweis und neuer Versuch nach 30 s, kein weißer Bildschirm (Test in Task 9).

---

### Task 1: Konfiguration – PDH-Abschnitt und Validierung in Teilen

**Files:**
- Create: `internal/config/pdh.go`, `internal/config/pdh_test.go`
- Modify: `internal/config/config.go` (Feld), `internal/config/validate.go` (Aufteilung), `internal/web/admin_transfer.go` (`merge` behält lokales `PDH`)

**Interfaces:**
- Produces: `type PDHCfg struct{ URL string `json:"url,omitempty"`; AllowHTTP bool `json:"allowHttp,omitempty"`; DeviceName string `json:"deviceName,omitempty"`; ExtraOrigins []string `json:"extraOrigins,omitempty"` }`; Feld `Config.PDH PDHCfg `json:"pdh,omitempty"``; `ValidatePDH(p PDHCfg) error`; `PDHOrigins(p PDHCfg) []string` (PDH-Ursprung + Zusatzursprünge, dedupliziert); `ValidateCore(c Config) error`, `ValidateHMI(c Config) error`; `Validate(c) = ValidateCore + ValidateHMI + ValidatePDH` (Verhalten für bestehende Dateien gleich).
- Regeln: URL leer erlaubt; sonst `config.WebOrigin(URL)==URL`, Schema https oder (http und AllowHTTP); `ExtraOrigins` je `ValidOrigin`, höchstens `MaxWebOrigins` minus 1; DeviceName per `CheckName` oder leer.

- [ ] **Step 1: Tests** `TestValidatePDH` (Tabelle: leer ok; `https://pdh.firma.de` ok; `https://pdh.firma.de/` → Fehler „nur Adresse ohne Pfad“; `http://10.0.0.5:8080` ohne AllowHTTP Fehler, mit ok; `ftp://x` Fehler; zu viele Zusatzursprünge Fehler), `TestPDHOrigins` (Duplikate entfernt, Reihenfolge PDH zuerst), `TestOldConfigUnchanged` (bestehende Testdatei geladen, gespeichert → identische JSON-Bytes, kein `"pdh"`-Schlüssel), `TestImportKeepsLocalPDH` in `internal/web` (Import einer Exportdatei mit `pdh` überschreibt lokales `PDH` nicht).
- [ ] **Step 2:** FAIL → implementieren (Aufteilung von `Validate` rein mechanisch: Abschnitte verschieben, keine Regel ändern) → `scripts/dgo test -race -count=1 ./internal/config/ ./internal/web/` PASS.
- [ ] **Step 3:** Commit `config: PDH-Abschnitt und Validierung in Kern/HMI/PDH`.

### Task 2: Admin-Grundgerüst mit Produktbeschreibung

**Files:**
- Create: `internal/web/product.go`, `internal/web/product_test.go`
- Modify: `internal/web/admin.go` (`AdminDeps.Product`, Seitenliste, Routen, `/admin/{$}`-Weiterleitung, `adminView.Nav`, `adminView.HMI`), `internal/web/assets/admin/layout.html` (Menü aus `.Nav`, Marke aus `.ProductName`), `assets/admin/network.html` und `system.html` (HMI-Teile in `{{if .HMI}}`)

**Interfaces:**
- Produces:
  - `type NavItem struct{ Section, Label, Path string }`
  - `type AdminFrame interface{ Render(w http.ResponseWriter, status int, page, section, title, errMsg string, body any); Update(change func(*config.Config) error) error; Load() (config.Config, error); Helper() HelperAPI; Logf(format string, args ...any) }`
  - `type ProductRoute struct{ Method, Path string; Upload bool; Handle func(f AdminFrame, w http.ResponseWriter, r *http.Request) }` (alle Produktrouten sind angemeldete Sitzungsrouten mit CSRF wie `routeSession`)
  - `type AdminProduct struct{ ID, Name, Home string; Nav []NavItem; Pages map[string]string /* Seitenname → Template-Text */; Routes []ProductRoute; IdleSeconds int; HMI bool }`
  - `func HMIKioskAdmin() *AdminProduct` (heutiges Menü HMIs, Client, Kiosk, Terminals, Netzwerk, System; Home `/admin/hmis`; `HMI: true`; keine Zusatzseiten – HMI-Routen bleiben intern)
  - `AdminDeps.Product *AdminProduct` (nil ⇒ `HMIKioskAdmin()`).
- Verhalten: Mit `HMI=false` werden HMI-Seiten/-Routen (hmis, hmi_edit, client, kiosk, terminals, remote key, Peers-Schlüssel, Dauerprüfung-VNC-Teile) nicht registriert; Netzwerk, System, Sicherung, Wartung, Updates, Anmeldung bleiben. Produktseiten werden mit `layout.html` geparst; ein Seitenname, der mit einer Kernseite kollidiert, führt in `NewAdmin` zu panic (Programmierfehler).

- [ ] **Step 1: Tests** `TestHMIKioskNavUnchanged` (gerendertes Layout enthält dieselben sechs Links wie vor der Änderung, Marke „HMI-Kiosk“), `TestProductWithoutHMI` (Produkt `{ID:"x",Name:"PDH-Terminal",Home:"/admin/pdh",HMI:false,Pages:{"pdh":"{{define \"content\"}}PDH-Seite{{end}}"},Routes:[GET /admin/pdh]}`: `/admin/` → 303 `/admin/pdh`; `/admin/hmis` → 404; `/admin/pdh` angemeldet → 200 „PDH-Seite“; Menü enthält „PDH“ und „Netzwerk“, nicht „HMIs“; Netzwerkseite ohne Abschnitt „Fernsteuerung (PDH)“), `TestProductRouteCSRF` (POST ohne CSRF → 403), `TestProductPageCollision` (panic).
- [ ] **Step 2:** FAIL → implementieren → alle `internal/web`-Tests PASS (keine bestehenden Erwartungen ändern).
- [ ] **Step 3:** Commit `web: Admin-Grundgerüst mit Produktbeschreibung`.

### Task 3: Laufzeit-Bausteine in `internal/appcore`, Selbstprüfung je Produkt

**Files:**
- Create: `internal/appcore/` (`journal.go`, `sdnotify.go`, `healthy.go`, `origins.go`, `localadmin.go`, `updates.go` + Tests verschoben)
- Modify: `internal/app/*` (nutzt appcore), `internal/selftest/selftest.go`

**Interfaces:**
- Produces (Namen exportiert, Verhalten unverändert, Dateien per `git mv` + Umbenennung verschieben):
  - `appcore.NewJournal(dir string) (*Journal, error)`
  - `appcore.Notify(state string) error`, `appcore.Watchdog(ctx context.Context)` (heutiges sd_notify)
  - `appcore.WriteHealthy(runDir, version string) error`
  - `appcore.NewOriginSync(call func(ctx context.Context, cmd, arg string) error) *OriginSync` mit `Set(origins []string)`, `AssumeWritten(origins []string)`, `Run(ctx)`
  - `appcore.ServeLocalAdmin(ctx context.Context, addr string, h http.Handler, presence *AdminPresence) (stop func(), err error)` (heute `localadmin.go`)
  - `appcore.NewUpdateCheck(p release.Product, settings func() config.UpdateCfg, logf func(string, ...any)) *updatecheck.Service`, `appcore.RunUpdateCheck(ctx, s *updatecheck.Service)` (3 min + Zufall, dann 24 h)
- `selftest`: `type Step = step` exportiert als `selftest.Step{Name string; Run func(ctx, Deps) StepResult}`; `selftest.HMISteps() []Step` (heutige sieben in heutiger Reihenfolge); `selftest.CoreSteps() []Step` (nur `schutz`); `Run(ctx, d)` bleibt = `RunSteps(ctx, d, HMISteps())`; `RunSteps(ctx context.Context, d Deps, steps []Step) []StepResult`.

- [ ] **Step 1: Tests** `TestCoreStepsOnlyProtection` (Namen = `["schutz"]`), `TestRunStepsOrder` (zwei Fake-Schritte laufen in Reihenfolge, Panik eines Schritts → Fehlschlag mit Detail, nächster läuft). Bestehende Tests der verschobenen Dateien wandern mit.
- [ ] **Step 2:** Implementieren; `scripts/dgo vet ./...`, `scripts/dgo test -race -count=1 ./...` komplett grün; `git diff --stat` zeigt für `internal/app` nur Aufrufänderungen.
- [ ] **Step 3:** Commit `appcore: gemeinsame Laufzeit-Bausteine, Selbstprüfung je Produkt`.

### Task 4: Produktkennung in Paketen und Update-Prüfung

**Files:**
- Create: `internal/release/product.go`, `internal/release/product_test.go`
- Modify: `internal/update/package.go`, `install.go`, Tests; `internal/updatecheck/select.go`, `service.go`, Tests; `cmd/hmi-kiosk-pack/main.go`; `internal/app/servers.go`, `updates.go` (HMI-Kiosk übergibt `release.HMIKiosk`)

**Interfaces:**
- Produces: `type Product struct{ ID, Name, Binary string; SystemLevel int; LegacyTags bool }`; `var HMIKiosk = Product{ID:"hmi-kiosk", Name:"HMI-Kiosk", Binary:"hmi-kiosk", SystemLevel: SystemLevel, LegacyTags: true}`; `var PDHTerminal = Product{ID:"pdh-terminal", Name:"PDH-Terminal", Binary:"pdh-terminal", SystemLevel: 1}`; `func ProductByID(id string) (Product, bool)`; `(Product).PackageName(version string) string` = `ID+"-"+version+".pkg"`; `(Product).TagVersion(tag string) (string, bool)` (`<ID>-v<x.y.z>`; bei `LegacyTags` auch `v<x.y.z>`).
  - `update.Pack(dir, version string, p release.Product, systemLevel int, priv ed25519.PrivateKey) ([]byte, error)` – Programmeintrag heißt `p.Binary`, PAX `HMIKIOSK.product = p.ID` (für `hmi-kiosk` **ohne** Record schreiben, damit Pakete byte-kompatibel bleiben).
  - `Package.Product string` (Verify: aus PAX; fehlt → `"hmi-kiosk"`; Programmeintrag muss `ProductByID(Product).Binary` heißen, sonst Fehler).
  - `Installer.Product release.Product`; Installation lehnt `p.Product != in.Product.ID` ab: Fehlertext `Paket gehört zu <Name des Paketprodukts>, dieses Gerät ist <in.Product.Name>.`; `update.Install` entpackt nach `<root>/app/<ver>/<Binary>`.
  - `updatecheck.Select(rels []Release, p release.Product, current string, prerelease bool) (*Offer, error)`; `updatecheck.Service.Product release.Product`.
  - `hmi-kiosk-pack pack … -product <id>` (Standard `hmi-kiosk`; `-in`-Ordner muss `<Binary>` enthalten).

- [ ] **Step 1: Tests** `TestTagVersion` (`v0.1.9` nur HMI; `hmi-kiosk-v0.1.9` HMI; `pdh-terminal-v0.2.0` nur PDH; `pdh-terminal-0.2.0` nein), `TestPackHMIKioskUnchanged` (Paket ohne `HMIKIOSK.product`-Record, Eintragsnamen `VERSION`,`hmi-kiosk`), `TestPackPDHTerminal` (Verify → Product `pdh-terminal`), `TestVerifyRejectsWrongBinaryName` (PAX `pdh-terminal` aber Eintrag `hmi-kiosk` → Fehler), `TestInstallRejectsForeignProduct` (beide Richtungen, Text enthält „Paket gehört zu PDH-Terminal“ bzw. „Paket gehört zu HMI-Kiosk“), `TestOldPackageAcceptedByHMI` (bestehendes Testpaket ohne Record wird installiert), `TestSelectByProduct` (Releases mit `v0.1.9`, `pdh-terminal-v0.3.0`, `hmi-kiosk-v0.1.10` → HMI wählt 0.1.10, PDH 0.3.0; Asset-Name je Produkt).
- [ ] **Step 2:** FAIL → implementieren → `scripts/dgo test -race -count=1 ./...` PASS.
- [ ] **Step 3:** Commit `update: Produktkennung in Paketen und Update-Prüfung`.

### Task 5: Paketierung je Produkt

**Files:**
- Create: `packaging/products/hmi-kiosk/product.env`, `packaging/products/pdh-terminal/product.env`, `packaging/products/pdh-terminal/rootfs/etc/systemd/system/hmi-kiosk.service`, `packaging/products/pdh-terminal/rootfs/etc/chromium/policies/managed/hmi-kiosk.json`, `packaging/products/pdh-terminal/rootfs/etc/hmi-kiosk/system-level` (`1`), `packaging/test-products.sh`
- Modify: `packaging/build-image.sh`, `packaging/bundle.sh`, `packaging/install.sh`, `packaging/image/stage-hmi/01-hmi/00-run.sh`, `packaging/image/files/firstboot.sh` (bzw. Pfad laut Repo), `packaging/image/verify-image.sh`, `test/install/run.sh` + `check.sh`, `Makefile`
- `product.env` Schlüssel (Shell-Variablen): `PRODUCT_ID`, `PRODUCT_NAME`, `PRODUCT_BINARY`, `IMG_NAME`, `TARGET_HOSTNAME`. HMI-Kiosk: `hmi-kiosk`, `HMI-Kiosk`, `hmi-kiosk`, `hmi-kiosk`, `hmi-kiosk`. PDH-Terminal: `pdh-terminal`, `PDH-Terminal`, `pdh-terminal`, `pdh-terminal`, `pdh-terminal`.
- Overlay PDH-Terminal: Haupteinheit wie heute, nur `ExecStart=/data/app/current/pdh-terminal …`; Richtlinie: wie heute ohne Web-Kachel-Einträge (Grundliste `127.0.0.1:8080`, `localhost:8080`, `127.0.0.1:8081`, `localhost:8081`).
- Skripte: Option `--product <id>` (Standard `hmi-kiosk`), lesen `product.env`, bauen `./cmd/$PRODUCT_BINARY`, legen Overlay nach dem gemeinsamen rootfs ab, schreiben `/etc/hmi-kiosk/product`, `firstboot.sh` kopiert `/opt/hmi-kiosk/initial/<v>/$(cat /etc/hmi-kiosk/product)`-Programm, `install.sh`-Drop-in nutzt `$PRODUCT_BINARY`. `Makefile`: `PRODUCT ?= hmi-kiosk`. Ausgabedateien `dist/$IMG_NAME-<v>.img.xz`, `dist/$PRODUCT_ID-bundle-<v>-trixie-<arch>.tar.gz`.
- `verify-image.sh --product` prüft Programm, Overlay-Einheit, `product`-Datei; HMI-Prüfungen (rc.xml Web-Fenster) nur für `hmi-kiosk`.

- [ ] **Step 1:** `packaging/test-products.sh`: beide `product.env` vollständig; Overlay enthält nur Pfade unter `etc/`; Haupteinheit im Overlay unterscheidet sich vom gemeinsamen rootfs nur in der `ExecStart`-Zeile (`diff` zählt genau 1 geänderte Zeile); Lauf → zunächst FAIL (Dateien fehlen).
- [ ] **Step 2:** Dateien und Skriptänderungen; `bash packaging/test-products.sh`, `bash test/install/run.sh` (hmi-kiosk, unverändert grün), `packaging/image/test-firstboot.sh` grün. `test/install/run.sh --product pdh-terminal` erst nach Task 10 (Programm existiert dann); bis dahin Ausgabe dokumentieren.
- [ ] **Step 3:** Commit `packaging: Abbild, Bundle und Installation je Produkt`.

**Prüfpunkt nach Task 5:** `scripts/dgo test -race -count=1 ./...`, `bash test/install/run.sh`, `packaging/image/test-*.sh`, `PDH_DIR=C:/workplace/pdh-server-terminal bash test/pdh-e2e/run.sh` grün; Abbild nur bauen, wenn Docker-Ressourcen reichen (`make image`, dann `verify-image.sh` läuft im Bau mit). Ergebnis in den Bericht.

### Task 6: Browser-Ziele Start und Admin

**Files:** Modify `internal/browser/browser.go`, `browser_test.go`

**Interfaces:**
- Produces: neue Ziele `TargetStart`, `TargetAdmin`; `(*Supervisor).SetStartURL(u string)` (vor `Run`); `TargetStart` → `--kiosk <startURL>`, `TargetAdmin` → `--kiosk http://127.0.0.1:8081/admin/`. Bestehende Ziele unverändert.
- [ ] **Step 1:** Test `TestArgsStartAndAdmin` (Argumente enthalten die URLs, `--kiosk`, gleiche Profilwahl wie `TargetTiles`); bestehende Arg-Tests unverändert grün.
- [ ] **Step 2:** FAIL → implementieren → PASS; Commit `browser: Ziele Start und Admin`.

### Task 7: Eckgeste über den Touchscreen

**Files:** Create `internal/platform/corner.go` (plattformneutrale Logik), `corner_linux.go` (Gerätezugriff), `corner_other.go` (Stub), `corner_test.go`

**Interfaces:**
- Produces: `type CornerCfg struct{ ScreenW, ScreenH int; RegionPx int /*80*/; Hold time.Duration /*5s*/ }`; `type AxisRange struct{ Min, Max int32 }`; `type cornerDetector struct{…}` mit `newCornerDetector(cfg CornerCfg, x, y AxisRange) *cornerDetector` und `feed(ev inputEvent, now time.Time) bool` (true genau einmal je Halten); `WatchCorner(ctx context.Context, devDir string, cfg CornerCfg, hit func()) error` (Linux: alle `event*` mit `ABS_X`/`ABS_MT_POSITION_X`, Bereiche per `EVIOCGABS`, erneutes Suchen alle 5 s wie `WatchTouch`; anderes OS: blockiert bis ctx Ende).
- Bereich: x ≥ Max − Breite·RegionPx/ScreenW und y ≤ Min + Höhe·RegionPx/ScreenH; ist ScreenW/H 0 → 6 % der Achse. Halten beginnt bei Berührung im Feld (BTN_TOUCH 1 bzw. Tracking-ID ≥ 0), endet bei Loslassen oder Verlassen; Treffer nach `Hold`, danach erst nach Loslassen erneut.
- [ ] **Step 1: Tests** (`cornerDetector` mit synthetischen Ereignissen): Halten 5 s in der Ecke → genau ein Treffer; 4,9 s → keiner; Verlassen des Felds nach 3 s → keiner; nur `ABS_X/ABS_Y` (Einzelpunkt) und nur `ABS_MT_POSITION_X/Y` jeweils Treffer; untere linke Ecke → keiner; ScreenW 0 → 6-%-Regel.
- [ ] **Step 2:** FAIL → implementieren → PASS (`scripts/dgo test ./internal/platform/`); Commit `platform: Eckgeste über den Touchscreen`.

### Task 8: Produktpaket `internal/product/pdhterminal` – Kopplung und Admin-Seiten

**Files:** Create `internal/product/pdhterminal/{device.go, pair.go, admin.go, admin_pdh.html, admin_card.html, *_test.go}`

**Interfaces:**
- Consumes: Task 1 `config.PDHCfg`, `ValidatePDH`; Task 2 `web.AdminProduct`, `AdminFrame`, `ProductRoute`; TP0b-API (`POST /api/terminal/v1/pair`, `GET /api/terminal/v1/device`).
- Produces:
  - `type Device struct{ DeviceID, Secret, User, URL string; PairedAt time.Time }`; `LoadDevice(dataDir string) (*Device, error)` (fehlt → nil, nil); `SaveDevice(dataDir string, d Device) error` (`<data>/pdh/device.json`, Ordner 0700, Datei 0600, atomar); `RemoveDevice(dataDir string) error`. `Device` hat `String()` ohne Geheimnis (Schutz vor versehentlichem Loggen).
  - `type Client struct{ HTTP *http.Client }`; `(c Client) Pair(ctx, baseURL, code, name string) (Device, error)`; `(c Client) Check(ctx, d Device) (State, error)`; `type State int` mit `StateOK`, `StateRevoked`, `StateUnreachable`, `StateUntrusted`. Fehlertexte deutsch: 422 → Text aus PDH; 429 → „Zu viele Fehlversuche …“; x509-Fehler → `StateUntrusted` bzw. „Zertifikat von PDH wird nicht vertraut“; Netzfehler → „PDH nicht erreichbar“. Fristen 10 s, Antwort ≤ 64 KiB, keine Weiterleitungen.
  - `func Admin(dataDir string, client Client) *web.AdminProduct` mit ID `pdh-terminal`, Name „PDH-Terminal“, Home `/admin/pdh`, Nav PDH, Kartenleser, Netzwerk, System; `HMI: false`; `IdleSeconds: 120`; Routen `GET /admin/pdh`, `POST /admin/pdh/settings` (URL, AllowHTTP, Zusatzursprünge zeilenweise, Gerätename), `POST /admin/pdh/pair` (Code), `POST /admin/pdh/unpair`, `GET /admin/card` (Platzhalter „Kartenleser folgt mit Teilprojekt 2“).
  - Seite PDH zeigt: Adresse, Zustand (nicht gekoppelt / gekoppelt als „<User>“ seit … – Zustand aus `Check`: verbunden, gesperrt, nicht erreichbar, Zertifikat nicht vertraut), Formulare. Adresswechsel bei bestehender Kopplung → Kopplung lokal entfernen (Hinweis anzeigen). Code/Geheimnis nie in Antwort, URL oder Protokoll.
- [ ] **Step 1: Tests** gegen `httptest.NewTLSServer` mit nachgebildeter PDH-API: `TestPairSuccess` (Datei 0600, Inhalt enthält Geheimnis, Seite nicht), `TestPairErrors` (422-Text durchgereicht, 429, falsches JSON), `TestPairUntrustedCert` (Client ohne Test-CA → „Zertifikat von PDH wird nicht vertraut“), `TestCheckStates` (200, 401, Verbindungsfehler), `TestAdminPages` (über `web.NewAdmin` mit `Product: Admin(...)`: Speichern ungültiger URL → Fehlermeldung; Unpair löscht Datei; URL-Wechsel entfernt Kopplung; Seite enthält nirgends `pdht_`), `TestDeviceStringHidesSecret`.
- [ ] **Step 2:** FAIL → implementieren → PASS; Commit `pdhterminal: Kopplung und Admin-Seiten`.

### Task 9: Startseite und Anmeldung bei PDH

**Files:** Create `internal/product/pdhterminal/start.go`, `start.html`, `start_test.go`

**Interfaces:**
- Produces: `func StartHandler(dataDir string, cfg func() config.PDHCfg, client Client) http.Handler` – `GET /` → 302 `/start`; `GET /start`: kein Gerät/keine URL → Seite „Gerät nicht gekoppelt. Admin-Seite: 5 Sekunden oben rechts drücken.“; `Check`: OK → Seite mit `<form method="post" action="<URL>/terminal/geraet">` + verstecktem Feld `secret` und sofortigem Absenden (Inline-Skript, CSP `default-src 'none'; script-src 'unsafe-inline'; form-action <URL>`); Revoked → „Gerät in PDH gesperrt …“; Unreachable/Untrusted → Hinweis + `<meta http-equiv="refresh" content="30">`. Antwort-Header `Cache-Control: no-store`. Nur Loopback (Host-Prüfung wie `web.NewLocal`).
- [ ] **Step 1: Tests** OK-Fall: Formular-Aktion exakt `https://…/terminal/geraet`, Geheimnis nur im Body, nicht in Location/URL, `no-store`; Unreachable → `refresh` 30; nicht gekoppelt → Hinweistext; fremder Host-Header → 403.
- [ ] **Step 2:** FAIL → implementieren → PASS; Commit `pdhterminal: Startseite mit Geräteanmeldung`.

### Task 10: Programm `cmd/pdh-terminal`, Verdrahtung, Sicherung, Importregel

**Files:** Create `internal/product/pdhterminal/run.go`, `cmd/pdh-terminal/main.go` (`//go:build linux`), `internal/arch_test.go` (Importregel); Modify `internal/helper/backup_image.go`, `backup_files.go` (+Tests), `Makefile` (build/release für `PRODUCT`)

**Interfaces:**
- Consumes: Tasks 1–9.
- Produces: `pdhterminal.Run(ctx context.Context, o Options) error`; `Options{DataDir, RunDir, LocalAddr /*127.0.0.1:8080*/, AdminAddr /*:8443*/, LocalAdminAddr /*127.0.0.1:8081*/, HelperSocket string; ChromiumEnabled bool}`. Ablauf: Store laden, Journal, Box, Hardware-ID wie HMI (Wartungslogik nicht nötig), TLS-Zertifikat, Admin (`web.NewAdminHandlers` mit `Product: Admin(...)`, Installer mit `release.PDHTerminal` und Systemstand-Marker), Startseite auf `LocalAddr`, lokale Admin auf `LocalAdminAddr`, `OriginSync.Set(config.PDHOrigins(cfg.PDH))` bei jedem Speichern, Update-Prüfung (`release.PDHTerminal`), Browser `SetStartURL("http://127.0.0.1:8080/start")` + `Show(TargetStart)`, `WatchCorner` → `Show(TargetAdmin)`, Rückkehr über bestehendes `/admin/local/back` → `TargetStart`; Selbstprüfung `selftest.CoreSteps()`; `appcore.WriteHealthy`, sd_notify, Watchdog.
- Sicherung: Datei `pdh/device.json` wird ausgelassen – `buildDataImage` entfernt sie nach `mkfs.ext4 -d` mit `debugfs -w -R "rm /pdh/device.json"` (nur wenn vorhanden), `filesExcludes` erhält `/var/lib/hmi-kiosk/pdh/device.json`.
- Importregel `TestCoreDoesNotImportProducts`: `go list`-frei per `go/parser` über `internal/**` (ohne `_test.go`): kein Paket außerhalb von `internal/product/<x>` importiert `hmikiosk/internal/product/<x>`; `internal/product/pdhterminal` importiert nicht `coord`, `session`, `rfb`, `client`.
- [ ] **Step 1: Tests** Importregel (zunächst grün halten, mit absichtlichem Testfall per Tabelle gegen einen synthetischen Importgraphen), Sicherungs-Test (Fake-Exec sieht `debugfs`-Aufruf mit `rm /pdh/device.json`; `filesExcludes` enthält Pfad), `run.go` Verdrahtungs-Smoke-Test mit `ChromiumEnabled:false` und temporären Verzeichnissen: Admin `:0`-Listener antwortet, `/start` antwortet.
- [ ] **Step 2:** FAIL → implementieren → `scripts/dgo vet ./...`, `scripts/dgo test -race -count=1 ./...`, `scripts/dgo build ./cmd/pdh-terminal` (GOOS=linux GOARCH=arm64), `bash test/install/run.sh --product pdh-terminal` grün.
- [ ] **Step 3:** Commit `pdh-terminal: Programm, Verdrahtung, Sicherung ohne Geräte-Geheimnis`.

### Task 11: Dokumentation und Ende-zu-Ende mit PDH

**Files:** Modify `README.md` (de+en: Abschnitt „Produkte“ / „Products“, PDH-Terminal einrichten), `docs/betrieb.md` (Releases je Produkt, Tags, `-product`, Grenze 0.1.6/0.1.7, Kopplung, Sperren, Zertifikatvertrauen), `docs/entscheidungen.md` (Plattformnamen gemeinsam, Produktkennung als PAX, Geheimnis nicht in Sicherung, kein CA-Feld in TP0); Create `internal/product/pdhterminal/pdh_e2e_test.go` (`//go:build e2e`), Modify `test/pdh-e2e/run.sh` (zweites Szenario)
- Voraussetzung: TP0b umgesetzt (Worktree `C:\workplace\pdh-server-pairing`).
- E2E: PDH-Container erzeugt Terminal-Benutzer und Code (SQL-Hilfe im PDH-Test) → `Client.Pair` → `Check` OK → POST `/terminal/geraet` mit Geheimnis → `GET /terminal` 200 → Gerät in PDH sperren → nach ≤ 31 s `/terminal` ohne Sitzung, `Check` = Revoked.
- [ ] **Step 1:** E2E schreiben, `PDH_DIR=C:/workplace/pdh-server-pairing bash test/pdh-e2e/run.sh` → beide Szenarien grün.
- [ ] **Step 2:** Doku; Commit `docs: zwei Produkte, PDH-Terminal einrichten`.
