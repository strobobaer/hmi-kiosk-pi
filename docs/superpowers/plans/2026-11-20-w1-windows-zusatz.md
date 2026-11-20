# W1 Windows-Terminal „Zusatz“ Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das PDH-Terminal läuft als `pdh-terminal.exe` mit Edge auf Windows 11 neben normaler Arbeit. Ein USB-Tastaturleser wird abgefangen, und bei verdecktem Fenster erscheint eine Benachrichtigung in der Art, die PDH vorgibt. Installiert wird per Setup, Updates sind signiert.

**Architecture:** Derselbe Dienst `internal/product/pdhterminal`. Was Linux braucht, steckt hinter Build-Tags oder kleinen Schnittstellen:
- Prozessstart;
- Browser;
- Fenster;
- Leser;
- Benachrichtigung;
- Helfer (unter Windows `nil`).

Ein Starter `pdh-terminal-start.exe` wählt die höchste gültig signierte Version und überwacht sie. Inno Setup installiert die Teile. PDH bekommt in einem eigenen Worktree die Einstellung `card_notify`, die Rückmeldung an `/api/card/result` und Karten am Bildschirmschoner.

**Tech Stack:**
- Go 1.24 (`scripts/dgo`), `golang.org/x/sys/windows`, ohne CGO.
- Inno Setup 6 in Docker (Wine).
- PDH: Go und chi, PostgreSQL-Migrationen, Vanilla-JS in `web/static/terminal.js`.

**Spec:** `docs/superpowers/specs/2026-11-20-w1-windows-zusatz-design.md` (Übersicht: `docs/superpowers/specs/2026-11-12-plattformen-design.md`)

## Global Constraints

- **Rückwärtskompatibel**
  - Nur hinzufügende Migrationen und optionale Felder.
  - Alte `config.json` lädt unverändert.
  - Neue PDH-Stände arbeiten mit alten Terminals, neue Terminals mit alten PDH-Ständen.
  - Jede Review prüft das.
- **PDH-Migrationen**
  - Nie ändern, nur neue anlegen und dabei `migrations/checksums.txt` ergänzen.
  - Neue Tabellen bekommen `id UUID DEFAULT pdh_uuid_v7()` (hier keine neue Tabelle).
  - Vor jeder Prüfung läuft `TestUUIDv7Policy`.
- **PDH-Arbeitsort:** Worktree `C:\workplace\pdh-server-w1`, Zweig `feat/terminal-benachrichtigung`, von `origin/main` nach `git fetch origin main`. Die Migrationsnummer ist die höchste in `origin/main` plus 1, erwartet 185.
  - Den Hauptcheckout `C:\workplace\pdh-server` nie anfassen, dort liegen ungesicherte Änderungen des Nutzers.
  - Testcontainer `pdh-gt-*` nicht anfassen.
- **Terminal-Arbeitsort:** Worktree `C:\workplace\hmi-kiosk-pdhterm`, neuer Zweig `feat/w1-windows` von `feat/pdh-terminal`.
- **Git-Regeln**
  - Nie `git stash` ohne Namen.
  - Nie pushen ohne den Nutzer.
  - `.secrets/release.key` nie committen.
  - `hardware/gehaeuse/fusion360/PDH-Terminal-Gehaeuse/.vscode/` nicht committen.
- **Bau**
  - `scripts/dgo`, `CGO_ENABLED=0`.
  - Windows-Ziel `GOOS=windows GOARCH=amd64`.
  - `GOOS=linux GOARCH=arm64` und `amd64` bleiben grün, ebenso `scripts/dgo test -race ./...` und `bash packaging/test-products.sh`.
