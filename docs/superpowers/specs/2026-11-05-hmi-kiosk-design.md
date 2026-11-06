# hmi-kiosk – Design

Stand: 2026-11-05 · Status: zur Prüfung

## 1. Ziel

Touch-Terminals an der Anlage zeigen Kacheln für die eingerichteten HMIs. Ein Touch startet einen
nativen VNC-Client statt noVNC. Das System wird als neues Go-Programm gebaut und muss auf zwei
Raspberry Pi 5 stabil und produktionssicher laufen, auch im abgeschotteten Netz.

### Erfolgskriterien

- Nach einem Touch steht das HMI-Bild in etwa 1–2 Sekunden.
- Das Bild reagiert spürbar flüssiger als mit noVNC.
- Nach jedem Sitzungsende, auch nach einem Absturz des Clients, erscheint wieder die Kachelseite.
- Nach einem Stromausfall startet das Gerät ohne Eingriff wieder in den Kiosk.
- Das Gerät braucht im Betrieb keinen Internetzugang.

### Nicht-Ziele

- Integration in PDH. Sie folgt später; `pdh-server` bleibt unverändert.
- Android.
- Sperren fremder VNC-Clients, die nicht zu diesem System gehören (Laptop, PDH-Zugriff).
- Serverseitig erzwungenes „Nur ansehen“. „Nur ansehen“ ist eine Client-Einstellung.
- Automatische Synchronisierung der Konfiguration zwischen Terminals.

## 2. Plattform

| Thema | Festlegung |
|---|---|
| Hardware | Raspberry Pi 5, 4 GB RAM, 64-GB-SD-Karte |
| Display | iiyama ProLite, Touch, Annahme 1920×1080 |
| System | Raspberry Pi OS Lite 64-Bit (`arm64`), Annahme: Debian 13 „Trixie“ |
| Grafik | labwc (Wayland); X11 mit openbox als mitgeliefertes Ausweichprofil |
| Oberfläche | Chromium im Kiosk-Modus (Variante A) |
| VNC-Client | Remmina; TigerVNC als umschaltbares Profil |

**Andere Systeme** folgen als zweite Stufe, sobald die Raspis stabil laufen: `install.sh` für andere
Linux-Systeme und `install.ps1` mit `winget` für Windows. Remmina gibt es nur für Linux, dort wird
das TigerVNC-Profil verwendet. Der Code hält Plattformteile dafür hinter Schnittstellen.

## 3. Aufbau

| Teil | Aufgabe |
|---|---|
| `hmi-kiosk` | Go-Dienst ohne root: Konfiguration, Abstimmung, Sitzungen, Überwachung von Client und Browser, Kachel- und Admin-Seite |
| `hmi-kiosk-helper` | Kleiner root-Helfer mit fester Befehlsliste: WLAN, Bluetooth, Neustart, Update aktivieren, Uhrzeit |
| Chromium | Zeigt Kachelseite und Sitzungsleiste, Profil im RAM |
| VNC-Client | Kindprozess, je Sitzung neu gestartet |
| labwc | Fensterverwaltung im eigenen Kiosk-Benutzer |

Jeder Teil darf abstürzen und wird neu gestartet. Ein Absturz der Oberfläche beendet keine VNC-Sitzung.

Go-Pakete:

- `config` – Laden, Prüfen, atomares Speichern
- `coord` – Abstimmung zwischen Terminals, ohne Abhängigkeit von Prozessen oder Oberfläche
- `session` – Zustandsautomat einer Sitzung
- `client` – Client-Profile, Schnittstelle `Start`, `Stop`, `Wait`
- `browser` – Start und Überwachung von Chromium
- `web` – Kachelseite, Sitzungsleiste, Admin-Seite
- `helperapi` – Verbindung zum root-Helfer
- `platform` – Auflösung, Netzwerk, systemabhängige Teile

Ports: Kachelseite und Leiste auf `127.0.0.1:8080` ohne Anmeldung, Admin-Seite und Terminal-API auf
`:8443` per HTTPS.

## 4. Ablauf einer Sitzung

Zustände: `Kacheln → Anfrage → (Rückfrage) → Start → Verbunden → Beenden → Kacheln`

