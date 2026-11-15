# L1: PN532 über SPI/UART und OLED-Statusdisplay – Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Das PDH-Terminal liest Karten mit dem PN532 auch über SPI und UART und zeigt auf einem SH1106-OLED
das Statusbild aus raspi-Oled; Abbild, Admin-Seite und Selbstprüfung kennen beides.

**Architecture:** Das PN532-Protokoll in `card/pn532.go` bekommt eine Transportschnittstelle mit drei
Umsetzungen (I²C, SPI, UART). Ein neues Paket `pdhterminal/oled` zeichnet das Bild in einen 1-Bit-Puffer und
schickt ihn per I²C an den SH1106; ein eigener Dienst läuft neben dem Kartendienst. Die Konfiguration bekommt
nur neue, optionale Felder; unbekannte Werte gelten beim Laden als „aus“ mit Warnung, die Admin-Seite prüft
streng.

**Tech Stack:** Go (nur Standardbibliothek, `syscall` für ioctl/termios), POSIX-Shell für das Abbild,
Tests über `scripts/dgo test -race ./...` und `bash packaging/test-products.sh`.

**Spec:** `docs/superpowers/specs/2026-11-15-l1-pn532-spi-uart-oled-design.md`

**Zweig:** `feat/l1-leser-oled`, von `feat/pdh-terminal` (Stand `2e07346`), im Worktree
`C:\workplace\hmi-kiosk-pdhterm` (dort `git switch -c feat/l1-leser-oled`).

## Global Constraints

- Rückwärtskompatibel: nur neue Felder mit `omitempty`/`omitzero`; `card.type` bleibt für jeden PN532 `"pn532-i2c"`; eine alte `config.json` lädt und speichert Byte für Byte gleich.
- Neue Felder: `card.pn532Bus` (`""`|`"spi"`|`"uart"`), `card.spiDev` (Standard `/dev/spidev0.0`), `card.uartDev` (Standard `/dev/ttyAMA0`); Abschnitt `statusDisplay` (`omitzero`) mit `type` (`""`|`"sh1106"`), `i2cBus` (Standard `/dev/i2c-1`), `i2cAddr` (Standard `0x3C`).
- Den Abschnitt `display` (HMI-Bildschirm) nicht anfassen.
- Laden (`config.Validate`): unbekannter `card.type`, `card.pn532Bus`, `statusDisplay.type` ist kein Fehler; Admin-Seite speichert nur bekannte Werte. Ungültige Gerätepfade sind immer ein Fehler.
- Gerätemuster: `spiDev` `^/dev/spidev[0-9]\.[0-9]$`, `uartDev` `^/dev/tty(AMA|S|USB|ACM)[0-9]{1,2}$`, `statusDisplay.i2cBus` wie `card.i2cBus` (`^/dev/i2c-[0-9]{1,2}$`), Adressen 0x08–0x77.
- Zeiten unverändert: Abfrage 250 ms, „bereit“ 100 ms, Neustart nach Fehler 5 s; Display alle 1 s, Neustart 5 s.
- Nur Standardbibliothek; keine neuen Module in `go.mod`.
- Texte deutsch, im Stil der vorhandenen Meldungen; Code-Kommentare deutsch.
- Abbild-Änderungen nur für `HMI_PRODUCT_ID = pdh-terminal`; das HMI-Abbild bleibt unverändert.
- Kein `git stash` ohne Namen; `.secrets/` nie committen; nicht pushen.
- Große Schrift des Displays (Spec „ca. 24 px“): die kleine 5×7-Schrift mit Faktor 3 (21 px hoch) – keine zweite Schrifttabelle.

## Review Focus

1. Alte `config.json` (nur `card.type: "pn532-i2c"`, kein `statusDisplay`) → lädt, wird beim Speichern Byte für Byte gleich geschrieben (Task 1).
2. Konfiguration aus einer künftigen Version (`card.type: "pn7150"` oder `pn532Bus: "i2c3"`) → Terminal startet, Leser „aus“ mit Warnung, Wert bleibt in der Datei (Tasks 1, 5).
3. UART liefert Antworten in Bruchstücken oder mit führenden Nullbytes → Rahmen wird trotzdem erkannt (Task 4).
4. SPI/UART-Gerät fehlt, weil `config.txt` alt ist → klare Meldung mit `dtparam=…`, kein Absturz, Neustartschleife alle 5 s (Task 5).
5. Display am Bus abgezogen während des Betriebs → Display-Dienst meldet Fehler, startet nach 5 s neu, Kartenleser läuft weiter (Task 7).

