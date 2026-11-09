# Betrieb

Dieses Handbuch beschreibt den laufenden Betrieb: Rückfall, Update, Wartung und Fehlersuche. Die Inbetriebnahme steht in [inbetriebnahme.md](inbetriebnahme.md).

## Inhalt

1. Rückfall durch Kartentausch
2. Programm-Update einspielen
3. Wartung (Konfiguration, SD-Sicherung, Schlüssel prüfen und reparieren, Wartungsmodus von Hand, Admin-Seite am Terminal, Bildschirmtastatur, Kachelseite im Netz für Tests, Web-Kacheln, PDH-Fernsteuerung)
4. Fehlersuche

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

Ein Update ersetzt nur das Programm `hmi-kiosk` (den Dienst). Der Helfer und das System bleiben unverändert. Änderungen am Helfer oder an Systempaketen kommen als neues Abbild.

### Paket bauen (am Entwicklungsrechner)

1. Neue Version festlegen, zum Beispiel `1.0.1`. Sie muss im Format x.y.z stehen und sich von der laufenden Version unterscheiden.
2. Programme bauen:

   ```
   make release VERSION=1.0.1
   ```

   Das braucht `.secrets/release.pub`. Die Version im Befehl **muss** die des Pakets sein. Das gebaute Programm meldet sie dem Update-Wächter. Stimmt sie nicht, schaltet das Gerät nach 60 Sekunden zurück.
3. Paket signieren:

   ```
   scripts/dgo run ./cmd/hmi-kiosk-pack pack -key .secrets/release.key -version 1.0.1 -in dist -out dist/hmi-kiosk-1.0.1.pkg
   ```

   Aus dem Ordner `dist` wird nur `hmi-kiosk` verwendet. Der Helfer ist nie Teil eines Pakets.
4. Die Datei `dist/hmi-kiosk-1.0.1.pkg` ist das signierte Paket. Der private Schlüssel `release.key` bleibt auf dem Entwicklungsrechner.

### Paket als Release-Datei bereitstellen

Damit Terminals das Update selbst finden, kommt das Paket als Datei an das GitHub-Release der Version. Der Name muss genau `hmi-kiosk-<version>.pkg` lauten, das Tag `v<version>` (oder `<version>`):

```
gh release upload v1.0.1 dist/hmi-kiosk-1.0.1.pkg
```

Fehlt die Paketdatei (zum Beispiel, weil die Version neuere Systemteile braucht und nur als Abbild und Bundle erscheint), zeigen die Terminals die Version mit einem Verweis auf die Release-Seite an, aber ohne Knopf zum Installieren. Ein Release als Vorabversion („Pre-release“) gilt nur für Terminals, bei denen „Vorabversionen einbeziehen“ eingeschaltet ist. Entwürfe (Drafts) sieht kein Terminal.

**Wichtig:** Ein Paket, das einen höheren Systemstand verlangt (`release.SystemLevel` wurde erhöht), **nie** als Release-Datei anhängen. Terminals mit einer Version vor dem Systemstand (0.1.6 und älter) prüfen ihn nicht und würden das Paket installieren; das neue Programm liefe dann auf zu alten Systemteilen. Solche Versionen nur als Abbild und Bundle veröffentlichen.

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
- Technisch läuft diese Admin-Seite über HTTP nur auf `127.0.0.1:8081`, nie im Netz (die Chromium-Richtlinie erlaubt genau diese Adresse zusätzlich zur Kachelseite). Sie hat eigene Cookies; eine Anmeldung am Terminal und eine über HTTPS stören sich nicht. Die Terminal-API (`/peer/`) gibt es dort nicht.
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

Für Tests lässt sich die Kachelseite von einem anderen PC im Netz aufrufen. Ab Werk ist das aus, und die Kachelseite antwortet nur auf `127.0.0.1:8080`.

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

### PDH-Fernsteuerung (HMI aus PDH am Terminal zeigen)

PDH kann über die Admin-Adresse des Terminals (`https://<IP>:8443`) ein HMI auf dem **Bildschirm dieses Terminals** öffnen („Am Terminal zeigen“ bzw. „Am Terminal bedienen“) und wieder schließen. Die Schnittstelle (Version 1) steht in `docs/superpowers/specs/2026-11-09-pdh-integration.md`.

Einrichten am Terminal: Admin-Seite → **Netzwerk** → **Fernsteuerung (PDH)**.

1. **Schlüssel erzeugen** drücken. Der Schlüssel (`hmik_…`) wird **nur dieses eine Mal** angezeigt; gespeichert wird nur sein SHA-256. Sofort in PDH unter **Kiosk-Terminals** beim passenden Terminal eintragen. Neu laden der Seite zeigt ihn nicht noch einmal und erzeugt keinen neuen. Geht er verloren, einfach neu erzeugen.
2. Den **Fingerabdruck** von derselben Seite in PDH eintragen. PDH prüft damit das selbstsignierte HTTPS-Zertifikat (SHA-256 über den öffentlichen Schlüssel); eine Zertifizierungsstelle ist nicht nötig. Der Fingerabdruck bleibt gleich, wenn das Terminal sein Zertifikat erneuert (neue IP-Adresse, Ablauf). Er ändert sich nur, wenn die Schlüsseldatei verloren geht oder das Gerät im Wartungsmodus **als neues Terminal** eingerichtet wird; dann in PDH neu eintragen. Als **Ersatz** eingerichtet, bleiben Fingerabdruck und Schlüssel gleich.
3. In PDH **Verbindung prüfen**.

