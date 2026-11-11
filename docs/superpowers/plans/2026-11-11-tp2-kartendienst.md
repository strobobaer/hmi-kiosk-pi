# TP2 Kartendienst und Kartensitzung Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das PDH-Terminal liest RFID-Karten (PN532 über I²C oder USB-Leser) und meldet sie lokal an die PDH-Seite `/terminal`; PDH erkennt die Person und hält eine Kartensitzung für dieses Terminal.

**Architecture:** Pi-Seite: neues Paket `internal/product/pdhterminal/card` (Leser, Entprellung, Ereignis-Hub, SSE-Handler), eingebunden in die Startseite auf `127.0.0.1:8080`, Admin-Seite „Kartenleser“, Selbstprüfungsschritt, Abbild mit I²C. PDH-Seite: Tabelle `terminal_card_sessions`, Handler `POST /terminal/card`, `…/ping`, `…/abmelden`, `terminal.js` abonniert den Ereignisstrom nur auf gekoppelten Geräten. Rechte der Kartenperson werden mit der Person geprüft, nie mit dem Terminal-Benutzer.

**Tech Stack:** Go 1.24 (nur Standardbibliothek, `syscall` wie `internal/platform/corner_linux.go`), pi-gen-Abbild; PDH: Go, chi, PostgreSQL, Vanilla-JS.

**Spec:** `docs/superpowers/specs/2026-11-09-anlagenterminal-design.md`, Teil 2 (Zeilen 84–110). Faktensammlung: Code-Stellen stehen bei den Tasks.

**Zweige:**
- hmi-kiosk: `feat/kartendienst` von `feat/pdh-terminal`, Worktree `C:\workplace\hmi-kiosk-pdhterm` (Tasks 1–6).
- PDH: `feat/kartensitzung` von `feat/terminal-kopplung`, neuer Worktree `C:\workplace\pdh-server-karten` (Tasks 7–9). `C:\workplace\pdh-server` nie anfassen.

## Global Constraints

