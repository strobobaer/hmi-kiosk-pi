# Betrieb

Dieses Handbuch beschreibt den laufenden Betrieb: Rückfall, Update, Wartung, das PDH-Terminal und Fehlersuche. Die Inbetriebnahme steht in [inbetriebnahme.md](inbetriebnahme.md).

## Inhalt

1. Rückfall durch Kartentausch
2. Programm-Update einspielen (je Produkt)
3. Wartung (Konfiguration, SD-Sicherung, Schlüssel prüfen und reparieren, Wartungsmodus von Hand, Admin-Seite am Terminal, Bildschirmtastatur, Kachelseite im Netz für Tests, Web-Kacheln, PDH-Fernsteuerung)
4. PDH-Terminal (Kopplung, Sperren, Sicherung, Zertifikat, Kartenleser, Kartenleser und Statusdisplay, Windows)
5. Fehlersuche

## 1. Rückfall durch Kartentausch

Der Rückfall ist immer der Austausch der Karte. Die alte Karte wird bei der Inbetriebnahme nicht verändert.

1. Das Terminal ausschalten (Strom trennen).
2. Die neue Karte entnehmen. Beschriften: Datum, Version, Grund.
3. Die alte, beschriftete Karte einsetzen.
4. Einschalten. Das Terminal läuft wie vorher.

Beachten:

- Die alte Karte kennt keine Abstimmung mit dem anderen Terminal. Solange nur ein Terminal umgestellt ist, nimmt die alte Version nicht an der Abstimmung teil.
- Änderungen, die nach dem Tausch auf der neuen Karte gemacht wurden (HMIs, Einstellungen), stehen nicht auf der alten Karte. Vorher unter System die Konfiguration sichern (Abschnitt 3).
- Die Karte erst nach vollständigem Abschalten ziehen.

## 2. Programm-Update einspielen

Ein Update ersetzt nur das Programm des Produkts (`hmi-kiosk` bzw. `pdh-terminal`). Der Helfer und das System bleiben unverändert. Änderungen am Helfer oder an Systempaketen kommen als neues Abbild.

### Paket bauen (am Entwicklungsrechner)

1. Neue Version festlegen, zum Beispiel `1.0.1`. Sie muss im Format x.y.z stehen und sich von der laufenden Version unterscheiden.
2. Programme bauen:

   ```
   make release VERSION=1.0.1
   ```

   Das braucht `.secrets/release.pub`. Die Version im Befehl **muss** die des Pakets sein. Das gebaute Programm meldet sie dem Update-Wächter. Stimmt sie nicht, schaltet das Gerät nach 60 Sekunden zurück.
   Für das PDH-Terminal gilt dasselbe mit `make release PRODUCT=pdh-terminal VERSION=x.y.z`.
3. Paket signieren:

   ```
   scripts/dgo run ./cmd/hmi-kiosk-pack pack -product hmi-kiosk -key .secrets/release.key -version 1.0.1 -in dist -out dist/hmi-kiosk-1.0.1.pkg
   ```

   Aus dem Ordner `dist` wird nur die Programmdatei des Produkts verwendet (`hmi-kiosk` bzw. `pdh-terminal`). Der Helfer ist nie Teil eines Pakets. Ohne `-product` gilt `hmi-kiosk`.
4. Die Datei `dist/hmi-kiosk-1.0.1.pkg` ist das signierte Paket. Der private Schlüssel `release.key` bleibt auf dem Entwicklungsrechner.

### Paket als Release-Datei bereitstellen

Damit Terminals das Update selbst finden, kommt das Paket als Datei an das GitHub-Release der Version. Der Name muss genau `<produkt>-<version>.pkg` lauten, das Tag je Produkt wie in der Tabelle:

```
gh release upload v1.0.1 dist/hmi-kiosk-1.0.1.pkg
```

| Produkt | Tag | Paketdatei |
|---|---|---|
| HMI-Kiosk | `v<version>` (weiterhin so taggen, solange ältere Geräte im Einsatz sind) | `hmi-kiosk-<version>.pkg` |
| PDH-Terminal | `pdh-terminal-v<version>` | `pdh-terminal-<version>.pkg` |
| PDH-Terminal (Windows) | `pdh-terminal-v<version>` (derselbe Tag) | `pdh-terminal-<version>-windows-amd64.pkg` |

Der Tag legt das Produkt fest: Ein Terminal sieht nur Releases seines eigenen Produkts.

#### Release-Ablauf PDH-Terminal: beide Plattformen am selben Tag

Raspberry Pi und Windows erscheinen mit **derselben Version** am **selben Tag** `pdh-terminal-v<version>`. Ein Terminal wählt aus den Dateien des Releases die seiner Plattform (Pi: `pdh-terminal-<version>.pkg`, Windows: `pdh-terminal-<version>-windows-amd64.pkg`); der Name der anderen Plattform stört nicht.

1. Pi wie oben: `make release PRODUCT=pdh-terminal VERSION=x.y.z`, Paket signieren, Abbild und Bundle bauen.
2. Windows (am Entwicklungsrechner, Docker nötig, nur `x.y.z`):

   ```
   packaging/windows/build-setup.sh --version x.y.z
   ```

   Das Skript baut `pdh-terminal.exe`, `pdh-terminal-start.exe` und den Kiosk-Helfer `pdh-terminal-kiosk.exe` mit **derselben** Version und demselben öffentlichen Schlüssel (`.secrets/release.pub`, oder `HMI_RELEASE_PUB`), erzeugt mit Inno Setup (Docker-Abbild mit festem Digest, siehe Skript) das Setup und signiert das Windows-Paket. Der private Schlüssel kommt aus `HMI_RELEASE_KEY_DIR` (Standard `.secrets`) und wird nur lesend in den Container eingebunden. Ergebnis in `dist/`: `PDH-Terminal-Setup-<version>.exe`, `pdh-terminal-<version>-windows-amd64.pkg` und je eine `.sha256`.
3. Alles an dasselbe Release hängen:

   ```
   gh release create pdh-terminal-vX.Y.Z --notes "…"   # falls es noch nicht besteht
   gh release upload pdh-terminal-vX.Y.Z dist/pdh-terminal-X.Y.Z.pkg dist/pdh-terminal-X.Y.Z-windows-amd64.pkg dist/pdh-terminal-X.Y.Z-windows-amd64.pkg.sha256 dist/PDH-Terminal-Setup-X.Y.Z.exe dist/PDH-Terminal-Setup-X.Y.Z.exe.sha256
   ```

   (zusammen mit Abbild, Bundle und deren `.sha256`). Das Setup ist für Neuinstallationen, das `.pkg` für die Updates der laufenden Windows-Terminals.

**Wichtig für HMI-Kiosk:** Alle Versionen vor dieser erkennen nur Tags der Form `v<version>` (oder `<version>`). Ein Release mit dem Tag `hmi-kiosk-v<version>` bleibt für sie unsichtbar. Das Tag `hmi-kiosk-v<version>` wird erst ab dieser Version erkannt; HMI-Kiosk-Releases deshalb weiter als `v<version>` anlegen. PDH-Terminal gibt es erst ab dieser Version und nutzt `pdh-terminal-v<version>`.

Fehlt die Paketdatei (zum Beispiel, weil die Version neuere Systemteile braucht und nur als Abbild und Bundle erscheint), zeigen die Terminals die Version mit einem Verweis auf die Release-Seite an, aber ohne Knopf zum Installieren. Ein Release als Vorabversion („Pre-release“) gilt nur für Terminals, bei denen „Vorabversionen einbeziehen“ eingeschaltet ist. Entwürfe (Drafts) sieht kein Terminal.

**Wichtig:** Ein Paket, das einen höheren Systemstand verlangt (`release.SystemLevel` wurde erhöht), **nie** als Release-Datei anhängen. Terminals mit einer Version vor dem Systemstand (0.1.6 und älter) prüfen ihn nicht und würden das Paket installieren; das neue Programm liefe dann auf zu alten Systemteilen. Solche Versionen nur als Abbild und Bundle veröffentlichen.

### Produktkennung

Jedes Paket trägt seine Produktkennung. Ein Gerät lehnt ein Paket eines anderen Produkts ab, ohne etwas zu ändern (Meldung „Paket gehört zu …“).

**Grenze:** Alle Versionen vor dieser kennen die Produktkennung noch nicht. Sie lehnen ein PDH-Terminal-Paket trotzdem ab, weil dessen Programmdatei anders heißt.

### Updates per Klick

1. Admin-Seite, Menü „System“, Abschnitt „Updates“. Dort stehen die laufende Version und das Ergebnis der letzten Prüfung.
2. Das Terminal prüft einmal am Tag von selbst (die erste Prüfung einige Minuten nach dem Start) und zeigt eine neuere Version nur an. Es installiert nie von selbst. „Jetzt nach Updates suchen“ prüft sofort.
3. Wird eine Version gefunden, steht dort „Version X verfügbar“ mit dem Anfang der Release-Notiz. „Herunterladen und installieren“ drücken und bestätigen. Vorher die Konfiguration sichern und außerhalb der Betriebszeit arbeiten, wie beim Hochladen.
4. Das Terminal lädt die Paketdatei und installiert sie auf demselben Weg wie ein hochgeladenes Paket: Signatur, Systemstand, Versionsregeln und der Rückfall nach 60 Sekunden gelten unverändert. GitHub ist nur der Transportweg; ein Paket ohne gültige Signatur wird abgelehnt.
5. Quelle: Standard ist `strobobaer/hmi-kiosk-pi`. Ein leeres Feld schaltet die Prüfung aus. „Vorabversionen einbeziehen“ ist ab Werk eingeschaltet; ausschalten, wenn das Terminal nur stabile Versionen angeboten bekommen soll. Quelle und Schalter gehören zur Konfiguration und reisen mit Export und Import.

**Proxy:** Das Terminal fragt `https://api.github.com` und lädt von `github.com` (Weiterleitung auf `objects.githubusercontent.com`), nur über HTTPS. Braucht das Netz einen Proxy, ihn dem Dienst über ein systemd-Drop-in mitgeben:

```
sudo systemctl edit hmi-kiosk.service
```

```
[Service]
Environment=HTTPS_PROXY=http://proxy.firma.local:3128
Environment=NO_PROXY=localhost,127.0.0.1
```

Danach `sudo systemctl restart hmi-kiosk.service`. Auf dem SD-Abbild ist `/etc` ein Overlay im Arbeitsspeicher; das Drop-in muss dort ins Abbild bzw. bei abgeschaltetem Schreibschutz angelegt werden.

**Ohne Internet** bleibt alles wie bisher: Paket herunterladen und unter „Update hochladen“ einspielen (unten). Die Prüfung meldet dann nur „GitHub nicht erreichbar“.

### Auf dem Terminal einspielen

Ohne Internet oder für ein Paket, das nicht als Release-Datei bereitsteht:

1. Vor dem Update die Konfiguration sichern (System, „Konfiguration sichern“).
2. Außerhalb der Betriebszeit arbeiten. Das Update startet den Dienst neu und beendet laufende Sitzungen.
3. Admin-Seite öffnen, Menü „System“.
4. Notieren, welche „Version“ unter „Status“ steht.
5. Bei „Update hochladen“ die Datei wählen (höchstens 200 MB) und „Installieren“ drücken, bestätigen.
6. Das Gerät prüft die Signatur, legt die neue Version in einen neuen Versionsordner und startet den Dienst neu.
7. Zwei Minuten warten. Dann neu anmelden, falls nötig.
8. Menü „System“: Die „Version“ muss die neue sein.

### Automatischer Rückfall nach 60 Sekunden

Meldet sich die neue Version nicht innerhalb von 60 Sekunden gesund, schaltet das Gerät von selbst auf die vorherige Version zurück. Das gilt auch, wenn der Strom mitten im Update ausfällt: Der Wächter läuft nach dem Neustart weiter.

So erkennt man, dass der Rückfall stattgefunden hat:

1. Menü „System“: Die „Version“ ist **noch die alte**, obwohl das Update angenommen wurde.
2. Im „Protokoll“ steht „Dienst läuft, Version <Nummer>“. Hat die neue Version noch gestartet, folgt nach dem Rückfall erneut ein Eintrag mit der alten Version. Ein Rückfall ohne einen Eintrag der neuen Version heißt: Sie ist gar nicht bis zur Meldung gekommen.
3. Das Terminal arbeitet danach normal mit der alten Version.

Was dann zu tun ist:

1. Protokoll herunterladen (System, „Protokoll herunterladen“) und aufbewahren.
2. Prüfen, ob Paketversion und gebaute Version gleich waren (Schritte 2 und 3 oben).
3. Das Paket neu bauen oder die Ursache klären. Nicht dasselbe Paket mehrfach einspielen.

Eine zurückgeschaltete Version ist nur über erneutes Einspielen wieder erreichbar. Das Hochladen der Version, die als „vorherige“ gemerkt ist, wird abgelehnt.

## 3. Wartung

### Konfiguration sichern und wiederherstellen

1. System, „Konfiguration sichern“. Passwort für die Datei vergeben (mindestens 10 Zeichen), „Exportieren“.
2. Die Datei `.hkx` und das Passwort getrennt aufbewahren.
3. Wiederherstellen: System, „Konfiguration wiederherstellen“ (Passwort der Datei und Datei). Name, Kennung und Rolle des Terminals, Admin-Passwort und Netzwerk-Einstellungen bleiben dabei unverändert.
4. Das Admin-Passwort ist nicht in der Datei.

### SD-Sicherung

Die SD-Sicherung ist ein Abbild des ganzen Datenträgers als Datei `hmikiosk-<Terminalname>-<Datum>-<Uhrzeit>.img.gz`. Anders als die Konfiguration (oben) enthält sie auch das Betriebssystem und alle installierten Programmversionen. Es gibt zwei Wege; das Terminal wählt den passenden selbst:

- **SD-Abbild-System** (aus dem fertigen Abbild, mit Schreibschutz): der schnelle Weg, unten „Kompaktes Abbild“.
- **System ohne Schreibschutz** (Installation mit `install.sh`, z. B. Raspberry Pi OS Desktop): das Abbild wird dateiweise neu gebaut, unten „Dateiweise Sicherung“.

#### Kompaktes Abbild (SD-Abbild-System)

Das Abbild ist **kompakt**: Boot- und Systempartition werden unverändert gesichert, die Datenpartition `/data` wird neu in eine kleine Partition gepackt (belegte Bytes plus 512 Byte je Datei und Ordner für die Dateisystem-Tabellen, davon 25 % und 64 MB mehr, höchstens 1 GB). Es passt deshalb auf **jede Karte ab 16 GB**, auch auf eine größere als die Originalkarte. Die Seite zeigt vorab die erwartete Rohgröße; die Datei ist komprimiert kleiner. Dauert etwa so lange, wie die Karte zum Lesen braucht (einige Minuten).

**Erstellen** (Menü System, „SD-Sicherung“, Hinweise bestätigen):

1. **Herunterladen:** Die Sicherung wird im Browser gespeichert. Das Terminal baut zuerst die Datenpartition auf (bis zu einigen Minuten), dann beginnt der Download. Die Seite zeigt den Fortschritt; „Abbrechen“ beendet ihn.
2. **Auf USB-Stick schreiben:** Stick am Terminal einstecken (genau einer), „Auf USB-Stick schreiben“. Erlaubt sind exFAT, ext4 und NTFS; FAT32 wird abgelehnt (Dateien über 4 GB). Es müssen mindestens 4 GB frei sein. Erst bei der Meldung „Der Stick kann entfernt werden“ abziehen.

Während einer Sicherung sind Neustart, Update und Funkeinstellungen gesperrt; es läuft immer nur eine Sicherung. Die Kopie entsteht im laufenden Betrieb: Boot und System sind schreibgeschützt und damit stimmig, `/data` wird nicht eingefroren, die Kopie entspricht einem Stand nach Stromausfall (das Dateisystem wird beim Start geprüft und repariert, die Konfiguration wird atomar geschrieben). Reicht der freie Arbeitsspeicher nicht (Reserve 512 MB) oder sind die Daten zu groß, lehnt das Terminal ab und nennt den Grund.

#### Dateiweise Sicherung (System ohne Schreibschutz)

Ein System ohne Schreibschutz läuft mit beschreibbarem Dateisystem; eine Rohkopie der Karte wäre nicht stimmig. Der Helfer baut deshalb ein **neues Abbild** und kopiert die Dateien hinein: Partitionstabelle mit neuer Disk-ID, FAT für `/boot/firmware` und ext4 für `/` (Beschriftung und Kennungen der Quelle bleiben), Kopie mit `rsync -aHAXx` (Besitzer, Rechte, feste Verweise und erweiterte Attribute bleiben; dünn besetzte Dateien bleiben dünn, `-S`). In `cmdline.txt` und `/etc/fstab` des Abbilds steht danach die neue Disk-ID (`PARTUUID=`). Nicht kopiert werden `/proc`, `/sys`, `/dev`, `/run`, `/tmp`, `/mnt`, `/media`, `/lost+found`, `/var/tmp/*`, Auslagerungsdateien und der Arbeitsordner der Sicherung; die leeren Einhängeordner legt das Terminal im Abbild an.