---

### Task 1: Konfiguration – neue Felder, Prüfung beim Laden und beim Speichern

**Files:**
- Modify: `internal/config/card.go`
- Create: `internal/config/statusdisplay.go`
- Modify: `internal/config/config.go` (Feld `StatusDisplay` in `Config`), `internal/config/validate.go` (`Validate` ruft die lockeren Prüfungen)
- Test: `internal/config/card_test.go`, `internal/config/statusdisplay_test.go`

**Interfaces:**
- Produces:
  - `CardCfg` neu: `PN532Bus string \`json:"pn532Bus,omitempty"\``, `SPIDev string \`json:"spiDev,omitempty"\``, `UARTDev string \`json:"uartDev,omitempty"\``
  - Konstanten `CardBusSPI = "spi"`, `CardBusUART = "uart"`; Kommentar an `CardTypePN532I2C`: gespeicherter Wert für jeden PN532, Bus steht in `PN532Bus` (Rückwärtskompatibilität, nicht umbenennen)
  - `func ValidateCard(c CardCfg) error` – locker: unbekannter `Type`/`PN532Bus` erlaubt, alle Formatprüfungen bleiben
  - `func ValidateCardStrict(c CardCfg) error` – `ValidateCard` + nur bekannte `Type` und `PN532Bus`
  - `func (c CardCfg) Known() (ok bool, reason string)` – `reason` ist `Lesertyp „X“ wird von dieser Version nicht unterstützt` bzw. `PN532-Anschluss „X“ wird von dieser Version nicht unterstützt`
  - `func (c CardCfg) SPIDevice() string` (Standard `/dev/spidev0.0`), `func (c CardCfg) UARTDevice() string` (Standard `/dev/ttyAMA0`)
  - `type StatusDisplayCfg struct { Type string \`json:"type,omitempty"\`; I2CBus string \`json:"i2cBus,omitempty"\`; I2CAddr int \`json:"i2cAddr,omitempty"\` }`, Konstante `StatusDisplaySH1106 = "sh1106"`
  - `Config.StatusDisplay StatusDisplayCfg \`json:"statusDisplay,omitzero"\``
  - `func ValidateStatusDisplay(c StatusDisplayCfg) error`, `func ValidateStatusDisplayStrict(c StatusDisplayCfg) error`, `func (c StatusDisplayCfg) Known() (bool, string)` (Grund `Displaytyp „X“ wird von dieser Version nicht unterstützt`), `func (c StatusDisplayCfg) Settings() (bus string, addr int)` (Standard `/dev/i2c-1`, `0x3C`)

- [ ] **Step 1: Tests schreiben**

In `card_test.go` die vorhandenen Fälle „unbekannter Typ ist Fehler“ nach `ValidateCardStrict` verschieben und ergänzen:
`TestValidateCardLenientUnknownType` (`{Type:"pn7150"}` → `ValidateCard` nil, `ValidateCardStrict` Fehler, `Known()` false mit Grund `Lesertyp „pn7150“ wird von dieser Version nicht unterstützt`),
`TestValidateCardBus` (`""`/`spi`/`uart` streng gültig; `"i2c3"` locker gültig, streng Fehler),
`TestValidateCardDevices` (`/dev/spidev0.1`, `/dev/ttyAMA0`, `/dev/ttyUSB12` gültig; `/dev/spidev0`, `/dev/sda`, `/dev/ttyAMA0;x` Fehler in beiden Prüfungen),
`TestCardDefaults` (`SPIDevice()`=`/dev/spidev0.0`, `UARTDevice()`=`/dev/ttyAMA0`, gesetzte Werte gewinnen),
`TestOldCardConfigRoundTrip`: `config.Default()` mit `Card{Type:"pn532-i2c", I2CBus:"/dev/i2c-1"}` per `json.MarshalIndent(c,"","  ")` schreiben, wieder laden, erneut schreiben → beide Ausgaben identisch und ohne die Schlüssel `statusDisplay`, `pn532Bus`, `spiDev`, `uartDev`.
In `statusdisplay_test.go`: gleiches Muster (`sh1106` gültig, `"ssd1306"` locker gültig/streng Fehler, Bus/Adresse geprüft, `Settings()` liefert `/dev/i2c-1`, `0x3C`), und `TestStoreLoadsFutureValues`: `Store.Save` + `Store.Load` mit `Card{Type:"pn7150"}` und `StatusDisplay{Type:"ssd1306"}` → lädt ohne Rückfall, Werte erhalten.