- **Neuen Schlüssel erzeugen** macht den alten sofort ungültig. **Aus** löscht ihn; PDH erhält dann „Schlüssel falsch“.
- **Firewall:** PDH-Server → Terminal, TCP **8443** freigeben (derselbe Port wie Admin-Seite und Terminal-Abstimmung). Kachelseite (8080) und Admin-Seite am Terminal (8081) beantworten keine Fernbefehle.
- **Falscher Schlüssel:** Nach 5 Fehlversuchen sperrt das Terminal Fernbefehle **von dieser Quelladresse** für 5 Minuten (Antwort 429), wie die Admin-Anmeldung. Bei IPv6 gilt die Sperre für das ganze /64-Netz. Die Admin-Anmeldung selbst und andere Absender bleiben frei.
- **Belegt:** Ist am Terminal eine Sitzung offen (auch die Daueranzeige oder ihr Neuverbinden), lehnt es ab; PDH zeigt „belegt“ mit HMI und Beginn. Startet oder endet gerade eine Sitzung, läuft der Wartungsmodus, eine Dauerprüfung, eine Sicherung oder ist die Admin-Seite am Terminal offen, meldet es „nicht bereit“ (503).
- **Welche Sitzung:** Gibt es eine VNC-Kachel mit gleichem Port und gleichem Host (oder demselben Host nach Namensauflösung), öffnet das Terminal **diese** und stimmt sich wie beim Antippen mit den anderen Terminals ab. In PDH deshalb **denselben Host wie in der Kachel** eintragen (gleiche Schreibweise, Name oder IP), sonst kann bei nicht auflösbarem Namen eine nur lokale Sitzung ohne Abstimmung entstehen. Hält ein anderes Terminal das HMI, heißt das „belegt“ (PDH übernimmt nie; Mitschauen bei einer Daueranzeige geht nur zum Ansehen). Ohne passende Kachel startet eine nur lokale Sitzung ohne Abstimmung; der VNC-Client verbindet immer geteilt, andere Betrachter bleiben verbunden. Passwort und Modus kommen von PDH (**ansehen** = nur ansehen); eine Kachel, die am Terminal auf **nur ansehen** steht, bleibt es aber auch bei **bedienen**. Das Terminal prüft die VNC-Anmeldung, bevor es antwortet: Lehnt das HMI das Passwort ab, meldet PDH „VNC-Anmeldung am HMI fehlgeschlagen“ (422), es startet kein Client, und keine Kachel wird markiert. Ziele auf dem Terminal selbst (localhost, 127.x, ::1) lehnt es ab. Bricht PDH vor der Antwort ab, entsteht keine Sitzung.
- Die Leiste zeigt „<Label> – von PDH: <Benutzer>“. **Zurück** und die Rückkehr bei Inaktivität gelten wie sonst; danach startet eine eingestellte Daueranzeige wieder. PDH kann nur eine **von PDH** geöffnete Sitzung schließen.
- **Protokoll:** Jeder Befehl steht mit Label, Benutzer, Modus und Quelladresse im Protokoll (`Fernsteuerung (PDH): …`), das Sitzungsprotokoll mit Modus „PDH, nur ansehen“ bzw. „PDH, bedienen“. Das VNC-Passwort wird nie gespeichert oder protokolliert; es liegt nur für die Dauer der Sitzung im RAM-Profil des VNC-Clients.
- **Export/Import:** Der Schlüssel gehört zu diesem Terminal. Der Export enthält ihn nicht, der Import ändert ihn nicht.

#### PDH-Terminalmodus (PDH als Kachel)

Damit Bediener am Terminal selbst in PDH arbeiten:

1. In PDH einen **System-Benutzer** für das Terminal anlegen (nur die Rechte, die am Terminal gebraucht werden).
2. Am Terminal eine **Web-Kachel** mit der PDH-Adresse anlegen (siehe Web-Kacheln oben), z. B. `https://pdh.werk/`. PDH muss ein Zertifikat haben, dem das Terminal vertraut, oder im Werksnetz über `http://` laufen; Anmeldung und Anmeldeserver müssen auf demselben Server liegen.
3. Die Kachel einmal öffnen und als System-Benutzer anmelden. Die Anmeldung bleibt im Web-Profil des Terminals (Cookie mit Ablaufdatum nötig).
4. Personen melden sich für Aktionen in PDH per Karte oder Override an (vorhandenes PDH-Konzept).

## 4. Fehlersuche

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