- **Unterstützt** ist nur das Raspberry-Pi-Layout: Partition 1 FAT (`/boot/firmware`), Partition 2 ext4 (`/`) auf SD-Karte (`mmcblk`), NVMe-SSD (`nvme0n1`) oder USB-Laufwerk (`sda`), sonst nichts. Andere Layouts (weitere Partitionen, LVM, btrfs) lehnt das Terminal mit einer Meldung ab; dann den Konfigurations-Export nutzen.
- **Größe:** belegte Bytes von `/` plus 25 % plus 512 MB, die Boot-Partition so groß wie die Quelle, dazu 4 MB Vorlauf. Die Seite zeigt diese Rohgröße und den nötigen und freien Platz.
- **Dauer:** kann 15 bis 40 Minuten dauern (Kopie der Dateien, danach Komprimieren). Die Seite zeigt die Phasen „Vorbereiten“, „Kopieren“, „Komprimieren“ oder „Übertragen“ und „Fertig“ mit Fortschritt; „Abbrechen“ räumt alles auf.
- **Auf USB-Stick schreiben:** Das Terminal baut das Abbild als Rohdatei (`.img.part`) direkt auf dem Stick und komprimiert es dann zu `.img.gz`; die Rohdatei wird gelöscht. Weil Rohdatei und komprimierte Datei zugleich auf dem Stick liegen, müssen Rohgröße plus 70 % plus 256 MB frei sein, sonst lehnt das Terminal vorab ab und nennt die nötige Größe; die Seite erklärt das und nennt als Auswege einen größeren Stick oder den Download. exFAT und NTFS werden nur für root lesbar eingehängt (`umask=0077`), weil die Rohdatei /etc/shadow und Schlüssel enthält.
- **Herunterladen:** Das Abbild entsteht zuerst als Rohdatei in `/var/cache/hmi-kiosk-backup` auf der Karte (nur root, wird immer gelöscht); frei sein müssen Rohgröße plus 512 MB. Erst danach beginnt die Übertragung zum Browser: Der Browser wartet bis dahin ohne Fortschrittsanzeige, den Stand zeigt die Seite in einem zweiten Browser-Tab. Bei wenig freiem Platz auf der Karte ist nur der Download gesperrt, der Stick-Weg bleibt.
- **Programme:** `rsync`, `dosfstools`, `fdisk`, `e2fsprogs` und `util-linux` (`losetup`) bringt `install.sh` mit. Fehlt eines, nennt die Seite das Paket zum Nachinstallieren (`sudo apt install ...`).
- **Im laufenden Betrieb:** Die Dateien werden kopiert, während das System läuft. Dateien, die sich währenddessen ändern, können in einem Zwischenstand landen (wie nach einem Stromausfall); das Dateisystem wird beim Start geprüft. Dateien, die verschwinden, sind kein Fehler.
- **Beschriftung und UUID:** Solange das Abbild als Loop-Gerät besteht, trägt es dieselbe UUID und Beschriftung wie das laufende System. Das Terminal spricht nur Gerätepfade an, und das Abbild wird vor dem Ende wieder gelöst. In dieser Zeit lässt sich ein `mount UUID=…` oder `LABEL=…` von Hand nicht eindeutig auflösen.
- **Vertraulich:** Das Abbild enthält **alle Daten des Systems**, auch Benutzerordner, gespeicherte Passwörter und Schlüssel. Vertraulich aufbewahren.

Beim Zurückspielen (Raspberry Pi Imager wie unten, „Eigenes Image“, ohne OS-Anpassungen) **wächst die Systempartition beim ersten Start auf den ganzen Datenträger**: `install.sh` richtet dafür den Dienst `hmi-kiosk-grow-root` ein, der bei jedem Start die Systempartition online vergrößert und `resize2fs` ausführt (nur auf `mmcblk`, `nvme` und `sd` mit genau zwei Partitionen, Wurzel auf Partition 2; hält den Start nicht auf, höchstens 3 Minuten; idempotent, Fehler nur im Journal: `journalctl -u hmi-kiosk-grow-root`). Auch die Netzwerk-Kennung (`hmi-kiosk-identity`) ist dort eingerichtet; auf anderer Hardware bekommt die Kopie eine eigene Maschinen-ID und einen eigenen Rechnernamen (ohne Overlay werden `/etc/machine-id` und `/etc/hostname` dauerhaft geschrieben, die Kennung liegt im Datenordner unter `system/identity`). Die Wartungsmodus-Erkennung des Dienstes (Hardware-Kennung) wirkt wie beim SD-Abbild. Gesichert werden kann erst, nachdem `install.sh` dieser Fassung gelaufen ist (die Dienste müssen im Quellsystem vorhanden sein, damit sie in jeder Kopie stecken). `install.sh` startet `hmi-kiosk-identity` am Ende einmal sofort; dieser erste Lauf hält nur die Seriennummer und die aktuellen Werte fest und ändert nichts. So trägt auch eine Sicherung unmittelbar nach der Installation die Seriennummer des Quellgeräts und wird auf anderer Hardware als Klon erkannt.

**Vertraulich aufbewahren:** Das Abbild enthält alle Einstellungen, Passwörter und Schlüssel. Es gehört zu **diesem** Terminal. Auf einem anderen Gerät läuft es im Wartungsmodus (Abschnitt „SD-Sicherung auf ein anderes Gerät“); nur die Einstellungen überträgt der Konfigurations-Export.

**Zurückspielen:**

1. Raspberry Pi Imager starten, Gerät „Raspberry Pi 5“, Betriebssystem „Eigenes Image“ (unten in der Liste) und die Datei `.img.gz` wählen. Der Imager entpackt sie selbst.
2. Zielkarte (ab 16 GB) wählen. Beim Hinweis auf **OS-Anpassungen** „Nein“ wählen: Das Abbild enthält schon alles; Anpassungen des Imagers gehören nicht hinein.
3. Schreiben, Karte einsetzen, starten. Beim ersten Start **wächst die Datenpartition automatisch auf den ganzen Datenträger** (Dienst `hmi-kiosk-grow-data`, danach kein weiterer Neustart nötig). Einzelheiten stehen im Journal (`journalctl -u hmi-kiosk-grow-data`); die Boot-Partition ist dann schreibgeschützt, deshalb gibt es dort keine Protokolldatei. Scheitert das Wachsen, startet das Gerät trotzdem mit der kleinen Partition; der nächste Start versucht es erneut.

### SD-Sicherung auf ein anderes Gerät

Eine SD-Sicherung läuft auch in einem **anderen** Raspberry Pi: als Ersatz für ein ausgefallenes Gerät oder als Vorlage für ein weiteres Terminal. Das Terminal merkt den Gerätewechsel selbst. Es vergleicht beim Start die Seriennummer des Raspberry Pi mit der gespeicherten Hardware-Kennung (`/proc/device-tree/serial-number`; auf einem PC die DMI-Produkt-UUID). Ohne lesbare Kennung gibt es keine Erkennung, die Seite System zeigt dann nur einen Hinweis.

**Ersatzgerät oder neues Terminal aus der Vorlage:**

1. Sicherung zurückspielen und das Gerät mit dem Netzwerkkabel starten (Abschnitt „SD-Sicherung“, Zurückspielen).
2. Das Terminal startet im **Wartungsmodus**: Die Kachelseite zeigt oben „Wartungsmodus: Das Terminal wurde auf ein anderes Gerät übertragen. Einrichtung in der Admin-Seite.“, alle Kacheln und die Daueranzeige sind gesperrt. Das Terminal sendet keine Herzschläge, stellt keine Anfragen und antwortet den anderen Terminals mit 503, gilt dort also als nicht erreichbar.
3. Die Admin-Seite (Adresse des **neuen** Geräts, Port 8443) nach der Anmeldung leitet jede Seite auf die Wartungsseite um. Nur Abmelden, das System-Protokoll und die Wartungsseite selbst bleiben erreichbar. Die Seite zeigt Name und Rolle der Sicherung sowie die alte und die neue Hardware-Kennung (gekürzt) und bietet zwei Wege:
   - **Als Ersatz für <Name> übernehmen:** Kennung, Name und Rolle bleiben, die Hardware-Kennung wird die des neuen Geräts. Das **alte Gerät muss außer Betrieb sein**, sonst arbeiten zwei Terminals mit derselben Kennung (Bestätigung nötig).
   - **Als neues Terminal einrichten:** Das Gerät bekommt eine neue Terminal-Kennung, Name und Rolle werden neu angegeben (Regeln wie unter „Terminals“; der Name darf keinem Eintrag unter „Andere Terminals“ entsprechen). Das alte Terminal wird **nicht** automatisch eingetragen: Bei Bedarf unter „Terminals“ als anderes Terminal ergänzen, und auf dem alten Terminal das neue.
4. Nach der Wahl wird die Konfiguration gespeichert, das Zertifikat der Admin-Seite neu erzeugt (der Browser warnt einmal erneut) und der Dienst neu gestartet; danach erneut anmelden. Ist der Neustart nicht möglich, steht ein Hinweis da: das Terminal dann neu starten. Der Wartungsmodus endet mit dem Speichern.

**Rechnername und Netzwerk-Kennung:** Zwei Geräte mit demselben Rechnernamen und derselben Maschinen-ID (`/etc/machine-id`, daraus bildet der DHCP-Client seine Kennung) würden sich im Netz um dieselbe Adresse streiten. Darum läuft im SD-Abbild bei jedem Start vor dem Netzwerk der Dienst `hmi-kiosk-identity`. Er hält die Identität des Geräts in `/data/system/identity` fest (Seriennummer, Maschinen-ID, Rechnername):

| Fall | Was geschieht |
|---|---|
| Erster Start auf dem Originalgerät | Die aktuelle Maschinen-ID und der Rechnername werden festgehalten, nichts ändert sich. |
| Dieselbe Seriennummer | Maschinen-ID und Rechnername aus der Datei gelten wieder (das Overlay verwirft `/etc` bei jedem Start). |
| Andere Seriennummer (Klon) | Neue Maschinen-ID, Rechnername `hmi-kiosk-<letzte 6 Zeichen der Seriennummer>`, die Datei wird neu geschrieben. Das Gerät bekommt im Netz also eine andere Adresse als das Original. |

Der Dienst ist unabhängig vom Wartungsmodus und blockiert den Start nie; Meldungen stehen im Journal (`journalctl -u hmi-kiosk-identity`). Die Datei wird nur beachtet, wenn `/data/system` ein echter, root gehörender Ordner ist und die Datei darin ebenso; sonst geschieht nichts. Bei einer Installation mit `install.sh` (kein Abbild, kein Overlay) gibt es diesen Dienst nicht; dort den Rechnernamen von Hand ändern, wenn die Installation geklont wird.

**Der Konfigurations-Export** übernimmt die Hardware-Kennung nicht, sie bleibt lokal wie Name, Kennung und Rolle.

**Sicherungen aus älteren Versionen:** Eine Sicherung, die vor dieser Version erstellt wurde, enthält keine Hardware-Kennung. Auf einem anderen Gerät zurückgespielt, hält das Terminal dieses Gerät beim Start für das Original: Es gibt keinen Wartungsmodus. Soll es ein neues Terminal sein, es von Hand einrichten: unter „Terminals“ neuen Namen setzen und Rolle prüfen (die Terminal-Kennung lässt sich dort nicht ändern; läuft das Original weiter, entspricht das Gerät in der Abstimmung dem Original, bis es als neues Terminal angelegt wird).

**Systeme ohne Raspberry Pi** (PC, Container): Der Dienst liest dort die DMI-Produkt-UUID. Sie ist oft nur für root lesbar oder fehlt; dann gibt es keine Erkennung, und die Seite System zeigt den Hinweis zur nicht lesbaren Hardware-Kennung.

### Schlüssel prüfen und reparieren

Auf der Seite **System**, Abschnitt „Schlüssel“, steht bei jedem Aufruf der aktuelle Stand:

- **Geräteschlüssel** (`/data/secrets.key`): „passt“, wenn sich alle gespeicherten Passwörter der HMIs und der gemeinsame Terminal-Schlüssel öffnen lassen. Sonst steht dort die Liste der betroffenen Einträge (HMI-Namen, „Terminal-Schlüssel“) und ein Knopf „Reparieren“.
- **Schlüssel der anderen Terminals:** je eingetragenem Terminal „Schlüssel stimmt“, „Schlüssel falsch“ oder „nicht erreichbar“. Die Abfrage ändert nichts am anderen Terminal; je Terminal gilt ein Zeitlimit von 3 Sekunden, alle laufen gleichzeitig. Ohne Terminals steht dort „keine Terminals eingetragen“.
- **Update-Schlüssel:** Fingerabdruck (die ersten 16 Hex-Zeichen des SHA-256) des eingebauten öffentlichen Schlüssels. Steht dort „kein Schlüssel eingebaut“, sind Updates gesperrt.

**Zwei Fälle bei Schlüsselproblemen**

1. *Schlüsseldatei fehlt oder ist beschädigt, obwohl verschlüsselte Werte gespeichert sind.* Die Admin-Seite bleibt erreichbar (die Anmeldung hängt nicht am Geräteschlüssel), leitet aber nach der Anmeldung jede Seite auf `/admin/repair` um. Erreichbar bleiben Abmelden, Protokoll und die Reparaturseite. Die Kachelseite läuft weiter; ein HMI ohne lesbares Passwort zeigt beim Antippen „VNC-Passwort fehlt – bitte in der Admin-Seite neu eingeben.“ und startet keinen Client. Eine fehlende Schlüsseldatei wird **nicht** stillschweigend neu angelegt, solange verschlüsselte Werte vorhanden sind.
2. *Die Schlüsseldatei ist lesbar, aber einzelne Werte öffnen sich nicht* (zum Beispiel stammt die Datei von einem anderen Gerät). Es gibt keine Umleitung. Die System-Seite, die HMI-Liste und die betroffenen HMI-Seiten zeigen einen Hinweis mit dem Knopf „Reparieren“; das HMI verhält sich beim Antippen wie oben.

**Reparieren:** Auf der Reparaturseite „Neuen Geräteschlüssel erzeugen und betroffene Passwörter löschen“ wählen und bestätigen. Danach gilt ein neuer Schlüssel. Werte, die sich noch öffnen ließen, bleiben erhalten (neu verschlüsselt); alle anderen sind geleert. Die Seite nennt, was neu einzugeben ist, mit Links zu den betroffenen HMIs und zur Seite „Terminals“. In der HMI-Liste steht bei geleerten Passwörtern „Passwort fehlt“ (bei HMIs, die nie ein Passwort hatten, weiter „Ohne Passwort“); das Kennzeichen verschwindet, sobald ein neues Passwort gespeichert oder „Passwort entfernen“ gewählt wird.

**Reihenfolge bei Stromausfall:** Zuerst entsteht die neue Konfiguration nur im Speicher. Dann wird der alte Schlüssel als `secrets.key.old` (Modus 0600) gesichert, der neue Schlüssel atomar geschrieben und danach die Konfiguration atomar gespeichert; erst dann wird `secrets.key.old` gelöscht. Scheitert das Speichern, kommt der alte Schlüssel zurück, und es bleibt alles beim Alten. Fällt der Strom genau zwischen Schlüssel und Konfiguration aus, liegt der neue Schlüssel zur alten Konfiguration auf der Platte; beim nächsten Start erkennt der Dienst, dass `secrets.key.old` mehr Werte öffnet als der aktuelle Schlüssel, nimmt den alten Schlüssel wieder in Gebrauch und meldet die zurückgenommene Reparatur auf der System-Seite. Die Reparatur lässt sich dann wiederholen; es gehen keine Passwörter verloren. Bleibt nach einer fertigen Reparatur nur die Kopie liegen, löscht der Dienst sie beim Start. Restrisiko: Fehlte die Schlüsseldatei schon vorher, gibt es keine Kopie, die Werte waren dann aber ohnehin nicht lesbar.

### Wartungsmodus von Hand starten