- [ ] **Step 2: Tests laufen lassen, sie schlagen fehl**

Run: `scripts/dgo test ./internal/config/`
Expected: FAIL (undefinierte Felder/Funktionen)

- [ ] **Step 3: Implementieren** wie in Interfaces; `Validate` ruft `ValidateCard(c.Card)` und `ValidateStatusDisplay(c.StatusDisplay)`.

- [ ] **Step 4: Tests grün**

Run: `scripts/dgo test -race ./internal/config/ && scripts/dgo vet ./internal/config/`
Expected: PASS

- [ ] **Step 5: Commit** `feat(config): PN532-Bus, Statusdisplay, unbekannte Werte beim Laden zulassen`

---

### Task 2: PN532 – Transportschnittstelle (reiner Umbau, I²C unverändert)

**Files:**
- Modify: `internal/product/pdhterminal/card/pn532.go`, `internal/product/pdhterminal/card/i2c_linux.go`
- Create: `internal/product/pdhterminal/card/transport_i2c.go`
- Test: `internal/product/pdhterminal/card/pn532_test.go`

**Interfaces:**
- Produces:
  ```go
  // pn532Transport bewegt Rahmen zum und vom PN532; das Protokoll kennt den Bus nicht.
  type pn532Transport interface {
      Name() string                                    // "I²C", "SPI", "UART"
      WriteFrame(ctx context.Context, frame []byte) error
      // ReadFrame wartet höchstens pn532ReadyTimeout auf „bereit“ und liefert bis zu max Bytes ab dem
      // Rahmenanfang (00 00 FF …), ohne Statusbyte.
      ReadFrame(ctx context.Context, max int) ([]byte, error)
      Close() error
  }
  func newI2CTransport(conn i2cConn) pn532Transport
  func newPN532(t pn532Transport, format string) *pn532
  ```
  - `decodeFrame(frame []byte, cmd byte)` erwartet den Rahmen **ohne** Statusbyte (Mindestlänge 7).
  - `exchange`: `WriteFrame`, `ReadFrame(ctx, 6)` muss mit `pn532Ack` beginnen, dann `ReadFrame(ctx, pn532RespBuf)` → `decodeFrame`.
  - `Probe` liefert `PN532 über <Name>, Firmware <ver>.<rev>` (z. B. `PN532 über I²C, Firmware 1.6`).
  - `openI2C` liefert weiter `i2cConn`; `openReader` (Task 5) wickelt ihn in `newI2CTransport`.

- [ ] **Step 1: Tests anpassen** – `fakeConn` bleibt das I²C-Gerät; alle PN532-Tests bauen `newPN532(newI2CTransport(conn), fmt)`. `TestDecodeFrame` mit Rahmen ohne Statusbyte. `TestPN532Probe` erwartet `PN532 über I²C, Firmware 1.6`. Neuer `TestI2CTransportStripsStatus`: Lesungen `{0x01, 00 00 FF …}` → `ReadFrame` liefert ab `00 00 FF`; `{0x00}` bis Zeitgrenze → Fehler `PN532: keine Antwort`.

- [ ] **Step 2: Laufen lassen** – `scripts/dgo test ./internal/product/pdhterminal/card/` → FAIL

- [ ] **Step 3: Umbauen** – `waitReady` wandert in den I²C-Transport; das Lesen mit Statusbyte (1+max Bytes) und das Abschneiden ebenfalls.

- [ ] **Step 4: Alle Kartentests grün** – `scripts/dgo test -race ./internal/product/pdhterminal/...` → PASS

- [ ] **Step 5: Commit** `refactor(card): PN532-Transportschnittstelle, I²C als erste Umsetzung`

---

### Task 3: PN532 über SPI

**Files:**
- Create: `internal/product/pdhterminal/card/transport_spi.go` (Protokoll, plattformneutral), `internal/product/pdhterminal/card/spi_linux.go` (spidev), Ergänzung in `reader_other.go` (Stub)
- Test: `internal/product/pdhterminal/card/transport_spi_test.go`