1. Touch auf eine Kachel.
2. `coord` fragt die anderen Terminals (Abschnitt 5).
3. Der Dienst prüft Erreichbarkeit und Passwort vorab mit `rfb.Probe`. Ist `AuthOK` falsch, zeigt die Kachel einen Hinweis und es wird kein Client gestartet. Sonst schreibt der Dienst ein Sitzungsprofil in den RAM und startet den Client.
4. Sobald die TCP-Verbindung zum HMI im Zustand ESTABLISHED steht, wird die Kachelseite beendet und die Leiste gezeigt.
5. Sitzungsende durch „Zurück“, Übernahme, Inaktivität, Client-Ende oder Trennung durch das HMI.
6. Der Client wird beendet, die Sitzungsdaten im RAM werden gelöscht, die Kachelseite startet.

**Verbindung:** „Steht“ und „besteht“ meinen in diesem Abschnitt eine TCP-Verbindung zum HMI im Zustand
ESTABLISHED (`platform.HasEstablished`), nicht die Erreichbarkeit. Eine neue TCP-Prüfung zum HMI zählt
nicht, denn sie gelingt auch, wenn der Client selbst keine Verbindung hat.

**Startfrist:** Steht 15 Sekunden nach dem Start des Clients keine solche Verbindung, wird der Client
beendet und die Kachel zeigt einen Hinweis.

**Sitzungsende erkennen:** Maßgeblich sind das Prozessende des Clients und zusätzlich die
Verbindung zum HMI. Besteht für 5 Sekunden keine Verbindung mehr, gilt die Sitzung als beendet
und der Client wird beendet. So bleibt der Kiosk nicht hängen, falls der Client einen Abbruch nur als
Dialog anzeigt.

**Rückweg:** Eine feste Leiste von 48 Pixeln Höhe oben zeigt HMI-Name, Modus und „Zurück“. Das
HMI-Bild liegt darunter und wird auf die verbleibende Fläche skaliert. Unsichtbare Gesten gibt es
nicht, weil jede Berührung auch beim HMI ankommen könnte.

**Inaktivität:** Rückkehr zu den Kacheln nach einstellbarer Zeit (Standard 15 Minuten, 0 = aus).
Gilt nicht für Daueranzeigen. Die Zeit misst nur Touch-Eingaben am Terminal.

**Daueranzeige:** Das Gerät verbindet sich nach dem Start mit dem festen HMI. Bei Abbruch verbindet
es sich nach 2, 4, 8 … höchstens 30 Sekunden neu und zeigt solange einen Hinweis. Die Leiste entfällt
oder ist ausblendbar (Einstellung).

**Auflösung:** Beim Start liest der Dienst die aktive Auflösung aus `/sys/class/drm/*/modes` des
verbundenen Ausgangs. Findet er nichts, gilt 1920×1080. In der Admin-Seite lässt sie sich überschreiben.

## 5. Abstimmung zwischen Terminals

Terminals sprechen direkt miteinander, es gibt keine zentrale Stelle. Die Liste der Terminals wird in
der Admin-Seite gepflegt. Jedes Terminal hat eine feste, zufällig erzeugte Kennung, einen Namen und
die Rolle `primär` oder `normal`.

**Belegung:** Ein belegendes Terminal meldet alle 2 Sekunden `hmi, rolle, kennung, sitzung`. Beim
Empfänger läuft die Belegung nach 6 Sekunden ohne Erneuerung ab, gemessen mit seiner eigenen
monotonen Uhr. Die Uhren der Geräte müssen nicht übereinstimmen.

**Anfrage:** Vor dem Start fragt das Terminal alle anderen an (Zeitlimit 1 Sekunde je Terminal).

| Situation | Übernehmen | Sperren |
|---|---|---|
| Normal will HMI von normal | Übernahme nach Rückfrage | Kachel zeigt „belegt durch X“ |
| Primär will HMI von normal | Übernahme | Übernahme (Vorrang) |
| Normal will HMI von primär | Abgelehnt | Abgelehnt |
| Primär will HMI von primär | Das zuletzt anfragende übernimmt | Das erste behält es |

**Einstellungen:** Modus global mit Abweichung je HMI. Je HMI außerdem die Art der Übernahme:

- `rückfrage` (Standard): Das übernehmende Terminal fragt „Wird genutzt von X – übernehmen?“. Das
  betroffene Terminal zeigt 5 Sekunden lang einen Hinweis, dann wird es getrennt. Primäre Geräte
  überspringen die Rückfrage, die Vorwarnung bleibt.
- `sofort`: Keine Rückfrage und keine Wartezeit.

**Gleichzeitige Anfragen:** Laufen zwei Anfragen für dasselbe HMI gleichzeitig, gewinnt die höhere
Rolle, bei gleicher Rolle die kleinere Kennung. Das Ergebnis ist auf allen Geräten gleich.