Sicherungen aus der Zeit vor der Hardware-Kennung lassen sich nicht automatisch als „auf einem anderen Gerät“ erkennen. Wer eine Sicherung auf einem Ersatzgerät oder als neues Terminal einspielt, startet den Wartungsablauf selbst: Seite **System**, Abschnitt „Wartungsmodus“, Haken setzen, „Wartungsmodus starten“, bestätigen. Das Terminal verhält sich dann wie beim Erkennen eines anderen Geräts: Die Kacheln und die Daueranzeige sind gesperrt, das Terminal stimmt sich nicht mit anderen ab und antwortet ihnen mit 503. Hinweis auf der Kachelseite: „Wartungsmodus: Von Hand gestartet. Einrichtung in der Admin-Seite.“ Die Admin-Seite öffnet die Wartungsseite („Als Ersatz übernehmen“ oder „Als neues Terminal einrichten“, siehe „SD-Sicherung auf ein anderes Gerät“). Das Speichern beendet den Wartungsmodus. Auf der Wartungsseite beendet „Wartungsmodus beenden“ ihn ohne Änderung, solange es dasselbe Gerät ist (bei einem anderen Gerät ist die Entscheidung Ersatz oder neu nötig). Technisch ist es ein eigenes Kennzeichen in der Konfiguration (`terminal.maintenanceManual`); die gespeicherte Hardware-Kennung bleibt unberührt und wird beim Beenden auf die des Geräts gesetzt, sofern sie lesbar ist. Der Konfigurations-Export übernimmt das Kennzeichen nicht.

### Schreibschutz (Overlay) ausschalten

Das System läuft mit Schreibschutz: Änderungen am Betriebssystem sind nach einem Neustart weg. Nur `/data` bleibt beschreibbar. Der Schreibschutz lässt sich **nur durch eine Änderung an `cmdline.txt` auf einem PC** ausschalten. Die Admin-Seite kann es nicht. `raspi-config` erkennt die Eintragung nicht, weil sie als `overlayroot=tmpfs:recurse=0` geschrieben ist.

Ausschalten:

1. Terminal ausschalten, Karte entnehmen und in einen PC stecken.
2. Die kleine Boot-Partition (FAT) öffnen. Auf Windows heißt sie meist `bootfs`.
3. Die Datei `cmdline.txt` mit einem einfachen Texteditor öffnen. Sie besteht aus **einer einzigen Zeile**.
4. Das Wort `overlayroot=tmpfs:recurse=0` samt einem Leerzeichen davor oder danach löschen. Die Zeile bleibt eine Zeile, sonst startet das Gerät nicht.
5. Speichern, Karte auswerfen, einsetzen, einschalten.

Wieder einschalten:

1. Dieselben Schritte, aber `overlayroot=tmpfs:recurse=0` mit einem Leerzeichen am Ende der Zeile **wieder einfügen**, genau so geschrieben.
2. Der Schreibschutz kommt nicht von selbst zurück. Der Ablauf des ersten Starts läuft nur einmal.

Die Boot-Partition selbst ist im Betrieb schreibgeschützt. Der Helfer schaltet sie nur kurz für Einstellungen (WLAN, Bluetooth) auf schreibbar.

Solange der Schreibschutz aus ist, meldet die Admin-Seite unter System eine Warnung und die Selbstprüfung (`schutz`) einen Fehler. Das ist gewollt.

### WLAN einschalten

1. Menü „Netzwerk“, „WLAN einschalten“.
2. **Das Terminal neu starten** (System, „Terminal neu starten“). Erst danach ist das Funkmodul wieder da.
3. Das Land für WLAN ist im Abbild nicht gesetzt. Das Funkmodul bleibt gesperrt, bis ein Land eingestellt ist. Dazu ist Zugriff auf das Gerät nötig (SSH-Abbild).
4. Auch das Abschalten wirkt vollständig erst nach einem Neustart. Bluetooth verhält sich genauso.

### Feste IP-Adresse (Ethernet)

Menü „Netzwerk“, Abschnitt „Ethernet (IPv4)“. Er zeigt die Schnittstelle (auf dem Pi 5 `eth0`), ihre aktuelle Adresse und die gespeicherte Einstellung. „Nicht von dieser Seite verwaltet“ heißt: Es gilt die Einstellung des Systems (im Abbild DHCP).

1. „Feste Adresse“ wählen, IP-Adresse und Präfix eintragen (Standard 24; die Netzmaske, z. B. `255.255.255.0`, geht auch). Gateway und DNS sind freiwillig. Für DHCP „Automatisch (DHCP)“ wählen.
2. „Übernehmen“. Das Terminal prüft zuerst mit `arping`, ob ein anderes Gerät die Adresse schon benutzt. Wenn ja, bricht es ab: „Die Adresse … wird bereits von einem anderen Gerät benutzt.“ Während einer laufenden SD-Sicherung wird die Änderung abgelehnt.
3. Nach etwa 2 Sekunden schaltet das Terminal um. Die alte Verbindung zur Admin-Seite bricht dabei ab.
4. **Innerhalb von 2 Minuten** unter `https://<neue Adresse>:8443/admin/network` anmelden und im gelben Kasten „Bestätigen“ drücken. Eine feste Adresse lässt sich nur über diese neue Adresse bestätigen (das beweist, dass sie erreichbar ist); kurz nach dem Umschalten meldet die Seite unter Umständen „wird gerade aktiviert“, dann nach einigen Sekunden erneut bestätigen. Ohne Netzwerkkabel lehnt das Terminal eine feste Adresse ab („Kabel prüfen“). Erst dann ist die Einstellung gespeichert, auch über einen Neustart hinweg (im Abbild auf der Datenpartition, `/data/system/ethernet.json`). Bei DHCP vergibt der DHCP-Server die neue Adresse; dort nachsehen.
5. Ohne Bestätigung stellt das Terminal nach 2 Minuten die alte Einstellung wieder her. „Sofort zurücksetzen“ tut das gleich. Startet der Helfer oder das Terminal während der Frist neu, gilt ebenfalls die alte Einstellung.

Danach:

- Andere Terminals kennen dieses Terminal unter seiner Adresse: dort unter „Terminals“ die Adresse ändern.
- Die HMIs müssen im neuen Netz erreichbar sein (Kacheln prüfen).
- Der Browser zeigt die bekannte Zertifikatswarnung erneut: Das Terminal ergänzt sein Zertifikat um die neue Adresse.
- Der Import einer Konfiguration übernimmt die Ethernet-Einstellung nicht (wie WLAN, Bluetooth und NTP); eine feste Adresse gehört zu genau einem Gerät.

**Ausgesperrt?**

- Während der Frist: 2 Minuten warten, dann gilt die alte Adresse wieder.
- Nach einer Bestätigung mit falschen Werten (z. B. falsches Gateway, Netz später umgebaut): Einen Laptop direkt oder am selben Switch mit einer Adresse im neuen Netz verbinden und die Admin-Seite unter der festen Adresse öffnen; dort „Automatisch (DHCP)“ wählen. Oder direkt am Terminal: auf der Kachelseite das Zahnrad oben rechts antippen, anmelden, **Netzwerk** → „Automatisch (DHCP)“ (Abschnitt „Admin-Seite am Terminal“). DHCP lässt sich dort auch bestätigen; eine feste Adresse nur von einem PC über die neue Adresse.
- Letzter Ausweg: Die Datei `system/ethernet.json` auf der Datenpartition löschen (Karte an einem Linux-Rechner, oder SSH-Abbild: `sudo rm /data/system/ethernet.json`), danach neu starten. Ohne die Datei gilt die Einstellung des Systems (im Abbild DHCP).
- Eine dateiweise SD-Sicherung oder ein Abbild nimmt die feste Adresse mit. Die Kopie auf einem zweiten Gerät zuerst getrennt vom Original starten (z. B. direkt am Laptop) und dort DHCP oder eine andere Adresse einstellen, sonst gibt es einen Adresskonflikt.

### Ersatzkarte

Bei defekter Karte:

1. Das Abbild auf eine neue Karte schreiben (Inbetriebnahme, Abschnitt 3). Dieselbe Version wie auf dem übrigen Terminal wählen.
2. Starten, auf der Seite „Ersteinrichtung“ das Admin-Passwort festlegen („Passwort festlegen“).
3. **Vor dem Import** unter „Terminals“ Namen und Rolle dieses Terminals setzen, genau wie auf der ausgefallenen Karte. Die Sicherung ersetzt Name, Kennung und Rolle nicht, und ein Eintrag mit dem eigenen Namen in „Andere Terminals“ würde sonst das Speichern des Namens später blockieren (Inbetriebnahme, Abschnitt 9).
4. Die gesicherte Konfiguration importieren (System, „Konfiguration wiederherstellen“).
5. Das andere Terminal unter „Andere Terminals“ prüfen und bei Bedarf eintragen, Schlüssel prüfen (Status „gesetzt“), WLAN und Bluetooth abschalten, NTP eintragen.
6. Das Abbild enthält nur die Version vom Bau. Wurde später ein Update eingespielt, dasselbe Paket erneut einspielen (Abschnitt 2).
7. Selbstprüfung ausführen (Inbetriebnahme, Abschnitt 7).
8. Das andere Terminal braucht keine Änderung, wenn die neue Karte dieselbe Adresse (DHCP-Reservierung) bekommt. Sonst dort die Adresse unter „Andere Terminals“ ändern.
9. Die neue Karte erzeugt eine neue Kennung des Terminals. Das stört die Abstimmung nicht.

### SSH

SSH ist im Abbild **standardmäßig aus**. Alle Konten sind gesperrt, es gibt auch keine Anmeldung an der Konsole. Eine Eingabeaufforderung auf dem Gerät gibt es nur mit einem Abbild, das mit SSH gebaut wurde.

So baut man ein Abbild mit SSH:

1. Den öffentlichen SSH-Schlüssel des Wartungsrechners bereithalten, zum Beispiel aus `~/.ssh/id_ed25519.pub`.
2. Bauen:

   ```
   VERSION=1.0.0 HMI_ENABLE_SSH=1 HMI_SSH_PUBKEY="$(cat ~/.ssh/id_ed25519.pub)" packaging/build-image.sh
   ```

3. Das Abbild wie gewohnt schreiben (Inbetriebnahme, Abschnitt 3), ohne OS-Anpassung im Imager.
4. Anmelden als Benutzer `hmiadmin`, **nur mit Schlüssel**. Kennwortanmeldung gibt es nicht.
5. Die SSH-Host-Schlüssel werden wegen des Schreibschutzes bei jedem Start neu erzeugt. Der SSH-Client meldet deshalb nach jedem Neustart einen geänderten Host-Schlüssel.
6. Ob `hmiadmin` per `sudo` Rechte bekommt, ist bei diesem Abbild nicht geprüft.

Dieses Abbild ist ein anderes als das ohne SSH. Ob SSH in der Anlage erlaubt ist, ist eine Entscheidung der Anlage (Inbetriebnahme, Abschnitt 12). Eine Datei `ssh` auf der Boot-Partition schaltet SSH ebenfalls ein, hilft aber ohne Schlüssel nicht bei der Anmeldung.

### Admin-Seite am Terminal

Die Admin-Seite lässt sich direkt am Bildschirm des Terminals öffnen, ohne PC:

1. Auf der Kachelseite oben rechts das **Zahnrad** antippen. Chromium öffnet die Admin-Seite im selben Fenster.
2. Mit dem Admin-Passwort anmelden (dasselbe wie über `https://<IP>:8443`; dieselbe Sperre von 5 Minuten nach 5 Fehlversuchen).
3. Oben steht ein großer Knopf **Zurück zum Kiosk**. Er meldet ab und zeigt wieder die Kachelseite.

Wichtig:

- Nach **5 Minuten ohne Eingabe** meldet die Seite ab und kehrt zur Kachelseite zurück. Das gilt auch, wenn die Sitzung inzwischen abgelaufen ist oder nur die Anmeldeseite offen war. Die Sitzung am Terminal gilt auch auf dem Server nur 5 Minuten seit der letzten Anfrage; über HTTPS bleibt es bei 30 Minuten.
- Solange die Admin-Seite am Terminal offen ist, startet **keine HMI-Sitzung und keine Daueranzeige**, auch nicht nach dem Speichern einer Einstellung. Die Daueranzeige startet wieder, sobald die Kachelseite zurück ist (Zurück, Inaktivität oder Neustart von Chromium). Als offen gilt sie nach dem Antippen des Zahnrads und bei jeder Anfrage mit Anmeldung am Terminal, höchstens 5 Minuten seit der letzten; Abrufe anderer Seiten (etwa einer Web-Kachel) zählen nicht. Auf der Anmeldeseite gilt sie nur 5 Minuten ab dem Antippen des Zahnrads.
- **Gemeinsame Sperre:** Die Sperre nach 5 Fehlversuchen gilt für beide Zugänge zusammen. Wer im Netz `https://<IP>:8443` erreicht, kann mit falschen Passwörtern auch die Anmeldung am Terminal für 5 Minuten sperren. Dann 5 Minuten warten.
- Technisch läuft diese Admin-Seite über HTTP nur auf `127.0.0.1:8081` (beim PDH-Terminal: der Port der Startseite + 1, siehe „Port der lokalen Startseite“), nie im Netz (die Chromium-Richtlinie erlaubt genau diese Adresse zusätzlich zur Kachelseite). Sie hat eigene Cookies; eine Anmeldung am Terminal und eine über HTTPS stören sich nicht. Die Terminal-API (`/peer/`) gibt es dort nicht.
- Eine feste Ethernet-Adresse lässt sich am Terminal einstellen, aber nur von einem PC über die neue Adresse bestätigen (Abschnitt „Feste IP-Adresse“); DHCP lässt sich auch am Terminal bestätigen.
- Wird die Kachelseite von einem PC aus geöffnet (Kachelseite im Netz), führt das Zahnrad dort zu `https://<IP>:8443/admin/`.

### Bildschirmtastatur

Für Eingaben ohne Tastatur gibt es eine Bildschirmtastatur (wvkbd, Paket `wvkbd`, Programm `wvkbd-mobintl`). Der Kiosk-Dienst startet sie unter labwc verborgen, etwa ein Drittel der Bildschirmhöhe hoch, und startet sie nach einem Absturz neu.

- **Admin-Seite am Terminal:** Bei Touch-Bedienung erscheint sie beim Antippen eines Text-, Passwort- oder Zahlenfelds und verschwindet beim Verlassen des Felds oder beim Absenden.
- **Leiste während einer Sitzung:** Der Knopf **Tastatur** blendet sie ein und aus, etwa für Eingaben im HMI.
- **Maus-Bedienung** (Admin-Seite → Kiosk → Bedienung „Maus“): Sie erscheint nie von selbst, der Knopf in der Leiste bleibt.
- **Layout:** wvkbd hat kein deutsches Layout. Es gilt QWERTY mit Ziffernreihe; Umlaute und ß über die Taste **Cmp** und danach den Grundbuchstaben (a, o, u, s), Sonderzeichen über die zweite Ebene.
- **X11-Ausweichprofil:** Dort gibt es keine Bildschirmtastatur (wvkbd braucht Wayland). Der Knopf in der Leiste bleibt ohne Wirkung, Fehler entstehen keine.
- Fehlt das Programm (z. B. install.sh-System ohne das Paket), meldet das Protokoll „Bildschirmtastatur startet nicht“ und der Dienst versucht es in wachsenden Abständen bis 30 s erneut.

### Kachelseite im Netz (Test)

Für Tests lässt sich die Kachelseite von einem anderen PC im Netz aufrufen. Ab Werk ist das aus, und die Kachelseite antwortet nur auf `127.0.0.1:8080` (Port der Startseite; beim HMI-Kiosk fest).

1. Admin-Seite → **Kiosk** → Haken bei **Kachelseite im Netz freigeben (nur für Tests)** setzen, **Speichern**. Die Umstellung wirkt sofort, ohne Neustart des Dienstes. Eine laufende Sitzung und Chromium auf dem Terminal bleiben unberührt.
2. Am PC im Browser `http://<IP des Terminals>:8080/` öffnen. Die Adresse steht oben auf jeder Admin-Seite, solange die Freigabe an ist.
3. Nach dem Test den Haken wieder entfernen und speichern.

Wichtig:

- Die Kachelseite hat **keine Anmeldung**. Jeder im Netz kann dann HMI-Sitzungen auf diesem Terminal starten, beenden und übernehmen.
- Das Remmina-Fenster öffnet sich immer **auf dem Bildschirm des Terminals**, nicht auf dem PC. Der PC dient nur als Fernbedienung der Kacheln.
- Aufrufen lässt sich die Seite nur über die IP-Adresse mit Port 8080, nicht über einen Gerätenamen. Das schützt vor DNS-Rebinding.
- Die Selbstprüfungs-API bleibt auf das Terminal selbst beschränkt.
- Der Import einer Konfiguration übernimmt die Freigabe nicht. Jedes Gerät behält seinen eigenen Stand.
- Lässt sich Port 8080 im Netz nicht öffnen, läuft die Kachelseite lokal weiter. Die Admin-Seite zeigt dann unter **System** eine Warnung. Scheitert das Zurückstellen auf lokal, den Dienst unter **System** neu starten.