**Interfaces:**
- Consumes: `pn532Transport` (Task 2)
- Produces:
  ```go
  type spiBus interface { Transfer(tx []byte) ([]byte, error); Close() error } // vollduplex, rx gleich lang wie tx
  func newSPITransport(bus spiBus) pn532Transport
  func openSPI(dev string) (spiBus, error) // linux: Modus 0, 8 Bit, 1 MHz; sonst Fehler „nur unter Linux“
  ```
  - Senden: `Transfer(rev([0x01] + frame))`.
  - Bereit: `Transfer(rev([0x02, 0x00]))`, bereit wenn `rev(rx[1])&0x01 == 1`; alle 5 ms bis 100 ms.
  - Lesen: `Transfer(rev([0x03, 0, …]))` mit `max` Nullbytes, Ergebnis `rev(rx[1:])`.
  - `rev` dreht die Bitreihenfolge jedes Bytes über eine 256er-Tabelle (LSB zuerst).
  - `spi_linux.go`: `SPI_IOC_WR_MODE` (0), `SPI_IOC_WR_BITS_PER_WORD` (8), `SPI_IOC_WR_MAX_SPEED_HZ` (1000000), Übertragung per `SPI_IOC_MESSAGE(1)` mit `spi_ioc_transfer` (32 Byte, `tx_buf`/`rx_buf`/`len`/`speed_hz`/`bits_per_word`); Werte aus `linux/spi/spidev.h`.

- [ ] **Step 1: Tests** – `fakeSPI` zeichnet tx auf und liefert vorbereitete rx.
`TestBitReverseTable` (`rev(0x01)=0x80`, `rev(0xD4)=0x2B`, `rev(rev(b))=b` für alle b);
`TestSPIWriteFramePrefix` (erste tx = `rev(01 00 00 FF …)`);
`TestSPIReadyAndRead` (zwei Status-Antworten „nicht bereit“, dann bereit, dann Daten → `ReadFrame` liefert unumgedrehte Bytes ab `00 00 FF`);
`TestSPIReadyTimeout` (nie bereit → Fehler nach ≤ 150 ms);
`TestPN532OverSPIProbe` (komplette Probe über `fakeSPI` → `PN532 über SPI, Firmware 1.6`).

- [ ] **Step 2: Laufen lassen** → FAIL

- [ ] **Step 3: Implementieren**

- [ ] **Step 4: Grün** – `scripts/dgo test -race ./internal/product/pdhterminal/card/` und `GOOS=linux GOARCH=arm64 scripts/dgo vet ./internal/product/pdhterminal/card/` → PASS

- [ ] **Step 5: Commit** `feat(card): PN532 über SPI`

---

### Task 4: PN532 über UART

**Files:**
- Create: `internal/product/pdhterminal/card/transport_uart.go`, `internal/product/pdhterminal/card/uart_linux.go`, Stub in `reader_other.go`
- Test: `internal/product/pdhterminal/card/transport_uart_test.go`

**Interfaces:**
- Consumes: `pn532Transport` (Task 2)
- Produces:
  ```go
  // serialPort liefert bei Zeitablauf (n=0, err=nil) statt zu blockieren.
  type serialPort interface { Read([]byte) (int, error); Write([]byte) (int, error); Close() error }
  func newUARTTransport(p serialPort) pn532Transport
  func openUART(dev string) (serialPort, error) // linux: 115200 8N1 roh, VMIN=0, VTIME=1 (100 ms)
  ```
  - Erster `WriteFrame` schickt vorher einmal `55 55 00 00 00 00 00 00 00 00 00 00 00 00 00 00`.
  - `ReadFrame` liest bis zur Zeitgrenze 100 ms (ab dem ersten Aufruf), überspringt Bytes bis `00 00 FF`, und liefert den Rahmen, sobald er vollständig ist: ACK (`00 00 FF 00 FF 00`) = 6 Byte; sonst Länge aus `LEN` → `6 + LEN + 2` Byte. Mehr als `max` Bytes → Fehler `PN532: Rahmen zu lang`.
  - `uart_linux.go`: `TCGETS`/`TCSETS` über `ioctl`, `cfmakeraw`-Flags von Hand, `B115200`, `CS8`, `CLOCAL|CREAD`, `TCFLSH` beim Öffnen.

- [ ] **Step 1: Tests** – `fakeSerial` mit einer Folge von Lese-Bruchstücken.
`TestUARTWakeupOnce` (zwei `WriteFrame` → Aufweckfolge genau einmal vorn);
`TestUARTFrameFromFragments` (ACK in `00`,`00 FF`,`00 FF 00` → ACK; Antwort in drei Teilen → vollständiger Rahmen);
`TestUARTSkipsLeadingZeros` (`00 00 00 00 FF …` → Rahmen ab dem letzten `00 00 FF`);
`TestUARTTimeout` (nur `n=0`-Lesungen → Fehler `PN532: keine Antwort` nach ≤ 150 ms);
`TestPN532OverUARTPoll` (Poll mit Karte über `fakeSerial` → UID im Format hex).