**Terminal nicht erreichbar:** Die Verbindung wird trotzdem erlaubt, die Kachel und die Leiste zeigen
einen Hinweis. Die Anzeige hat im Betrieb Vorrang; kurzzeitig können so zwei Terminals am selben HMI
hängen.

**Daueranzeige:** Das HMI gilt für andere dauerhaft als belegt. Je HMI ist „Mitschauen erlauben“
zuschaltbar (Standard aus): Andere verbinden sich dann mit `viewonly`, zusätzlich zur Daueranzeige.

**Beim übernommenen Terminal:** Empfängt ein Terminal eine Übernahme, beendet es seine Sitzung und
zeigt auf der Kachelseite, wer übernommen hat.

## 6. VNC-Client

Ein Profil besteht aus Paket, Befehlsvorlage und Sitzungsdaten. Standard ist Remmina.

### Remmina

Aufruf je Sitzung, ohne `--kiosk` und ohne `--enable-fullscreen`:

```bash
remmina --gapplication-app-id=de.hmikiosk.session<N> -c <RAM>/session.remmina --disable-toolbar --enable-extra-hardening --no-tray-icon
```

Geprüft im Remmina-Quellcode (Stand `master`, Abgleich mit der Trixie-Version in Abschnitt 14):

| Punkt | Befund | Folge |
|---|---|---|
| Instanzen | Remmina ist eine GApplication mit nur einer Instanz; ein zweiter Aufruf gibt an die laufende weiter | Eigene App-ID je Sitzung, nie eine Instanz weiterverwenden |
| Prozessende | Nach dem Trennen endet Remmina nur, wenn keine Verbindung, kein Hauptfenster und kein Tray-Symbol da sind | `--no-tray-icon` ist Pflicht; Prüfung auf eine ESTABLISHED-Verbindung und Startfrist aus Abschnitt 4 als Absicherung; Beenden per SIGTERM |
| `--kiosk` | Öffnet die Hauptansicht | Nicht verwenden |
| Skalierung | Clientseitig, Eingaben werden zurückgerechnet | Einsetzbar; das Seitenverhältnis bleibt erhalten, bei abweichendem Format entstehen schwarze Balken |
| Passwörter | Ohne Schlüsselbund: 3DES-CBC mit dem `secret` aus `remmina.pref` (24 Byte Schlüssel, 8 Byte IV), mit Nullen aufgefüllt, Base64 | Der Dienst erzeugt `secret` und Profil selbst. Das Secret-Plugin wird nicht installiert |

Erzeugte Profilfelder: `protocol=VNC`, `server`, `password`, `quality`, `colordepth`, `viewonly`,
`disableclipboard=1`, `closeonfailure=1`, `disablepasswordstoring=0`, `scale`, `viewmode`.

Die Felder `scale` und `viewmode` gehören nicht zum VNC-Plugin selbst. Gegen die Trixie-Version
bestätigte Werte: `scale=1` (an) oder `scale=0` (aus), `viewmode=1` (Abschnitt 14).

**Fenster:** Eine labwc-Fensterregel platziert Remmina ohne Rahmen bei `0,48` in der Größe
`Breite × (Höhe − 48)`. Die Leiste ist ein eigenes kleines Chromium-Fenster. Während der Sitzung läuft
also nur dieses Chromium-Fenster, nicht die Kachelseite.

**Daten im RAM:** Profil und `remmina.pref` liegen in einem eigenen `tmpfs` je Sitzung, unter
`XDG_CONFIG_HOME`, `XDG_DATA_HOME` und `XDG_CACHE_HOME` des Kindprozesses. Nach Sitzungsende werden sie
gelöscht. Das VNC-Passwort steht dort verschlüsselt, aber mit dem Schlüssel daneben; es ist also nur
durch die Rechte des Ordners und den RAM geschützt.

### TigerVNC

```bash
vncviewer -FullScreen -PasswordFile=<RAM>/passwd -ViewOnly=<0|1> <host>::<port>
```

Das Passwortformat erzeugt der Dienst. Skalierung gibt es bei diesem Profil voraussichtlich nicht. Die
Admin-Seite weist darauf hin.

## 7. Admin-Seite

Zugang per HTTPS mit selbst erzeugtem Zertifikat, Passwort wird beim ersten Start gesetzt und mit
Argon2id gespeichert. Sperre von 5 Minuten nach 5 Fehlversuchen.

