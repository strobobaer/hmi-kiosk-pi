# Teilprojekt L1: PN532 über SPI und UART, OLED-Statusdisplay

Stand 2026-11-15. Betrifft nur dieses Repository, Produkt `pdh-terminal` (Kartendienst, Konfiguration,
Admin-Seite, Selbstprüfung, Abbild). PDH ist nicht betroffen: Der Kartenstrom zu PDH bleibt gleich.
Das zugehörige Gehäuse G1 liegt fertig in `hardware/gehaeuse/` (Commit `be6f26e`).

## Entscheidungen des Nutzers

- **Ziel-Hardware:** Raspberry Pi 5 mit PN532-Modul (Elechouse V3) und OLED SH1106 1,3" an I²C.
- **Leser:** Der PN532 lässt sich zusätzlich zu I²C über **SPI** und über **UART (HSU)** anschließen.
  NXP PN7150/PN7160 und PN5180 sind gestrichen („Nur L1, L2 streichen“).
- **Statusdisplay „genau wie raspi-Oled“:** dasselbe Bild wie das Python-Skript aus `raspi-Oled`
  (`install-oled.sh`), jetzt im Programm des Terminals statt als eigener Python-Dienst.
- **Rückwärtskompatibel** (Regel vom 2026-11-12): Der Bus des PN532 kommt in ein **neues Feld**
  `card.pn532Bus`; `card.type` bleibt für jeden PN532 `"pn532-i2c"`. Ein unbekannter `card.type`
  macht die Konfiguration beim Laden nicht mehr ungültig, sondern gilt als „aus“ mit Warnung.

## Teil 1: Kartenleser

### Transportwege

Das Protokoll in `card/pn532.go` (Rahmen, ACK, Befehle, Abfrage) bleibt gleich. Die Schicht darunter,
heute `i2cConn` mit dem I²C-Statusbyte, wird zur Schnittstelle **`pn532Transport`**: Rahmen schreiben,
auf „bereit“ warten, eine Antwort ab dem Rahmenanfang lesen. Es gibt drei Umsetzungen.

| `card.pn532Bus` | Gerät (Standard) | Umsetzung |
|---|---|---|
| `""` (I²C, wie bisher) | `card.i2cBus` `/dev/i2c-1`, `card.i2cAddr` `0x24` | Bisheriger Weg; das Statusbyte am Anfang jeder Antwort zeigt „bereit“. |
| `"spi"` | `card.spiDev` `/dev/spidev0.0` | Modus 0, 1 MHz. Vor jeder Übertragung ein Präfixbyte: `0x01` Daten schreiben, `0x02` Status lesen (Bit 0 = bereit), `0x03` Daten lesen. Der PN532 sendet LSB zuerst, der SPI-Treiber des Pi kann das nicht; jedes Byte wird deshalb per Tabelle umgedreht. |
| `"uart"` | `card.uartDev` `/dev/ttyAMA0` | 115200 Baud, 8N1, roh (termios). Vor dem ersten Befehl die Aufweckfolge `55 55 00 00 00 00 00 00 00 00 00 00 00 00 00 00`. Es gibt kein Statusbyte: „bereit“ ist der erste vollständige Rahmen innerhalb der Zeitgrenze. |

Die UART-Umsetzung hängt nicht am Gerätenamen. B3 (USB-Seriell-Adapter, `/dev/ttyUSB*`) kann sie
später ohne Änderung übernehmen.

Die bestehenden Zeiten gelten für alle drei Wege: Abfrage alle 250 ms, 100 ms Zeitgrenze für
„bereit“, Neustart nach einem Fehler nach 5 s. Ein fehlendes Gerät meldet eine klare Ursache, zum
Beispiel: „SPI ist im System nicht eingeschaltet (/dev/spidev0.0 fehlt): neues Abbild oder
dtparam=spi=on in config.txt“. Für UART lautet die Meldung entsprechend `/dev/ttyAMA0` und
`dtparam=uart0=on`.

### Einstellungen (`config.json`)

| Feld | Werte | Fehlt es |
|---|---|---|
| `card.type` | `""`, `"pn532-i2c"`, `"usb"` (unverändert) | kein Leser |
| `card.pn532Bus` | `""`, `"spi"`, `"uart"` (neu) | I²C |
| `card.spiDev` | `/dev/spidevB.C` (neu) | `/dev/spidev0.0` |
| `card.uartDev` | `/dev/ttyAMA*`, `/dev/ttyS*`, `/dev/ttyUSB*`, `/dev/ttyACM*` (neu) | `/dev/ttyAMA0` |