- [ ] **Step 2: Laufen lassen** → FAIL

- [ ] **Step 3: Implementieren**

- [ ] **Step 4: Grün** – wie Task 3, Step 4

- [ ] **Step 5: Commit** `feat(card): PN532 über UART (HSU)`

---

### Task 5: Kartendienst – Bus wählen, unbekannte Werte, fehlende Geräte

**Files:**
- Modify: `internal/product/pdhterminal/card/service.go`, `internal/product/pdhterminal/admin_card.go` (`cardSettings`)
- Test: `internal/product/pdhterminal/card/service_test.go`, `internal/product/pdhterminal/admin_card_test.go`

**Interfaces:**
- Consumes: Tasks 1–4
- Produces:
  - `card.Settings` neu: `Bus string` (`""`, `BusSPI = "spi"`, `BusUART = "uart"`), `SPIDev`, `UARTDev string`, `Unsupported string` (Grund aus `config.CardCfg.Known()`, leer wenn bekannt)
  - `cardSettings(c config.CardCfg) card.Settings` füllt alles aus Task 1 (Standardgeräte über `SPIDevice()`/`UARTDevice()`).
  - `Apply` mit `Unsupported != ""`: kein Leser, kein Neustart, `Status{Detail: Unsupported, State: StateError}`; Logzeile einmal.
  - `openReader` bei `TypePN532I2C`: je `Bus` `openI2C`/`openSPI`/`openUART` + passender Transport. Fehlt das Gerät (`errors.Is(err, fs.ErrNotExist)`), lautet der Fehler genau
    `SPI ist im System nicht eingeschaltet (<dev> fehlt): neues Abbild oder dtparam=spi=on in config.txt` bzw.
    `UART ist im System nicht eingeschaltet (<dev> fehlt): neues Abbild oder dtparam=uart0=on in config.txt`.

- [ ] **Step 1: Tests** –
`TestServiceUnsupportedIsErrorWithoutRetry` (`Settings{Type:"pn7150", Unsupported:"Lesertyp …"}` → Status `error` mit dem Grund; `open` wird nie aufgerufen);
`TestOpenReaderMissingSPIDevice`/`…UART` (nicht vorhandener Pfad im Testverzeichnis → genaue Meldungen oben);
`TestCardSettingsMapping` in `admin_card_test.go` (`{Type:"pn532-i2c", PN532Bus:"spi"}` → `Bus:"spi", SPIDev:"/dev/spidev0.0"`; `{Type:"pn7150"}` → `Unsupported` gesetzt).

- [ ] **Step 2: Laufen lassen** → FAIL

- [ ] **Step 3: Implementieren**

- [ ] **Step 4: Grün** – `scripts/dgo test -race ./internal/product/pdhterminal/...` → PASS

- [ ] **Step 5: Commit** `feat(card): Leser über SPI/UART wählen, unbekannte Typen als aus mit Warnung`

---

### Task 6: OLED – Bild, Schrift, Messwerte (ohne Hardware)

**Files:**
- Create: `internal/product/pdhterminal/oled/canvas.go`, `font.go`, `screen.go`, `metrics.go`
- Test: `internal/product/pdhterminal/oled/screen_test.go`, `metrics_test.go`, `testdata/screen.txt`