### Web-Kacheln (Grafana-Dashboards und Webseiten)

Eine Kachel kann statt eines VNC-HMI eine Webseite zeigen, etwa ein Grafana-Dashboard. Sie verhält sich wie ein HMI: Antippen öffnet sie unter der Leiste (Name, **Tastatur**, **Zurück**), **Zurück** schließt sie, Rückkehr bei Inaktivität und Daueranzeige gelten wie bei VNC.

Einrichten: Admin-Seite → **HMIs** → **Webseite anlegen** (oder im Formular **Art: Webseite** wählen). Nötig sind nur Name, Adresse, Symbol, Farbe und Erreichbarkeit.

- **Adresse:** nur `http://` oder `https://`, mit Rechner und optional Port, Pfad und Abfrage, z. B. `http://grafana:3000/d/abc/linie-1?orgId=1&kiosk`. Benutzer und Passwort in der Adresse (`http://user:pw@…`) werden abgelehnt. Ebenso Adressen auf das Terminal selbst (`localhost`, `127.0.0.1`, `::1`) und Namen, die Chromium als Zahl liest (z. B. `1.2.3`, `010.1.1.1`, `0x7f.1`). Höchstens 32 verschiedene Server (Schema, Rechner, Port) sind möglich.
- **Grafana-Menüs ausblenden:** `?kiosk` an die Dashboard-Adresse hängen (`&kiosk`, wenn schon ein `?` darin steht). `&refresh=30s` lädt die Daten regelmäßig neu.
- **Anmeldung:** Man meldet sich einmal in der Webseite an (Bildschirmtastatur über den Knopf **Tastatur**). Die Anmeldung bleibt im Web-Profil des Terminals auf der Datenpartition gespeichert (`/data/web-profile`), auch über Neustarts. Passwörter speichert Chromium nicht (Richtlinie `PasswordManagerEnabled: false`), nur die Cookies der Seite. Über einen Neustart (des Terminals oder der Web-Kachel) bleiben nur Cookies mit Ablaufdatum erhalten; ein reines Sitzungs-Cookie (ohne `Expires`/`Max-Age`) ist danach weg (gemessen). Grafana setzt sein Anmelde-Cookie mit Ablaufdatum (`login_maximum_lifetime_duration`). Grafana ohne Anmeldung geht ebenso: in `grafana.ini` `[auth.anonymous] enabled = true` (mit `org_role = Viewer`).
- **Gespeicherte Anmeldungen löschen:** Admin-Seite → **Kiosk** → **Gespeicherte Web-Anmeldungen löschen**. Das geht nur, wenn gerade keine Web-Kachel offen ist (sonst erst mit **Zurück** schließen). Danach muss man sich an jeder Webseite neu anmelden. Hinweis: Eine SD-Sicherung enthält das Web-Profil wie die übrige Datenpartition.
- **Nur derselbe Server:** Die Seite darf nur innerhalb ihres Ursprungs navigieren (Schema, Rechner und Port wie in der Adresse). Links auf andere Server bleiben gesperrt (Chromium zeigt „von Ihrem Administrator blockiert“). Das gilt auch für **Weiterleitungen auf einen anderen Anmeldeserver (SSO, z. B. Keycloak, Azure AD)**: Sie sind gesperrt, die Anmeldung muss auf demselben Server stattfinden. Auch eine Weiterleitung von `http://` auf `https://` ist ein anderer Ursprung und gesperrt: in diesem Fall gleich die `https://`-Adresse eintragen. Öffnet die Seite ein Popup (`window.open` mit Fenstergröße), erscheint es wie die Kachel unter der Leiste; ein Link mit `target=_blank` öffnet ein normales Chromium-Fenster mit Tab-Leiste, ebenfalls unter der Leiste und an dieselben Regeln gebunden, schließbar über das Tab-Kreuz (gemessen). Downloads, die Anmeldung bei Chromium und die Entwicklerwerkzeuge sind per Richtlinie abgeschaltet.
- **HTTPS mit selbstsigniertem Zertifikat:** Chromium zeigt eine Warnseite, und der Kiosk übergeht sie bewusst nicht. Im Werksnetz `http://` verwenden oder dem Server ein Zertifikat geben, dem das Terminal vertraut.
- **Keine Abstimmung:** Web-Kacheln sind lokal. Mehrere Terminals können dieselbe Webseite gleichzeitig zeigen; es gibt weder Übernahme noch Sperre noch „belegt durch“.
- **Erreichbarkeit:** Ping prüft den Rechner der Adresse, **Port** eine TCP-Verbindung zu ihrem Port (bei Webseiten unkritisch), **aus** zeigt nie „nicht erreichbar“. **Verbindung prüfen** in der HMI-Liste meldet bei Webseiten nur, ob der Port antwortet.
- **Absturz:** Endet das Chromium der Webseite unerwartet, zeigt die Kachelseite „Webseite „…“ unerwartet beendet“; eine Daueranzeige startet nach 2, 4, 8 … höchstens 30 s neu.
- **Daueranzeige ohne Leiste:** Die Web-Kachel belegt den ganzen Bildschirm (Chromium-Vollbild mit eigenem Profilordner `hmi-kiosk-web-voll`, einem Verweis auf das normale Profil, also mit derselben Anmeldung). Anmelden lässt sich ohne Leiste nicht (kein Tastatur-Knopf): vorher einmal per Touch mit Leiste öffnen und anmelden, oder anonymen Zugang nutzen.
- **Nach einer Änderung:** Eine neue oder geänderte Adresse lässt sich erst öffnen, wenn der Helfer die Richtlinie geschrieben hat (meist sofort). Gelingt das 5 s lang nicht, steht auf der Kachelseite „Die Freigabe für „…“ steht noch nicht in der Chromium-Richtlinie“; die Daueranzeige versucht es erneut.

Technik: Jede geöffnete Web-Kachel ist ein eigener Chromium-Prozess im App-Fenster (`--app=<Adresse>`, Profil `<Daten>/web-profile`, Profilordner und Klasse `hmi-kiosk-web`). Die Fensterregeln (labwc `chrome-*-hmi-kiosk-web`, openbox `class="hmi-kiosk-web"`) legen es ohne Rahmen unter die Leiste. Die Chromium-Richtlinie in `/etc/chromium/policies/managed/hmi-kiosk.json` schreibt der root-Helfer: bei jeder Änderung der Web-Kacheln (Befehl `set-web-origins`) und beim Start vor dem Kiosk-Dienst (`hmi-kiosk-helper web-policy` aus `/data/system/web-origins`), weil `/etc` auf dem Abbild bei jedem Start aus dem Paket kommt. Fest erlaubt bleiben immer die Kachelseite (`127.0.0.1:8080`, `localhost:8080`) und die Admin-Seite am Terminal (`127.0.0.1:8081`, `localhost:8081`). Chromium liest die geänderte Richtlinie im Lauf nach, ein Neustart ist nicht nötig. Schlägt das Schreiben fehl (Helfer nicht erreichbar), steht im Protokoll „Chromium-Richtlinie für Web-Kacheln nicht geschrieben“, und der Dienst versucht es alle 30 s erneut; eine Web-Kachel mit neuer Adresse öffnet bis dahin nicht (Hinweis auf der Kachelseite).

### PDH-Fernsteuerung (nur HMI-Kiosk: HMI aus PDH am Terminal zeigen)

PDH kann über die Admin-Adresse des Terminals (`https://<IP>:8443`) ein HMI auf dem **Bildschirm dieses Terminals** öffnen („Am Terminal zeigen“ bzw. „Am Terminal bedienen“) und wieder schließen. Die Schnittstelle (Version 1) steht in `docs/superpowers/specs/2026-11-09-pdh-integration.md`.

Einrichten am Terminal: Admin-Seite → **Netzwerk** → **Fernsteuerung (PDH)**.

1. **Schlüssel erzeugen** drücken. Der Schlüssel (`hmik_…`) wird **nur dieses eine Mal** angezeigt; gespeichert wird nur sein SHA-256. Sofort in PDH unter **Kiosk-Terminals** beim passenden Terminal eintragen. Neu laden der Seite zeigt ihn nicht noch einmal und erzeugt keinen neuen. Geht er verloren, einfach neu erzeugen.
2. Den **Fingerabdruck** von derselben Seite in PDH eintragen. PDH prüft damit das selbstsignierte HTTPS-Zertifikat (SHA-256 über den öffentlichen Schlüssel); eine Zertifizierungsstelle ist nicht nötig. Der Fingerabdruck bleibt gleich, wenn das Terminal sein Zertifikat erneuert (neue IP-Adresse, Ablauf). Er ändert sich nur, wenn die Schlüsseldatei verloren geht oder das Gerät im Wartungsmodus **als neues Terminal** eingerichtet wird; dann in PDH neu eintragen. Als **Ersatz** eingerichtet, bleiben Fingerabdruck und Schlüssel gleich.
3. In PDH **Verbindung prüfen**.

- **Neuen Schlüssel erzeugen** macht den alten sofort ungültig. **Aus** löscht ihn; PDH erhält dann „Schlüssel falsch“.
- **Firewall:** PDH-Server → Terminal, TCP **8443** freigeben (derselbe Port wie Admin-Seite und Terminal-Abstimmung). Kachelseite (Grundwert 8080) und Admin-Seite am Terminal (Grundwert 8081) beantworten keine Fernbefehle.
- **Falscher Schlüssel:** Nach 5 Fehlversuchen sperrt das Terminal Fernbefehle **von dieser Quelladresse** für 5 Minuten (Antwort 429), wie die Admin-Anmeldung. Bei IPv6 gilt die Sperre für das ganze /64-Netz. Die Admin-Anmeldung selbst und andere Absender bleiben frei.
- **Belegt:** Ist am Terminal eine Sitzung offen (auch die Daueranzeige oder ihr Neuverbinden), lehnt es ab; PDH zeigt „belegt“ mit HMI und Beginn. Startet oder endet gerade eine Sitzung, läuft der Wartungsmodus, eine Dauerprüfung, eine Sicherung oder ist die Admin-Seite am Terminal offen, meldet es „nicht bereit“ (503).
- **Welche Sitzung:** Gibt es eine VNC-Kachel mit gleichem Port und gleichem Host (oder demselben Host nach Namensauflösung), öffnet das Terminal **diese** und stimmt sich wie beim Antippen mit den anderen Terminals ab. In PDH deshalb **denselben Host wie in der Kachel** eintragen (gleiche Schreibweise, Name oder IP), sonst kann bei nicht auflösbarem Namen eine nur lokale Sitzung ohne Abstimmung entstehen. Hält ein anderes Terminal das HMI, heißt das „belegt“ (PDH übernimmt nie; Mitschauen bei einer Daueranzeige geht nur zum Ansehen). Ohne passende Kachel startet eine nur lokale Sitzung ohne Abstimmung; der VNC-Client verbindet immer geteilt, andere Betrachter bleiben verbunden. Passwort und Modus kommen von PDH (**ansehen** = nur ansehen); eine Kachel, die am Terminal auf **nur ansehen** steht, bleibt es aber auch bei **bedienen**. Das Terminal prüft die VNC-Anmeldung, bevor es antwortet: Lehnt das HMI das Passwort ab, meldet PDH „VNC-Anmeldung am HMI fehlgeschlagen“ (422), es startet kein Client, und keine Kachel wird markiert. Ziele auf dem Terminal selbst (localhost, 127.x, ::1) lehnt es ab. Bricht PDH vor der Antwort ab, entsteht keine Sitzung.
- Die Leiste zeigt „<Label> – von PDH: <Benutzer>“. **Zurück** und die Rückkehr bei Inaktivität gelten wie sonst; danach startet eine eingestellte Daueranzeige wieder. PDH kann nur eine **von PDH** geöffnete Sitzung schließen.
- **Protokoll:** Jeder Befehl steht mit Label, Benutzer, Modus und Quelladresse im Protokoll (`Fernsteuerung (PDH): …`), das Sitzungsprotokoll mit Modus „PDH, nur ansehen“ bzw. „PDH, bedienen“. Das VNC-Passwort wird nie gespeichert oder protokolliert; es liegt nur für die Dauer der Sitzung im RAM-Profil des VNC-Clients.
- **Export/Import:** Der Schlüssel gehört zu diesem Terminal. Der Export enthält ihn nicht, der Import ändert ihn nicht.
- **Ende-zu-Ende-Test (Entwicklung):** `PDH_DIR=<PDH-Repository> bash test/pdh-e2e/run.sh` prüft den echten PDH-Client gegen die echten Fernbefehle über TLS in zwei Docker-Containern (Build-Tag `e2e`, nur die Sitzungsverwaltung ist nachgebildet; nicht im Image).

#### PDH-Terminalmodus (PDH als Kachel)

Damit Bediener am Terminal selbst in PDH arbeiten:

1. In PDH einen **System-Benutzer** für das Terminal anlegen (nur die Rechte, die am Terminal gebraucht werden).
2. Am Terminal eine **Web-Kachel** mit der PDH-Adresse anlegen (siehe Web-Kacheln oben), z. B. `https://pdh.werk/`. PDH muss ein Zertifikat haben, dem das Terminal vertraut, oder im Werksnetz über `http://` laufen; Anmeldung und Anmeldeserver müssen auf demselben Server liegen.
3. Die Kachel einmal öffnen und als System-Benutzer anmelden. Die Anmeldung bleibt im Web-Profil des Terminals (Cookie mit Ablaufdatum nötig).
4. Personen melden sich für Aktionen in PDH per Karte oder Override an (vorhandenes PDH-Konzept).

## 4. PDH-Terminal

Das PDH-Terminal ist ein eigenes Produkt (Abbild `pdh-terminal-<Version>.img.xz`). Die Einrichtung steht in der README unter „PDH-Terminal einrichten“. Dieser Abschnitt beschreibt den Betrieb.

### Kopplung und Zustand

Die Startseite meldet das Gerät mit einem Geheimnis bei PDH an. Es entsteht bei der Kopplung, liegt auf der Datenpartition (`/data/pdh/device.json`, nur für den Dienst lesbar) und wird nie angezeigt oder protokolliert.

- **Kopplungscode:** gilt 10 Minuten und nur einmal. Ein neuer Code macht die offenen Codes desselben Benutzers ungültig. Ein falscher oder abgelaufener Code wird mit einer Meldung abgewiesen; dann wird nichts gespeichert. Nach mehreren Fehlversuchen bremst PDH die Kopplung von dieser Adresse.
- **Zustand** auf der Admin-Seite „PDH“:

| Zustand | Bedeutung | Was tun |
|---|---|---|
| gekoppelt als „<Benutzer>“, **verbunden** | PDH antwortet, das Gerät ist gültig. | Nichts. |
| **gesperrt** | In PDH gesperrt. Die Startseite zeigt „Gerät in PDH gesperrt …“. | Am Gerät die Admin-Seite öffnen (5 Sekunden oben rechts drücken), in PDH einen neuen Code erzeugen und neu koppeln. |
| **nicht erreichbar** | PDH antwortet nicht (Netz, Adresse). Die Startseite versucht es alle 30 Sekunden erneut. | Adresse und Netz prüfen. |
| **Zertifikat von PDH wird nicht vertraut** | PDH zeigt ein Zertifikat, dem das Gerät nicht vertraut. | Siehe Abschnitt „Zertifikat“ unten. |

- **Entkoppeln** (Admin-Seite „PDH“) löscht das Geheimnis nur auf dem Gerät. PDH kennt das Gerät dann weiter, bis es dort gesperrt wird: In PDH unter „Gekoppelte Geräte“ des Benutzers **Sperren** drücken. Gesperrt verliert das Gerät seine Sitzung spätestens nach 30 Sekunden.

### Sicherung und Wiederherstellung

Das Geheimnis ist **nicht** Teil der Sicherung. Bei der Sicherung als Abbild werden die Dateien unter `/data/pdh` überschrieben und entfernt, bevor die Kopie entsteht; bei der dateiweisen Sicherung ist der Ordner `<Datenordner>/pdh` ausgeschlossen (bei `install.sh` standardmäßig `/var/lib/hmi-kiosk/pdh`). Nach dem Zurückspielen fehlt das Geheimnis, auch auf einem anderen Gerät: Das Gerät muss neu gekoppelt werden (neuer Code aus PDH).

### Zertifikat (bekannte Einschränkung)