1. **HMIs:** Name, Adresse, Port, VNC-Passwort, Symbol, Farbe, Reihenfolge, „Nur ansehen“,
   Modus, Art der Übernahme, „Mitschauen erlauben“, Verbindungsprüfung mit Anzeige der Auflösung
2. **Client:** Profil, Qualität, Farbtiefe, Skalierung
3. **Kiosk:** Rückkehr bei Inaktivität, Kachel-Layout, Daueranzeige und festes HMI
4. **Terminals:** Name, Rolle, Liste der anderen Terminals, gemeinsamer Schlüssel, Modus global
5. **Netzwerk:** WLAN und Bluetooth dauerhaft aus, NTP-Server
6. **System:** Protokoll, Schreibschutz-Status, freier Platz, Version, Neustart, Uhrzeit setzen,
   Update hochladen, Konfiguration sichern und wiederherstellen, Selbstprüfung, Auflösung,
   Grafikprofil (labwc oder X11)

**Schreibrechte:** Die Admin-Seite schreibt nur auf die Datenpartition. Das System bleibt schreibgeschützt.
Nur WLAN und Bluetooth ändern über den Helfer die Boot-Partition. Betriebssystem und Pakete sind aus
der Admin-Seite nicht änderbar, dafür gibt es ein neues Abbild.

**WLAN abschalten:** Der Helfer setzt `dtoverlay=disable-wifi` in `/boot/firmware/config.txt` und sperrt
WLAN zusätzlich sofort per `rfkill`. Bluetooth entsprechend mit `disable-bt`. Das Abschalten wird
verweigert, wenn die Admin-Sitzung über WLAN läuft oder die Ethernet-Verbindung keinen Link hat. Das
Wiedereinschalten braucht einen Neustart.

**Export und Import:** Die Exportdatei ist mit einem eigenen Passwort verschlüsselt (Argon2id, AES-GCM).
Beim Import bleiben Name, Kennung und Rolle des Geräts erhalten.

## 8. Daten

| Pfad | Inhalt | Eigenschaft |
|---|---|---|
| `/data/config.json` | Konfiguration, Schemaversion | Atomar schreiben: neue Datei, `fsync`, umbenennen, Ordner-`fsync` |
| `/data/config.json.good` | Letzter Stand, der erfolgreich gestartet ist | Rückfall, wenn `config.json` unlesbar ist |
| `/data/secrets.key` | Schlüssel für VNC-Passwörter (AES-GCM) | Nur für den Dienst lesbar |
| `/data/log/` | Dienst- und Sitzungsprotokoll | Rotierend, höchstens 50 MB |
| `/data/app/<version>/` | Programmversionen | `current` und `previous` als Verweise |

Die Verschlüsselung schützt Sicherungen und Exporte. Wer die SD-Karte in der Hand hat, kommt auch an
den Schlüssel. Das ist bewusst akzeptiert.

Das Sitzungsprotokoll enthält Zeit, Terminal, HMI, Modus, Dauer und Grund des Endes.

## 9. Auslieferung

**SD-Abbild (Raspis):** Gebaut mit dem offiziellen `pi-gen` (arm64) in Docker. Es enthält:

- Raspberry Pi OS Lite, labwc, Chromium, Remmina mit VNC-Plugin, TigerVNC, openbox und alle Abhängigkeiten
- `hmi-kiosk` und `hmi-kiosk-helper`
- Datenpartition `/data` (ext4), Erweiterung der Systempartition ist deaktiviert
- Overlay-Dateisystem und schreibgeschützte Boot-Partition, beim ersten Start aktiviert
- Hardware-Watchdog (`RuntimeWatchdogSec` in systemd), automatische Updates deaktiviert

Das Abbild wird mit dem Raspberry Pi Imager geschrieben. Das Gerät braucht danach kein Netz. Die erste
Einrichtung passiert in der Admin-Seite.

**Programm-Updates:** Signiertes Paket (Ed25519) über die Admin-Seite. Es landet in einem neuen
Versionsordner. Startet die neue Version nicht innerhalb von 60 Sekunden sauber, schaltet der Helfer
automatisch auf `previous` zurück. Paket- oder Systemupdates kommen als neues Abbild.

**Installationsskripte (andere Systeme):** `install.sh` installiert Pakete und Dienst, ist wiederholbar
und erhält die Konfiguration. Ohne Schreibschutz und Datenpartition. Die Pfade kommen aus `platform`.
Offline-Pakete werden je OS-Version gebündelt; bei Abweichung bricht das Skript mit klarer Meldung ab.