**Interfaces:**
- Produces:
  ```go
  const Width, Height = 128, 64
  type Canvas struct{ /* 1 Bit je Pixel */ }
  func NewCanvas() *Canvas
  func (c *Canvas) Set(x, y int)                          // außerhalb: ignorieren
  func (c *Canvas) Text(x, y int, s string, scale int)    // 5×7-Zeichen in 6×8-Zellen, scale vergrößert
  func (c *Canvas) Rect(x0, y0, x1, y1 int)               // Umriss, Ecken eingeschlossen
  func (c *Canvas) FillRect(x0, y0, x1, y1 int)
  func (c *Canvas) Page(p int) []byte                     // 128 Byte, Bit 0 = oberste Zeile der Seite
  func (c *Canvas) ASCII() string                         // 64 Zeilen aus '#' und '.'
  type Metrics struct { CPU, RAM float64; TempC float64; TempOK bool; IP string }
  func Render(m Metrics) *Canvas
  type cpuTimes struct{ idle, total uint64 }
  func readCPU(procStat string) (cpuTimes, error)
  func cpuPercent(prev, cur cpuTimes) float64
  func readRAM(meminfo string) (float64, error)          // (MemTotal-MemAvailable)/MemTotal*100
  func readTemp(path string) (float64, bool)
  func ipAddress() string                                 // UDP-Connect 8.8.8.8:80, sonst "Offline"
  ```
  - `Render`-Lagen genau nach Spec: „CPU“ (0,0) scale 1; Temperatur `%.0f°C` bzw. `--` bei (85,0); CPU `%.0f%%` bei (0,10) scale 3; `RAM %.0f%%` bei (0,39); `Rect(57,41,127,48)`; `filled = round(68*RAM/100)`, wenn > 0 `FillRect(59,43,58+filled,46)`; Linie y=52 von x=0 bis 127; IP bei (0,53).
  - Schrift: ASCII 0x20–0x7E plus `°`, 5×7, in `font.go` als Tabelle.

- [ ] **Step 1: Tests** –
`TestRenderGolden`: `Render(Metrics{CPU:17, RAM:42, TempC:52, TempOK:true, IP:"192.168.1.20"}).ASCII()` gleich `testdata/screen.txt` (mit `-update`-Flag erzeugbar; die erste Fassung von Hand gegen die Lagen prüfen: Linie in Zeile 52 voll, Balkenrahmen in Zeilen 41 und 48 von x=57 bis 127, Füllung bis x=58+29=87).
`TestRenderNoTempAndOffline`: Zeichen „--“ ab x=85 gesetzt, „Offline“ in Zeile 53ff.
`TestRenderRAMZeroNoFill`: bei RAM 0 keine Pixel in (59..126, 43..46).
`TestPageBitOrder`: `Set(3,9)` → `Page(1)[3] == 0x02`.
`TestCPUPercent`: aus zwei `/proc/stat`-Zeilen `cpu 100 0 100 800 0 0 0 0 0 0` → `cpu 150 0 150 900 0 0 0 0 0 0` ergibt 50.
`TestReadRAM`: `MemTotal: 1000 kB`, `MemAvailable: 250 kB` → 75.
`TestReadTemp`: `52123` → 52.123, ok; fehlende Datei → false.

- [ ] **Step 2: Laufen lassen** – `scripts/dgo test ./internal/product/pdhterminal/oled/` → FAIL

- [ ] **Step 3: Implementieren**

- [ ] **Step 4: Grün** → PASS

- [ ] **Step 5: Commit** `feat(oled): Statusbild wie raspi-Oled`

---

### Task 7: OLED – SH1106-Treiber und Display-Dienst

**Files:**
- Create: `internal/product/pdhterminal/oled/sh1106.go`, `i2c_linux.go`, `i2c_other.go`, `service.go`
- Modify: `internal/product/pdhterminal/run.go` (Dienst erzeugen, `Apply` bei Start und nach Konfigurationsänderung wie `s.cards.Apply`, `Close` beim Beenden)
- Test: `internal/product/pdhterminal/oled/sh1106_test.go`, `service_test.go`

**Interfaces:**
- Consumes: `Canvas`, `Render`, Messfunktionen (Task 6); `config.StatusDisplayCfg` (Task 1)
- Produces:
  ```go
  type bus interface{ Write([]byte) (int, error); Close() error }
  func openBus(dev string, addr int) (bus, error)          // /dev/i2c-N + I2C_SLAVE
  type sh1106 struct{ b bus }
  func (d *sh1106) Init() error                            // eine Befehlsschreibung, siehe unten
  func (d *sh1106) Show(c *Canvas) error                   // je Seite: Befehle B0+p, 0x02, 0x10; dann 0x40 + 128 Byte
  func (d *sh1106) Off() error                             // leeres Bild, dann 0xAE
  func Probe(dev string, addr int) error                   // öffnet, schreibt {0x00, 0xE3}, schließt
  type Settings struct{ Type, Bus string; Addr int; Unsupported string }
  type Service struct{ … }
  func NewService(logf func(string, ...any)) *Service
  func (s *Service) Apply(st Settings)                     // gleiche Settings: nichts; aus: Off + stoppen
  func (s *Service) Status() (state, detail string)        // "ok"|"none"|"error"
  func (s *Service) Close()                                // stoppt den Lauf und schreibt vorher Off
  ```
  - Init-Befehle (Steuerbyte `0x00` vorn): `AE D5 80 A8 3F D3 00 40 AD 8B A1 C8 DA 12 81 CF D9 22 DB 40 A4 A6 AF`.
  - Lauf: `Init`, dann jede Sekunde `readCPU`/`readRAM`/`readTemp`/`ipAddress` → `Render` → `Show`. Fehler: Status `error` mit Meldung, Neustart nach 5 s. `Unsupported` gesetzt: Status `error` mit dem Grund, kein Lauf.
  - `run.go`: `statusDisplaySettings(c config.StatusDisplayCfg) oled.Settings` neben `cardSettings`.

