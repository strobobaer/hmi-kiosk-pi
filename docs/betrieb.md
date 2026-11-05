# Betrieb

Dieses Handbuch beschreibt den laufenden Betrieb: Rückfall, Update, Wartung und Fehlersuche. Die Inbetriebnahme steht in [inbetriebnahme.md](inbetriebnahme.md).

## Inhalt

1. Rückfall durch Kartentausch
2. Programm-Update einspielen
3. Wartung
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

### Auf dem Terminal einspielen

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

Dieses Abbild ist ein anderes als das ohne SSH. Ob SSH in der Anlage erlaubt ist, ist eine Entscheidung der Anlage (Inbetriebnahme, Abschnitt 11). Eine Datei `ssh` auf der Boot-Partition schaltet SSH ebenfalls ein, hilft aber ohne Schlüssel nicht bei der Anmeldung.

## 4. Fehlersuche

### Meldungen auf dem Bildschirm

| Meldung | Wo | Mögliche Ursache | Was tun |
|---|---|---|---|
| Kachel „nicht erreichbar“ | Kachelseite, unter dem Namen des HMI | Das HMI antwortet nicht auf dem VNC-Port. Netzwerkkabel, Adresse, Port, VNC-Server am HMI. | In der Admin-Seite unter HMIs „Verbindung prüfen“. Zeigt es „nicht erreichbar“, Adresse und Port prüfen, Kabel und Schalter prüfen, VNC-Dienst am HMI starten. |
| „VNC-Passwort falsch“ | Hinweis nach dem Antippen einer Kachel | Das gespeicherte Passwort stimmt nicht mehr mit dem VNC-Server überein. Es wird kein Client gestartet. | Unter HMIs beim HMI „Bearbeiten“, richtiges VNC-Passwort eintragen, speichern, „Verbindung prüfen“. Anmeldung muss „erfolgreich“ zeigen. |
| „belegt durch <Name>“ | Kachel | Ein anderes Terminal hält das HMI und der Modus ist „Sperre“, oder das haltende Terminal ist primär. | Das andere Terminal mit „Zurück“ beenden lassen. Nach wenigen Sekunden ist die Kachel frei. Oder Vorrang bzw. Modus ändern (Inbetriebnahme, Abschnitt 10). |
| „Schlüssel falsch: <Name>“ | Hinweisleiste oben auf der Kachelseite | Der gemeinsame Schlüssel dieses Terminals ist nicht der des Terminals <Name>. Die Abstimmung läuft dann nicht. | Auf beiden Terminals unter „Terminals“ den Schlüssel prüfen. Am besten auf einem neuen erzeugen und auf dem anderen von Hand eintragen. Danach die Selbstprüfung (`terminals`) wiederholen. |
| „Nicht erreichbar: <Name>“ | Hinweisleiste oben auf der Kachelseite | Das andere Terminal antwortet nicht. | Adresse unter „Terminals“, Netz und Strom des anderen Terminals prüfen. Die Anzeige bleibt nutzbar, kurzzeitig können zwei Terminals am selben HMI hängen. |
| „Keine Verbindung zu <HMI> zustande gekommen“ | Hinweis nach dem Antippen | Der Client hat in 15 Sekunden keine Verbindung hergestellt. | HMI prüfen (siehe „nicht erreichbar“). Unter Client das Profil wechseln. |
| „Verbindung zu <HMI> verloren“ / „beendet“ | Hinweis | Das HMI hat die Verbindung getrennt oder das Netz war weg. | HMI und Netz prüfen. Neu verbinden. |
| „Passwort von <HMI> lässt sich nicht entschlüsseln“ | Hinweis | Die Schlüsseldatei des Geräts passt nicht zu den gespeicherten Passwörtern. | Passwort des HMI neu eintragen. Tritt es bei allen HMIs auf, Konfiguration aus der Sicherung wiederherstellen. |
| „Dieses HMI gibt es nicht mehr“ | Hinweis | Das HMI wurde gelöscht, während es genutzt wurde. | Kachelseite neu laden lassen. Daueranzeige unter Kiosk prüfen. |

### Admin-Seite und Gerät

| Beobachtung | Ursache | Was tun |
|---|---|---|
| Admin-Seite nicht erreichbar | Falsche Adresse, Port 8443, Dienst nicht gestartet, Netz. | Adresse im Router prüfen. `https://` und `:8443` angeben. Zertifikatswarnung bestätigen. Kachelseite am Bildschirm ansehen: Zeigt sie sich nicht, läuft der Dienst nicht (Terminal aus- und einschalten). |
| Zertifikatswarnung | Das Gerät erzeugt ein eigenes Zertifikat. | Erwartet, bestätigen (Inbetriebnahme, Abschnitt 5). |
| Admin-Passwort vergessen | Es gibt keine Rücksetzung in der Admin-Seite. | Neues Abbild schreiben (Ersatzkarte), danach die gesicherte Konfiguration importieren. Das Admin-Passwort ist nicht in der Sicherung. |
| Admin-Seite meldet **500 „Konfiguration nicht lesbar“** | Siehe unten. | Siehe unten. |
| Admin-Seite meldet 503 | Der Schlüssel des Geräts (`secrets.key`) ist nicht lesbar. Die Ursache steht im Protokoll. | Terminal neu starten. Hält es an, Ersatzkarte (Abschnitt 3). |
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
   3. Falls eine Sicherung existiert, importieren. Die Ersteinrichtung ist bei einer Karte im Zustand der Auslieferung schnell wiederholt.
2. **Datei entfernen (nur mit Linux-PC):**
   1. Terminal ausschalten, Karte in einen Linux-PC stecken. Windows kann die Datenpartition (ext4, Label `HMIDATA`, dritte Partition) nicht lesen.
   2. Die Partition einhängen.
   3. `config.json` und `config.json.good` verschieben (nicht löschen, zur Sicherheit aufheben). Die Datei `secrets.key` bleibt.
   4. Karte zurück, Terminal starten. Das Gerät beginnt mit einer leeren Konfiguration: Admin-Passwort festlegen, Konfiguration importieren oder neu eingeben.
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