- Alles nur im Produkt **PDH-Terminal**; HMI-Kiosk bleibt unverändert lauffähig (`internal/arch_test.go` grün).
- Kartenleser-Typen: `aus` · `pn532-i2c` · `usb`. In der Konfiguration ist `""` = aus.
- PN532: Standard Bus `/dev/i2c-1`, Adresse `0x24`, Abfrage ca. 4×/s (250 ms), ISO 14443A.
- USB: Gerät exklusiv (`EVIOCGRAB`), Zeichen bis Enter; die Nummer gelangt nie als Tastatureingabe in ein Feld.
- UID-Format: `hex` (Standard, groß, ohne Trenner) · `hex-rev` · `dec`.
- Entprellen: gleiche Karte innerhalb von 2 s nur einmal; Ereignisse älter als 5 s werden nie geliefert.
- Ereignisstrom: `GET http://127.0.0.1:8080/api/card/events` (SSE), nur Loopback, nur `Origin` = Ursprung der eingetragenen PDH-Adresse, CORS mit genau diesem Ursprung, Preflight mit `Access-Control-Allow-Private-Network: true`. Ereignis `{"uid":"…","at":"RFC3339"}`.
- PDH: Kartensitzung endet nach Abschluss, nach 2 min ohne Eingabe, bei einer anderen Karte, über „Abmelden“ und bei Rückkehr zur Playlist. Sie ist keine PDH-Anmeldung.
- Texte deutsch, wörtlich: „Karte nicht registriert“, „Angemeldet: <Name>“, „Abmelden“.
- Keine neuen Go-Abhängigkeiten. Commits mit `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Tests hmi-kiosk: `scripts/dgo test -race ./...` und `scripts/dgo vet ./...`. PDH: `go test ./...` im Container wie bisher, DB-Tests mit `-tags integration` und `PDH_TEST_DSN`.

## Rulings (vorab)

- **`dec`** = vorzeichenlose Zahl der UID-Bytes **in umgekehrter Reihenfolge** (little-endian), ohne führende Nullen; das ist die Ausgabe der meisten Tastatur-Leser. Kostet bei falscher Annahme nur ein anderes Format in der Auswahl.
- **USB-Leser** liefern bereits Text: er wird getrimmt und großgeschrieben durchgereicht, `Format` gilt nur für PN532.
- **Kartensitzung in der Datenbank** (nicht im Speicher), damit sie bei mehreren PDH-Instanzen (HA) gilt.
- Der **Beteiligten-Dialog** (weitere Karten werden Beteiligte) gehört zu TP3. In TP2 ersetzt eine andere Karte die Sitzung.

## Review Focus

1. USB-Leser, dessen `EVIOCGRAB` scheitert → kein Lesen, kein Ereignis, Status zeigt den Fehler (sonst tippt der Leser in Felder). Test in Task 3.
2. SSE-Anfrage mit fremdem, fehlendem oder ohne eingetragenen PDH-Ursprung → 403 ohne CORS-Kopfzeilen. Test in Task 2.
3. Seite lädt kurz nach dem Kartenlesen neu → Ereignis kommt an, wenn ≤ 5 s alt; ältere nie. Test in Task 1.
4. Einstellung ändert sich bei laufendem Leser → alter Leser ist geschlossen, bevor der neue öffnet (exklusives Gerät, I²C-Bus). Test in Task 3.
5. Karte eines Systembenutzers, eines inaktiven Benutzers oder Kartenflut → „Karte nicht registriert“ bzw. 429. Test in Task 8.

---

## Teil A: PDH-Terminal (hmi-kiosk)

### Task 1: Ereignis-Hub, Entprellung, UID-Format

**Files:**
- Create: `internal/product/pdhterminal/card/hub.go`, `format.go`
- Test: `internal/product/pdhterminal/card/hub_test.go`, `format_test.go`

**Interfaces:**
- Produces:
  - `type Event struct { UID string \`json:"uid"\`; At time.Time \`json:"at"\` }`
  - `const DebounceWindow = 2 * time.Second; MaxAge = 5 * time.Second; MaxSubscribers = 8`
  - `func NewHub(now func() time.Time) *Hub`
  - `func (h *Hub) Publish(uid string)` – verwirft dieselbe UID innerhalb `DebounceWindow`; leere UID ignoriert.
  - `func (h *Hub) Subscribe() (<-chan Event, func(), error)` – `ErrTooManySubscribers` ab dem 9. Abo; liefert das letzte Ereignis sofort, wenn `now()-At <= MaxAge`; Kanalpuffer 4, nicht blockierend senden (voll → verwerfen).
  - `func (h *Hub) Fresh(e Event) bool` – `now()-e.At <= MaxAge`.
  - `func (h *Hub) Last() (Event, bool)` – letztes Ereignis beliebigen Alters (Anlernen).
  - `func FormatUID(raw []byte, format string) string` – `""`/`hex` → `DEADBEEF`, `hex-rev` → `EFBEADDE`, `dec` → `4022250974` (Ruling oben; für mehr als 8 Bytes `math/big`).

- [ ] **Step 1: Tests schreiben**
  - `TestFormatUID`: Tabelle mit `[]byte{0xDE,0xAD,0xBE,0xEF}` → die drei Werte oben; 7-Byte-UID `04 A1 B2 C3 D4 E5 F6` → `hex` `04A1B2C3D4E5F6`, `hex-rev` `F6E5D4C3B2A104`.
  - `TestHubDebounce`: Publish „A“ bei t0, „A“ bei t0+1,9 s → ein Ereignis; „A“ bei t0+2,1 s → zweites; „B“ bei t0+0,1 s → sofort.
  - `TestHubReplayFresh`: Publish bei t0, Subscribe bei t0+5 s → Ereignis; Subscribe bei t0+5,1 s → nichts.
  - `TestHubSubscriberLimit`: 8 Abos ok, 9. → `ErrTooManySubscribers`; nach `cancel()` eines Abos wieder möglich.
  - `TestHubSlowSubscriber`: ungelesenes Abo blockiert `Publish` nicht (6 verschiedene UIDs, Publish kehrt zurück).
- [ ] **Step 2:** `scripts/dgo test ./internal/product/pdhterminal/card/` → FAIL (nicht definiert).
- [ ] **Step 3:** Implementieren (Mutex, Map der Abos).
- [ ] **Step 4:** `scripts/dgo test -race ./internal/product/pdhterminal/card/` → PASS.
- [ ] **Step 5:** Commit `feat(pdh-terminal): Karten-Hub mit Entprellung und UID-Format`.

### Task 2: SSE-Ereignisstrom auf der Startseite

**Files:**
- Create: `internal/product/pdhterminal/card/events.go`, Test `events_test.go`
- Modify: `internal/product/pdhterminal/start.go:36-50` (Mux), `run.go:298` (Aufruf), `start_test.go`

**Interfaces:**
- Consumes: `Hub` aus Task 1; `config.PDHCfg.URL`.
- Produces: `func EventsHandler(h *Hub, origin func() string) http.Handler`; `StartHandler(dataDir string, cfg func() config.PDHCfg, client Client, cards http.Handler) http.Handler` (neuer letzter Parameter, `nil` = keine Kartenroute).

Verhalten von `EventsHandler`:
- `origin()` liefert den Ursprung der PDH-Adresse (`scheme://host[:port]`, kleingeschrieben, ohne Pfad) oder `""`. Anfrage-`Origin` muss exakt gleich sein, sonst **403** ohne CORS-Kopfzeilen (auch bei fehlendem `Origin` und bei `origin()==""`).
- `OPTIONS`: 204, `Access-Control-Allow-Origin: <origin>`, `Access-Control-Allow-Methods: GET`, `Access-Control-Max-Age: 600`, `Vary: Origin`; wenn `Access-Control-Request-Private-Network: true` → zusätzlich `Access-Control-Allow-Private-Network: true`.
- `GET`: `Content-Type: text/event-stream`, `Cache-Control: no-store`, `Access-Control-Allow-Origin`, `Vary: Origin`; Schreibfrist aufheben (`http.NewResponseController(w).SetWriteDeadline(time.Time{})`, weil `appcore.NewLocalServer` eine WriteTimeout setzen kann); je Ereignis `event: card\ndata: <JSON>\n\n` + Flush, nur wenn `h.Fresh(e)`; alle 25 s Kommentar `: ping\n\n`. Abo-Limit erreicht → 503.
- Andere Methoden → 405.
- In `StartHandler` die Route `/api/card/events` **vor** der GET/HEAD-Sperre einhängen, weiter hinter `web.SecureLocal` (Host-Prüfung bleibt).

- [ ] **Step 1: Tests**
  - `TestEventsOriginRejected`: Tabelle (kein Origin, `http://evil.example`, `https://pdh.example:8443` bei eingetragenem `https://pdh.example`, `origin()==""`) → 403, Kopfzeile `Access-Control-Allow-Origin` leer.
  - `TestEventsPreflight`: OPTIONS mit Origin `https://pdh.example` und `Access-Control-Request-Private-Network: true` → 204 und die vier Kopfzeilen.
  - `TestEventsStream`: `httptest.NewServer`, GET mit richtigem Origin, danach `hub.Publish("DEADBEEF")` → Body enthält `event: card` und `"uid":"DEADBEEF"`.
  - `TestEventsDropsStale`: Ereignis mit Uhr t0, Abo bei t0+6 s → in 300 ms kein `event: card`.
  - `TestStartCardRoute` (start_test.go): `StartHandler(…, cards)` leitet `/api/card/events` an `cards` weiter; Host `evil:8080` → 403 (SecureLocal).
- [ ] **Step 2:** Tests laufen lassen → FAIL.
- [ ] **Step 3:** Implementieren; `run.go` übergibt vorerst `card.EventsHandler(hub, …)` mit einem in `newService` erzeugten Hub (Task 3 hängt den Leser an). Ursprung über `config.PDHOrigins` bzw. `web.WebOrigin` ermitteln, nicht neu parsen.
- [ ] **Step 4:** `scripts/dgo test -race ./internal/product/pdhterminal/...` → PASS.
- [ ] **Step 5:** Commit `feat(pdh-terminal): Kartenereignisse per SSE für PDH`.

### Task 3: Leser (PN532/I²C, USB) und Kartendienst

**Files:**
- Create: `card/service.go`, `card/pn532.go` (Protokoll, plattformneutral), `card/i2c_linux.go`, `card/usb.go` (Tastencodes, plattformneutral), `card/usb_linux.go`, `card/reader_other.go`
- Test: `card/service_test.go`, `card/pn532_test.go`, `card/usb_test.go`

**Interfaces:**
- Consumes: `Hub`, `FormatUID`.
- Produces:
  - `type Settings struct { Type, I2CBus string; I2CAddr int; USBDevice, Format string }` (Paket `card` importiert `config` nicht).
  - `type Status struct { Active bool; Detail string }`
  - `type InputDevice struct { Path, Name, ID string }` (`ID` = `vvvv:pppp`, hex klein)
  - `func NewService(hub *Hub, logf func(string, ...any)) *Service`
  - `func (s *Service) Apply(st Settings)` – stoppt den laufenden Leser und **wartet auf dessen Ende**, dann startet den neuen; gleiche Settings → nichts tun.
  - `func (s *Service) Status() Status`, `func (s *Service) Hub() *Hub`, `func (s *Service) Probe(ctx context.Context) (string, error)`, `func (s *Service) Close()`
  - `func ListInputDevices(dir string) []InputDevice` (Linux; sonst nil)
  - intern: `type reader interface { Run(ctx context.Context, emit func(uid string)) error; Probe(ctx context.Context) (string, error) }` und ein Feld `open func(Settings) (reader, error)`, das Tests ersetzen.
- Fehler eines Lesers: Status `Active=false, Detail=<Fehler>`, Neustart nach 5 s, bis `Apply`/`Close`.

PN532-Protokoll (`pn532.go`), festgelegt:
- Befehlsrahmen `00 00 FF LEN LCS D4 CMD DATA… DCS 00`; `LEN = 2+len(DATA)`, `LCS = (0x100-LEN)&0xFF`, `DCS = (0x100 - (0xD4+CMD+ΣDATA))&0xFF`.
- I²C-Lesen beginnt mit Statusbyte; bereit, wenn `status&0x01 == 1`. Warten auf bereit höchstens 100 ms (Abfrage alle 5 ms). Nach jedem Befehl ACK `00 00 FF 00 FF 00` lesen, dann Antwort.
- Antwort: `00 00 FF LEN LCS D5 CMD+1 DATA… DCS 00`; LCS, DCS, `D5` und `CMD+1` prüfen.
- Start: `SAMConfiguration` `0x14 [01 14 01]`, `RFConfiguration` `0x32 [05 FF 01 02]` (kehrt ohne Karte schnell zurück).
- Abfrage alle 250 ms: `InListPassiveTarget` `0x4A [01 00]`; Antwortdaten `NbTg Tg SENS_RES(2) SEL_RES NFCIDLen NFCID…`; `NbTg==0` → keine Karte.
- `Probe`: `GetFirmwareVersion` `0x02` → `"PN532 v<Ver>.<Rev>"`.
- Funktionen: `encodeFrame(cmd byte, data []byte) []byte`, `decodeFrame(buf []byte, cmd byte) ([]byte, error)` (buf mit führendem Statusbyte), Transport über `type i2cConn interface { Read([]byte) (int, error); Write([]byte) (int, error); Close() error }`. Linux: `/dev/i2c-N` öffnen, `ioctl(I2C_SLAVE=0x0703, addr)` wie das ioctl-Muster in `internal/platform/corner_linux.go:29-37`.

USB (`usb.go`, `usb_linux.go`):
- Gerätewahl: `USBDevice` ist `vvvv:pppp` (exakt) oder Teil des Namens (ohne Groß/klein). `EVIOCGNAME(256)=0x81004506`, `EVIOCGID=0x80084502`, `EVIOCGRAB=0x40044590` mit Argument 1.
- **Erst greifen, dann lesen:** scheitert `EVIOCGRAB`, schließen und Fehler `"Leser kann nicht exklusiv übernommen werden: …"`.
- Ereignisse über `platform`s Dekodierung (`readInputEvents` in `internal/platform/corner.go:50`; falls nicht exportiert, als `platform.ReadInputEvents` exportieren). Nur `EV_KEY` mit Wert 1. Zeichen: `KEY_1..KEY_9`=2..10 → `1..9`, `KEY_0`=11; Ziffernblock 71–73→`7 8 9`, 75–77→`4 5 6`, 79–81→`1 2 3`, 82→`0`; Buchstaben nach Linux-Codes (Q16 W17 E18 R19 T20 Y21 U22 I23 O24 P25 A30 S31 D32 F33 G34 H35 J36 K37 L38 Z44 X45 C46 V47 B48 N49 M50). `KEY_ENTER` 28 / `KEY_KPENTER` 96 beendet. Puffer höchstens 32 Zeichen (Überlauf → bis Enter verwerfen); Pause > 1 s zwischen Zeichen → Puffer leeren. `func (b *keyBuffer) Key(code uint16, at time.Time) (uid string, done bool)`.

- [ ] **Step 1: Tests**
  - `TestEncodeFrame`: `encodeFrame(0x02,nil)` = `00 00 FF 02 FE D4 02 2A 00`; `encodeFrame(0x4A,[]byte{1,0})` = `00 00 FF 04 FC D4 4A 01 00 E1 00`.
  - `TestDecodeFrame`: Puffer `01 00 00 FF 0C F4 D5 4B 01 01 00 04 08 04 DE AD BE EF 96 00` mit cmd `0x4A` → Daten ab `01 01 …`; falsche DCS, falsches `CMD+1`, Status 0 → Fehler.
  - `TestPN532Poll`: Fake-`i2cConn` mit Skript (ACK + Antwort oben) → `emit("DEADBEEF")`; Antwort mit `NbTg=0` → kein emit.
  - `TestKeyBuffer`: `0 4 A 1` + Enter → `"04A1"`; 33 Zeichen + Enter → kein Ergebnis; Pause 1,5 s mitten drin → nur Zeichen nach der Pause.
  - `TestServiceApplyStopsOld`: Fake-`open` protokolliert open/close; `Apply(A)`, `Apply(B)` → Reihenfolge `open A, close A, open B`.
  - `TestServiceGrabFails` (Review Focus 1): Fake-Leser, dessen `Run` sofort den Grab-Fehler liefert → kein Hub-Ereignis, `Status().Active==false`, Detail enthält „exklusiv“.
  - `TestServiceOff`: `Apply(Settings{})` → kein open, Status Detail „aus“.
- [ ] **Step 2:** → FAIL.
- [ ] **Step 3:** Implementieren; `reader_other.go` liefert unter Nicht-Linux den Fehler `"Kartenleser nur unter Linux"`.
- [ ] **Step 4:** `scripts/dgo test -race ./internal/product/pdhterminal/... ./internal/platform/...` und `GOOS=linux GOARCH=arm64 scripts/dgo vet ./...` → PASS.
- [ ] **Step 5:** Commit `feat(pdh-terminal): Kartenleser PN532 über I²C und USB`.

### Task 4: Konfiguration, Admin-Seite „Kartenleser“, Verdrahtung

**Files:**
- Create: `internal/config/card.go`, `card_test.go`
- Modify: `internal/config/config.go:129-131` (Feld), `validate.go:56`, `internal/product/pdhterminal/admin.go:20,40,47-64`, `admin_card.html`, `run.go:184-191` (apply), `run.go:298`, `run.go:560`, `admin` Tests

**Interfaces:**
- Produces:
  - `type CardCfg struct { Type string \`json:"type,omitempty"\`; I2CBus string \`json:"i2cBus,omitempty"\`; I2CAddr int \`json:"i2cAddr,omitempty"\`; USBDevice string \`json:"usbDevice,omitempty"\`; Format string \`json:"format,omitempty"\` }`, in `Config` als `Card CardCfg \`json:"card,omitzero"\`` direkt nach `PDH`.
  - `func ValidateCard(c CardCfg) error`: Type ∈ {`""`,`pn532-i2c`,`usb`}; I2CBus leer oder `^/dev/i2c-[0-9]{1,2}$`; I2CAddr 0 oder 0x08–0x77; bei `usb` USBDevice Pflicht, ≤ 64 Zeichen, keine Steuerzeichen; Format ∈ {`""`,`hex`,`hex-rev`,`dec`}. In `Validate` nach `ValidatePDH`.
  - `func (c CardCfg) Settings() (bus string, addr int)` mit Standardwerten `/dev/i2c-1`, `0x24` (pdhterminal baut daraus `card.Settings`).
  - `Admin(dataDir string, client Client, cards *card.Service) *web.AdminProduct`
- Admin-Seite `GET /admin/card`: Formular Typ (Auswahl „Aus“, „PN532 (I²C)“, „USB-Leser“), Bus, Adresse (Eingabe hex `0x24`), USB-Gerät (Auswahlliste aus `card.ListInputDevices("/dev/input")` plus freies Feld), Format; darunter Zustand (`Status`) und **„Zuletzt gelesen: <UID> (vor N s)“** oder „Noch keine Karte gelesen“. `POST /admin/card/settings` → `f.Update(func(c *config.Config) error { c.Card = in; return nil })`, Validierungsfehler → 400 mit Meldung.
- `apply(c)` ruft zusätzlich `s.cards.Apply(...)`. Der Dienst startet in `serve()` unabhängig von `ChromiumEnabled` und wird beim Beenden mit `Close()` gestoppt.

- [ ] **Step 1: Tests**
  - `TestValidateCard`: Tabelle gültig/ungültig für jede Regel oben (u. a. `/dev/i2c-1;rm`, Adresse `0x78`, `usb` ohne Gerät, Format `bin`).
  - `TestConfigWithoutCardUnchanged`: alte Konfig ohne `card` laden und speichern → Bytes gleich (wie der vorhandene PDH-omitzero-Test).
  - `TestAdminCardSave` (admin-Test der pdhterminal): POST mit `pn532-i2c`, `0x24` → gespeicherte Konfig hat `I2CAddr==36`; ungültig → 400, Konfig unverändert.
  - `TestAdminCardShowsLast`: Hub-Ereignis „DEADBEEF“ → Seite enthält `DEADBEEF`.
  - `TestRunSmoke` bleibt grün.
- [ ] **Step 2:** → FAIL. **Step 3:** Implementieren. **Step 4:** `scripts/dgo test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(pdh-terminal): Admin-Seite Kartenleser`.

### Task 5: Selbstprüfung „Kartenleser“

**Files:**
- Modify: `internal/selftest/selftest.go:36,93-121,166`, `internal/product/pdhterminal/run.go:576-590`
- Test: `internal/selftest/selftest_test.go`

**Interfaces:**
- Produces: `const StepCard = "kartenleser"` (in `StepNames`), `Deps.CardReader func(ctx context.Context) (string, error)`, `func CardStep() Step`.
- Verhalten: `CardReader == nil` → OK, Detail „kein Kartenleser eingestellt“; Fehler → nicht OK, Detail = Fehlertext; sonst OK mit Detail (z. B. „PN532 v1.6“, „USB: <Name>“). `adminSelftest` nutzt `append(selftest.CoreSteps(), selftest.CardStep())` und setzt `CardReader` nur, wenn Typ ≠ aus. HMI-Kiosk-Schritte unverändert.

- [ ] **Step 1:** `TestCardStep` mit den drei Fällen; `TestHMIStepsUnchanged` (Anzahl/Namen von `HMISteps()` wie vorher).
- [ ] **Step 2–4:** FAIL → implementieren → `scripts/dgo test -race ./internal/selftest/ ./internal/product/...` PASS.
- [ ] **Step 5:** Commit `feat(pdh-terminal): Selbstprüfung Kartenleser`.

### Task 6: Abbild, Installation, Doku

**Files:**
- Modify: `packaging/image/stage-hmi/01-hmi/00-run.sh:18-22,90-96`, `packaging/image/verify-image.sh:173-177`, `packaging/install.sh:34,192-211`, `docs/betrieb.md` (Abschnitt 4), `README.md` („PDH-Terminal einrichten“), `docs/entscheidungen.md`
- Create: `packaging/products/pdh-terminal/rootfs/etc/modules-load.d/pdh-terminal-i2c.conf` (Inhalt `i2c-dev`)
- Test: `packaging/test-products.sh` bzw. vorhandene Skripttests

**Festlegungen:**
- `00-run.sh`: Gruppe `i2c` anlegen, falls fehlend (`getent group i2c || groupadd -r i2c`), `hmikiosk` in `i2c` (useradd `-G` und usermod). Nur wenn `HMI_PRODUCT_ID = pdh-terminal`: `dtparam=i2c_arm=on` im letzten `[all]`-Block nach dem `pciex1`-Muster.
- `verify-image.sh`: für `pdh-terminal` prüfen: `dtparam=i2c_arm=on` im letzten `[all]`, `hmikiosk` in `i2c`, Datei `modules-load.d/pdh-terminal-i2c.conf`. Für `hmi-kiosk` kein `i2c_arm` (unverändert).
- `install.sh`: `i2c` zu den Gruppen, wenn `getent group i2c` sie kennt.
- Doku: `betrieb.md` 4. „Kartenleser“ (Typen, Anschluss PN532 an Pin 3/5, 3,3 V, Modus I²C per Schalter am Modul; Anlernen über „Zuletzt gelesen“, UID beim PDH-Benutzer eintragen; Format passend zu den PDH-Einträgen; Zustand und Selbstprüfung), Platzhalter-Satz in „Bekannte Einschränkungen“ entfernen. README Schritt 7 „Kartenleser einstellen“. `entscheidungen.md`: SSE statt WebSocket, Ruling `dec`.

- [ ] **Step 1:** Skripttest erweitern (vorhandenes Muster in `packaging/test-products.sh`/`test-firstboot.sh`): `verify-image.sh` gegen ein Testverzeichnis ohne `i2c_arm` bei pdh-terminal → Fehler; mit → ok.
- [ ] **Step 2–4:** FAIL → umsetzen → `bash packaging/test-products.sh` und `bash -n` aller geänderten Skripte PASS; `scripts/dgo test ./...` PASS.
- [ ] **Step 5:** Commit `feat(pdh-terminal): I²C im Abbild, Doku Kartenleser`.

## Teil B: PDH (pdh-server)

### Task 7: Tabelle und Speicher der Kartensitzung

**Files:**
- Create: `migrations/158_terminal_card_sessions.up.sql`, `internal/web/terminal_card_store.go`
- Test: `internal/web/terminal_card_integration_test.go` (`//go:build integration`, Helfer `newATEnv` aus `anlagenterminal_authz_integration_test.go:35`)

**Interfaces:**
- Produces:
  - Tabelle `terminal_card_sessions(terminal_user_id PK → users(id) ON DELETE CASCADE, person_id → users(id) ON DELETE CASCADE NOT NULL, started_at, last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now())`; Spaltentyp wie `users.id`. Kopfkommentar `-- 158_terminal_card_sessions.up.sql`.
  - `const cardSessionIdle = 2 * time.Minute`
  - `func (h *Handler) cardSessionStart(ctx context.Context, terminalID, personID string) error` (Upsert, setzt beide Zeiten)
  - `func (h *Handler) cardSessionPerson(ctx context.Context, terminalID string) (*users.User, error)` – nil, wenn keine Sitzung, `last_seen_at < now()-cardSessionIdle` (Zeile dann löschen) oder Person nicht mehr aktiv.
  - `func (h *Handler) cardSessionTouch(ctx context.Context, terminalID string) (bool, error)` – nur gültige Sitzung verlängern.
  - `func (h *Handler) cardSessionEnd(ctx context.Context, terminalID string) error`
  - `func (h *Handler) cardPersonCan(ctx context.Context, person *users.User, perm, refType, id string) bool` – `h.rbac == nil` → false; sonst `h.rbac.HasPermissionInDepartment(person.ID, string(person.Role), perm, h.recordDepartments(ctx, refType, id))` (`rbac/scope.go:76`, `perm_gate.go:32`). Für TP3.

- [ ] **Step 1: Tests**: Start→Person liefert Person; Zeit `last_seen_at` per SQL um 121 s zurücksetzen → nil und Zeile weg; Person `active=false` → nil; Touch verlängert; End löscht; zweiter Start mit anderer Person ersetzt; `cardPersonCan` mit Recht `faults.view` nur für Abteilung X → true für Anlage in X, false für Anlage in Y, false für den Terminal-Benutzer selbst.
- [ ] **Step 2–4:** FAIL → umsetzen → `PDH_TEST_DSN=… go test -tags integration -run TerminalCard ./internal/web/` PASS.
- [ ] **Step 5:** Commit `feat(terminal): Kartensitzung speichern`.

### Task 8: Handler `/terminal/card`

**Files:**
- Create: `internal/web/terminal_card.go`, Test `terminal_card_test.go` (+ Integrationsfälle in `terminal_card_integration_test.go`)
- Modify: `internal/web/terminal_page.go:75-82` (Routen; Schoner beendet Sitzung)

**Interfaces:**
- Consumes: Task 7; `terminalGate`/`terminalUserID` (`terminal_location.go:45`), `h.users.GetByRFID` (`users/repository.go:136`), `authLog` und `maskUID` (`request_log.go:194`), Limiter `terminalFailLimit`/`limiterKey` (`terminal_devices.go:247-258`).
- Produces (alle nur für Terminal-Benutzer, sonst 403 wie `terminalGate`):
  - `POST /terminal/card` Body `{"uid":"…"}` (JSON, ≤ 1 KiB, UID 1–100 Zeichen nach `TrimSpace`, sonst 400) →
    - bekannte, aktive Person, **kein** Systembenutzer: Sitzung starten, `200 {"ok":true,"name":"<Anzeigename>"}`, `authLog(r, true, "rfid-terminal", "Karte "+maskUID(uid), id, name, "")`.
    - sonst: `200 {"ok":false,"message":"Karte nicht registriert"}`, `authLog(..., false, ..., "karte unbekannt oder nicht zulässig")`, Fehlversuch zählen mit Schlüssel `"card:"+terminalID`; ab 10 Fehlversuchen in 1 min → `429 {"ok":false,"message":"Zu viele unbekannte Karten. Bitte kurz warten."}`.
  - `POST /terminal/card/ping` → `200 {"active":true,"name":"…"}` nach `cardSessionTouch`, sonst `{"active":false}`.
  - `POST /terminal/card/abmelden` → Sitzung beenden, `204`.
  - `GET /terminal` (Schoner) ruft `cardSessionEnd` für den Terminal-Benutzer.

- [ ] **Step 1: Tests** (Unit mit Fake-Speicher nur, wenn der Handler ohne DB testbar ist; sonst Integration):
  - bekannte Karte → `ok:true`, Name; zweiter Aufruf mit anderer Karte ersetzt die Person.
  - unbekannte UID, inaktiver Benutzer, Systembenutzer → je `ok:false`, Text exakt „Karte nicht registriert“ (Review Focus 5).
  - 11. unbekannte Karte in 1 min → 429.
  - Nicht-Terminal-Benutzer → 403; leere UID / 101 Zeichen / kein JSON → 400.
  - `GET /terminal` beendet eine laufende Sitzung; `ping` danach `active:false`.
- [ ] **Step 2–4:** FAIL → umsetzen → `go test ./internal/web/ -run TerminalCard` und Integration PASS.
- [ ] **Step 5:** Commit `feat(terminal): Karte am Anlagenterminal anmelden`.

### Task 9: Seite und Skript, Hilfe, Changelog

**Files:**
- Modify: `web/templates/terminal.gohtml`, `web/static/terminal.js`, `internal/web/terminal_page.go` (Feld `CardEvents bool` in `terminalPage`), `web/templates/help.gohtml` (Abschnitt Anlagenterminal), `CHANGELOG.md`, `VERSION` falls üblich
- Test: `internal/web/terminal_page_test.go`

**Festlegungen:**
- `CardEvents` ist true genau dann, wenn die Sitzung ein Geräte-Token (Claim `device`) trägt; die vorhandene Auswertung in `handler.go` (sessionToken/deviceCache) wiederverwenden. Template setzt `data-card-events="1"` am `body` nur dann.
- `terminal.js` (nur bei `data-card-events="1"`): `new EventSource("http://127.0.0.1:8080/api/card/events")`, Ereignis `card` → `fetch("/terminal/card", {method:"POST", credentials:"same-origin", headers:{"Content-Type":"application/json"}, body:JSON.stringify({uid})})`. Antwort `ok:true` → Leiste „Angemeldet: <Name>“ mit Knopf „Abmelden“ (≥ 64 px); `ok:false` → Hinweis mit `message` für 4 s. Beim Laden `POST /terminal/card/ping`, um eine laufende Sitzung anzuzeigen. Bei `pointerdown`, solange angemeldet, höchstens alle 20 s `ping`; `active:false` → Leiste ausblenden. „Abmelden“ → `POST /terminal/card/abmelden`, Leiste weg. Fehler des EventSource still (er verbindet selbst neu). Kein `innerHTML` mit Namen (`textContent`).
- Im Schoner-Modus wird nichts abonniert (Karte dort ohne Wirkung; Berührung führt zur Info).
- Hilfe: Satz zum Kartenleser und Kartensitzung (2 min, Abmelden). Changelog-Eintrag unter neuer Version.

- [ ] **Step 1: Tests**: Seite im Modus `info` mit Geräte-Token → `data-card-events="1"`; ohne (normaler Systembenutzer im Browser) → fehlt; Schoner → fehlt. `TerminalScript` liefert ein Skript, das `127.0.0.1:8080/api/card/events` und `/terminal/card/ping` enthält.
- [ ] **Step 2–4:** FAIL → umsetzen → `go test ./internal/web/` PASS, `go vet ./...` sauber.
- [ ] **Step 5:** Commit `feat(terminal): Kartenleser auf der Terminalseite`.

## Abschluss

Ganzer Zweig je Repo: alle Tests, `test/pdh-e2e/run.sh` mit `PDH_DIR=C:\workplace\pdh-server-karten` (unverändert grün), dann Schluss-Review. Der echte Lesertest folgt am Gerät (PN532 bzw. USB-Leser).