- [ ] **Step 1: Tests** –
`TestSH1106Init` (fakeBus: erste Schreibung = `00` + obige Folge);
`TestSH1106ShowPages` (8 Seiten; je Seite `00 B0+p 02 10` dann `40` + `Canvas.Page(p)`);
`TestSH1106Off` (letzte Schreibung `00 AE`);
`TestServiceRetriesAfterWriteError` (fakeBus wirft beim 2. `Show` Fehler → Status `error`, nach verkürzter Wartezeit wieder `ok`);
`TestServiceOffClearsDisplay` (`Apply(Settings{})` nach Lauf → `Off` geschrieben, Status `none`);
`TestServiceUnsupported` (Status `error` mit Grund, `openBus` nie gerufen).
Zeiten und `openBus` im `Service` als ersetzbare Felder wie in `card.Service`.

- [ ] **Step 2: Laufen lassen** → FAIL

- [ ] **Step 3: Implementieren**

- [ ] **Step 4: Grün** – `scripts/dgo test -race ./internal/product/pdhterminal/...` und `GOOS=linux GOARCH=arm64 scripts/dgo vet ./internal/product/pdhterminal/...` → PASS

- [ ] **Step 5: Commit** `feat(oled): SH1106-Treiber und Display-Dienst`

---

### Task 8: Admin-Seiten und Selbstprüfung

**Files:**
- Modify: `internal/product/pdhterminal/admin_card.go`, `admin_card.html`, `admin.go` (Navigation/Route), `run.go` (Selbstprüfung)
- Create: `internal/product/pdhterminal/admin_statusdisplay.go`, `admin_statusdisplay.html`
- Modify: `internal/selftest/selftest.go`
- Test: `internal/product/pdhterminal/admin_card_test.go`, `admin_statusdisplay_test.go`, `internal/selftest/run_steps_test.go`

**Interfaces:**
- Consumes: Tasks 1, 5, 7
- Produces:
  - Kartenleser-Auswahl mit Werten `""`, `pn532-i2c`, `pn532-spi`, `pn532-uart`, `usb`. Nur das Formular kennt `pn532-spi`/`pn532-uart`; `cardSave` setzt daraus `Type:"pn532-i2c", PN532Bus:"spi"|"uart"`, und `renderCard` zeigt die Auswahl umgekehrt. Felder `spiDev`, `uartDev` nur bei passender Auswahl (wie die vorhandene Umschaltung). Schalterhinweis: I²C „SEL0 an, SEL1 aus“, SPI „SEL0 aus, SEL1 an“, UART „beide Schalter aus“.
  - `cardSave` prüft vor `f.Update` mit `config.ValidateCardStrict`; Fehlertext in der Seite.
  - Neue Seite `/admin/statusdisplay` (Navigation „Statusdisplay“ nach „Kartenleser“): Typ (Aus/SH1106), Bus, Adresse; Speichern prüft `ValidateStatusDisplayStrict`; `POST /admin/statusdisplay/test` ruft `oled.Probe` und zeigt `Statusdisplay antwortet (Bus, Adresse)` oder den Fehler. Zustand aus `Service.Status()` oben auf der Seite.
  - `selftest.StepStatusDisplay = "statusdisplay"` in `StepNames` nach `StepCard`; `Deps.StatusDisplay func(ctx context.Context) error` (nil = aus); `func StatusDisplayStep() Step`: nil → OK `Statusdisplay aus`; Fehler → nicht OK, Detail `Statusdisplay antwortet nicht (<bus>, <addr>): <err>`; sonst OK `Statusdisplay antwortet (<bus>, <addr>)`. `run.go` hängt `selftest.StatusDisplayStep()` nach `CardStep()` an.