Alle neuen Felder tragen `omitempty`. Eine alte `config.json` lädt unverändert und wird beim nächsten
Speichern Byte für Byte gleich geschrieben, solange nichts Neues eingestellt ist.

Im Go-Code heißt der Typ „PN532“; `pn532-i2c` ist nur der gespeicherte Wert. Ein Kommentar an der
Konstante erklärt, warum er nicht umbenannt wird.

### Prüfung beim Laden und beim Speichern

- **Laden** (`readConfig`, also `config.json` und die gute Kopie): Ein unbekannter `card.type` oder
  `card.pn532Bus` ist kein Fehler.
  - Der Kartendienst behandelt den Leser dann als „aus“.
  - Sein Zustand zeigt die Warnung „Lesertyp … wird von dieser Version nicht unterstützt“, im
    Kartenstrom ist der Zustand `error`.
  - Die Felder bleiben in der Datei erhalten, damit ein späteres Update sie wieder findet.
- **Speichern über die Admin-Seite:** Die Prüfung ist streng und lässt nur Werte zu, die diese
  Version kennt.
- **Format der Gerätefelder:** Ungültige Formate von `spiDev` und `uartDev` sind in beiden Fällen ein
  Fehler (Muster wie bei `i2cBus`). Eine kaputte Pfadangabe ist kein Zukunftswert.

Damit bricht auch ein künftig neuer Lesertyp beim Zurückrollen auf diese Version nichts mehr.

## Teil 2: Statusdisplay

### Bild (wie raspi-Oled)

SH1106, 128 × 64 Pixel. Die Koordinaten entsprechen denen des Python-Skripts:

| Inhalt | Lage |
|---|---|
| „CPU“ | (0, 0), kleine Schrift |
| CPU-Temperatur, z. B. `52°C`, ohne Wert `--` | (85, 0), klein |
| CPU-Last, z. B. `17%` | (0, 10), große Schrift (ca. 24 px hoch) |
| „RAM xx%“ | (0, 39), klein |
| RAM-Balken: Rahmen (57,41)–(127,48), Füllung (59,43)–(58+⌊68·ram/100⌉,46) | |
| Linie | (0,52)–(127,52) |
| IPv4-Adresse oder „Offline“ | (0, 53), klein |

- **Aktualisierung:** Das Bild wird jede Sekunde neu gezeichnet.
- **Messwerte:**
  - die CPU-Last aus der Differenz zweier Lesungen von `/proc/stat`;
  - der RAM-Wert aus `/proc/meminfo` als `(MemTotal − MemAvailable) / MemTotal`, wie bei psutil;
  - die Temperatur aus `/sys/class/thermal/thermal_zone0/temp`.
- **IP-Adresse:** Sie wird wie im Skript per UDP-„connect“ auf 8.8.8.8:80 ermittelt. Dabei wird kein
  Paket gesendet. Schlägt das fehl, steht „Offline“ da.
- **Schriften:** Die beiden Rasterschriften (klein ca. 6 × 8, groß ca. 24 px, nur Ziffern und `%`)
  liegen fest im Programm. Es gibt keine Abhängigkeit von Systemschriften.

### Treiber und Ablauf

- Neues Paket `internal/product/pdhterminal/oled`.
- **Ansteuerung:** I²C wie beim Kartenleser (`/dev/i2c-N`, Ziel­adresse per ioctl).
  - Zur Einrichtung schickt der Treiber die übliche SH1106-Befehlsfolge.
  - Das Bild geht in 8 Seiten zu je 128 Spalten mit Spaltenversatz 2 hinaus, weil der SH1106 132
    Spalten breit ist.
- **Zeichnen und Senden sind getrennt:** Das Bild entsteht in einem 1-Bit-Puffer und ist so ohne
  Hardware testbar.
- **Ausschalten:** Beim Beenden des Dienstes und beim Abschalten in der Admin-Seite wird das Display
  gelöscht und ausgeschaltet. Es bleibt kein eingefrorenes Bild stehen.
- **Eigene Goroutine:** Das Display läuft getrennt vom Kartenleser und startet nach einem Fehler nach
  5 s neu. Leser und Display teilen den I²C-Bus, aber keine Dateideskriptoren; der Kernel ordnet die
  Zugriffe.

### Einstellungen