**Build:** `hmi-kiosk` und der Helfer sind reines Go (`CGO_ENABLED=0`, `GOARCH=arm64`), alle Web-Dateien
sind in die Binärdatei eingebettet. Es gibt keine Abrufe aus dem Internet.

## 10. Fehlerbehandlung

| Fall | Verhalten |
|---|---|
| Client stürzt ab | Rückkehr zur Kachelseite mit Hinweis; Daueranzeige verbindet neu |
| HMI nicht erreichbar | Kachel zeigt das durch eine TCP-Prüfung alle 10 Sekunden |
| Falsches VNC-Passwort | Vorabprüfung mit `rfb.Probe`: Ist `AuthOK` falsch, zeigt die Kachel einen Hinweis und es wird kein Client gestartet. Wird das Passwort danach abgelehnt, zeigt Remmina trotz `closeonfailure=1` einen Passwortdialog und endet nicht (Abschnitt 14); dann beendet die Startfrist von 15 Sekunden aus Abschnitt 4 den Client, mit Hinweis |
| Chromium stürzt ab | Neustart durch den Dienst mit wachsender Wartezeit; VNC-Sitzung läuft weiter |
| Dienst stürzt ab | systemd startet ihn neu, beim Start werden Kindprozesse aufgeräumt |
| System hängt | Hardware-Watchdog startet neu |
| Terminal nicht erreichbar | Abschnitt 5 |
| Konfiguration unlesbar | Rückfall auf `.good`, deutlicher Hinweis in der Admin-Seite |
| Datenpartition defekt oder schreibgeschützt | Kachelseite läuft mit geladener Konfiguration weiter, Admin-Seite meldet den Fehler |
| Monitor nicht gefunden | 1920×1080, Hinweis im Protokoll |
| Uhrzeit falsch | Keine Funktion hängt davon ab, nur die Protokollzeit ist betroffen |

## 11. Sicherheit

- Der Dienst läuft ohne root. Der Helfer nimmt nur feste Befehle an, über
  einen Unix-Socket mit Prüfung der aufrufenden Benutzerkennung. `set-time`, `set-ntp` und `update-activate` tragen
  je einen streng geprüften Wert, alle anderen Befehle nehmen keinen; ein nicht leerer Wert dort wird abgelehnt.
- Terminal-API: Anfragen sind per HMAC-SHA-256 mit dem gemeinsamen Schlüssel signiert. Sendet ein Gerät
  einen ändernden Befehl, holt es zuerst eine einmalige Zufallszahl vom Empfänger; so sind weder Uhren
  noch Wiederholungen ein Problem.
- Die Kachelseite ist nur auf `127.0.0.1` erreichbar und zeigt nie Adressen oder Passwörter.
- Chromium läuft mit Einschränkungen: kein Menü, keine Tastenkürzel, Navigation nur zur lokalen Adresse.
- Die VNC-Verbindung zum HMI ist je nach HMI unverschlüsselt. Das ist im abgeschotteten Anlagennetz
  akzeptiert. Der Weg zum HMI ist kein Internetweg.

## 12. Tests

**Automatisch (ohne Hardware):**

- `coord`: Tabelle aus Abschnitt 5, gleichzeitige Anfragen, Ablauf, Netzausfall, mit simulierter Uhr
- `session`: Zustandsautomat mit gefälschtem Client (Absturz, Hänger, Übernahme während des Starts)
- `client`: Remmina-Profil und Verschlüsselung gegen Testwerte, Verbindung mit der echten
  `remmina`-Version in einem Container, soweit ohne Display möglich
- `config`: Atomares Speichern, beschädigte Datei, Rückfall auf `.good`
- Integrationstest: Mehrere Dienste plus simulierter VNC-Server in Containern auf Linux (`arm64` per QEMU)

**Selbstprüfung am Gerät** (`hmi-kiosk selftest`, auch in der Admin-Seite):

1. Client startet, zeigt ein Bild eines mitgelieferten Test-VNC-Servers, Skalierung passt
2. Fensterplatz und Leiste stimmen
3. Touch: Zielpunkte antippen, Treffer prüfen
4. Rückkehr: „Zurück“, Absturz des Clients und Trennung durch den Server führen zur Kachelseite
5. Prozessende von Remmina stimmt mit der Erwartung überein
6. Andere Terminals sind erreichbar, der Schlüssel stimmt
7. Schreibschutz und Watchdog sind aktiv