- [ ] **Step 1: Tests** –
`TestCardSaveSPI` (POST `type=pn532-spi`, `spiDev=/dev/spidev0.1` → gespeichert `Type:"pn532-i2c", PN532Bus:"spi", SPIDev:"/dev/spidev0.1"`; GET zeigt `pn532-spi` ausgewählt);
`TestCardSaveRejectsUnknown` (POST `type=pn7150` → 400, Konfiguration unverändert);
`TestCardPageShowsUnsupported` (Konfiguration mit `Type:"pn7150"` → Seite zeigt den Grund aus `Known()`);
`TestStatusDisplaySaveAndTest` (Speichern SH1106/0x3C; Test-Knopf mit ersetzbarer Probe → beide Meldungen);
`TestStatusDisplayStep` (nil, Fehler, ok → drei Ergebnisse wie oben).

- [ ] **Step 2: Laufen lassen** → FAIL

- [ ] **Step 3: Implementieren**

- [ ] **Step 4: Grün** – `scripts/dgo test -race ./... && scripts/dgo vet ./...` → PASS

- [ ] **Step 5: Commit** `feat(pdh-terminal): Admin-Seiten für PN532-Bus und Statusdisplay, Selbstprüfung`

---

### Task 9: Abbild, Installation, Doku, Geräteprüfung

**Files:**
- Modify: `packaging/image/stage-hmi/01-hmi/00-run.sh`, `packaging/install.sh`, `packaging/image/verify-i2c.sh` (oder neu `verify-busses.sh`, eingebunden wie `verify-i2c.sh`), `packaging/image/verify-image.sh`, `packaging/test-products.sh`, `docs/betrieb.md`
- Create: `dist/geraetetest-l1.md` (nicht im Repo, `dist/` ist ignoriert; trotzdem anlegen)

**Interfaces:**
- Consumes: nichts aus Go
- Produces:
  - `00-run.sh` (nur pdh-terminal): `HMI_GROUPS=input,video,render,i2c,spi,dialout`; Gruppe `spi` anlegen wie `i2c` (`getent group spi >/dev/null || groupadd -r spi`); `dtparam=spi=on` und `dtparam=uart0=on` je einzeln, nur wenn noch nicht im letzten `[all]`-Block (`final_all_block`), angehängt wie `i2c_arm` mit Kommentarzeile `# SPI fuer den Kartenleser (PN532)` bzw. `# UART fuer den Kartenleser (PN532)`.
  - `install.sh`: bei pdh-terminal zusätzlich `spi,dialout` in `KIOSK_GROUPS`, `spi` anlegen falls fehlend; `config.txt` bleibt unberührt.
  - Prüffunktion `check_busses <produkt> <wurzel> <boot>` (ok/bad wie `check_i2c`, Name `kartenleser-spi-uart`): bei pdh-terminal `spi=on`, `uart0=on` im letzten `[all]`, `hmikiosk` in `spi` und `dialout`, `cmdline.txt` ohne `console=ttyAMA0`; bei hmi-kiosk: keine der beiden Zeilen in `config.txt`, sonst `bad`.
  - `test-products.sh`: Fälle wie `expect_i2c` (gut, Zeile fehlt, Gruppe fehlt, `console=ttyAMA0`, HMI mit Zeile → Fehler, HMI ohne → ok) und die `grep`-Prüfungen gegen `00-run.sh`/`install.sh` analog zu den I²C-Zeilen.
  - `docs/betrieb.md`: Abschnitt „Kartenleser und Statusdisplay“ mit der Verdrahtungstabelle aus Spec Teil 5, Schalterstellungen, den zwei `config.txt`-Zeilen für Installationen ohne neues Abbild, Hinweis auf `hardware/gehaeuse/`.
  - `dist/geraetetest-l1.md`: Prüfliste nach Spec „Geräteprüfung“ im Stil von `dist/geraetetest-tp2.md` (Tabellen mit #, Schritt, Erwartet).

- [ ] **Step 1: Tests zuerst in `test-products.sh`** ergänzen.

- [ ] **Step 2: Laufen lassen** – `bash packaging/test-products.sh` → neue Fälle FEHLER

- [ ] **Step 3: Skripte und Doku ändern**

- [ ] **Step 4: Grün** – `bash packaging/test-products.sh` (alle OK) und `scripts/dgo test -race ./...` → PASS; `shellcheck` falls im Repo üblich (sonst entfällt).

- [ ] **Step 5: Commit** `feat(image): SPI und UART für den PN532, Gruppen spi/dialout; Doku Verdrahtung`