Das Gerät vertraut nur dem Zertifikatspeicher des Systems. Ein PDH mit einem Zertifikat einer internen Zertifizierungsstelle (zum Beispiel eine PDH-eigene PKI) wird in dieser Version **nicht** akzeptiert; der Zustand lautet dann „Zertifikat von PDH wird nicht vertraut“. Bis das geändert ist: PDH mit einem öffentlich vertrauenswürdigen Zertifikat betreiben, oder im abgeschotteten Werksnetz `http://` mit dem Haken „unverschlüsselt erlauben“ verwenden.

### Kartenleser

Das PDH-Terminal kann Mitarbeiterkarten lesen. Die Startseite von PDH bekommt die gelesene Kartennummer vom Gerät und meldet damit den Benutzer an (Kartensitzung in PDH: nach 2 Minuten ohne Eingabe oder mit „Abmelden“ endet sie). Das Gerät schickt die Nummer nur an die Seite, die von der eingestellten PDH-Adresse stammt. Eine aufliegende Karte wird einmal gemeldet (erst nach dem Wegnehmen und erneutem Auflegen wieder), damit sie die Kartensitzung nicht endlos verlängert.

**Wichtig:** Der Ursprung der Seite muss genau der eingestellten PDH-Adresse entsprechen. Leitet PDH auf ein anderes Schema oder einen anderen Rechnernamen weiter (zum Beispiel `http` auf `https`), stimmt der Ursprung nicht mehr, und es kommen keine Karten an: dann die endgültige Adresse eintragen. Das Terminal trägt diesen Ursprung in der Chromium-Richtlinie `LocalNetworkAccessAllowedForUrls` ein, damit Chromium den Zugriff der PDH-Seite auf das Gerät (`127.0.0.1:8080`) ohne Rückfrage erlaubt.

Einstellen unter Admin-Seite (5 Sekunden oben rechts drücken) → **Kartenleser**:

| Typ | Gerät | Hinweis |
|---|---|---|
| **aus** (Standard) | – | Keine Karte wird gelesen. |
| **PN532 über I²C** | NFC-Modul PN532 am I²C-Bus, Bus `/dev/i2c-1`, Adresse `0x24` | Bus und Adresse nur ändern, wenn das Modul anders angeschlossen ist. Die Adresse als Hex-Zahl eintragen (`0x24`). |
| **USB-Leser** | Leser, der sich wie eine Tastatur meldet | Das Gerät aus der Liste wählen oder von Hand eintragen: Kennung `vvvv:pppp`, Namensteil oder genauer Pfad (`/dev/input/event…`). Meldet sich ein Leser mit mehreren Eingabeknoten (gleiche Kennung oder gleicher Namensteil), übernimmt das Terminal alle allein; ein von Hand geändertes Feld hat Vorrang vor der Liste. Das Terminal belegt die Geräte allein; die Nummer landet nie in einem Eingabefeld. Text wird gekürzt und in Großbuchstaben gewandelt; das Format (Feld `format`) gilt hier nicht, es ist Sache des PN532. Wer einen Leser mit anderer Ausgabe braucht, setzt in `config.json` das Feld `usbFormat` (unter Windows stellt es die Admin-Seite ein): Es sagt, was der Leser tippt, und das Terminal wandelt es in die Schreibweise des PN532 (Hex, Großbuchstaben): hex bleibt, hex-rev dreht die Bytes um, dec ist die Umkehrung des PN532-Formats dec (bis 10 Ziffern 4 Byte, sonst 7). Ohne `usbFormat` bleibt der Text unverändert. |

**Anschluss des PN532** (Raspberry Pi, 3,3 V):

| PN532 | Pi (Stiftleiste) |
|---|---|
| VCC | Pin 1 (3,3 V) |
| GND | Pin 6 (GND) |
| SDA | Pin 3 (GPIO 2, SDA) |
| SCL | Pin 5 (GPIO 3, SCL) |

- **Nur 3,3 V** anschließen, nicht die 5 V des Pi.
- Am Modul den **Modus I²C** mit den Schaltern einstellen: SEL0 an (1), SEL1 aus (0), Beispiel Elechouse V3 und Nachbauten. HSU: beide aus; SPI: SEL0 aus, SEL1 an. Im Zweifel gilt die Beschriftung der Platine bzw. die Tabelle des Herstellers.
- Das Abbild schaltet den I²C-Bus ein (`dtparam=i2c_arm=on`), lädt `i2c-dev` und nimmt den Dienstbenutzer in die Gruppe `i2c` auf. Das gilt nur für das Produkt PDH-Terminal: `install.sh` legt die Gruppe `i2c` bei Bedarf an und nimmt den Dienstbenutzer nur bei diesem Produkt auf, das HMI-Kiosk-Abbild und sein `install.sh` kennen sie nicht. Bei `install.sh` auf einem vorhandenen System müssen außerdem `dtparam=i2c_arm=on` und `i2c-dev` selbst eingerichtet sein.

**Format der Nummer** (nur PN532): PDH vergleicht die Nummer als Text mit dem Eintrag beim Benutzer. Das Format muss zu den Einträgen in PDH passen.

| Format | Beispiel für die Karte mit den Bytes DE AD BE EF |
|---|---|
| **hex** (Standard) | `DEADBEEF` |
| **hex-rev** | `EFBEADDE` (Bytes umgekehrt) |
| **dec** | `4022250974` (Bytes umgekehrt, als vorzeichenlose Zahl, kleinstes Byte zuerst) |

**Karte anlernen:**

1. Karte an den Leser halten.
2. Auf der Seite „Kartenleser“ steht unter **Zuletzt gelesen** die Nummer („<UID> (vor N s)“; ohne Karte „Noch keine Karte gelesen“).
3. Diese Nummer in PDH beim Benutzer als Karte eintragen. Steht in PDH ein anderes Format (zum Beispiel Dezimalzahlen), hier das passende Format einstellen und die Karte erneut halten.

**Zustand und Selbstprüfung:** Die Seite zeigt den Zustand des Lesers (zum Beispiel „bereit“ oder die Fehlermeldung beim Öffnen des Geräts). Die Selbstprüfung des Terminals hat einen Schritt **kartenleser**; bei Typ „aus“ besteht er mit „kein Kartenleser eingestellt“, sonst prüft er, ob der Leser antwortet (Zeitgrenze 5 Sekunden).

**Leserstatus im Kartenstrom:** Der Strom des Geräts (`/api/card/events`) meldet zusätzlich zum Ereignis `card` das Ereignis `reader` mit `{"state":"ok"|"none"|"error"}`: sofort nach dem Verbinden und bei jedem Wechsel. `none` heißt Typ „aus“, `error` heißt Leser eingestellt, aber nicht verfügbar; beim Start gilt `ok`, damit die Eingabe nicht aufblitzt. Ohne Leser (`none`/`error`) zeigt die PDH-Seite die in PDH eingestellte Anmeldeart (Eingabe von Kartennummer/PIN oder Benutzer/Passwort) statt „Karte auflegen“. Ältere PDH-Seiten ignorieren das Ereignis; die Karten kommen unverändert.

| Beobachtung | Mögliche Ursache |
|---|---|
| Zustand zeigt einen Fehler beim Öffnen von `/dev/i2c-1` | I²C-Bus aus (kein `dtparam=i2c_arm=on`), `i2c-dev` nicht geladen oder Dienstbenutzer nicht in der Gruppe `i2c`. |
| PN532 antwortet nicht | Falsche Adresse, Schalter SEL0/SEL1 nicht auf I²C (SEL0 an, SEL1 aus), SDA und SCL vertauscht, 3,3 V fehlen. |
| USB-Leser fehlt in der Liste | Gerät stecken, Seite neu laden; Pfad notfalls von Hand eintragen. |
| „Zuletzt gelesen“ zeigt eine Nummer, PDH meldet nicht an | Format passt nicht zum Eintrag in PDH, oder die Karte ist in PDH nicht eingetragen. |
| Keine Karte kommt in PDH an, Seite „Kartenleser“ zeigt die Nummer | In `chrome://policy` prüfen (die Adresse ist am Kiosk gesperrt; ersatzweise die Datei `/etc/chromium/policies/managed/hmi-kiosk.json` ansehen), ob `LocalNetworkAccessAllowedForUrls` den Ursprung der PDH-Adresse enthält; die Richtlinie schreibt der Helfer beim Start und bei jeder Änderung der PDH-Adresse. Stimmt der Ursprung der Seite nicht mit der eingestellten Adresse überein (Weiterleitung auf anderes Schema oder anderen Rechner), stimmt auch der Eintrag nicht: endgültige Adresse eintragen. |
| Karten kommen nach einem Fehler nicht mehr an, bis die Seite neu geladen wird | Antwortet das Gerät auf den Kartenstrom mit 403 (falscher Ursprung) oder 503 (zu viele gleichzeitige Verbindungen), gibt der Browser das Verbinden auf. Erst beim nächsten Laden der Seite oder der Rückkehr vom Bildschirmschoner versucht die Seite es wieder. |

**USB-Leser unter Windows (Hinweis):** Das Terminal kann die Tasten eines Tastaturlesers nur am Zeitmuster erkennen, nicht am Gerät: Solange der gewählte Leser angesteckt ist, gilt eine Folge von Ziffern oder A–F mit höchstens 40 ms Abstand, die eine UID-Länge hat (8, 14 oder 20 Hexzeichen oder 10 Ziffern) und mit Enter endet, als Karte und wird nicht an andere Programme weitergegeben. Restrisiko: Ein Barcodescanner einer echten Hardware, der eine solche Folge mit Enter tippt, wird dann ebenfalls als Karte behandelt. Von anderen Programmen eingespielte Tasten (Passwortmanager, Fernwartung) und Tastatureingaben in Menschengeschwindigkeit sind nie betroffen; jede Kartentaste der echten Tastatur (0-9, A-F, Enter, Ziffernblock, Umschalt) wird erst gehalten und kommt dann um bis zu etwa 55 ms später an (40 ms Wartezeit plus die Körnung des Windows-Zeitgebers, ca. 15 ms). In Fenstern mit höheren Rechten (als Administrator gestartete Programme) kann das Terminal nichts wiederholen; dort wird nichts gehalten und der Leser nicht abgefangen: Solange ein solches Fenster im Vordergrund ist, landet die Eingabe des Lesers in diesem Fenster, ebenso wenn das Wiederholen scheitert (Meldung mit der Zahl der nicht weitergegebenen Tasten im Zustand der Seite Kartenleser). Ist der Leser nicht angesteckt, wird nichts angefasst.

### Kartenleser und Statusdisplay

Dieser Abschnitt ergänzt „Kartenleser“ um den Anschluss des PN532 über **SPI** oder **UART** und um das **Statusdisplay** (OLED SH1106, 1,3 Zoll). Alles gilt nur für das PDH-Terminal auf dem Raspberry Pi 5, alles mit 3,3 V. Die Verdrahtung mit Bildern steht in `hardware/verdrahtung/pdh-terminal-verdrahtung.pdf`, das Gehäuse in `hardware/gehaeuse/`.

**Verdrahtung** (Pin der Stiftleiste des Pi):

| Modul | Anschluss | Pi-Pin |
|---|---|---|
| OLED SH1106 | VCC, GND, SDA, SCL | 1, 9, 3, 5 (I²C-Bus; Adresse `0x3C`) |
| PN532 über I²C | VCC, GND, SDA, SCL; SEL0 an, SEL1 aus | 17, 6, 3, 5 (Adresse `0x24`) |
| PN532 über SPI | SCK, MOSI, MISO, SS; SEL0 aus, SEL1 an | 23, 19, 21, 24 (CE0); dazu 3,3 V an 17, GND an 20 |
| PN532 über UART | TXD des Moduls an Pin 10 (RXD), RXD des Moduls an Pin 8 (TXD); beide Schalter aus | 3,3 V an 17, GND an 14 |

- Das OLED nimmt Pin 1 für 3,3 V, der PN532 Pin 17; so braucht die Versorgung keinen Verteiler.
- **Korrektur (2026-11-18):** Läuft der PN532 über I²C, teilen sich OLED und PN532 **Pin 3 (SDA) und Pin 5 (SCL)**. Dafür braucht es je einen **Y-Verbinder** (oder ein Stück Lochraster). Bei SPI und UART hängt das OLED allein am I²C-Bus.
- **Schalterstellung am Modul** (Elechouse V3 und Nachbauten; im Zweifel gilt die Beschriftung der Platine): I²C: SEL0 an, SEL1 aus. SPI: SEL0 aus, SEL1 an. UART (HSU): beide aus.

**Einstellen** (Admin-Seite, 5 Sekunden oben rechts drücken):

- **Kartenleser:** Typ **PN532 (I²C)**, **PN532 (SPI)** oder **PN532 (UART)**. Gerät bei SPI `/dev/spidev0.0`, bei UART `/dev/ttyAMA0`; nur ändern, wenn das Modul anders angeschlossen ist. Die Schalterstellung steht als Hinweis neben der Auswahl.
- **Statusdisplay:** Typ **SH1106**, Bus `/dev/i2c-1`, Adresse `0x3C`. Der Knopf **Testen** führt die Prüfung der Selbstprüfung aus und zeigt das Ergebnis. Das Display zeigt CPU-Temperatur, CPU-Last, RAM-Auslastung und die IPv4-Adresse (ohne Netz „Offline“) und wird jede Sekunde neu gezeichnet. Beim Beenden oder Ausschalten wird es gelöscht. Es läuft getrennt vom Kartenleser: Fällt es aus, liest der Leser weiter, und es startet nach 5 Sekunden neu.
- Die **Selbstprüfung** hat die Schritte **kartenleser** (nennt den Bus, zum Beispiel „PN532 über SPI, Firmware 1.6“) und **statusdisplay** (bei Typ „aus“: „Statusdisplay aus“).

**Abbild und Installation:** Das Abbild des PDH-Terminals schaltet SPI und UART ein (`dtparam=spi=on` und `dtparam=uart0=on` im letzten `[all]`-Abschnitt von `config.txt`) und nimmt den Dienstbenutzer in die Gruppen `spi` und `dialout` auf. `install.sh` auf einem vorhandenen System legt die Gruppe `spi` bei Bedarf an und nimmt den Dienstbenutzer bei diesem Produkt in `spi` und `dialout` auf, schreibt aber **nichts in `config.txt`**. Bei einer Installation ohne neues Abbild die beiden Zeilen selbst am Ende von `/boot/firmware/config.txt` im letzten `[all]`-Abschnitt eintragen und neu starten:

```
dtparam=spi=on
dtparam=uart0=on
```

Außerdem den Dienstbenutzer in die Gruppen aufnehmen und den Dienst danach neu starten (sonst meldet der Leser „Kein Zugriff“):

```
sudo usermod -aG spi,dialout hmikiosk
sudo systemctl restart hmi-kiosk.service
```

`cmdline.txt` darf kein `console=ttyAMA0` enthalten (der UART an Pin 8/10 muss frei bleiben); am Pi 5 zeigt `serial0` auf den Debug-Stecker, die Standardeinstellung passt.

| Beobachtung | Mögliche Ursache |
|---|---|
| Leser meldet „SPI ist im System nicht eingeschaltet (/dev/spidev0.0 fehlt)“ | `dtparam=spi=on` fehlt in `config.txt` (Installation ohne neues Abbild): Zeile eintragen, neu starten. |
| Leser meldet „Kein Zugriff auf /dev/spidev0.0 (oder /dev/ttyAMA0): Dienstbenutzer nicht in der Gruppe spi (dialout)“ | Programm-Update ohne neues Abbild: `sudo usermod -aG spi,dialout hmikiosk`, danach den Dienst neu starten (`sudo systemctl restart hmi-kiosk`). |
| Leser meldet „UART … (/dev/ttyAMA0 fehlt)“ oder antwortet nicht | `dtparam=uart0=on` fehlt, Konsole auf `ttyAMA0` in `cmdline.txt`, TXD/RXD nicht gekreuzt, Schalter nicht beide aus. |
| PN532 über SPI antwortet nicht | Schalter nicht SEL0 aus/SEL1 an, SS nicht an Pin 24 (CE0), Dienstbenutzer nicht in der Gruppe `spi`. |
| Display bleibt dunkel, Selbstprüfung „statusdisplay“ rot | Falsche Adresse oder falscher Bus, SDA/SCL vertauscht, I²C-Bus aus; bei I²C-Leser: Y-Verbinder prüfen. |
| Nach Zurückrollen auf 0.2.x meldet der Leser einen Fehler | Gewollt: das alte Programm kennt SPI/UART nicht; das Terminal startet, die Anmeldung per Tastatur bleibt nutzbar. |

### Windows