**Dauerprüfung vor dem Produktivgang** auf dem ersten umgestellten Raspi: Sitzungen wiederholt starten
und beenden, mindestens 24 Stunden, dabei den Speicherbedarf beobachten (`hmi-kiosk soak`).

## 13. Inbetriebnahme und Rückfall

Es gibt kein separates Testgerät. Die erste Prüfung auf echter Hardware ist also die Inbetriebnahme.

1. Abbild auf eine neue SD-Karte schreiben. Die alte Karte bleibt unverändert und wird beschriftet.
2. Am ersten Raspi in einem Wartungsfenster Karte tauschen und einrichten.
3. Selbstprüfung, dann Dauerprüfung außerhalb der Betriebszeit.
4. Abweichungen werden korrigiert, Rückfall jederzeit durch Kartentausch.
5. Konfiguration exportieren, zweiten Raspi genauso umstellen und importieren.
6. Abstimmung zwischen beiden Geräten prüfen: Übernahme, Sperren, Vorrang, Netztrennung.
7. Stromausfall mehrfach prüfen, soweit die Anlage das zulässt.

Solange nur ein Raspi umgestellt ist, greift die Abstimmung noch nicht: Die alte Version nimmt daran nicht teil.

## 14. Offene Punkte und bestätigte Ergebnisse

### Stand der Abschlussprüfung

Stand nach Aufgabe 19. Alle automatischen Prüfungen laufen durch (Ergebnis und Zeile-für-Zeile-Abgleich der Abschnitte 1, 4, 5, 7, 10, 11 und 12 in [`docs/abgleich.md`](../../abgleich.md)). Die verbleibenden Punkte sind nur am Raspi prüfbar. Die geordnete Liste zum Abhaken steht am Ende von [`docs/inbetriebnahme.md`](../../inbetriebnahme.md), Abschnitt 12. Die Nummern H1 bis H25 unten sind dieselben wie dort.

### Entschiedene Punkte

| Punkt | Entscheidung | Wo |
|---|---|---|
| Verhalten bei falschem VNC-Passwort | Vorabprüfung mit `rfb.Probe` und Startfrist 15 s, `closeonfailure` greift nicht | Abschnitte 4 und 10, Abschnitt „Bestätigt“ unten, `internal/session` |
| Remmina-Version, Flags, Passwortformat, App-IDs, Skalierung, `viewmode` | Mit 1.4.39 in `debian:trixie` (arm64, Emulation) bestätigt | Abschnitt „Bestätigt“ unten, `docs/remmina-check/` |
| Fensterregel für Remmina | Per `app_id` und `WM_CLASS` `org.remmina.Remmina` statt Titel; die Regel greift beim ersten Anzeigen, wenn der Titel noch fehlt | `packaging/rootfs/etc/xdg/labwc/rc.xml`, Test im Abbild-Container mit openbox |
| Schreibschutz | `overlayroot=tmpfs:recurse=0`, nur per `cmdline.txt` abschaltbar, Prüfung im Abbild über `verify-image.sh` | `docs/betrieb.md`, Wartung |
| Datenpartition | Zwei Partitionen im Abbild; `/data` entsteht beim ersten Start, erst nach erfolgreichem Probe-Einhängen kommt der `fstab`-Eintrag, Dienste hängen per `RequiresMountsFor` daran | `packaging/image/`, `docs/inbetriebnahme.md` Abschnitt 4 |
| Programm-Updates | Paket enthält nur den Dienst, nie den Helfer; Rückfall nach 60 s durch den Guard, übersteht Stromausfall | `internal/update`, `docs/betrieb.md` Abschnitt 2 |
| Uhrzeit und NTP | `set-time` schaltet zuerst NTP ab, `set-ntp` schreibt die Datei und startet `systemd-timesyncd` neu | `internal/helper`, Abschnitt 11 (präzisiert) |
| Remmina-Farbtiefe | 8, 16 oder 32 (kein 24), im Admin-Formular erzwungen | `internal/web/admin_settings.go` |
| Terminal-API-Antworten | Anfragen sind signiert, Antworten nicht; ein Angreifer im Anlagennetz könnte nur „belegt“ vortäuschen | `internal/coord` |
| Daueranzeige gegen primäres Terminal | Automatische Starts und Neuverbindungen verdrängen nie einen primären Halter; nur Touch tut das | `internal/session` |
| Selbstprüfung und Daueranzeige | Selbstprüfung und Dauerprüfung werden verweigert, solange eine Daueranzeige eingestellt ist oder eine Sitzung läuft | `docs/inbetriebnahme.md` Abschnitte 7 und 8 |
| Dauerprüfung ohne Shell | Start, Stopp und Ergebnis in der Admin-Seite; die Messwerte bleiben in `soak.jsonl` | `internal/web/admin_soak.go` |
| Import auf einem anderen Terminal | Name, Kennung, Rolle und Netzwerk (NTP, WLAN) des Geräts bleiben erhalten | `docs/betrieb.md` Abschnitt 4 |
| Integrationstest | Dienste laufen im Testprozess auf amd64, nicht in Containern auf arm64 per QEMU | `test/integration/` |