Einen Abschnitt `display` gibt es in `config.json` schon (Bildschirm des HMI: Auflösung,
Grafikprofil). Das OLED bekommt deshalb einen **eigenen Abschnitt `statusDisplay`** (`omitzero`).

| Feld | Werte | Fehlt es |
|---|---|---|
| `statusDisplay.type` | `""` (aus), `"sh1106"` | aus |
| `statusDisplay.i2cBus` | `/dev/i2c-N` | `/dev/i2c-1` |
| `statusDisplay.i2cAddr` | 0x08–0x77 | `0x3C` |

Die Prüfung beim Laden und Speichern folgt denselben Regeln wie beim Kartenleser. Ein unbekannter
Typ gilt beim Laden als „aus“ mit Warnung, beim Speichern über die Admin-Seite ist er ein Fehler.

## Teil 3: Admin-Seite und Selbstprüfung

- **Seite Kartenleser:** Die Auswahl hat die Einträge Aus, PN532 (I²C), PN532 (SPI), PN532 (UART)
  und USB-Leser.
  - Gespeichert werden `type` und `pn532Bus`.
  - Je nach Auswahl erscheinen die passenden Felder: Bus und Adresse, SPI-Gerät oder UART-Gerät.
  - Die Schalterstellung des Moduls steht als Hinweis daneben: I²C SEL0 an/SEL1 aus, SPI SEL0
    aus/SEL1 an, UART beide aus.
- **Neue Seite Statusdisplay:** Auswahl Aus oder SH1106, Bus, Adresse und ein Knopf „Testen“. Der
  Knopf führt die Prüfung der Selbstprüfung aus und zeigt das Ergebnis an.
- **Neuer Schritt `statusdisplay` in der Selbstprüfung** (nur pdh-terminal, nach `kartenleser`):
  - Ist das Display aus, gilt der Schritt als bestanden mit „Statusdisplay aus“.
  - Sonst schreibt der Schritt einen SH1106-NOP-Befehl (`0xE3`). Antwortet das Gerät nicht, ist der
    Schritt rot mit Bus und Adresse in der Meldung.
- **Kartenleser-Schritt:** Er nennt den Bus in der Meldung, z. B. „PN532 über SPI, Firmware 1.6“.

## Teil 4: Abbild und Installation

Alles in diesem Teil gilt nur für das Produkt `pdh-terminal`. Das Abbild des HMI-Kiosks ändert sich
nicht.

- **`packaging/image/stage-hmi/01-hmi/00-run.sh`:**
  - schreibt `dtparam=spi=on` und `dtparam=uart0=on` in `config.txt`, neben das vorhandene
    `dtparam=i2c_arm=on`. Ein erneuter Lauf trägt sie nicht doppelt ein; die Prüfung erfolgt wie bei
    I²C über `final_all_block`.
  - nimmt den Dienstbenutzer zusätzlich in die Gruppen `spi` und `dialout` auf. Raspberry Pi OS setzt
    diese Gruppen für `/dev/spidev*` und `/dev/ttyAMA0` selbst, eine eigene udev-Regel ist nicht nötig.
- **Serielle Konsole:** Am Pi 5 zeigt `serial0` auf den Debug-Stecker (`ttyAMA10`). `ttyAMA0` an
  Pin 8/10 bleibt frei.
- **`verify-image.sh` und `test-products.sh`** prüfen:
  - die neuen Zeilen und Gruppen;
  - dass `cmdline.txt` kein `console=ttyAMA0` enthält;
  - dass das HMI-Abbild diese Zeilen **nicht** hat.
- **`install.sh`** (Installation auf vorhandenem Raspberry Pi OS) nimmt den Benutzer bei pdh-terminal
  in `spi` und `dialout` auf. In `config.txt` schreibt es weiterhin nichts, wie bisher bei I²C;
  `docs/betrieb.md` nennt die beiden Zeilen.

## Teil 5: Verdrahtung am Pi 5 (alles 3,3 V)

Kommt nach `docs/betrieb.md` und in die Geräteprüfliste.

| Modul | Anschluss | Pi-Pin |
|---|---|---|
| OLED SH1106 | VCC, GND, SDA, SCL | 1, 9, 3, 5 (I²C-Bus mit dem PN532 geteilt, Adressen 0x3C und 0x24) |
| PN532 I²C | VCC, GND, SDA, SCL; SEL0 an, SEL1 aus | 17, 6, 3, 5 |
| PN532 SPI | SCK, MOSI, MISO, SS; SEL0 aus, SEL1 an | 23, 19, 21, 24 (CE0), dazu 3,3 V an 17, GND an 20 |
| PN532 UART | TXD des Moduls → Pin 10 (RXD), RXD des Moduls → Pin 8 (TXD); beide Schalter aus | 3,3 V an 17, GND an 14 |