Das PDH-Terminal läuft auch auf Windows 11 (Betriebsart „Zusatz“: ein normaler PC, auf dem das Terminal neben anderen Programmen läuft und die Karte anmeldet; oder Betriebsart „Kiosk“, siehe unten: der PC zeigt nur das Terminal). Es startet beim Anmelden, zeigt PDH in einem eigenen Edge-Fenster und liest die Karte des USB-Lesers. Kopplung, Admin-Seite und Zustände wie oben; Besonderheiten:

#### Installation

1. `PDH-Terminal-Setup-<Version>.exe` und `PDH-Terminal-Setup-<Version>.exe.sha256` von der Release-Seite laden, Prüfsumme mit `Get-FileHash` vergleichen.
2. Setup starten und die einmalige Abfrage der Benutzerkontensteuerung bestätigen (Administratorrechte). **SmartScreen:** Das Setup ist nicht mit einem Zertifikat signiert, Windows meldet „Unbekannter Herausgeber“: „Weitere Informationen“ → „Trotzdem ausführen“. Die Echtheit sichert die Prüfsumme; Updates sind davon unabhängig und werden vom Terminal selbst per Signatur geprüft.
3. Das Setup fragt auf der Seite „Betriebsart“ nach „Zusatz“ (Vorgabe) oder „Kiosk“ (siehe „Betriebsart Kiosk“); die folgenden Angaben beschreiben Zusatz. Es installiert für alle Benutzer nach `C:\Program Files\PDH-Terminal`, legt den Datenordner `C:\ProgramData\PDH-Terminal` an (Kopplung, Einstellungen, geladene Updates), installiert Tailscale (für den Rückfall über das Tailnet) still nach, falls `C:\Program Files\Tailscale\tailscale.exe` fehlt (Download von `pkgs.tailscale.com`, Prüfsumme fest im Setup; Fehler beim Download oder bei der Prüfsumme kommen nur ins Setup-Protokoll; scheitert msiexec, zeigt das Setup im interaktiven Modus einen Hinweis (still nicht); nichts davon bricht die Installation ab, und Tailscale startet den PC nie neu (`/norestart`); eine vorhandene Tailscale-Installation bleibt unberührt, ebenso bei der Deinstallation), trägt den Autostart ein (Registrierung `HKLM\...\Run`, startet `pdh-terminal-start.exe`), legt im Startmenü „PDH-Terminal“ und „PDH-Terminal Admin“ an, installiert das feste Skript `toast.ps1` für die Windows-Mitteilung in den Programmordner, setzt die Edge-Richtlinie `HideFirstRunExperience` unter HKLM (kein Erststart-Assistent im Terminal-Fenster; sie wird bei der Deinstallation nur entfernt, wenn das Setup sie angelegt hat; weitere Edge-Richtlinien, auch für lokale Netzzugriffe, setzt das Setup nicht) und öffnet in der Windows-Firewall TCP 8443 (Regel „PDH-Terminal Admin“, nur Profile Domäne und Privat, nicht Öffentlich) für die Admin-Seite aus dem Netz. Danach startet das Terminal.
4. Die Admin-Seite öffnet „PDH-Terminal Admin“ im Startmenü (`https://127.0.0.1:8443/admin/`) oder das Tray-Symbol; aus dem Netz `https://<PC>:8443`. Kopplung wie in der README, „PDH-Terminal einrichten“, Schritte 4 bis 6.

Autostart und Startmenü starten immer den **Starter** `pdh-terminal-start.exe`, nie `pdh-terminal.exe`: Nach einem Update beendet sich das Programm mit Code 4, und nur der Starter startet es in der neuen Version neu.

**Stille Verteilung** (Software-Verteilung, Skript): `PDH-Terminal-Setup-<Version>.exe /VERYSILENT /SUPPRESSMSGBOXES` als Administrator. Es erscheint kein Fenster, und das Terminal startet nicht sofort, sondern bei der nächsten Anmeldung (Autostart). Mit `/SILENT` bleibt der Fortschritt sichtbar und das Terminal startet danach.

**Update per Setup:** Ein Setup mit derselben oder einer höheren Version über eine vorhandene Installation ist ein Update: Es beendet das laufende Terminal, ersetzt Programm und Starter und lässt Datenordner und Kopplung unberührt. Ein Setup mit einer niedrigeren Version als der installierten bricht mit einer Meldung ab. Programm-Updates (Abschnitt 2, signiertes Windows-Paket) legen die neue Version unter `C:\ProgramData\PDH-Terminal\app\<Version>\` ab; der Starter nimmt die höchste gültige.

**Deinstallieren** (Einstellungen → Apps oder `unins000.exe`): Das Setup beendet zuerst `pdh-terminal-start.exe` und `pdh-terminal.exe` samt zugehörigem Edge und entfernt Programm, Autostart, Firewall-Regel „PDH-Terminal Admin“ und Startmenü. Der Datenordner bleibt, damit eine Neuinstallation die Kopplung behält (Hinweis „Kopplung und Einstellungen bleiben in C:\ProgramData\PDH-Terminal“). Ganz entfernen: den Ordner danach von Hand löschen; das Gerät in PDH zusätzlich sperren (siehe „Kopplung und Zustand“).

#### Leser einrichten

1. USB-Leser anstecken. Es muss ein Leser für **13,56 MHz** sein (Mifare/NFC-Karten), der sich als **Tastatur** meldet (Tastaturmodus, Ausgabe der Nummer mit Enter am Ende).
2. Admin-Seite → **Kartenleser** → Typ „USB-Leser“. Das Gerät aus der Liste wählen (Name und VID:PID) oder: **„Karte vorhalten“** drücken und eine Karte an den Leser halten. Das Terminal erkennt das Gerät, das zuletzt eine schnelle Ziffernfolge mit Enter geschickt hat, trägt es ein und schlägt die Schreibweise des Lesers vor (genau 8 oder 14 Hexzeichen: `hex`; nur 10 Ziffern: `dec`; im Zweifel `hex`). Ein zweites Absenden des Knopfs (zum Beispiel das Enter des Lesers auf dem fokussierten Knopf) ist gesperrt; kommt es doch an, liefert das Terminal 10 Sekunden lang das schon erkannte Ergebnis.
3. Die Seite zeigt die erkannte Nummer in allen drei Schreibweisen (hex, hex-rev, dec). Wer die Karte vom Pi kennt, sieht so, welche passt. Die Nummer in PDH beim Benutzer eintragen, Schreibweise wie dort. Wird der Leser abgezogen, zeigt das Tray-Symbol rot „Kartenleser getrennt“ und die Seite „Kartenleser“ den Zustand „Leser getrennt“; nach dem Wiederanstecken (auch an einem anderen Anschluss) geht es ohne Neustart weiter.

Wie Tasten des Lesers abgefangen werden, einschließlich Fenstern mit höheren Rechten: Absatz „USB-Leser unter Windows (Hinweis)“ im Abschnitt „Kartenleser“.

#### Benachrichtigung bei der Anmeldung

Meldet eine Karte an, während das Terminal-Fenster nicht vorne ist (ein anderes Programm ist aktiv oder das Fenster ist geschlossen), zeigt das Terminal das Ergebnis selbst an. Die Art stellt man **in PDH** ein, je Terminal beim Terminal-Benutzer neben der Anmeldeart (Hinweis dort: „Wirkt nur an Windows-Terminals in der Betriebsart Zusatz“):

| Wert | Anzeige |
|---|---|
| Windows-Mitteilung (`toast`) | Mitteilung von Windows über die Verknüpfung „PDH-Terminal“ im Startmenü (App-Kennung `PDH.Terminal`). Ein Klick auf die Mitteilung oder „Terminal öffnen“ startet den Eintrag erneut; das laufende Terminal derselben Sitzung zeigt dann sein Fenster (der zweite Start endet danach sofort; in einer anderen Sitzung geschieht nichts). Windows startet dazu `powershell.exe` mit `-File` auf `toast.ps1` im Programmordner (ohne `-EncodedCommand`, mit `-ExecutionPolicy RemoteSigned`); fehlt die Datei, steht es im Protokoll und es gibt keine Mitteilung. Ohne die Verknüpfung (zum Beispiel nach einem Kopieren des Programms ohne Setup) erscheint ebenfalls keine Mitteilung. |
| Fenster in der Mitte (`overlay`, Vorgabe bei leerer Auswahl) | Eigenes Fenster immer obenauf ohne Fokus mit Name, Ergebnis und Uhrzeit; schließt nach 4 Sekunden oder per Klick. |
| Leiste oben (`banner`) | 48 Pixel hohe Leiste über die volle Breite des Hauptbildschirms, schließt nach 4 Sekunden. |
| Terminal nach vorn (`front`) | Das Edge-Fenster wird geholt; wo Windows das nicht erlaubt, blinkt die Taskleiste. |
| Keine Benachrichtigung (`off`) | Nichts. |

Erfolg grün, Fehler rot, Abmeldung grau; Texte „Angemeldet: <Name>“, „Abgemeldet: <Name>“, bei einem Fehler die Meldung von PDH. Ist das Terminal-Fenster vorne, zeigt die PDH-Seite das Ergebnis selbst. Mehrere Werte lassen sich kombinieren. Ein PDH ohne diese Einstellung oder ein Terminal ohne den Endpunkt `/api/card/result` läuft ohne Fehler weiter, nur ohne Benachrichtigung.

#### Betriebsart Kiosk

Im Kiosk zeigt der PC nur das Terminal (zum Beispiel ein Gerät am Eingang): Ein eigener Standardbenutzer `PDH-Kiosk` meldet sich beim Start selbst an, und sein Windows hat statt des Explorers den Starter `pdh-terminal-start.exe -kiosk` als Shell. Es gibt keine Taskleiste, keinen Desktop und kein Tray-Symbol; Edge läuft im Vollbild (`--kiosk`) und startet nach einem Absturz sofort neu. Ein globales Terminal (Infobildschirm) zeigt hier seine Folien.

**Einrichten.** Auf der Seite „Betriebsart“ des Setups „Kiosk – dieser PC zeigt nur das Terminal“ wählen oder still `PDH-Terminal-Setup-<Version>.exe /VERYSILENT /SUPPRESSMSGBOXES /MODE=kiosk /RESTART=yes` (als Administrator). Ohne `/MODE` bleibt die bisherige Betriebsart (bei Neuinstallation `zusatz`); `/RESTART=yes` startet nach gelungener Einrichtung still neu, interaktiv bietet das Setup den Neustart an. Das Setup ruft verborgen `pdh-terminal-kiosk.exe -setup -app "<Programmordner>"` auf (der Helfer ist nur für das Setup gedacht und wird nicht von Hand gestartet). Er

1. legt den lokalen Standardbenutzer `PDH-Kiosk` (kein Administrator) mit einem zufälligen 32-Zeichen-Passwort an; das Passwort wird nirgends angezeigt oder in Dateien gespeichert, außer als LSA-Geheimnis für den Autologon;
2. schaltet den Autologon ein (`AutoAdminLogon`, Benutzer `PDH-Kiosk`, Passwort als LSA-Geheimnis `DefaultPassword`, nicht im Klartext);
3. setzt die Shell des Benutzers auf `"<Programmordner>\pdh-terminal-start.exe" -kiosk`;
4. sperrt für `PDH-Kiosk` Task-Manager, Sperren des PCs, Kennwortänderung, „Ausführen“, Windows-Tasten und das Kontextmenü und schaltet die Bildschirmsperre aus (der Energiesparplan bleibt unverändert);
5. gibt den Datenordner `C:\ProgramData\PDH-Terminal` nur noch den Administratoren, SYSTEM und `PDH-Kiosk` zum Schreiben (statt der Gruppe „Benutzer“): Die Kopplung ist damit nicht mehr für alle Benutzer des PCs lesbar. Das Setup setzt im Kiosk selbst keine Rechte am Datenordner (kein „Benutzer: Ändern“); bei einem Update eines Kiosks setzt es sie zur Sicherheit per `pdh-terminal-kiosk.exe -acl` neu (nur die Rechte, über die beim Einrichten gemerkte SID von `PDH-Kiosk`). Ebenso nach einem gescheiterten Rückbau, der den Kiosk bestehen lässt. Prüfen: `icacls C:\ProgramData\PDH-Terminal` zeigt nur Administratoren, SYSTEM und `PDH-Kiosk` (und keine Gruppe „Benutzer“).

Der Autostart-Eintrag (`HKLM\...\Run`) entfällt im Kiosk, weil die Shell den Starter startet. Das Protokoll des Helfers steht in `C:\ProgramData\PDH-Terminal\log\kiosk-setup.log` (`kiosk.log` im selben Ordner ist das Journal des Terminalprogramms). **Scheitert** ein Schritt, nimmt der Helfer die schon gemachten zurück; das Setup meldet den Fehler (mit Verweis auf `kiosk-setup.log`), bricht die Installation nicht ab, und der PC bleibt in der Betriebsart Zusatz (der Autostart-Eintrag bleibt bzw. wird wieder angelegt).

**Edition.** Kiosk geht nur auf Windows 11 **Pro, Enterprise (auch IoT Enterprise) oder Education** (Edition aus `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\EditionID`: `Professional*`, `Enterprise*`, `IoTEnterprise*` (zählt als Enterprise), `Education*`). Auf anderen Editionen (zum Beispiel Home) ist „Kiosk“ auf der Seite gesperrt („Der Kioskbetrieb braucht Windows 11 Pro, Enterprise oder Education.“); ein stilles `/MODE=kiosk` bricht dort mit Fehlercode ab, ohne etwas zu ändern. Meldet der Helfer selbst den Exitcode 2 (Edition nicht unterstützt), behandelt das Setup das ebenso: Meldung, Zusatz bleibt. Ein unbekannter `/MODE`-Wert bricht ebenfalls ab. **Vorhandenes Konto `PDH-Kiosk`:** Besteht schon ein lokales Konto dieses Namens, das nicht von dieser Einrichtung stammt (der Marker `KioskSID` fehlt), lehnt der Helfer die Einrichtung ab (Exitcode 1, Eintrag in `kiosk-setup.log`) und löscht nichts; der PC bleibt in Zusatz. Das Konto umbenennen oder entfernen und das Setup erneut ausführen. **Achtung stille Installation:** Scheitert nur der Kiosk-Schritt (`-setup` oder `-undo`), endet das Setup trotzdem mit Exitcode 0, damit die Installation nicht scheitert; das Ergebnis steht in `kiosk-setup.log` und im Marker `HKLM\SOFTWARE\PDH-Terminal`, Wert `Betriebsart` (`kiosk` nur, wenn der Kiosk eingerichtet ist).

**Admin-Seite im Kiosk (Geste).** Es gibt kein Tray-Symbol. Das Feld **80 × 80 Pixel oben rechts 5 Sekunden lang halten** (Finger oder Maus) öffnet die lokale Admin-Seite (Admin-Passwort wie bisher); ein kurzes Antippen tut nichts, und wer den Finger aus dem Feld zieht, bricht ab. „Zurück“ auf der Admin-Seite führt zum Terminal. Die Admin-Seite hat im Kiosk den Abschnitt „Kiosk“ mit **„Kiosk-Benutzer abmelden“** (danach meldet sich `PDH-Kiosk` wieder an) und dem Hinweis zum Weg zu Windows.

**An Windows kommen.** Am Gerät **Strg+Alt+Entf** drücken und **„Benutzer wechseln“** wählen, dann mit einem Administratorkonto anmelden. Der Kiosk-Benutzer bleibt dabei angemeldet; nach der Arbeit das Administratorkonto abmelden, dann steht der Kiosk wieder da. Aus dem Netz geht weiterhin die Admin-Seite über `https://<PC>:8443`.

**Rückbau und Wechsel.** Das Setup erneut ausführen und „Zusatz“ wählen (still `/MODE=zusatz`): Es ruft `pdh-terminal-kiosk.exe -undo` auf und trägt den Autostart wieder ein. Ein Setup mit `/MODE=kiosk` auf einem Gerät, das schon Kiosk ist (zum Beispiel ein stilles Update), ruft den Helfer nicht erneut auf und lässt Benutzer und Profil unberührt; die Einrichtung selbst ist wiederholbar (der Helfer räumt zuerst auf). Scheitert der Rückbau, bleibt die Betriebsart Kiosk eingetragen, und ein erneutes Setup mit `/MODE=zusatz` versucht ihn noch einmal. Auch die Deinstallation beendet erst das Terminal und ruft dann vor dem Löschen der Dateien `-undo` auf (es ist unschädlich, wenn nichts eingerichtet ist); scheitert es, meldet sie das (bei stiller Deinstallation nur im Setup-Protokoll), behält den Marker `kiosk` und deinstalliert trotzdem weiter. Dann das Setup erneut aufspielen und danach `pdh-terminal-kiosk.exe -undo` von Hand ausführen, oder Benutzer `PDH-Kiosk`, Autologon und Shell per Hand entfernen. Der Rückbau