### Offene Punkte (nur Hardware)

Diese Punkte sind nicht bestätigt. Jeder hat eine Nummer, die in der Liste in `docs/inbetriebnahme.md` wiederkehrt.

| Nr. | Punkt | Wird entschieden | Rückfall |
|---|---|---|---|
| H1 | Debian-Version der Raspis (Annahme Trixie) | Beim Bau, Erststart | Abbild ersetzt das System ohnehin |
| H2 | Display-Auflösung und Touch-Eingabe am iiyama | Selbstprüfung `touch`, Anzeige unter System | Auflösung überschreiben |
| H3 | HMI-Auflösung, Schärfe der Skalierung | Selbstprüfung und Betrieb | Qualität oder TigerVNC |
| H4 | Remmina-VNC-Plugin unter Wayland/labwc | Selbstprüfung | Grafikprofil X11 |
| H5 | labwc-Fensterregel (Platzierung, Größe, kein Rahmen) auf der Hardware; bisher nur mit der gleichwertigen openbox-Regel im Container bestätigt | Selbstprüfung `fenster-leiste` | Grafikprofil X11 mit openbox |
| H6 | Geteilter Zugriff bei „Mitschauen“ und bei parallelem Zugriff über PDH | Prüfung am HMI | Option aus lassen |
| H7 | VNC-Server der HMIs: Verhalten bei mehreren Clients und bei der zusätzlichen Verbindung der Vorabprüfung | Inbetriebnahme | – |

### Neue offene Punkte (aus der Umsetzung)

| Nr. | Punkt | Wird entschieden | Rückfall |
|---|---|---|---|
| H8 | Erster Start unter echtem frühem Boot: Partition anlegen bei laufender Wurzel (bisher nur mit Loop-Gerät), Probe-Einhängen, Neustart mit Overlay; Dauer ungemessen | Erststart, `hmi-kiosk-firstboot.log` | Karte neu beschreiben |
| H9 | PAM-Sitzung unter `ExecStartPre` und tty1 (logind, Anmeldung der Grafiksitzung) | Erststart | Neustart; Fehlersuche in `docs/betrieb.md` |
| H10 | Rootloses Xorg und Pi-5-Treiber im Profil X11 | Profil X11 einmal einschalten | Profil labwc |
| H11 | Namen der Overlays `disable-wifi` und `disable-bt` auf dem Pi 5, Wirkung nach Neustart | Selbstprüfung `schutz` | Funk bleibt an, nur Hinweis |
| H12 | Chromium-Sandbox ohne Paket `chromium-sandbox` (User-Namespaces) | Erststart, Protokoll | Paket ins Abbild aufnehmen |
| H13 | Mauszeiger bleibt unter labwc über Chromium und Remmina unsichtbar | Sichtprüfung | Cursor-Thema im Abbild |
| H14 | `timedatectl` und Uhrzeit setzen um die Sommerzeit-Umstellung (lokale Zeit, mehrdeutige Stunde) | Am Gerät | NTP statt Hand-Uhrzeit |
| H15 | Dauerprüfung in der Admin-Seite verliert ihren Stand bei Neustart des Dienstes (Messwerte bleiben in `soak.jsonl`) | Bei der 24-Stunden-Prüfung | Prüfung neu starten |
| H16 | Chromium-Navigationssperre (Richtlinie `URLBlocklist` `*`, `URLAllowlist` nur 127.0.0.1:8080 und localhost:8080) wirkt am Gerät; Tastenkürzel bei angeschlossener Tastatur (Abgleich 11.8; Richtlinie im Container mit Chromium 156 belegt) | Am Gerät mit Tastatur: Adresse eintippen, Kachelseite lädt weiter | Richtlinie anpassen oder entfernen |
| H17 | Chromium-Absturz während einer Sitzung: Leiste kehrt zurück, VNC läuft weiter (nur aus dem Aufbau abgeleitet) | Am Gerät, Prozess beenden | – |
| H18 | Echte Stromausfälle, auch während einer Sitzung und während der Ersteinrichtung (Rückfall `.good`, 500 „Konfiguration nicht lesbar“) | Inbetriebnahme | Karte tauschen |
| H19 | Hardware-Watchdog: Einstellung wird gemeldet, ein echter Aussetzer wird nicht erzeugt | Am Gerät | – |
| H20 | Programm-Update mit echtem Rückfall nach 60 s (Guard, Neustart) | Am Gerät | Karte tauschen |
| H21 | SSH-Regel der Anlage (Abbild: aus) und IP-Konfiguration (Abbild: DHCP) | Vor Ort | – |
| H22 | Zwei Raspis mit gleichem Hostnamen und gleicher PARTUUID im selben Netz | Zweiter Raspi | Hostnamen ändern |
| H23 | Zeit bis zum Bild nach Touch (1 bis 2 s) und Eindruck gegenüber noVNC | Betrieb | Qualität oder TigerVNC |
| H24 | Touch-Inaktivität misst nur Eingaben am echten Eingabegerät | Am Gerät, 15 min warten | – |
| H25 | Abstimmung zwischen zwei Raspis: Übernahme, Sperre, Vorrang, Netztrennung auf echten Geräten | Inbetriebnahme Abschnitt 10 | – |