- **Windows-Pfade**
  - Datenordner `%ProgramData%\PDH-Terminal`.
  - Laufzeitordner `%LOCALAPPDATA%\PDH-Terminal\run`.
  - Edge-Profil `%LOCALAPPDATA%\PDH-Terminal\edge`.
  - Programm `%ProgramFiles%\PDH-Terminal\`.
  - Updates `%ProgramData%\PDH-Terminal\app\<version>\`.
- **Namen und Werte**
  - App-Kennung `PDH.Terminal`.
  - Mutex `Global\PDH-Terminal`.
  - Firewall TCP 8443 im Profil Domäne/Privat.
  - Release-Datei `pdh-terminal-<version>-windows-amd64.pkg` am Tag `pdh-terminal-v<version>`.
  - PAX-Datensatz `HMIKIOSK.platform` mit dem Wert `windows-amd64`.
  - Archivname der Programmdatei `pdh-terminal.exe`.
- **Benachrichtigung**
  - Werte `toast`, `overlay`, `banner`, `front`, `off`. Eine leere Liste bedeutet Vorgabe `{overlay}`.
  - B und C schließen nach 4 s, C ist 48 px hoch.
  - Erfolg grün, Fehler rot, Abmeldung grau.
  - Texte „Angemeldet: <Name>“, „Abgemeldet: <Name>“, bei Fehler die Meldung von PDH.
- **Starter:** Gesundmeldung innerhalb von 60 s, Neustartpausen 5 s, 30 s, 5 min.
- **Edge-Fenster:** Schließen startet Edge sofort neu, minimiert und ohne Fokus.
- **Texte:** Sprache Deutsch, Stil wie bestehende Meldungen.

## Review Focus

1. **Schnell tippender Mensch oder Passwortmanager:** Tasten der echten Tastatur kommen immer an. Nur Tasten des gewählten Lesergeräts werden verschluckt (Test in Task 5).
2. **Leser abgezogen und wieder angesteckt:** Das Gerätehandle ist dann neu. Der Leser wird über VID:PID oder den Namen wiedergefunden, ohne Neustart (Test in Task 5).
3. **Edge schließt sofort wieder (Absturzschleife, Richtlinie sperrt):** Es gibt Pausen statt Dauerschleife, und das Fenster nimmt nie den Fokus (Test in Task 4).
4. **Neue PDH-Seite an altem Terminal:** `/api/card/result` liefert 404, die Seite läuft ohne Fehler weiter. Ein alter PDH-Stand schickt kein `notify`, dann gibt es keine Benachrichtigung (Tests in Task 6 und 11).
5. **Manipulierter oder halber Versionsordner unter `app\`:** Er wird übersprungen, und die nächstniedrigere gültige Version startet (Test in Task 8).

---

### Task 1: Spike Virenscanner (wegwerfen)

**Files:** nur `C:\Users\michael\AppData\Local\Temp\claude\...\scratchpad\hookspike\` (nicht im Repo).

- [ ] **Step 1:** Mini-Programm `hookspike.exe` bauen (`GOOS=windows`). Es registriert Raw Input (`RIDEV_INPUTSINK`) für Tastaturen und `WH_KEYBOARD_LL`, protokolliert Gerätehandle und Scancode und verschluckt nichts.
- [ ] **Step 2:** Auf dem Entwicklungsrechner 30 s laufen lassen und dabei tippen. Danach prüfen:
  - `Get-MpThreatDetection` meldet nichts Neues.
  - Die Exe ist nicht in Quarantäne.
  - Das Protokoll zeigt Gerätehandles.
- [ ] **Step 3:** Ergebnis als Ruling ins Ledger schreiben. Meldet Defender etwas: STOP und den Nutzer fragen.

### Task 2: Paketplattform

**Files:**
- Modify: `internal/update/package.go`, `internal/update/install.go`, `internal/release/product.go`, `internal/updatecheck/check.go`, `cmd/hmi-kiosk-pack/main.go`
- Test: `internal/update/package_test.go`, `internal/update/install_test.go`, `internal/updatecheck/product_test.go`, `cmd/hmi-kiosk-pack/*_test.go`

**Interfaces:**
- Produces:
  - `const PlatformWindowsAMD64 = "windows-amd64"` in `internal/release`.
  - `release.CurrentPlatform() string`: `"windows-amd64"` unter Windows, sonst `""` (Linux schreibt kein Feld).
  - `Package.Platform string`.
  - `(p Product) PackageNameFor(version, platform string) string`: `pdh-terminal-1.2.3.pkg` bei leerem `platform`, sonst `pdh-terminal-1.2.3-windows-amd64.pkg`.
  - Im Archiv heißt die Programmdatei `p.Binary + ".exe"`, wenn `platform` mit `windows-` beginnt.
  - Pack-Schalter `-platform windows-amd64`. Dann liest das Werkzeug `<in>/pdh-terminal.exe`.

- [ ] **Step 1: Tests schreiben**
  - `TestVerifyWindowsPackage`: Packen mit Plattform ergibt nach `Verify` `Platform=="windows-amd64"`, `Binary=="pdh-terminal.exe"`.
  - `TestLegacyRejectsWindowsPackage`: Ein Windows-Paket enthält genau einen Eintrag `pdh-terminal.exe`. `allBinaries()` der Linux-Fassung kennt den Namen nicht, so wie alte Geräte prüfen; der bestehende Pfad liefert den Fehler mit „unzulässige Datei“.
  - `TestInstallerRejectsOtherPlatform`: Ein Installer mit `Platform:"windows-amd64"` lehnt ein Linux-Paket ab, und umgekehrt. Die Meldung enthält „anderes Betriebssystem“.
  - `TestLinuxPackageUnchanged`: Ein Linux-Paket ohne `-platform` ist byteweise gleich dem bisherigen, gemessen an einer Golden-Datei aus dem Stand vor der Änderung.
  - `TestPackageNameFor`: zwei Fälle wie oben.
  - Updateprüfung: Unter `platform=windows-amd64` sucht sie die Datei `…-windows-amd64.pkg`, unter Linux weiter `…pkg`.
- [ ] **Step 2:** `scripts/dgo test ./internal/update/ ./internal/release/ ./internal/updatecheck/ ./cmd/hmi-kiosk-pack/` scheitert.
- [ ] **Step 3: Umsetzen**
  - PAX-Datensatz `HMIKIOSK.platform` lesen und schreiben.
  - `Installer` bekommt das Feld `Platform string` (leer = Linux). Ungleiche Plattform ergibt den Fehler „Paket ist für ein anderes Betriebssystem“.
  - `Install` schreibt unter Windows `app/<version>/pdh-terminal.exe` und legt das Paket selbst als `app/<version>/package.pkg` daneben (Task 8 prüft es).
- [ ] **Step 4:** Tests grün. `GOOS=windows scripts/dgo vet ./internal/update/ ./internal/release/` ist grün.
- [ ] **Step 5:** Commit `feat(update): Plattform im Paket, Windows-Pakete`.

### Task 3: Kern übersetzt und startet unter Windows (ohne Browser)

**Files:**
- Modify: `cmd/pdh-terminal/main.go` (Build-Tag entfernen); `internal/product/pdhterminal/run.go`
- Create: `cmd/pdh-terminal/defaults_linux.go`, `cmd/pdh-terminal/defaults_windows.go`, `internal/client/process_windows.go`, `internal/product/pdhterminal/plat_linux.go`, `internal/product/pdhterminal/plat_windows.go`
- Modify: Linux-Teile von `internal/keyboard/clientrun`, `internal/helper` (nur Build-Tags `linux`, soweit `pdh-terminal` sie unter Windows nicht braucht), `internal/web` (Admin-Abschnitte ohne Helfer)
- Test: `internal/product/pdhterminal/run_test.go`, `internal/web/admin_*_test.go`

**Interfaces:**
- Produces:
  - Plattformvorgaben in `cmd/pdh-terminal`: `defaultData`, `defaultRun`, `defaultHelperSocket`. Unter Windows sind das `%ProgramData%\PDH-Terminal`, `%LOCALAPPDATA%\PDH-Terminal\run` und `""`.
  - `Options.HelperSocket == ""` heißt: kein Helfer. In diesem Fall gilt:
    - `s.helper` ist `nil`, `AdminDeps.Helper/Backup/Net` sind `nil`.
    - `Update.Activate` ist die Windows-Aktivierung aus Task 8. Bis dahin liefert sie einen Fehler „noch nicht verfügbar“.
    - `SystemLevel` ist `nil`, der Ursprungsabgleich ist ohne Wirkung.
  - `client.NewExecRunner()` gibt es auch für Windows. Der Prozess läuft in einem Job-Objekt mit Kill-on-close; `Stop` beendet den ganzen Baum.
  - `platformHardwareID() string`: unter Windows `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`.
- **Verhalten:**
  - Unter Windows startet `wvkbd` nicht (`s.kbd == nil`).
  - Das OLED steht auf „Statusdisplay nur am Raspberry Pi“.
  - Der Lesertyp PN532 gilt als nicht unterstützt (`Unsupported`), mit dem Text „Nur am Raspberry Pi“.
  - Unter Windows zeigt die Admin-Seite keine Netzwerk-, Zeit-, Sicherungs- oder Abbild-Abschnitte. Stattdessen steht dort „Unter Windows über die Windows-Einstellungen“.
  - Die Selbstprüfung unter Windows besteht aus Konfiguration, Kartenleser und „Edge gefunden“ (Task 4 füllt diesen Schritt).

- [ ] **Step 1: Tests schreiben**
  - `TestRunWithoutHelper` (plattformneutral): `Run` mit `HelperSocket:""`, `ChromiumEnabled:false` und Port 0 ruft `Ready`. Die Admin-Seite liefert `/admin/` mit 200, und das HTML enthält nicht „Netzwerk“ als Abschnittslink.
  - `TestUpdateActivateWithoutHelper`: Liefert den Fehler mit „noch nicht verfügbar“.
- [ ] **Step 2:** `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 scripts/dgo build ./cmd/pdh-terminal` scheitert heute mit „build constraints exclude all Go files“.
- [ ] **Step 3: Umsetzen.** Linux-Teile wandern hinter Build-Tags oder hinter `plat_*.go`; die Aufteilung wählt der Implementer.
- [ ] **Step 4: Prüfen**
  - Der Windows-Bau von `./cmd/pdh-terminal` gelingt.
  - `GOOS=windows scripts/dgo vet ./cmd/pdh-terminal/ ./internal/product/pdhterminal/...` ist grün.
  - `scripts/dgo test -race ./...` und `bash packaging/test-products.sh` sind grün.
  - Probelauf auf diesem Rechner:

    ```
    dist\win\pdh-terminal.exe -no-chromium -data <scratch>\data -run <scratch>\run
    ```

    Danach liefert `Invoke-WebRequest http://127.0.0.1:8080/start` 200. Beenden mit Strg+C.
- [ ] **Step 5:** Commit `feat(windows): pdh-terminal übersetzt und startet unter Windows`.

### Task 4: Edge-Fenster unter Windows

**Files:**
- Create: `internal/browser/edge.go` (plattformneutral: Argumente), `internal/browser/edge_windows.go` (Pfad, Fenster), `internal/product/pdhterminal/window.go`
- Modify: `internal/product/pdhterminal/run.go` (`startBrowser`), `internal/product/pdhterminal/plat_*.go`
- Test: `internal/browser/edge_test.go`

**Interfaces:**
- Produces:
  - `browser.FindEdge(readReg func(key string) (string, error)) (string, error)`: liest `HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\msedge.exe`.
  - `browser.EdgeArgs(startURL, profileDir string) []string` enthält:
    - `--app=<startURL>`, `--user-data-dir=<profileDir>`;
    - `--no-first-run`, `--no-default-browser-check`, `--disable-features=msEdgeSleepingTabs,LocalNetworkAccessChecks,PrivateNetworkAccessRespectPreflightResults`;
    - `--disable-background-timer-throttling`, `--disable-renderer-backgrounding`, `--disable-backgrounding-occluded-windows`.
  - `type window interface { Foreground() bool; Raise() error; MinimizeNoActivate() error }` in `pdhterminal`. Unter Linux sind das Platzhalter: `Foreground()` liefert `true`, die übrigen `nil`.
  - `browser.Supervisor` bekommt die Option `OnRestart func(n int)`. `n` ist die Zahl der Neustarts, ab 1. Unter Windows ruft sie `MinimizeNoActivate` am neuen Fenster auf. Der erste Start bleibt normal sichtbar.
- **Fenster finden:** sichtbares Fenster der Klasse `Chrome_WidgetWin_1` eines Prozesses im Job-Objekt aus Task 3.
- **Risiko:** `msedge.exe` reicht den Start an einen schon laufenden Prozess desselben Profils weiter und endet dann sofort. Der Supervisor erkennt das am Job-Objekt: Er wartet, bis der Job leer ist, statt nur auf den Startprozess.
- **Lokales Netz:** Reichen die Schalter nicht (Probe in Step 4), ist das ein Befund für den Nutzer (Ruling R15): Es gibt keinen HKCU-Rückfall (`HKCU\Software\Policies` ist für Benutzer nicht beschreibbar) und keine maschinenweite Richtlinie für lokale Netzzugriffe. Das Setup schreibt nur HKLM `SOFTWARE\Policies\Microsoft\Edge` `HideFirstRunExperience` = 1.

- [ ] **Step 1: Tests schreiben**
  - `TestEdgeArgs`: Die Liste enthält genau die Schalter oben, in fester Reihenfolge.
  - `TestFindEdgeMissing`: Leerer Registry-Wert ergibt den Fehler „Edge nicht gefunden“.
  - `TestSupervisorOnRestart`: Mit Fake-Runner und Fake-Clock wird `OnRestart(1)` nach dem ersten Ende gerufen, `OnRestart(2)` nach dem zweiten. Endet der Prozess fünfmal kurz nacheinander, wachsen die Pausen bis zur Obergrenze.
- [ ] **Step 2:** Tests scheitern.
- [ ] **Step 3:** Umsetzen. Ohne Edge gilt: Warnung, Selbstprüfungsschritt „Edge“ rot mit Grund, neuer Versuch alle 30 s.
- [ ] **Step 4: Prüfen**
  - Tests grün, Windows-Bau grün.
  - Probelauf auf diesem Rechner ohne `-no-chromium`:
    - Ein Edge-Fenster zeigt `/start`.
    - Nach dem Schließen ist es binnen 5 s minimiert in der Taskleiste zurück, ohne Fokus; ein Editor im Vordergrund bleibt vorne.
  - Das normale Edge des Nutzers bleibt unberührt, geprüft an seinem Profilordner.
- [ ] **Step 5:** Commit `feat(windows): Edge-Fenster mit Neustart minimiert`.

### Task 5: USB-Tastaturleser unter Windows

**Files:**
- Create: `internal/product/pdhterminal/card/usb_windows.go`, `internal/product/pdhterminal/card/learn.go` (plattformneutral)
- Modify: `internal/product/pdhterminal/card/reader_other.go` (Build-Tag `!linux && !windows`), `internal/product/pdhterminal/admin_card.go`, `internal/product/pdhterminal/admin_card.html`
- Test: `internal/product/pdhterminal/card/learn_test.go`, `internal/product/pdhterminal/card/usb_windows_test.go` (nur Logik, Build-Tag `windows` mit Fakes, läuft per `GOOS=windows vet`), `internal/product/pdhterminal/admin_card_test.go`

**Interfaces:**
- Consumes: `keyBuffer` aus `card/usb.go`. Windows-Scancodes des Hauptblocks und des Ziffernblocks (Satz 1) entsprechen den Linux-Tastencodes, deshalb gehen sie unverändert in `keyBuffer.Key`. Enter hat 28, Ziffernblock-Enter ist Scancode 28 mit dem Flag E0 und wird auf 96 abgebildet.
- Produces:
  - `ListInputDevices(dir string) []InputDevice` unter Windows; `dir` wird ignoriert. Grundlage sind `GetRawInputDeviceList` und `RIDI_DEVICENAME`. Das Gerätekennzeichen ist `vvvv:pppp` aus `VID_xxxx&PID_xxxx`.
  - `SuggestFormat(typed string) string`: Genau 8 oder 14 Hex-Zeichen ergeben `hex`, genau 10 Ziffern `dec`, alles andere ebenfalls `hex`.
  - `Learn(ctx context.Context, d time.Duration) (InputDevice, string, error)`: wartet bis zu `d` auf die erste Folge, die mit Enter endet, und liefert Gerät und getippten Text. Unter Linux kommt `errNotSupported`, und die Seite blendet den Knopf aus.
- **Abfangen**
  - Der Hook verschluckt eine Taste nur, wenn das letzte Raw-Input-Ereignis mit demselben Scancode und Make/Break höchstens 50 ms zurückliegt und vom gewählten Gerät stammt. Jede andere Taste läuft weiter.
  - Raw Input und Hook laufen im selben Thread mit eigener Nachrichtenschleife. So kommt Raw Input vor dem Hook an (dokumentiertes Verhalten bei `RIDEV_INPUTSINK` mit Nachrichtenfenster im Hook-Thread). Die Zuordnung prüft der Implementer im Probelauf.
- **Admin-Seite (nur Windows):**
  - Knopf „Karte vorhalten“, er wartet 15 s.
  - Danach trägt er Gerät und Formatvorschlag ins Formular ein und zeigt die UID in `hex`, `hex-rev` und `dec`. Gespeichert wird erst mit „Speichern“.

- [ ] **Step 1: Tests schreiben**
  - `TestSuggestFormat`: `"04A1B2C3"→hex`, `"04A1B2C3D4E5F6"→hex`, `"0123456789"→dec`, `"12345"→hex`.
  - `TestSwallowDecision` (Windows-Logik als reine Funktion `swallow(last rawEvent, hook hookEvent, want deviceID) bool`):
    - Gleiches Gerät, gleicher Scancode, 10 ms Abstand: `true`.
    - Anderes Gerät: `false`.
    - Gleicher Scancode nach 80 ms: `false`.
    - Kein Raw-Ereignis: `false`.
  - `TestDeviceMatchAfterReplug`: Neues Handle mit gleicher VID:PID passt zu `usbDevice="1a2b:3c4d"`.
  - `TestCardFormWindowsTypes`: Mit Plattform Windows listet das Formular nur „kein Leser“ und „USB-Leser (Tastatur)“.
- [ ] **Step 2:** Tests scheitern. Windows-Tests prüft `GOOS=windows scripts/dgo vet`, ausführen geht nur im Probelauf.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4: Prüfen**
  - Tests grün, Windows-Bau grün.
  - Probelauf: mit `pdh-terminal.exe -no-chromium`, Editor im Vordergrund, und einer schnell getippten Folge als Leser-Ersatz von einer zweiten Tastatur oder USB-Leser, falls vorhanden.
  - Vom Lesergerät kommt nichts im Editor an. Von der echten Tastatur kommt alles an.
  - Ist kein zweites Gerät vorhanden, steht das im Report, und die Prüfung wandert in `dist/geraetetest-w1.md`.
- [ ] **Step 5:** Commit `feat(windows): USB-Tastaturleser mit Abfangen und Lernknopf`.

### Task 6: Rückmeldung und Benachrichtigung

**Files:**
- Create: `internal/product/pdhterminal/notify/notify.go` (plattformneutral), `internal/product/pdhterminal/notify/show_windows.go`, `internal/product/pdhterminal/notify/show_other.go`
- Modify: `internal/product/pdhterminal/card/events.go` (Route `/api/card/result`), `internal/product/pdhterminal/start.go`, `internal/product/pdhterminal/run.go`
- Test: `internal/product/pdhterminal/notify/notify_test.go`, `internal/product/pdhterminal/card/events_test.go`

**Interfaces:**
- Consumes: `window` aus Task 4 (`Foreground`, `Raise`).
- Produces:
  - `type Result struct { OK bool; Name, Message string; Notify []string; Logout bool }`. JSON-Felder `ok`, `name`, `message`, `notify`, `logout`.
  - `notify.Kinds(notifyField []string) []string`:
    - Bekannte Werte in fester Reihenfolge `toast`, `overlay`, `banner`, `front`.
    - `off` schließt alles aus und ergibt eine leere Liste.
    - Unbekannte Werte werden ignoriert.
    - Leere oder fehlende Liste ergibt eine leere Liste: ohne Angabe von PDH keine Benachrichtigung, Schutz für alte PDH-Stände. Die Vorgabe `{overlay}` setzt PDH (Task 10).
  - `notify.Text(r Result) (text string, tone string)`:
    - Erfolg: Ton `"ok"`, Text „Angemeldet: <Name>“.
    - Abmeldung: Ton `"neutral"`, Text „Abgemeldet: <Name>“.
    - Fehler: Ton `"error"`, `Message` oder „Anmeldung fehlgeschlagen“.
  - `type Shower interface { Show(kinds []string, text, tone string) }`. Unter Windows:
    - `toast`: Mitteilung über AppUserModelID `PDH.Terminal`, Knopf „Terminal öffnen“.
    - `overlay`: Fenster immer oben, ohne Aktivierung, Mitte des Hauptbildschirms, 4 s, Klick schließt.
    - `banner`: volle Breite oben, 48 px, 4 s.
    - `front`: `window.Raise()`.
    - Farben: ok `#27ae60`, error `#c0392b`, neutral `#7f8c8d`.
    - Andere Plattformen: Platzhalter ohne Wirkung.
  - `POST /api/card/result`:
    - nur Ursprung `cardOrigin`, CORS-Vorabanfrage samt `Access-Control-Allow-Private-Network` wie bei `/api/card/events`;
    - Inhalt höchstens 2 KiB JSON, Name und Meldung je höchstens 120 Zeichen, Steuerzeichen entfernt;
    - Antwort 204.
    - Zeigt nur dann etwas, wenn `!window.Foreground()` und `Kinds` nicht leer ist.
- **Gestaltung:** Fenster B/C mit GDI (`golang.org/x/sys/windows` und eigene `syscall`-Proc-Aufrufe), Schrift Segoe UI. Die genaue Gestaltung wählt der Implementer.

- [ ] **Step 1: Tests schreiben**
  - `TestKinds`:
    - `[]→[]`;
    - `["banner","overlay"]→["overlay","banner"]`;
    - `["off","toast"]→[]`;
    - `["x","front"]→["front"]`.
  - `TestText`: Die drei Fälle mit den genauen Texten oben.
  - `TestResultEndpoint`:
    - Falscher Ursprung: 403.
    - Richtiger Ursprung: 204, und der Fake-Shower bekommt `["overlay"]`, „Angemeldet: M. Muster“, `"ok"`, wenn Fake-`Foreground` `false` ist.
    - Bei `Foreground=true` wird nichts gezeigt.
    - Mehr als 2 KiB: 413.
    - Vorabanfrage `OPTIONS` mit `Access-Control-Request-Private-Network: true`: Antwort mit `Access-Control-Allow-Private-Network: true`.
  - `TestResultEndpointLinux`: Am Pi kommt 204, und der Platzhalter-Shower zeigt nie etwas (Fake-`window` liefert immer `true`).
- [ ] **Step 2:** Tests scheitern.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4:** Tests grün, Windows-Bau grün. Probelauf: per `Invoke-WebRequest` mit gesetztem Origin-Header an `/api/card/result` posten und dabei den Editor vorne haben. A, B, C und D erscheinen einzeln und kombiniert.
- [ ] **Step 5:** Commit `feat(windows): Benachrichtigung bei Kartenanmeldung im Hintergrund`.

### Task 7: Tray-Symbol

**Files:**
- Create: `internal/product/pdhterminal/tray_windows.go`, `internal/product/pdhterminal/tray_other.go`
- Modify: `internal/product/pdhterminal/run.go`, `cmd/pdh-terminal/main.go`

**Interfaces:**
- Produces:
  - `const ExitQuit = 3`: Rückgabewert, wenn der Benutzer „Beenden“ wählt.
  - `const ExitRestart = 4`: Rückgabewert, mit dem ein Update um einen Neustart bittet (Task 8).
  - `Run` liefert `ErrQuit` bzw. `ErrRestart`, und `main` setzt den Rückgabewert.
- **Menü:**
  - „Terminal öffnen“ startet oder holt das Edge-Fenster (`Raise`).
  - „Admin-Seite“ öffnet `https://127.0.0.1:8443/admin/` im Standardbrowser.
  - „Selbstprüfung“ öffnet die Admin-Seite der Selbstprüfung.
  - „Beenden“ fragt nach und beendet dann.
- **Symbolfarbe:** grün im Betrieb, rot bei Warnung (Edge fehlt, Port belegt, Leser getrennt), Tooltip mit dem Grund.

- [ ] **Step 1:** Test `TestQuitExitCode` (plattformneutral mit Fake-Tray): Ein Quit-Signal aus dem Tray beendet `Run` mit `ErrQuit`.
- [ ] **Step 2:** Test scheitert.
- [ ] **Step 3:** Umsetzen. Symbole als eingebettete `.ico`, gezeichnet als einfacher Kreis grün bzw. rot mit „P“.
- [ ] **Step 4:** Test grün, Windows-Bau grün. Probelauf: Tray-Symbol sichtbar, Menü funktioniert, „Beenden“ ergibt Rückgabewert 3.
- [ ] **Step 5:** Commit `feat(windows): Tray-Symbol mit Menü`.

### Task 8: Starter und Updates unter Windows

**Files:**
- Create: `internal/winstart/choose.go` (plattformneutral), `internal/winstart/run_windows.go`, `cmd/pdh-terminal-start/main.go` (Build-Tag `windows`), `internal/product/pdhterminal/activate_windows.go`
- Test: `internal/winstart/choose_test.go`, `internal/winstart/supervise_test.go`

**Interfaces:**
- Consumes:
  - `update.Verify`, `Package.Platform`, das Feld `Installer.Platform`, `package.pkg` im Versionsordner (Task 2).
  - `ExitQuit` und `ExitRestart` (Task 7).
  - `appcore.HealthyFile` im Laufzeitordner.
- Produces:
  - `type Candidate struct { Version, Exe, Pkg string; Base, Bad bool }`.
  - `winstart.Choose(cands []Candidate, verify func(Candidate) error) (Candidate, error)`:
    - Vergleicht numerisch nach x.y.z.
    - Überspringt `Bad` und alles, wofür `verify` einen Fehler liefert.
    - Die Grundversion (`Base`) wird nicht geprüft: Sie liegt in Program Files und ist nur für Administratoren beschreibbar.
  - `winstart.VerifyCandidate(c Candidate, pub ed25519.PublicKey) error`:
    - Die Signatur von `package.pkg` stimmt, Produkt `pdh-terminal`, Plattform `windows-amd64`, die Version entspricht dem Ordnernamen.
    - Der SHA-256 der Exe im Paket gleicht dem von `Exe`.
    - Die Exe bleibt zwischen Prüfung und Start ohne Schreibfreigabe geöffnet.
  - `winstart.Supervise(ctx, start func(Candidate) (proc, error), clock)`. Er regelt:
    - Nach dem Start wird bis zu 60 s gewartet, bis `healthy` die Version nennt. Bleibt das aus, kommt `bad` in den Versionsordner, der Prozess wird beendet und neu gewählt.
    - `ExitQuit`: Der Starter endet.
    - `ExitRestart`: Sofort neu wählen und starten.
    - Andere Enden: Pausen 5 s, 30 s, dann 5 min. Ein Lauf über 10 min setzt die Pausen zurück.
  - Mutex `Global\PDH-Terminal`. Ist er belegt, endet der Starter still mit Rückgabewert 0.
  - Windows-Aktivierung für `Installer.Activate`: Sie schreibt nichts, ruft `ErrRestart` aus. Der Dienst endet, und der Starter wählt die neue Version.
  - Aufräumen: Nach gesundem Start bleiben die laufende Version und die nächstniedrigere. Ältere Ordner werden gelöscht, und Versionen unter der Grundversion aus Program Files fallen ganz weg.

- [ ] **Step 1: Tests schreiben**
  - `TestChooseHighestValid`:
    - Grundversion 0.4.0, Ordner 0.4.2 gültig, 0.5.0 `Bad`, 0.6.0 mit `verify`-Fehler: gewählt wird 0.4.2.
    - Nur die Grundversion: gewählt wird die Grundversion.
    - Ordner 0.3.0 unter der Grundversion 0.4.0: gewählt wird die Grundversion.
  - `TestVerifyCandidate`:
    - Gültig: `nil`.
    - Exe ausgetauscht: Fehler mit „Prüfsumme“.
    - Linux-Paket: Fehler mit „Betriebssystem“.
    - Ordnername ≠ Version: Fehler.
    - Fehlendes `package.pkg`: Fehler.
  - `TestSuperviseHealthTimeout` (Fake-Clock): 60 s ohne `healthy` ergibt `bad`-Datei und Start der nächsten Wahl.
  - `TestSuperviseRestartBackoff`: Drei Abstürze ergeben Pausen 5 s, 30 s, 5 min.
  - `TestSuperviseQuit`: Rückgabewert 3 beendet den Starter.
  - `TestSuperviseRestartCode`: Rückgabewert 4 startet ohne Pause neu.
- [ ] **Step 2:** Tests scheitern.
- [ ] **Step 3:** Umsetzen. Prozessstart der gewählten Exe mit denselben Argumenten, die der Starter bekommen hat.
- [ ] **Step 4: Prüfen**
  - Tests grün.
  - Windows-Bau grün für `./cmd/pdh-terminal-start`.
  - Probelauf mit einem Scratch-Datenordner:
    - Ein selbst gepacktes 0.0.2-Windows-Paket (Testschlüssel aus den Tests, nicht `release.key`) wird über die Admin-Seite installiert.
    - Der Starter startet danach 0.0.2.
    - Ein Paket, dessen Programm sich nie gesund meldet (`-version` falsch gesetzt), fällt nach 60 s auf die vorherige Version zurück.
- [ ] **Step 5:** Commit `feat(windows): Starter mit Versionswahl, Gesundmeldung und Rückfall`.

### Task 9: Setup, Bau und Doku

**Files:**
- Create: `packaging/windows/pdh-terminal.iss`, `packaging/windows/build-setup.sh`, `packaging/windows/README.md`
- Modify: `packaging/test-products.sh` (Prüfung des `.iss` auf Pflichtteile), `docs/betrieb.md` (Abschnitt „Windows“), `README.md` (Download-Tabelle, Installation Windows)

**Interfaces:**
- Consumes: `dist/win/pdh-terminal.exe`, `dist/win/pdh-terminal-start.exe` aus `build-setup.sh` (gebaut mit `ReleasePublicKeyHex` und `Version` wie im Makefile-Ziel `release`).
- Produces:
  - `packaging/windows/build-setup.sh --version x.y.z`: baut beide Exe und `dist/PDH-Terminal-Setup-x.y.z.exe`, signiert das Paket `dist/pdh-terminal-x.y.z-windows-amd64.pkg` und schreibt beide `.sha256`.
  - Den Schlüssel liest das Skript über `HMI_RELEASE_KEY_DIR` (lesend eingebunden), Standard `.secrets`.
  - Inno Setup kommt aus dem Docker-Abbild `amake/innosetup` mit festgelegtem Digest. Der Digest wird im Skript notiert.
- **Inhalt `.iss`:**
  - Installation für alle Benutzer.
  - `{autopf}\PDH-Terminal`, `{commonappdata}\PDH-Terminal` mit `Permissions: users-modify`.
  - Run-Eintrag HKLM `PDH-Terminal` für `"{app}\pdh-terminal-start.exe"`.
  - Startmenü „PDH-Terminal“ mit `AppUserModelID: PDH.Terminal`, „PDH-Terminal Admin“ (URL `https://127.0.0.1:8443/admin/`).
  - Firewall: `netsh advfirewall firewall add rule name="PDH-Terminal Admin" dir=in action=allow protocol=TCP localport=8443 profile=domain,private`. Bei der Deinstallation wird die Regel per Namen gelöscht.
  - Nach der Installation startet der Starter, außer bei `/VERYSILENT`.
  - Deinstallation: Den Datenordner nicht löschen und den Hinweis „Kopplung und Einstellungen bleiben in C:\ProgramData\PDH-Terminal“ zeigen. Vorher laufende Prozesse beenden.
  - Ein Setup derselben oder höheren Version über eine bestehende Installation ist ein Update und behält die Daten.
- **Doku:**
  - Installation, stille Verteilung, Leser einrichten (13,56 MHz, Tastaturmodus, „Karte vorhalten“), Benachrichtigung in PDH.
  - Grenzen: ein Terminal je PC, Datenordner für alle Benutzer lesbar, SmartScreen „Unbekannter Herausgeber“.
  - Release-Ablauf mit beiden Plattformen am selben Tag.

- [ ] **Step 1:** Prüfung in `packaging/test-products.sh` ergänzen. Sie verlangt im `.iss`:
  - `AppUserModelID: "PDH.Terminal"`;
  - den Run-Eintrag;
  - `localport=8443`;
  - `profile=domain,private`;
  - `users-modify`;
  - kein `Flags: deleteafterinstall` auf dem Datenordner.

  Die Prüfung schlägt zunächst fehl.
- [ ] **Step 2:** `.iss`, Bau-Skript und Doku schreiben.
- [ ] **Step 3:** Prüfen.
  - `bash packaging/test-products.sh` ist grün.
  - `bash packaging/windows/build-setup.sh --version 0.0.1-test` scheitert an der Versionsprüfung (nur x.y.z).
  - `--version 0.4.0` erzeugt Setup und Paket, und `Verify` des Pakets mit `release.pub` gelingt.
- [ ] **Step 4:** Commit `feat(windows): Setup mit Inno Setup, Bau-Skript und Doku`.

### Task 10: PDH: Einstellung `card_notify`

**Arbeitsort:** `C:\workplace\pdh-server-w1`, siehe Global Constraints.

**Files:**
- Create: `migrations/<N>_terminal_card_notify.up.sql` (N = höchste Nummer + 1)
- Modify:
  - `migrations/checksums.txt`
  - `internal/web/terminal_login_store.go` (`terminalLoginSettings` um `CardNotify []string`)
  - `internal/web/terminal_events.go` (Feld `card_notify`)
  - `internal/web/user_detail.go`, `web/templates/user_detail.gohtml` (Kästchen neben der Anmeldeart)
  - `web/templates/terminal.gohtml` (`data-card-notify`)
- Test: `internal/web/terminal_events_integration_test.go`, `internal/web/user_detail_*_test.go`

**Interfaces:**
- Produces:
  - **Migration:**
    `ALTER TABLE terminal_settings ADD COLUMN IF NOT EXISTS card_notify TEXT[] NOT NULL DEFAULT '{}'`
    mit einer CHECK-Bedingung:
    - jedes Element ist eines von `toast`, `overlay`, `banner`, `front`, `off`;
    - `off` steht nur allein.
  - **Ereignis `settings` und `data-card-notify`:** Sie tragen die *wirksame* Liste. `{}` wird zu `["overlay"]`, gespeichert bleibt die leere Liste.
  - **Formular:**
    - Vier Kästchen „Windows-Mitteilung“, „Fenster in der Mitte“, „Leiste oben“, „Terminal nach vorn“ und ein Kästchen „Keine Benachrichtigung“, das die anderen ausschließt.
    - Hinweis: „Wirkt nur an Windows-Terminals in der Betriebsart Zusatz.“
    - Nichts angehakt bedeutet Vorgabe, dazu der Hinweis „(Vorgabe: Fenster in der Mitte)“.
  - Speichern ruft `notifyTerminalSettings`, wie es bei `manual_login` geschieht.

- [ ] **Step 1: Tests schreiben**
  - Integration:
    - Ohne Eintrag liefert das Ereignis `card_notify:["overlay"]`.
    - Nach dem Speichern von `banner` und `toast`: `["toast","banner"]`.
    - `off`: `["off"]`.
  - Speichern von `off` zusammen mit `toast` wird abgelehnt, ebenso ein unbekannter Wert.
  - Migration: `TestUUIDv7Policy` und der Test der eingefrorenen Migrationen sind grün.
- [ ] **Step 2:** Tests scheitern.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4:** `go test ./internal/web/... ./pkg/database/...` grün, wie in `AGENTS.md` des PDH-Servers beschrieben.
- [ ] **Step 5:** Commit `feat(terminal): Benachrichtigungsart je Terminal (card_notify)`.

### Task 11: PDH: Rückmeldung und Karten am Bildschirmschoner

**Arbeitsort:** wie Task 10.

**Files:**
- Modify: `web/static/terminal.js`, `web/templates/terminal.gohtml` (Schoner mit `data-card-events`)
- Test: `internal/web/terminal_page_test.go`. JS-Tests nach dem bestehenden Muster, falls eins existiert; sonst als Seiten-Test, der die Attribute prüft.

**Interfaces:**
- Consumes:
  - `/api/card/result` am Terminal (Task 6). Der Ursprung ist der Kartenstrom-Ursprung `http://127.0.0.1:8080`, die Basis kommt aus `data-card-events-url`, wie der Kartenstrom sie heute findet.
  - `data-card-notify` und das Ereignis `settings.card_notify` (Task 10).
- Produces:
  - **Rückmeldung:**
    - Nach jeder Antwort von `/terminal/card` und nach `/terminal/card/abmelden` postet die Seite:
      `{ok, name, message, notify, logout}`
    - Der Aufruf läuft mit `fetch(…, {mode:'cors'})`.
    - Jeder Fehler und jedes 404 wird still ignoriert.
  - **Schoner:**
    - Im Modus `schoner` abonniert die Seite den Kartenstrom ebenfalls.
    - Bei einer Karte legt sie die UID in `sessionStorage['pdh.pendingCard']` und wechselt zu `/terminal/info`.
    - Die Info-Seite liest den Eintrag beim Laden, löscht ihn sofort und meldet die Karte an wie eine frisch gelesene.
    - Ein Eintrag, der älter als 10 s ist, wird verworfen. Dazu speichert die Seite die UID mit Zeitstempel.

- [ ] **Step 1: Tests schreiben**
  - Seiten-Test:
    - Der Schoner trägt `data-card-events="1"` für Gerätesitzungen.
    - `data-card-notify` enthält die wirksame Liste.
  - JS-Logik als reine Funktionen in `terminal.js`, nach dem Muster `loginView`:
    - `resultPayload(d, notify, logout)` baut die Nutzdaten.
    - `takePendingCard(store, now)`: Liefert die UID, wenn der Eintrag höchstens 10 s alt ist, und löscht ihn immer.
  - Diese Funktionen testen, wenn es ein JS-Testmuster gibt; sonst begründet im Report vermerken.
- [ ] **Step 2:** Tests scheitern.
- [ ] **Step 3:** Umsetzen.
- [ ] **Step 4:** PDH-Tests grün. Ein alter Terminalstand (404 auf `/api/card/result`) erzeugt keine Konsolenfehler; der Seiten-Test prüft das `catch`.
- [ ] **Step 5:** Commit `feat(terminal): Ergebnis an das Terminal melden, Karte weckt den Bildschirmschoner`.

### Task 12: Gesamtprobe und Geräteprüfliste

**Files:** `dist/geraetetest-w1.md` (git-ignoriert, nicht committen)

- [ ] **Step 1:** Prüfliste nach Spec Abschnitt 7 schreiben:
  1. Installieren, koppeln.
  2. Leser wählen mit „Karte vorhalten“.
  3. Karte vor Excel.
  4. A–D einzeln und kombiniert.
  5. Fenster schließen: kommt minimiert zurück.
  6. Karte am Bildschirmschoner, am Pi und unter Windows.
  7. Update per Release-Datei; absichtlicher Fehlstart fällt zurück.
  8. Zweiter Windows-Benutzer.
  9. Deinstallieren, Daten bleiben.
  10. Alter PDH-Stand mit neuem Terminal.

  Zu jedem Punkt gehört das erwartete Ergebnis.
- [ ] **Step 2:** Volle Suite beider Repos:
  - Terminal: `scripts/dgo test -race ./...`, `vet` für linux/arm64, linux/amd64 und windows/amd64, `bash packaging/test-products.sh`.
  - PDH: die Suite laut `AGENTS.md`.
- [ ] **Step 3:** Nichts committen, nichts pushen. Ergebnis ins Ledger.