- schaltet den Autologon aus und löscht das LSA-Geheimnis; von einem zuvor vorhandenen Autologon stellt er Benutzername, Domäne und Zähler (`AutoLogonCount`) wieder her, **nicht** aber `AutoAdminLogon`: Das Passwort (Klartext oder LSA-Geheimnis) lässt sich nicht sicher sichern, ein Autologon ohne Passwort liefe ins Leere. Nach dem Rückbau ist der Autologon deshalb aus; ein früherer Autologon muss mit seinem Passwort wieder eingeschaltet werden (zum Beispiel `netplwiz` oder die Winlogon-Werte von Hand);
- entfernt Shell und Sperren, löscht den Benutzer `PDH-Kiosk` samt Profil;
- stellt die Rechte des Datenordners wie in Zusatz her (Gruppe „Benutzer“: Ändern);
- lässt die Daten des Terminals (Kopplung, Einstellungen, Updates) unberührt.

**Grenzen im Kiosk.**

- **Früherer Autologon:** War vorher ein Autologon eingerichtet (Klartext-Passwort in Winlogon `DefaultPassword` oder LSA-Geheimnis), schaltet die Kiosk-Einrichtung ihn aus, löscht das Klartext-Passwort und stellt beim Rückbau nur Benutzername, Domäne und Zähler wieder her, **nicht** `AutoAdminLogon` und kein Passwort (ein Passwort wird nicht aufbewahrt). Ein früherer Autologon muss deshalb nach dem Rückbau mit seinem Passwort von Hand wieder eingeschaltet werden.
- Das Setup sperrt nichts für Administratoren, die sich über „Benutzer wechseln“ anmelden.
- Die Edge-Richtlinie `HideFirstRunExperience` aus der normalen Installation bleibt; weitere Richtlinien setzt das Setup nicht.
- Kein Tray und keine Windows-Mitteilungen im Kiosk (das Terminal ist immer vorne): Die Einstellung „Benachrichtigung bei der Anmeldung“ wirkt nur in der Betriebsart Zusatz.
- Endet der Starter (die Shell), meldet Windows `PDH-Kiosk` ab; es gibt deshalb kein „Beenden“. Fehlt Edge, zeigt das Programm ein dauerhaftes Hinweisfenster (immer obenauf, ohne Fokus, per Klick nicht zu schließen) mit „Edge nicht gefunden“ und „Strg+Alt+Entf → Benutzer wechseln (Administrator)“; es verschwindet, sobald Edge gefunden wird und startet (das Programm sucht alle 30 Sekunden neu). Ein Browser-Fenster gibt es dann nicht, die Admin-Geste hat keine Seite zum Öffnen: Ein Administrator meldet sich über „Benutzer wechseln“ an und installiert Edge.
- Ein Setup-Update über einen laufenden Kiosk beendet den Starter und damit die Sitzung von `PDH-Kiosk`: Der Bildschirm bliebe leer. Deshalb bietet das Setup am Ende den Neustart an (still nur mit `/RESTART=yes`); nach dem Neustart steht der Kiosk wieder. Programm-Updates per Paket (Abschnitt 2) laufen im Kiosk wie sonst von selbst.

#### MSI (Intune, Gruppenrichtlinie)

Für die verwaltete Verteilung gibt es `PDH-Terminal-<Version>.msi` (je PC, 64 Bit, mit `packaging/windows/build-msi.sh --version x.y.z` gebaut; nicht signiert). Inhalt wie das Setup.

- **Installieren (still):** `msiexec /i PDH-Terminal-<Version>.msi MODE=zusatz /qn /norestart /l*v C:\Windows\Temp\pdh-terminal-msi.log`. `MODE` ist `zusatz` (Vorgabe) oder `kiosk`; ohne Angabe bleibt die bisherige Betriebsart. Kiosk braucht Windows 11 Pro, Enterprise oder Education und plant einen Neustart (Rückgabe 3010; mit `/norestart` (gleichwertig `REBOOT=ReallySuppress`) startet msiexec nicht selbst neu, Intune übernimmt den Neustart; dann zeigt der Kiosk erst nach dem nächsten Neustart).
- **Intune (Branchen-App „Windows-App (Win32)“ oder MSI-Branchen-App):** Installationsbefehl wie oben; Deinstallation `msiexec /x {ProductCode} /qn /norestart`; Neustartverhalten „Gerät bei Bedarf neu starten“ für Kiosk. **Erkennungsregel:** Typ „MSI“ mit der ProductCode der Version (`build-msi.sh` gibt sie am Ende aus; sie ist je Version fest). Bei einem Update die Erkennung auf die neue ProductCode stellen; der MSI ersetzt die ältere Version selbst (MajorUpgrade, feste UpgradeCode `{4491D06A-73CE-400E-A1F7-AE1A4539DB19}`).
- **Betriebsart wechseln:** mit höherer Version `msiexec /i … MODE=kiosk /qn /norestart`; bei gleicher Version `msiexec /i … REINSTALL=ALL REINSTALLMODE=vamus MODE=kiosk /qn /norestart`. Kiosk nach Zusatz und die Deinstallation rufen den Rückbau (`pdh-terminal-kiosk.exe -undo`); bei einem Fehler läuft die Deinstallation weiter, Einzelheiten stehen im MSI-Protokoll und in `C:\ProgramData\PDH-Terminal\log\kiosk-setup.log`.
- **Tailscale (nur MSI):** Der MSI installiert Tailscale nicht (ein MSI im MSI geht wegen der Sperre des Windows Installers nicht). Die IT verteilt Tailscale getrennt, zum Beispiel als eigene Win32-App oder Software-Paket, vor oder nach dem PDH-Terminal: `msiexec /i tailscale-setup-<Version>-amd64.msi /qn TS_UNATTENDEDMODE=always` (Version und Prüfsumme stehen in `packaging/windows/tailscale.env`; Download von `https://pkgs.tailscale.com/stable/`). Erkennungsregel: Datei `C:\Program Files\Tailscale\tailscale.exe`. Das Setup (Setup.exe) lädt Tailscale dagegen selbst nach, wenn es fehlt.
- **MSI und Setup schließen sich aus:** Der MSI bricht ab, wenn das Setup installiert ist, und umgekehrt (Meldung nennt das Vorgehen: zuerst das andere deinstallieren; Kopplung und Einstellungen in `C:\ProgramData\PDH-Terminal` bleiben).

#### Grenzen unter Windows

- **Ein Terminal je PC.** Es gehört der zuerst angemeldeten Sitzung. Ein zweiter Benutzer (schneller Benutzerwechsel) bekommt keine zweite Instanz; sein Starter endet still ohne Tray-Symbol.
- **Der Datenordner ist in der Betriebsart Zusatz für alle Benutzer des PCs lesbar** (im Kiosk nicht, siehe oben): Die Gruppe „Benutzer“ hat „Ändern“, weil das Programm als angemeldeter Benutzer läuft, also auch das Gerätegeheimnis der Kopplung. In der Betriebsart Zusatz gelten die Benutzer des PCs als vertrauenswürdig. Auf gemeinsam genutzten PCs mit fremden Benutzern ist das Terminal deshalb nicht geeignet.
- **Setup ohne Code-Signatur:** SmartScreen zeigt „Unbekannter Herausgeber“ (siehe Installation).
- **Fenster mit höheren Rechten:** Karten des Lesers landen dort im Fenster (Absatz „USB-Leser unter Windows (Hinweis)“).
- Abschnitte der Admin-Seite, die den Helfer brauchen (Netzwerk, Zeit, Sicherung, Abbild), fehlen unter Windows.
- Läuft Edge nicht oder ist Port 8080 (Startseite, einstellbar, siehe „Port der lokalen Startseite“) oder 8443 belegt, zeigt das Tray-Symbol rot („Port 8080 belegt“) und die Selbstprüfung den Grund. Das Terminal bleibt dabei oben (es endet nicht, der Starter zählt es nicht als Fehlstart) und versucht Edge und die Ports alle 30 Sekunden neu; ist ein Port wieder frei, wird er ohne Neustart gebunden. Die Gesundmeldung des Starters steht trotzdem, weil der Prozess selbst gesund ist.
- Das Terminal ist ein Programm ohne Konsolenfenster. Beenden geht über das Tray-Symbol („Beenden“) oder im Task-Manager.

### Funktionen, Tastatur und Farben (ab 0.4.3)

**Reiter „Funktionen“ (Admin-Seite).** Schalter für Störung, Ticket, Rüsten, Stillstand, HMI, Wartungen, Vorgänge, Schichtplan (beide ab 0.4.5; eine ältere PDH sendet sie nicht, dann bleibt der lokale Wert) und „Folienschau als Bildschirmschoner“. Es gilt die jüngere Änderung: Jede Änderung am Terminal bekommt einen Zeitstempel und geht an PDH (`PUT /api/terminal/v1/functions`); Änderungen in PDH kommen über die Statusabfrage (alle 30 Sekunden) oder das Ereignis `settings` und gelten, wenn ihr Zeitstempel jünger ist (bei Gleichstand gewinnt PDH). Ist PDH nicht erreichbar, bleibt der lokale Wert; er wird später übertragen („noch nicht an PDH übertragen“ steht dann neben dem Schalter, auch nach einem Neustart). Eine ältere PDH-Version ohne die Schnittstelle (404) gilt als „nur lokal“: Die Seite sagt es, das Terminal fragt erst nach einer Stunde wieder. Der genaue Vertrag steht in `docs/superpowers/specs/2026-11-22-funktionen-sync-contract.md`. Gespeichert wird unter `<Datenordner>/pdh/functions.json`.

**Bildschirmtastatur nur auf Anforderung.**

- *Raspberry Pi:* Die Tastatur ist wvkbd und startet verborgen (siehe „Bildschirmtastatur“); sie erscheint nur auf Anforderung. Auf den eigenen Seiten (Admin-Seite) zeigt `keyboard.js` sie beim Fokus auf ein Textfeld und verbirgt sie danach. Neu: Auf den PDH-Seiten rufen deren Skripte `POST http://127.0.0.1:8080/api/keyboard/show|hide` auf (nur mit dem Ursprung der PDH-Adresse); das Skript auf der PDH-Seite beschreibt Abschnitt 9 des Vertrags. Bis PDH es aufruft, bleibt die Tastatur auf PDH-Seiten aus.
- *Windows:* Im Kiosk setzt die Einrichtung (`-setup`, nur dort) im Benutzer-Hive von PDH-Kiosk `HKCU\Software\Microsoft\TabletTip\1.7\TouchKeyboardTapInvoke = 2` und `EnableDesktopModeAutoInvoke = 1`: Die Windows-Tastatur erscheint beim Antippen eines Eingabefelds in Edge, auch mit angeschlossener Tastatur (der USB-Kartenleser meldet sich als Tastatur). Der Rückbau entfernt beide Werte. Außerhalb des Kiosks (Betriebsart Zusatz) ändert das Terminal nichts an den Windows-Einstellungen: dort gilt die Einstellung des Benutzers (Einstellungen › Zeit und Sprache › Eingabe › Bildschirmtastatur).

**Uhr und Abgleich.** Ein Gerät mit unplausibler Uhr (vor 2025, kein NTP-Abgleich) verliert lokale Änderungen gegen PDH, weil deren Zeitstempel zu alt sind; die Seite „Funktionen“ zeigt dann einen Hinweis. Der Ereigniskanal wird nach 90 Sekunden ohne Daten neu aufgebaut, die Statusabfrage (30 Sekunden) bleibt der Rückfall.

**Farben.** Admin-Seite, Startseite/Meldeseiten und Offline-Folienschau haben den Hintergrund der Terminalseite von PDH (dunkel `#0f1117`, Flächen `#1a1d27`/`#222536`; hell `#f3f3f3`). Sendet PDH im Feld `theme` eigene Farben, gelten diese (gespeichert in `<Datenordner>/pdh/theme.json`).

### Umzug beim Neukoppeln (ab 0.4.4)

Koppelt man ein Terminal neu, während lokal noch eine Kopplung gespeichert ist (auch eine, die PDH widerrufen hat oder die nie funktionierte), sendet das Terminal im Koppelaufruf zusätzlich `"previous": {"device_id": ..., "secret": ...}`. PDH übernimmt das Terminal dann aus dem alten Benutzer und antwortet mit `"moved": true`; die Admin-Seite (Reiter PDH) zeigt „Terminal aus altem Benutzer übernommen“. Das alte Geheimnis geht nur an dieselbe PDH-Adresse, mit der das Gerät gekoppelt wurde, nie an eine geänderte. Nach Erfolg ersetzt die neue Kopplung die alte; schlägt das Koppeln fehl, bleibt die alte unverändert. Geheimnisse (alt wie neu) werden nicht protokolliert. Das braucht ein PDH mit Umzugsunterstützung; eine ältere PDH ignoriert `previous`, sendet kein `moved` und das Terminal verhält sich wie bisher: Dann das alte Gerät in PDH von Hand löschen.

### Zahnrad in PDH (ab 0.4.5)

Jede Anfrage an PDH trägt zusätzlich `X-Terminal-Admin-URL: https://<IP des Geräts>:<Admin-Port>/admin/` (IPv6 in eckigen Klammern). Die IP ist die, über die das Gerät PDH erreicht; sie wird höchstens alle 5 Minuten neu bestimmt. PDH zeigt damit am Terminal ein Zahnrad, das zur Admin-Seite führt. Lässt sich die Adresse nicht bestimmen, fehlt die Kopfzeile und PDH zeigt kein Zahnrad. Zugangsdaten sind nie enthalten. Das braucht ein PDH, das die Kopfzeile auswertet; ältere PDH ignorieren sie.

**Nur am echten Gerät prüfbar (Gerätetest vor dem Release):**

1. Pi: Auf der Admin-Seite zeigt wvkbd sich beim Antippen eines Textfelds und verschwindet beim Verlassen; kein Flackern beim Wechsel zwischen zwei Feldern.
2. Pi: Auf einer PDH-Seite mit Eingabefeld (Anmeldung mit PIN/Passwort) erscheint die Tastatur, sobald PDH das Skript mit `/api/keyboard/show|hide` liefert; Fehlermeldung „Ursprung nicht erlaubt“ erscheint nicht (PDH-Adresse stimmt mit dem Origin überein).
3. Windows-Kiosk: Nach `-setup` und Neustart erscheint die Windows-Tastatur beim Antippen eines Feldes in Edge (Touch), und zwar auch mit angeschlossenem USB-Kartenleser; sie verschwindet beim Verlassen. Nach dem Rückbau sind die beiden TabletTip-Werte entfernt. (Der Wert `TouchKeyboardTapInvoke` und das Verhalten von Edge im Kiosk-Modus sind auf der Zielversion von Windows zu bestätigen.)
4. Farben: Admin-Seite, Startseite, Meldeseiten und Offline-Folienschau zeigen denselben Hintergrund wie die PDH-Terminalseite (kein sichtbarer Sprung beim Wechsel).
5. Abgleich gegen ein PDH mit der neuen Schnittstelle: am Terminal ändern, in PDH ändern (jünger gewinnt), PDH kurz trennen und wieder verbinden.

### Port der lokalen Startseite (ab 0.4.6)

Die Startseite des Terminals läuft nur auf diesem Rechner (`127.0.0.1`), ab Werk an **Port 8080**, die lokale Admin-Seite einen Port darüber (**8081**). Ist 8080 auf einem Rechner gesperrt oder belegt (Windows meldet dann „Startseite nicht verfügbar: listen tcp 127.0.0.1:8080: bind: An attempt was made to access a socket in a way forbidden by its access permissions“; das passiert oft, wenn Hyper-V, WSL oder Docker den Bereich reserviert haben), lässt sich der Port ändern:

1. Admin-Seite über HTTPS öffnen (`https://<IP>:8443/admin/`, unter Windows das Tray-Symbol oder die Verknüpfung „PDH-Terminal Admin“; sie ist unabhängig von 8080 erreichbar).
2. Reiter **PDH**, Abschnitt **Lokale Startseite**, Feld **Port der lokalen Startseite**: neuen Port eintragen (leer oder 8080 ist der Grundwert) und speichern.
3. Das Terminal startet danach selbst neu und zeigt die Startseite am neuen Port; die lokale Admin-Seite liegt am Port darüber. Der Abschnitt zeigt den tatsächlich benutzten Port und, falls die Startseite nicht gebunden werden konnte, den Grund.