### Bestätigt: Remmina in der Trixie-Version

Remmina 1.4.39 (`1.4.39+dfsg-1+deb13u1`, mit `remmina-plugin-vnc`, ohne `remmina-plugin-secret` und
`gnome-keyring`), geprüft am 2026-11-05 mit `packaging/remmina-check` in `debian:trixie` unter
linux/arm64 (Emulation), Xvfb und openbox.

| Punkt | Ergebnis |
|---|---|
| Flags | Alle Flags aus Abschnitt 6 werden angenommen |
| Erzeugtes Passwort | Wird mit 5, 8 und 12 Zeichen und mit Umlauten angenommen; `secret` bleibt unverändert, kein Klartext im Sitzungsordner |
| Trennung durch das HMI | Prozess endet nach unter 1 s |
| SIGTERM | Prozess endet sofort, auch aus dem Passwortdialog |
| Zwei App-IDs | Zwei getrennte Prozesse und Verbindungen; das Ende der einen lässt die andere bestehen |
| Gleiche App-ID | Der zweite Aufruf endet und übergibt die Verbindung an die erste Instanz; eigene IDs sind also nötig |
| Ohne Sitzungsbus | Verbindung, zwei Sitzungen und Prozessende funktionieren auch ohne D-Bus und ohne Desktop-Sitzung |
| HMI nicht erreichbar | Mit `closeonfailure=1` endet der Prozess nach etwa 3 s ohne Dialog |
| **Falsches Passwort (Abweichung)** | `closeonfailure=1` greift nicht: Remmina bleibt mit dem Dialog „Enter VNC password“ offen, ohne TCP-Verbindung zum HMI und ohne Prozessende (nach 40 s beobachtet). Kein Profilfeld verhindert das. Folge: Vorabprüfung und Startfrist aus Abschnitt 4 |
| `scale` | `1` skaliert clientseitig und behält das Seitenverhältnis; `0` zeigt das Bild mittig in Originalgröße |
| Seitenverhältnis | Passt das HMI-Format nicht zur Fläche, entstehen Balken (800×600 in 1280×752: Maßstab 1,25, Ursprung bei x=138). `scaler_expand=1` würde verzerrt füllen und wird nicht gesetzt |
| `viewmode` | `1` und `2`: normales Fenster, die Fensterregel gilt. `0`: maximiert. `3` und `4`: Vollbild. Fest eingestellt ist `1` |
| Eingaben | Klicks kommen bei `scale=1` an der richtigen Serverposition an, auch mit Balken (Abweichung höchstens 1 Pixel); bei `viewonly=1` kommen keine an |
| Toolbar und Fenster | Keine Toolbar im Bild, genau ein Fenster mit dem Titel aus dem Feld `name`; `WM_CLASS` ist unabhängig von der App-ID `org.remmina.Remmina` |

Nur unter Wayland/labwc auf der Hardware zu bestätigen: die Fensterregel, die dort gemeldete App-ID und
das Fehlen von Rahmen. Die Platzierung ist bisher nur mit einer gleichwertigen openbox-Regel unter X11
bestätigt. Bildschirmfotos liegen in `docs/remmina-check/`.