Das OLED nimmt Pin 1 für 3,3 V, der PN532 Pin 17. So braucht die Versorgung keinen Verteiler.
Korrektur (2026-11-18): Läuft der PN532 über I²C, teilen sich OLED und PN532 aber Pin 3 (SDA) und
Pin 5 (SCL); dafür braucht es je einen Y-Verbinder oder ein Stück Lochraster. Bei SPI und UART hängt
das OLED allein am I²C-Bus. Die Verdrahtung mit Bildern steht in
`hardware/verdrahtung/pdh-terminal-verdrahtung.pdf`.

## Rückwärtskompatibilität

| Fall | Ergebnis |
|---|---|
| Neues Programm, alte `config.json` | lädt unverändert; Leser wie bisher, Display aus |
| Altes Programm (0.2.x), neue `config.json` mit `pn532Bus: "spi"` | altes Programm überspringt das Feld, versucht I²C, meldet „Leser: Fehler“ und läuft weiter; Anmeldung per Tastatur (K1) bleibt nutzbar |
| Altes Programm, neue `config.json` mit `statusDisplay` | Abschnitt wird übersprungen, kein Display, sonst normal |
| Neues Programm, künftiger unbekannter `card.type` | lädt; Leser „aus“ mit Warnung |
| Programm-Update ohne neues Abbild | I²C-Leser, USB-Leser und Display laufen (I²C ist schon an); SPI und UART melden klar, dass `config.txt` fehlt |
| Kartenstrom zu PDH | unverändert (Ereignisse `card`, `reader`) |

Keine Änderung an PDH, keine Migration.

## Tests

- **Unit, ohne Hardware:**
  - **SPI** mit nachgebildetem Gerät: Präfixbytes, Bitumkehr in beide Richtungen, Warten auf
    „bereit“ und auf ACK.
  - **UART** mit nachgebildetem Gerät: Aufweckfolge einmal, Rahmen aus geteilten Lesungen
    zusammensetzen, Zeitgrenze.
  - Die **Protokolltests** des PN532 laufen über alle drei Wege.
- **Unit, Konfiguration:**
  - Ein alter Stand lädt Byte für Byte gleich.
  - Ein unbekannter `card.type`, `pn532Bus` oder `statusDisplay.type` lädt und meldet „aus“ mit
    Warnung, wird beim Speichern über die Admin-Seite aber abgelehnt.
  - Gerätepfade werden geprüft.
- **Unit, Display:**
  - Das Bild für feste Messwerte wird mit einer Vorlage als ASCII-Raster verglichen.
  - Die Befehlsfolge beim Einrichten und beim Senden hat Spaltenversatz 2 und 8 Seiten.
  - CPU-Last aus zwei `/proc/stat`-Ständen, RAM aus `meminfo`.
- **Admin und Selbstprüfung:**
  - Speichern der drei PN532-Varianten, Ablehnen unbekannter Werte.
  - Der Schritt `statusdisplay` bei aus, bei „antwortet“ und bei „antwortet nicht“.
- **Abbild:** `test-products.sh` mit den neuen Prüfungen; `verify-image.sh` gegen das gebaute Abbild.
- **Geräteprüfung** (`dist/geraetetest-l1.md`):
  - PN532 an I²C, SPI und UART jeweils mit Karte;
  - Display zeigt das Bild, IP und „Offline“ ohne Netz;
  - Selbstprüfung grün;
  - Rückfall auf 0.2.1 mit SPI-Konfiguration: Terminal startet, Leser meldet Fehler.

## Nicht Teil von L1

- PN7150/PN7160, PN5180 (gestrichen).
- USB-Seriell-Adapter für den PN532 (B3; die UART-Umsetzung ist dafür vorbereitet).
- Weitere Bildinhalte, Drehen oder Helligkeit des Displays. Das Bild ist bewusst genau das von
  raspi-Oled.
- Das Gehäuse G1 ist schon geliefert. Seine Maße werden mit der Geräteprüfung nachgemessen.

## Umsetzung

Folgezweig `feat/l1-leser-oled` von `feat/pdh-terminal`. Ausführung wie bei TP2 und K1:
subagent-driven mit Token-Ökonomie.