Regeln: erlaubt sind **1024 bis 65534**; weder der Port noch der Port darüber darf der Port der HTTPS-Admin-Seite sein (ab Werk 8443). Ungültige Werte lehnt die Admin-Seite beim Speichern mit einer Meldung ab; eine Konfigurationsdatei mit ungültigem Wert gilt wie jede andere ungültige Datei (Rückfall auf den letzten guten Stand). Der Wert gehört zum Rechner und steht in `config.json` unter `kiosk.local_port` (fehlt der Wert oder ist er 0, gilt 8080); Export und Import übernehmen ihn nicht. Mit dem Grundwert bleibt alles wie vor 0.4.6 (Chromium-Richtlinie, Adressen, Verknüpfungen).

- *Raspberry Pi:* Der Dienst schreibt den Port über den root-Helfer (Befehl `set-local-port`, Datei `<Daten>/system/local-port`) in die Chromium-Richtlinie `/etc/chromium/policies/managed/hmi-kiosk.json`. Sie erlaubt genau `127.0.0.1` und `localhost` an Port und Port+1, nichts weiter; derselbe Stand wird beim Start des Helfers wieder geschrieben. Die Admin-Seite setzt die Richtlinie **vor** dem Speichern und prüft zuvor, ob sich der Port und der Port darüber auf diesem Rechner öffnen lassen. Misslingt eins von beidem (zum Beispiel mit einem älteren Helfer ohne den Befehl), steht der neue Port **nicht** in der Konfiguration; die Meldung lautet „Browser-Richtlinie konnte nicht angepasst werden – Port nicht geändert (Systemteile aktualisieren)“. Dafür braucht das Gerät Systemteile mit dem Helferbefehl (neues SD-Abbild oder `install.sh` aus dem Bundle). Der Dienst gleicht die Richtlinie beim Start nur ab, wenn der Port vom Grundwert abweicht oder die Richtlinie noch auf einem anderen Port stand.
- *Port lässt sich beim Start nicht binden:* Die HTTPS-Admin-Seite startet trotzdem (nie von der Startseite abhängig), die Meldung nennt den Port, das Binden wird alle 30 Sekunden neu versucht. Ist der eingestellte Port nicht 8080 und 8080 frei, läuft die Startseite auf 8080 („Eingestellter Port X nicht verfügbar, läuft auf 8080“ unter **System**); die gespeicherte Einstellung bleibt, bis man sie ändert.
- *Windows:* Es gibt keine Richtlinie und keine festen Adressen in Starter, Autostart oder Setup; der Starter startet das Programm nach dem Speichern neu, Edge bekommt die Adresse am neuen Port. Ist der Port belegt oder gesperrt, zeigt das Tray-Symbol rot „Port <Port> belegt, Startseite nicht verfügbar – Port im Admin-Bereich ändern“.
- *PDH:* Bei einem anderen Port als 8080 trägt jede Anfrage an PDH zusätzlich `X-Terminal-Local-Port: <Port>`. Die PDH-Seiten erreichen das Terminal (Kartenstrom, Bildschirmtastatur) unter `http://127.0.0.1:<Port>`; das Skript der PDH-Seite (`CARD_ORIGIN`) muss den Port aus der Kopfzeile verwenden, sonst arbeiten Kartenleser und Tastatur in den PDH-Seiten nur am Grundwert 8080. Die Startseite und die Anmeldung bei PDH laufen unabhängig davon.
- Die HMI-Kiosk-Variante bleibt bei 8080/8081 (kein Feld in der Admin-Seite).

### Bekannte Einschränkungen

- Die Bildschirmtastatur erscheint auf den PDH-Seiten erst, wenn deren Skript die Tastatur-Schnittstelle des Geräts aufruft (Vertrag, Abschnitt 9); auf der Admin-Seite des Geräts funktioniert sie bereits.

## 5. Fehlersuche

### Meldungen auf dem Bildschirm

| Meldung | Wo | Mögliche Ursache | Was tun |
|---|---|---|---|
| Kachel „nicht erreichbar“ | Kachelseite, unter dem Namen des HMI | Das HMI antwortet nicht auf Ping (Standard der Erreichbarkeitsprüfung, siehe unten) oder, je nach Einstellung, nicht auf dem VNC-Port. Netzwerkkabel, Adresse, Strom des HMI; blockiert eine Firewall Ping, am HMI die Prüfart ändern. | In der Admin-Seite unter HMIs „Verbindung prüfen“. Zeigt es „nicht erreichbar“, Adresse und Port prüfen, Kabel und Schalter prüfen, VNC-Dienst am HMI starten. |
| „VNC-Passwort wurde abgelehnt – bitte in der Admin-Seite prüfen.“, Kachel „Passwort falsch“ | Hinweis nach dem Antippen oder beim Start der Daueranzeige; Kachel | Das gespeicherte Passwort stimmt nicht mit dem VNC-Server überein. Das HMI ist als „Passwort abgelehnt“ markiert: Antippen meldet nicht mehr an, die Daueranzeige versucht es nicht erneut (sonst sperrt der Server die Adresse, siehe unten). Es wird kein Client gestartet. | Unter HMIs beim HMI „Bearbeiten“, richtiges VNC-Passwort eintragen und speichern (löscht die Markierung), dann „Verbindung prüfen“. Anmeldung muss „erfolgreich“ zeigen. Auch eine erfolgreiche Prüfung löscht die Markierung. Der Import übernimmt die Markierung nicht. |
| „Der VNC-Server hat dieses Terminal vorübergehend gesperrt (zu viele Fehlversuche) …“ | Hinweis auf der Kachelseite, Ergebnis von „Verbindung prüfen“ | RealVNC Server hat die Adresse dieses Terminals gesperrt (Meldung „Too many security failures“). | Siehe „RealVNC-Sperre“ unten. |
| „belegt durch <Name>“ | Kachel | Ein anderes Terminal hält das HMI und der Modus ist „Sperre“, oder das haltende Terminal ist primär. | Das andere Terminal mit „Zurück“ beenden lassen. Nach wenigen Sekunden ist die Kachel frei. Oder Vorrang bzw. Modus ändern (Inbetriebnahme, Abschnitt 10). |
| „Schlüssel falsch: <Name>“ | Hinweisleiste oben auf der Kachelseite | Der gemeinsame Schlüssel dieses Terminals ist nicht der des Terminals <Name>. Die Abstimmung läuft dann nicht. | Auf beiden Terminals unter „Terminals“ den Schlüssel prüfen. Am besten auf einem neuen erzeugen und auf dem anderen von Hand eintragen. Danach die Selbstprüfung (`terminals`) wiederholen. |
| „Nicht erreichbar: <Name>“ | Hinweisleiste oben auf der Kachelseite | Das andere Terminal antwortet nicht. | Adresse unter „Terminals“, Netz und Strom des anderen Terminals prüfen. Die Anzeige bleibt nutzbar, kurzzeitig können zwei Terminals am selben HMI hängen. |
| „Keine Verbindung zu <HMI> zustande gekommen“ | Hinweis nach dem Antippen | Der Client hat in 15 Sekunden keine Verbindung hergestellt. | HMI prüfen (siehe „nicht erreichbar“). Unter Client das Profil wechseln. |
| „Verbindung zu <HMI> verloren“ / „beendet“ | Hinweis | Das HMI hat die Verbindung getrennt oder das Netz war weg. | HMI und Netz prüfen. Neu verbinden. |
| „VNC-Passwort fehlt – bitte in der Admin-Seite neu eingeben.“ | Hinweis nach dem Antippen einer Kachel | Das Passwort des HMI fehlt oder lässt sich mit dem Schlüssel des Geräts nicht öffnen. Es wird kein Client gestartet. | Passwort des HMI unter „Bearbeiten“ neu eintragen. Betrifft es mehrere HMIs, unter System „Reparieren“ (Abschnitt „Schlüssel prüfen und reparieren“). |
| „Wartungsmodus: Das Terminal wurde auf ein anderes Gerät übertragen.“ | Hinweis auf der Kachelseite, Wartungsseite in der Admin-Seite | Der Datenträger (SD-Karte, SSD oder USB) läuft in einem anderen Raspberry Pi als dem, auf dem sie eingerichtet wurde. | Admin-Seite öffnen und „Als Ersatz übernehmen“ oder „Als neues Terminal einrichten“ wählen (Abschnitt „SD-Sicherung auf ein anderes Gerät“). |
| „Das Layout dieses Datenträgers (SD-Karte, SSD oder USB-Laufwerk) wird von der dateiweisen Sicherung nicht unterstützt (…)“ | Seite SD-Sicherung, Knöpfe gesperrt | System ohne Schreibschutz, dessen Karte nicht das Raspberry-Pi-Layout hat (weitere Partitionen, LVM, btrfs). | Konfigurations-Export nutzen (Abschnitt „SD-Sicherung“, Dateiweise Sicherung). |
| „Dieses HMI gibt es nicht mehr“ | Hinweis | Das HMI wurde gelöscht, während es genutzt wurde. | Kachelseite neu laden lassen. Daueranzeige unter Kiosk prüfen. |

### RealVNC-Sperre und Erreichbarkeitsprüfung

**Ursache.** RealVNC Server (meldet RFB 3.3) zählt jede Verbindung, die ohne Anmeldung endet, als Fehlversuch. Nach wenigen Versuchen sperrt er die Adresse („Too many security failures“), und die Sperrzeit wächst mit jeder weiteren Verbindung. Eine Erreichbarkeitsprüfung, die nur den VNC-Port öffnet und wieder schließt, löst das aus.

**Abhilfe am HMI.** Den VNC-Dienst am HMI neu starten hebt die Sperre auf; alternativ einige Minuten warten. Dauerhaft lässt sich die Schwelle am RealVNC Server anheben (Parameter `BlacklistThreshold`, Sperrzeit `BlacklistTimeout`). Danach unter HMIs „Verbindung prüfen“ und das Passwort kontrollieren. Das Terminal selbst wiederholt eine gesperrte Anmeldung nicht: Die Daueranzeige wartet 5 Minuten, bevor sie es erneut versucht.

**Erreichbarkeitsprüfung je HMI** (Admin-Seite, HMI bearbeiten, „Erreichbarkeit prüfen“; die Kachel zeigt sie als „nicht erreichbar“):

- **Ping (Standard, auch für ältere Konfigurationen):** alle 10 Sekunden ein ICMP-Echo mit 1 Sekunde Frist über das System-`ping` (`ping -n -c 1 -W 1 <Adresse>`; Raspberry Pi OS Trixie erlaubt das ohne root). Es entsteht keine VNC-Verbindung, also keine Sperre. Voraussetzung: Das HMI und das Netz lassen Ping zu.
- **VNC-Port:** das frühere Verhalten (TCP-Verbindung zum VNC-Port). Nur für Server wählen, die das nicht als Fehlversuch zählen; bei RealVNC führt es zur Sperre.
- **aus:** keine Prüfung, die Kachel zeigt keinen Erreichbarkeits-Zustand. Das Antippen prüft das HMI trotzdem vorab mit Anmeldung.

### Admin-Seite und Gerät

| Beobachtung | Ursache | Was tun |
|---|---|---|
| Admin-Seite nicht erreichbar | Falsche Adresse, Port 8443, Dienst nicht gestartet, Netz. | Adresse im Router prüfen. `https://` und `:8443` angeben. Zertifikatswarnung bestätigen. Kachelseite am Bildschirm ansehen: Zeigt sie sich nicht, läuft der Dienst nicht (Terminal aus- und einschalten). |
| Zertifikatswarnung | Das Gerät erzeugt ein eigenes Zertifikat. | Erwartet, bestätigen (Inbetriebnahme, Abschnitt 5). |
| Admin-Passwort vergessen | Es gibt keine Rücksetzung in der Admin-Seite. | Neues Abbild schreiben (Ersatzkarte), Admin-Passwort festlegen, **Name und Rolle des Geräts setzen**, danach die gesicherte Konfiguration importieren (auch die eines anderen Terminals: der Import behält Name, Kennung und Rolle des Geräts). Das Admin-Passwort ist nicht in der Sicherung. |
| Admin-Seite meldet **500 „Konfiguration nicht lesbar“** | Siehe unten. | Siehe unten. |
| Admin-Seite leitet nach der Anmeldung auf „Schlüssel reparieren“ um | Die Schlüsseldatei (`secrets.key`) fehlt oder ist beschädigt, obwohl verschlüsselte Werte gespeichert sind. | Abschnitt „Schlüssel prüfen und reparieren“. Alternativ aus einer Sicherung wiederherstellen. |
| Warnung „Der Schreibschutz (Overlay auf /) ist nicht aktiv.“ (System) | Das Overlay ist aus, zum Beispiel nach Wartung. | `overlayroot=tmpfs:recurse=0` in `cmdline.txt` wieder einfügen (Abschnitt 3). |
| Warnung „Kein Monitor erkannt“ (System) | Bildschirm nicht erkannt, es gilt 1920×1080. | Kabel und Bildschirm prüfen oder unter System die Auflösung fest eintragen. |
| Warnung zur Konfiguration | Die letzte Konfigurationsdatei war defekt, das Gerät läuft mit der letzten guten Fassung. | Einstellungen prüfen, bei Bedarf aus der Sicherung wiederherstellen. |
| Terminal bleibt nach Einschalten auf einer leeren Seite | Erster Start läuft noch oder bricht ab. | Etwa 10 Minuten warten (ungemessener Schätzwert). Dann `hmi-kiosk-firstboot.log` auf der Boot-Partition am PC lesen (Inbetriebnahme, Abschnitt 4). |
| Terminal startet in den Notfallmodus | Datenpartition nicht eingehängt. | Neustart. Hilft es nicht, die Karte tauschen (Rückfall). |
| Update angenommen, aber die alte Version läuft | Automatischer Rückfall nach 60 Sekunden. | Abschnitt 2. |

### 500 „Konfiguration nicht lesbar“ nach einem Konfigurations-Rückfall

Diese Seite erscheint, wenn die Konfigurationsdatei verloren oder defekt war, das Gerät auf die letzte gute Fassung zurückgefallen ist und **in dieser Fassung noch kein Admin-Passwort gesetzt war**. Das passiert zum Beispiel, wenn der Strom während der Ersteinrichtung ausfällt. Das Gerät öffnet die Ersteinrichtung dann bewusst nicht. Sonst könnte jeder im Anlagennetz ein Passwort setzen.

Folge: Alle Admin-Seiten antworten mit 500. In der Admin-Seite gibt es keinen Ausweg.

Wege zur Erholung:

1. **Neu schreiben (Standardweg):**
   1. Das Abbild neu auf die Karte schreiben (Inbetriebnahme, Abschnitt 3).
   2. Starten, Admin-Passwort festlegen.
   3. Unter „Terminals“ zuerst Name und Rolle dieses Geräts setzen, dann eine vorhandene Sicherung importieren (auch die eines anderen Terminals: der Import behält Name, Kennung und Rolle des Geräts). Die Ersteinrichtung ist bei einer Karte im Zustand der Auslieferung schnell wiederholt.
2. **Datei entfernen (nur mit Linux-PC):**
   1. Terminal ausschalten, Karte in einen Linux-PC stecken. Windows kann die Datenpartition (ext4, Label `HMIDATA`, dritte Partition) nicht lesen.
   2. Die Partition einhängen.
   3. `config.json` und `config.json.good` verschieben (nicht löschen, zur Sicherheit aufheben). Die Datei `secrets.key` bleibt.
   4. Karte zurück, Terminal starten. Das Gerät beginnt mit einer leeren Konfiguration: Admin-Passwort festlegen, Name und Rolle des Geräts setzen, danach Konfiguration importieren (auch die eines anderen Terminals) oder neu eingeben.
3. **Mit Zugriff per SSH-Abbild:**

   ```
   sudo systemctl stop hmi-kiosk
   sudo mv /data/config.json /data/config.json.defekt
   sudo mv /data/config.json.good /data/config.json.good.defekt
   sudo systemctl start hmi-kiosk
   ```

   (Ist eine der beiden Dateien nicht da, den Befehl dafür weglassen. Beide müssen weg sein.)

Eine Tastatur am Gerät hilft beim Standard-Abbild nicht: Es gibt keinen Anmeldebildschirm, und alle Konten sind gesperrt.

### Protokoll

Unter System steht das Protokoll des Dienstes (die letzten Zeilen). „Protokoll herunterladen“ liefert die Datei `hmi-kiosk.log`. Die Dauerprüfung schreibt getrennt nach `/data/log/soak.jsonl`. Das Protokoll des ersten Starts liegt auf der Boot-Partition als `hmi-kiosk-firstboot.log`.

Bei einer Störmeldung an die Entwicklung bitte angeben: Version (System), Uhrzeit, die Meldung auf dem Bildschirm, das heruntergeladene Protokoll und das Ergebnis der Selbstprüfung.
