# Inbetriebnahme

Dieses Handbuch führt vom Bau des Abbilds bis zum zweiten Terminal. Es gilt für die Raspberry Pi 5 mit dem fertigen SD-Abbild. Für andere Linux-Systeme siehe Abschnitt 11.

Alle Schritte sind für eine Anlage mit zwei Terminals geschrieben. Es gibt kein Testgerät. Die erste Prüfung auf echter Hardware ist die Inbetriebnahme selbst. Der Rückfall ist immer der Kartentausch.

Der Betrieb danach steht in [betrieb.md](betrieb.md).

## Inhalt

1. Vorbereitung am Entwicklungsrechner
2. Abbild bauen und prüfen
3. SD-Karte schreiben
4. Erster Start
5. Admin-Seite aufrufen
6. Einrichtung in der Admin-Seite
7. Selbstprüfung
8. Dauerprüfung
9. Zweiten Raspi umstellen
10. Prüfung mit zwei Terminals
11. Andere Linux-Systeme (install.sh)
12. Offene Punkte nur auf Hardware

## 1. Vorbereitung am Entwicklungsrechner

Nötig sind Docker (mit Emulation für arm64), Git Bash oder eine Linux-Shell und Internet für den Bau. Go muss nicht installiert sein. Alle Go-Befehle laufen über `scripts/dgo`.

### Release-Schlüssel erzeugen (einmalig)

Der Release-Schlüssel signiert Updates. Das Abbild enthält nur den öffentlichen Teil.

1. Im Hauptordner des Projekts ausführen:

   ```
   scripts/dgo run ./cmd/hmi-kiosk-pack keygen -out .secrets
   ```

2. Es entstehen `.secrets/release.key` (geheim) und `.secrets/release.pub` (öffentlich).
3. `release.key` jetzt offline sichern, zum Beispiel auf einem verschlüsselten USB-Stick im Tresor.
4. `release.key` nie ins Repository legen. Der Ordner `.secrets/` ist von Git ausgeschlossen.
5. Der Befehl überschreibt nie einen vorhandenen Schlüssel.

**Wichtig:** Geht `release.key` verloren, kann kein Gerät mehr ein Update annehmen. Dann hilft nur ein neues Abbild mit neuem Schlüssel. Ein neuer Schlüssel passt nicht zu bereits geschriebenen Karten.

## 2. Abbild bauen und prüfen

1. Version festlegen, zum Beispiel `1.0.0` (Form x.y.z).
2. Bauen:

   ```
   make image VERSION=1.0.0
   ```

   Gleichwertig: `VERSION=1.0.0 packaging/build-image.sh`.
3. Der Bau dauert rund 25 Minuten. Er braucht Internet und einen privilegierten Docker-Container.
4. Das Ergebnis liegt in `dist/`:
   - `hmi-kiosk-1.0.0.img.xz` (das Abbild)
   - `hmi-kiosk-1.0.0.img.xz.sha256` (Prüfsumme)
   - `hmi-kiosk-1.0.0-pakete.txt` (Paketversionen)
   - `hmi-kiosk-1.0.0-build.log` (Protokoll)
5. Der Bau prüft das Abbild selbst (`packaging/image/verify-image.sh`). Bei einem Fehler bricht er ab. Dann kein Abbild verwenden.
6. Prüfsumme kontrollieren:

   ```
   cd dist
   sha256sum -c hmi-kiosk-1.0.0.img.xz.sha256
   ```

   Es muss `hmi-kiosk-1.0.0.img.xz: OK` erscheinen. Sonst Datei verwerfen und neu bauen.
7. Unter Windows ohne `sha256sum`: `Get-FileHash dist\hmi-kiosk-1.0.0.img.xz -Algorithm SHA256` und den Wert mit der Datei `.sha256` vergleichen.

**Nie ein Test-Abbild verwenden.** Ein Abbild, das mit einem Wegwerf-Schlüssel gebaut wurde, trägt einen öffentlichen Schlüssel ohne aufbewahrten privaten Teil. Updates sind dann nie möglich. Im Zweifel das Abbild mit `.secrets/release.pub` neu bauen.

## 3. SD-Karte schreiben

Nötig: eine neue SD-Karte und der Raspberry Pi Imager auf dem PC.

1. **Die alte Karte aus dem Gerät nehmen, beschriften (Datum, Gerät) und aufbewahren.** Sie bleibt unverändert. Sie ist der Rückfall.
2. Neue Karte in den PC stecken.
3. Raspberry Pi Imager starten.
4. Unter „Betriebssystem“ ganz unten „Eigenes Image“ wählen. Die Datei `hmi-kiosk-1.0.0.img.xz` auswählen. Der Imager entpackt sie selbst.
5. Die neue Karte als Speicher wählen.
6. Auf „Weiter“ klicken. Fragt der Imager nach der **OS-Anpassung** (Einstellungen bearbeiten), mit **„Nein“** antworten. Keine Anpassung anwenden.
   - Der Grund: Die Anpassung legt eine Datei `firstrun.sh` an und ändert `cmdline.txt`. Das Abbild ist darauf nicht vorbereitet.
   - Hostname, Benutzer, WLAN und SSH werden also nicht im Imager gesetzt.
7. Schreiben und Prüfen abwarten.
8. Karte auswerfen und in den Raspi stecken.

Die Beschriftung der neuen Karte: Datum und Version.

## 4. Erster Start

Raspi mit Netzwerkkabel, Bildschirm und Strom verbinden.

Was passiert beim ersten Start, ohne dass jemand etwas tun muss:

1. Das Gerät legt im freien Rest der Karte die Datenpartition `/data` an.
2. Es richtet `/data` ein und kopiert die erste Programmversion.
3. Es schaltet den Schreibschutz (Overlay) und die schreibgeschützte Boot-Partition ein.
4. Es startet **einmal von selbst neu**.
5. Nach dem Neustart erscheint auf dem Bildschirm die Kachelseite.

Was man sieht:

- Zuerst kann der Bildschirm dunkel bleiben oder Konsolentext zeigen. Das ist normal.
- Nach dem Neustart zeigt der Bildschirm die Kachelseite. Solange noch kein HMI eingerichtet ist, ist sie leer (keine Kacheln).

Wie lange es dauert:

- Rechnen Sie mit einigen Minuten bis zum Neustart. Die genaue Dauer auf echter Hardware ist noch nicht gemessen (siehe Abschnitt 12).
- Warten Sie etwa 10 Minuten (ungemessener Schätzwert, siehe Abschnitt 12). Schalten Sie das Gerät in dieser Zeit nicht aus.

Wenn nach etwa 10 Minuten nichts erscheint:

1. Gerät ausschalten und Karte in einen PC stecken.
2. Auf der Boot-Partition (die kleine, FAT) die Datei `hmi-kiosk-firstboot.log` öffnen. Sie nennt den letzten erledigten Schritt und einen eventuellen Fehler.
3. Jeder Schritt kann wiederholt werden. Ein erneuter Start setzt den Ablauf fort.
4. Hilft das nicht: Alte Karte einsetzen (Rückfall) und den Fehler melden.

## 5. Admin-Seite aufrufen

Die Admin-Seite ist nur im Netzwerk erreichbar. Die Kachelseite am Bildschirm zeigt keine Adresse.

### IP-Adresse finden

1. Im Router oder DHCP-Server die Liste der angeschlossenen Geräte öffnen.
2. Den Raspi suchen. Der Hostname des Abbilds ist `hmi-kiosk`.
3. Beide Terminals haben denselben Hostnamen. Zur Unterscheidung die MAC-Adresse oder den Zeitpunkt des Einschaltens nutzen.
4. Empfehlung: im DHCP-Server für jedes Terminal eine feste Adresse (Reservierung) eintragen. Die Adresse braucht man später für die Terminal-Liste.

### Anmelden

1. Im Browser öffnen: `https://<IP>:8443`
2. Der Browser warnt vor dem Zertifikat („Verbindung nicht privat“). Das Gerät erzeugt sein Zertifikat selbst. Die Warnung ist hier erwartet.
3. „Erweitert“ wählen und „Weiter zu <IP>“ bestätigen.
4. Beim ersten Aufruf erscheint die Seite „Ersteinrichtung“ mit dem Feld „Neues Passwort“ und der Schaltfläche „Passwort festlegen“. Das Gerät hat noch kein Admin-Passwort.
5. Ein Passwort mit mindestens 10 Zeichen eingeben und wiederholen. Es gibt kein Standardpasswort.
6. Das Passwort sicher aufbewahren. Ohne Passwort hilft nur ein neues Abbild (siehe [betrieb.md](betrieb.md), Fehlersuche).

**Wichtig:** Das Passwort gleich beim ersten Aufruf festlegen. Bis dahin kann jeder im Anlagennetz die Ersteinrichtung ausführen.

## 6. Einrichtung in der Admin-Seite

Die Reihenfolge ist wichtig. Die Menüpunkte oben heißen: HMIs, Client, Kiosk, Terminals, Netzwerk, System.

### 6.1 Passwort

Schon in Abschnitt 5 erledigt. Ändern später unter System, „Admin-Passwort ändern“.

### 6.2 Terminal-Name und Rolle

1. Menü „Terminals“.
2. Bei „Dieses Terminal“ einen **Namen** vergeben, zum Beispiel „Halle 1“. Er erscheint auf anderen Terminals („belegt durch Halle 1“).
3. **Rolle** wählen:
   - „Normal“: Standard.
   - „Primär“: hat Vorrang. Ein primäres Terminal übernimmt jedes HMI von einem normalen. Ein normales kann nie von einem primären übernehmen.
4. **Modus global** wählen: „Übernahme“ oder „Sperre“ (siehe Abschnitt 10).
5. „Speichern“.

### 6.3 HMIs anlegen und testen

1. Menü „HMIs“, „HMI anlegen“.
2. Name, Adresse (IP oder Hostname) und Port des VNC-Servers eintragen.
3. VNC-Passwort eintragen. Es wird verschlüsselt gespeichert und nie angezeigt.
4. Symbol, Farbe und Reihenfolge der Kachel wählen.
5. Optionen je HMI: „Nur ansehen“, „Mitschauen erlauben“, Modus und Art der Übernahme.
6. „Speichern“.
7. In der Liste „Verbindung prüfen“ drücken. Das Ergebnis zeigt:
   - Erreichbar: ja oder „nicht erreichbar“
   - Anmeldung: „erfolgreich“, „abgelehnt“ oder „nicht geprüft“
   - Auflösung und Name des HMI
8. Erst weitermachen, wenn „Erreichbar: ja“ und „Anmeldung: erfolgreich“ steht.
9. Bei „abgelehnt“: VNC-Passwort prüfen. Bei „nicht erreichbar“: Adresse, Port, Kabel, Firewall und VNC-Server am HMI prüfen.

Das Passwort beim HMI muss ein VNC-Passwort sein. Die Prüfung am HMI zeigt auch, wie der VNC-Server mehrere Clients behandelt (siehe Abschnitt 12).

### 6.4 Terminals und gemeinsamer Schlüssel

Die Terminals sprechen direkt miteinander. Dafür trägt jedes die anderen ein und alle teilen einen Schlüssel.

Auf dem **ersten** Terminal:

1. Menü „Terminals“. Unter „Andere Terminals“ Name und Adresse des zweiten Terminals eintragen. Die Adresse ist die IP oder der Hostname. Der Port 8443 gilt automatisch.
2. „Speichern“.
3. Unter „Gemeinsamer Schlüssel“ auf „Schlüssel erzeugen“ drücken.
4. Der Schlüssel wird **nur dieses eine Mal** angezeigt. Sofort notieren oder kopieren.
5. Der Status zeigt danach „gesetzt“.

Auf dem **zweiten** Terminal gibt es zwei Wege:

- **Über die Konfigurationsdatei** (Abschnitt 9): Der Export enthält den Schlüssel. Nach dem Import steht der Status bei „gesetzt“.
- **Von Hand:** Menü „Terminals“, „Schlüssel von Hand eintragen“, den kopierten Schlüssel einfügen, „Schlüssel speichern“.

Erst wenn beide Terminals denselben Schlüssel haben, funktioniert die Abstimmung. Ein Terminal ohne passenden Schlüssel zeigt auf den Kacheln „Schlüssel falsch: <Name>“.

Solange nur ein Raspi umgestellt ist, greift die Abstimmung noch nicht. Die alte Version der Anlage nimmt daran nicht teil.

### 6.5 Daueranzeige

Wenn ein Terminal dauerhaft ein HMI zeigen soll:

1. Menü „Kiosk“, „Daueranzeige (festes HMI)“.
2. Das HMI wählen, „Speichern“.
3. Das Terminal verbindet sich nach dem Start selbst und versucht es nach einem Abbruch erneut (nach 2, 4, 8 bis höchstens 30 Sekunden).

**Für die Selbstprüfung (Abschnitt 7) die Daueranzeige vorübergehend entfernen:** Daueranzeige auf „Keine“ stellen, speichern, Prüfung durchführen, danach wieder eintragen. Die Selbstprüfung läuft sonst nicht, weil sich die Daueranzeige zwischen den Schritten neu startet.

Weitere Einstellungen unter „Kiosk“: Rückkehr zu den Kacheln bei Inaktivität (Minuten, 0 = nie), Kachel-Layout, Bedienung (Touch: Mauszeiger ausgeblendet, Maus: Mauszeiger sichtbar; wirkt nach dem nächsten Neustart des Terminals), Leiste ausblenden. Unter „Client“: Profil (Remmina oder TigerVNC), Qualität, Farbtiefe, Skalierung.

### 6.6 WLAN und Bluetooth abschalten

1. Menü „Netzwerk“.
2. Bei „WLAN“ auf „WLAN dauerhaft abschalten“ drücken und bestätigen.
3. Bei „Bluetooth“ ebenso.
4. Die Abschaltung wird verweigert, wenn die Admin-Sitzung über WLAN läuft oder das Ethernet-Kabel keinen Link hat. Dann mit Kabel arbeiten.
5. Nach dem nächsten Neustart ist die Abschaltung vollständig wirksam.

### 6.7 Zeit (NTP)

1. Menü „Netzwerk“, Feld „NTP-Server“.
2. Den Zeitserver der Anlage eintragen und „Setzen“ drücken.
3. Unter System steht die aktuelle „Uhrzeit“.
4. Ohne erreichbaren Zeitserver kann man die Uhrzeit unter System von Hand setzen („Uhrzeit setzen“). **Achtung:** Das schaltet die Zeitsynchronisierung ab, bis wieder ein NTP-Server gesetzt wird.

## 7. Selbstprüfung

Die Selbstprüfung testet am Gerät, was ohne Hardware nicht testbar ist. Sie nutzt einen eingebauten Test-VNC-Server auf dem Gerät. Sie berührt kein echtes HMI.

### Vorbereitung

1. Die Daueranzeige ist auf „Keine“ gestellt (Abschnitt 6.5).
2. Das Terminal steht auf der Kachelseite. Es läuft keine Sitzung. Sonst die Sitzung mit „Zurück“ beenden.
3. Jemand steht am Gerät und kann den Bildschirm antippen.

### Durchführung

1. Menü „System“, „Selbstprüfung“, „Selbstprüfung starten“.
2. Die Prüfung läuft einige Minuten (höchstens 8 Minuten). Am Bildschirm folgt man den Anweisungen:
   - Erst erscheint die Touch-Seite mit fünf Zielen (oben links, oben rechts, Mitte, unten links, unten rechts). **Alle fünf antippen**, innerhalb von 60 Sekunden.
   - Dann erscheint ein Testbild mit orangen Markierungen. **Eine Markierung antippen**, innerhalb von 60 Sekunden.
   - Beim Testbild das Bild ansehen: Der weiße Rand ist rundum vollständig, die Markierungen sind quadratisch, das Bild liegt unter der Leiste.
3. Das Ergebnis erscheint auf der Seite als Liste. Über „Ergebnis herunterladen“ gibt es eine Textdatei zum Ablegen.

### Ergebnisstufen

| Stufe | Bedeutung | Was zu tun ist |
|---|---|---|
| **OK** | Gemessen und in Ordnung. | Nichts. |
| **PRÜFEN** | Es wurde nichts Falsches gemessen, aber eine Person muss etwas bestätigen (Bild, Fensterplatz, Funkmodule). | Die genannte Sichtprüfung durchführen. Passt es, gilt der Schritt als bestanden. Passt es nicht, wie bei FEHLER behandeln. |
| **FEHLER** | Ein gemessener Fehler oder eine nicht mögliche Messung. | Ursache beheben (Tabelle), Prüfung wiederholen. |

Die Meldung „Voraussetzung“ statt der sieben Schritte heißt: Die Prüfung ist gar nicht gelaufen (Daueranzeige eingestellt oder Sitzung aktiv). Beheben und neu starten.

### Die sieben Schritte

| Schritt | Was geprüft wird | Bei PRÜFEN oder FEHLER |
|---|---|---|
| `client-bild` | Der Client zeigt das Testbild, Skalierung passt, falsches Passwort führt zu keiner Verbindung. | PRÜFEN: Testbild ansehen. FEHLER: Client verbindet nicht. Unter „Client“ das Profil wechseln (TigerVNC statt Remmina), erneut prüfen. |
| `fenster-leiste` | Leiste oben, Bild darunter, Clientfenster ohne Rahmen. | PRÜFEN mit Meldung „Fenster liegt bei … erwartet …“: Die Fensterregel greift nicht. Unter System das Grafikprofil auf „X11 (openbox)“ stellen, speichern, das Terminal neu starten, Prüfung wiederholen. Unter labwc wird die Fensterlage nicht automatisch gemessen: Sichtprüfung nötig. |
| `touch` | Fünf Ziele getroffen, Tipp auf das VNC-Bild liegt höchstens 15 Pixel neben der Markierung. | FEHLER: Nicht alle Ziele in 60 Sekunden angetippt (neu versuchen) oder Abweichung zu groß. Touch-Eichung und Skalierung prüfen, unter System die Auflösung der Anzeige prüfen. |
| `rueckkehr` | Nach „Zurück“, Absturz des Clients und Trennung durch den Server erscheint wieder die Kachelseite. | FEHLER: Meldung lesen, welcher der drei Fälle scheitert. Protokoll unter System ansehen. |
| `prozessende` | Der Client endet nach der Trennung durch den Server. | OK auch dann, wenn erst SIGTERM ihn beendet. FEHLER: Protokoll ansehen. |
| `terminals` | Andere Terminals erreichbar, Schlüssel stimmt. | „keine Terminals eingetragen“ ist OK. „nicht erreichbar“: Adresse und Netz prüfen. „Schlüssel falsch“: Schlüssel angleichen (Abschnitt 6.4). |
| `schutz` | Overlay auf `/`, Boot-Partition schreibgeschützt, Watchdog, WLAN und Bluetooth wie eingestellt. | PRÜFEN: Nach dem Abschalten von WLAN/Bluetooth bestätigen, dass die Funkmodule fehlen (nach Neustart). FEHLER bei „Kein Overlay“: Das System ist nicht schreibgeschützt (siehe [betrieb.md](betrieb.md), Wartung). FEHLER bei Watchdog: Abbild prüfen. |

Danach die Daueranzeige wieder eintragen, falls sie vorgesehen ist.

Die Selbstprüfung gibt es auch als Befehl auf dem Gerät (`hmi-kiosk selftest`). Sie läuft dabei im laufenden Dienst, auf derselben Anzeige. Am Bildschirm gelten dieselben Anweisungen.

## 8. Dauerprüfung

Die Dauerprüfung startet und beendet wiederholt einen Client gegen den eingebauten Test-VNC-Server und misst dabei den Speicher von Dienst und Client. Sie berührt kein echtes HMI. Sie muss **mindestens 24 Stunden** laufen, **außerhalb der Betriebszeit**.

### Voraussetzungen

- Keine Sitzung läuft, keine Daueranzeige ist eingestellt (Abschnitt 6.5) und keine Selbstprüfung läuft. Sonst verweigert die Admin-Seite den Start und nennt den Grund.
- **Niemand bedient das Terminal während der Prüfung.** Die Kacheln sind währenddessen gesperrt.
- Es wird keine Eingabeaufforderung gebraucht. Alles geht in der Admin-Seite.

### Start in der Admin-Seite (Normalfall)

1. Menü „System“, Bereich „Dauerprüfung“.
2. Bei „Dauer“ eine der Auswahlen nehmen: 1 Stunde, 8 Stunden, **24 Stunden** (voreingestellt) oder 72 Stunden. Für die Inbetriebnahme 24 Stunden oder mehr wählen.
3. Das Kästchen „Das Terminal ist währenddessen nicht bedienbar“ ankreuzen.
4. „Dauerprüfung starten“ drücken und bestätigen.
5. Die Prüfung endet nach der gewählten Dauer von selbst. Mit „Dauerprüfung stoppen“ lässt sie sich vorzeitig beenden.
6. Die Seite „System“ zeigt den Stand: „läuft seit“ oder „beendet“ mit Zeiten, „Durchgänge“, „Fehler“, „Speicher (Dienst und Client)“, „Leck“ und gegebenenfalls „Letzter Fehler“. Zum Aktualisieren die Seite neu laden.
7. Ein Durchgang dauert etwa eine halbe Minute plus Start und Ende des Clients. In 24 Stunden sind das einige tausend Durchgänge.
8. Startet der Dienst während der Prüfung neu (zum Beispiel nach einem Stromausfall), ist die Prüfung beendet und die Anzeige des Stands leer. Die Messwerte bis dahin bleiben in der Datei erhalten. Dann neu starten.

### Ergebnis lesen

Unter dem Stand gibt es zwei Schaltflächen:

- „Messwerte herunterladen“ liefert `soak.jsonl`: pro Durchgang eine JSON-Zeile.
- „Zusammenfassung herunterladen“ liefert `dauerpruefung.txt`: Dauer, Durchgänge, Fehler, Speicher, Leck, letzter Fehler.

Felder in `soak.jsonl`:

| Feld | Bedeutung |
|---|---|
| `cycle` | Nummer des Durchgangs |
| `time` | Zeitpunkt |
| `rssBytes` | Speicher von Dienst und Client zusammen |
| `serviceRss`, `clientRss` | Speicher von Dienst und Client einzeln |
| `failures` | Anzahl aller Fehler seit dem Anfang |
| `error` | Fehlertext dieses Durchgangs (nur wenn einer aufgetreten ist) |
| `leak` | `true`, wenn der Speicher wächst |
| `slopeBytesPerHour` | Zuwachs in Bytes je Stunde (nur bei `leak`) |

Die Datei sammelt alle Läufe nacheinander (angehängt). Zeitpunkte trennen die Läufe; `cycle` beginnt bei jedem Lauf wieder mit 1.

So wertet man aus:

1. „Fehler“ muss 0 sein. Fehlertexte stehen in `error` und unter „Letzter Fehler“.
2. „Leck“ muss „nein“ sein. Ein Zuwachs wird ab 20 Durchgängen gemeldet, wenn er über 1 MB je Stunde liegt. Bei „ja“ nennt die Seite den Zuwachs in MB je Stunde.
3. Bei einem Leck in der Messwertdatei `serviceRss` und `clientRss` vergleichen: Der größere Zuwachs nennt den Verursacher.
4. Auch ohne Meldung `rssBytes` am Anfang und Ende vergleichen. Sie sollten ähnlich sein.

Bestanden ist die Dauerprüfung bei 0 Fehlern und ohne Speicherzuwachs über mindestens 24 Stunden. Sonst beide Dateien aufheben und melden. Das Terminal bleibt bis zur Klärung nicht im Dauerbetrieb.

### Alternative: Befehl auf dem Gerät (SSH-Abbild oder install.sh-System)

Nur nötig, wenn man auf dem Gerät arbeitet. Das Standard-Abbild hat keine Eingabeaufforderung: SSH ist aus, alle Konten sind gesperrt. Ein Zugang besteht bei einem Abbild mit SSH (siehe [betrieb.md](betrieb.md), Abschnitt „SSH“) oder auf einem System aus `install.sh` (Abschnitt 11).

Als Benutzer `hmikiosk` mit der Umgebung der Grafiksitzung. Beispiel für das Abbild (Datenordner `/data`, Benutzerkennung 1100):

```
sudo -u hmikiosk env XDG_RUNTIME_DIR=/run/user/1100 $(cat /run/hmi-kiosk-session/env) \
  /data/app/current/hmi-kiosk soak -duration 24h
```

Beispiel für ein System aus `install.sh` mit Standard-Datenordner. Die Kennung steht in `id -u hmikiosk`:

```
sudo -u hmikiosk env XDG_RUNTIME_DIR=/run/user/$(id -u hmikiosk) $(cat /run/hmi-kiosk-session/env) \
  /var/lib/hmi-kiosk/app/current/hmi-kiosk soak -duration 24h -data /var/lib/hmi-kiosk
```

- Bei einem anderen Datenordner (`--data-dir`) Pfad und `-data` entsprechend ändern.
- Ausgabe: eine JSON-Zeile je Durchgang auf dem Bildschirm und in `<Datenordner>/log/soak.jsonl` (im Abbild `/data/log/soak.jsonl`).
- Mit `nohup` oder `tmux` starten, damit die Prüfung das Trennen der SSH-Sitzung übersteht. Abbruch mit Strg+C ist erlaubt.
- Am Ende steht auf der Fehlerausgabe „Dauerprüfung beendet: N Durchgänge, M Fehler“. Exit-Code 0: keine Fehler und kein Zuwachs. 1: Fehler oder Zuwachs. 2: falscher Aufruf oder nicht gestartet.
- Auswertung: `tail -n 1 <Datenordner>/log/soak.jsonl` (Feld `failures`), `grep '"error"' ...`, `grep '"leak":true' ...`.

## 9. Zweiten Raspi umstellen

Die Reihenfolge ist wichtig: Zuerst bekommt das zweite Terminal seinen Namen, erst danach wird importiert. Ein frisch eingerichtetes Terminal heißt „Terminal“. Bleibt es dabei, steht nach dem Import ein Eintrag mit dem eigenen Namen in der Liste, und das spätere Speichern des Namens scheitert.

1. Auf dem ersten Terminal: Menü „Terminals“, unter „Andere Terminals“ das zweite Terminal eintragen. Der **Name** dort muss genau der Name sein, den das zweite Terminal in Schritt 6 bekommt. Speichern.
2. Menü „System“, „Konfiguration sichern“. Ein Passwort für die Datei vergeben (mindestens 10 Zeichen) und wiederholen, „Exportieren“.
3. Die Datei (`.hkx`) speichern. Das Passwort für die Datei gut aufbewahren: Ohne es ist die Datei nicht wiederherstellbar. Die Datei enthält VNC-Passwörter und den gemeinsamen Schlüssel.
4. Den zweiten Raspi wie ab Abschnitt 3 vorbereiten: alte Karte beschriften und aufbewahren, neue Karte schreiben, starten, Admin-Seite öffnen. Dort erscheint die „Ersteinrichtung“: Admin-Passwort festlegen („Passwort festlegen“).
5. Anmelden.
6. Auf dem zweiten Terminal **vor dem Import**: Menü „Terminals“, bei „Dieses Terminal“ den Namen setzen (genau wie in Schritt 1 eingetragen) und die Rolle wählen. Speichern.
7. Menü „System“, „Konfiguration wiederherstellen“. Passwort der Datei eingeben, Datei auswählen, „Importieren“.
8. Was der Import ändert und was nicht:
   - Übernommen werden HMIs, Client-, Kiosk- und Anzeige-Einstellungen, die Terminal-Liste und der gemeinsame Schlüssel.
   - Name, Kennung und Rolle dieses Terminals, das Admin-Passwort und die Netzwerk-Einstellungen (WLAN, Bluetooth, NTP) bleiben unverändert.
   - Ein Eintrag in der Terminal-Liste, dessen Name dem eigenen Namen entspricht, wird entfernt.
9. Auf dem zweiten Terminal nacharbeiten:
   - Menü „Terminals“: das erste Terminal (Name und Adresse) unter „Andere Terminals“ eintragen. Der Export des ersten Terminals enthält es nicht.
   - Unter „Gemeinsamer Schlüssel“ muss der Status „gesetzt“ sein.
   - Unter „Netzwerk“: WLAN und Bluetooth abschalten, NTP eintragen (diese Werte gelten je Gerät).
   - Eine Daueranzeige nach Bedarf eintragen.
10. Auf dem ersten Terminal prüfen, dass das zweite in „Andere Terminals“ steht.
11. Auf dem zweiten Terminal die Selbstprüfung (Abschnitt 7) und die Dauerprüfung (Abschnitt 8) ausführen.

Wenn beim Speichern die Fehlermeldung „Peer „…“ entspricht dem eigenen Terminalnamen“ erscheint: Unter „Terminals“ im Bereich „Andere Terminals“ die Zeile mit dem eigenen Namen leeren (Name und Adresse löschen) und erneut speichern. Das passiert, wenn der Name erst nach dem Import geändert wurde.

Beide Raspis haben denselben Hostnamen. Das stört die Abstimmung nicht, sie läuft über die eingetragenen Adressen.

## 10. Prüfung mit zwei Terminals

Beide Terminals sind eingerichtet, tragen sich gegenseitig ein und haben denselben Schlüssel. Zuerst im Menü „Terminals“ prüfen: Die Selbstprüfung (`terminals`) muss auf beiden „erreichbar, Schlüssel stimmt“ melden.

Wer das erwartete Verhalten kennt, sieht Abweichungen sofort. So verhalten sich die Terminals:

| Situation | Modus „Übernahme“ | Modus „Sperre“ |
|---|---|---|
| Normal will HMI von normal | Übernahme nach Rückfrage | Kachel zeigt „belegt durch <Name>“ |
| Primär will HMI von normal | Übernahme | Übernahme (Vorrang) |
| Normal will HMI von primär | abgelehnt | abgelehnt |
| Primär will HMI von primär | das zuletzt anfragende übernimmt | das erste behält es |

Der Modus gilt global und kann je HMI abweichen. Die Art der Übernahme je HMI: „Mit Rückfrage“ (Standard) oder „Sofort“.

### 10.1 Übernahme

1. Beide Terminals normal, Modus „Übernahme“, Art „Mit Rückfrage“.
2. Terminal A verbindet sich mit einem HMI.
3. Terminal B tippt dieselbe Kachel an. Es fragt: „Wird genutzt von A – übernehmen?“ (Text auf dem Bildschirm von B, Tasten „Übernehmen“ und „Abbrechen“).
4. B drückt „Übernehmen“. A zeigt 5 Sekunden lang den Hinweis „B übernimmt, Trennung in 5 s“ und wird dann getrennt. B verbindet sich.
5. Auf der Kachelseite von A steht, wer übernommen hat.
6. Wiederholen mit Art „Sofort“: keine Rückfrage, keine Wartezeit.

### 10.2 Sperre

1. Modus auf „Sperre“ stellen (global oder je HMI).
2. A verbindet sich mit einem HMI.
3. B sieht auf der Kachel „belegt durch A“ und kann nicht verbinden.
4. A beendet mit „Zurück“. Nach wenigen Sekunden (die Belegung läuft nach 6 Sekunden ohne Erneuerung ab) ist die Kachel bei B frei.

### 10.3 Vorrang

1. Ein Terminal auf Rolle „Primär“ stellen, das andere „Normal“.
2. Normal verbindet sich mit einem HMI. Primär tippt dieselbe Kachel an: Übernahme, auch im Modus „Sperre“.
3. Umgekehrt: Primär hält das HMI, Normal tippt an: abgelehnt, Hinweis „belegt“.

### 10.4 Netztrennung

1. A hält ein HMI.
2. Netzwerkkabel von B ziehen. B tippt dieselbe Kachel an.
3. Erwartet: B darf die Verbindung trotzdem herstellen und zeigt oben den Hinweis „Nicht erreichbar: <Name von A>“. Die Anzeige hat im Betrieb Vorrang. Kurzzeitig können so zwei Terminals am selben HMI hängen.
4. Kabel wieder anstecken. Nach einigen Sekunden ist die Abstimmung wieder da.
5. Zusätzlich das Kabel am HMI ziehen: Die Kachel zeigt „nicht erreichbar“, eine laufende Sitzung endet nach 5 Sekunden ohne Verbindung. Mit Daueranzeige beginnt die Wiederholung (2, 4, 8 bis 30 Sekunden).

### 10.5 Stromausfall

Soweit die Anlage das zulässt, mehrfach wiederholen.

1. Im Leerlauf (Kachelseite) den Strom ziehen und wieder anstecken.
2. Während einer Sitzung den Strom ziehen und wieder anstecken.
3. Während eines Updates nicht ausführen, außer man will den Rückfall prüfen (siehe [betrieb.md](betrieb.md)).
4. Erwartet: Das Gerät startet von selbst und zeigt die Kachelseite (oder verbindet die Daueranzeige). Konfiguration und Passwort sind unverändert. Die Admin-Seite lässt sich anmelden.
5. Nach jedem Test unter System „Datenpartition“ und das Protokoll ansehen.
6. Besteht der Verdacht auf Schäden: Karte tauschen (Rückfall).

Solange eine Abweichung offen ist, bleibt die alte Karte des Terminals beschriftet und zur Hand.

## 11. Andere Linux-Systeme (install.sh)

Für ein System ohne das fertige SD-Abbild gibt es ein Offline-Bundle und `install.sh`. Es gilt für Debian oder Raspberry Pi OS **Trixie** auf arm64 oder amd64. Es gibt keinen Schreibschutz und keine eigene Datenpartition. Das Abbild bleibt der empfohlene Weg.

**Voraussetzung:** Beim Start darf kein Desktop (Display-Manager, zum Beispiel lightdm, gdm, sddm) laufen. Die Kiosk-Sitzung belegt die Konsole `tty1` und den Bildschirm allein. Findet `install.sh` einen Display-Manager, bricht es ab, ohne etwas zu ändern. Mit `--disable-desktop` schaltet es ihn ab und stellt den Start auf die Textkonsole um; die Desktop-Pakete bleiben installiert. Die Kiosk-Dienste starten dann erst nach dem Neustart (`sudo reboot`). Rückgängig: `sudo systemctl enable lightdm && sudo systemctl set-default graphical.target`. Das ist der Weg für Raspberry Pi OS Desktop. Die labwc-Einstellungen des Kiosks liegen in einem eigenen Ordner `/etc/hmi-kiosk/labwc` mit leerem Autostart, so laufen Taskleiste und Autostart des Desktops nicht mit. `install.sh` schreibt außerdem `/etc/xdg/openbox/rc.xml`; eine vorhandene abweichende Datei bleibt einmal als `.vor-hmi-kiosk` erhalten. Bis Version 0.1.1 lagen die labwc-Dateien in `/etc/xdg/labwc`; ein neueres `install.sh` legt die Desktop-Fassung dort wieder zurück. Am besten ein frisch installiertes Debian Trixie Lite (ohne Desktop) verwenden.

### Bundle bauen

Auf dem Entwicklungsrechner, mit Docker und Internet:

```
packaging/bundle.sh --version 1.0.0 --arch arm64
```

Für ein PC-System `--arch amd64`. Das Programm wird dabei mit `.secrets/release.pub` gebaut (Abschnitt 1). Das Ergebnis liegt in `dist/`: `hmi-kiosk-bundle-1.0.0-trixie-arm64.tar.gz` und die Prüfsumme dazu. Das Bundle enthält die Programme, die Dienstdateien und alle Pakete samt Abhängigkeiten.

### Installieren

1. Bundle auf das Zielsystem kopieren und Prüfsumme kontrollieren (`sha256sum -c`).
2. Unter `/opt` entpacken (der Ordner muss bleiben, siehe unten): `sudo tar xzf hmi-kiosk-bundle-1.0.0-trixie-arm64.tar.gz -C /opt`
3. Als root ausführen:

   ```
   cd /opt/hmi-kiosk-bundle-1.0.0-trixie-arm64
   sudo ./install.sh
   ```

   Auf Raspberry Pi OS Desktop zusätzlich `--disable-desktop`, danach `sudo reboot`. Mit Internet am Gerät ist `--online` die bessere Wahl: Die Pakete kommen dann aus den Quellen von Raspberry Pi OS statt aus dem Bundle.

4. Ohne `--online` braucht das Skript kein Netz. Es prüft vor allem anderen:
   - `VERSION_CODENAME` in `/etc/os-release` gleich `trixie`
   - die dpkg-Architektur gleich der des Bundles
   - die Prüfsummen des Bundles
   - der Datenordner: neu, leer oder mit vorhandenen Kiosk-Dateien (`config.json` oder `app/`); ein anderer, nicht leerer Ordner wird nicht angefasst
   - die Version: ist die laufende Version neuer als die im Bundle, bricht das Skript ab (Rückschritt nur mit `--force-downgrade`)
5. Bei einer Abweichung bricht es mit einer klaren Meldung ab und verändert nichts.
6. Das entpackte Bundle unter `/opt` aufheben. Es wird für Wiederholungen und spätere Bundles gebraucht.

Optionen:

| Option | Wirkung |
|---|---|
| `--no-start` | Nur einrichten, Dienste weder aktivieren noch starten (für Tests) |
| `--online` | Pakete aus den Paketquellen des Systems holen statt aus dem Bundle |
| `--data-dir ORDNER` | Datenordner, Standard `/var/lib/hmi-kiosk` (im Abbild `/data`) |
| `--force-downgrade` | Erlaubt, eine ältere Version als die laufende zu installieren (sonst Abbruch) |

### Was das Skript einrichtet

- Pakete aus dem Bundle (ohne die Punkte, die nur das Abbild braucht: `raspi-config`, `overlayroot`, `fdisk`).
- Benutzer `hmikiosk` (Kennung 1100, wenn frei) in den Gruppen `input`, `video`, `render`, ohne Passwort.
- Der Helfer unter `/usr/local/bin/hmi-kiosk-helper`, der Dienst unter `<Datenordner>/app/<Version>/hmi-kiosk` mit dem Verweis `current`.
- Die vier Dienste (`hmi-kiosk`, `hmi-kiosk-helper`, `hmi-kiosk-session`, `hmi-kiosk-update-guard`) in `/etc/systemd/system`, mit einer Ergänzungsdatei `10-install.conf` für Datenordner und Benutzerkennung.
- Ohne systemd (zum Beispiel in einem Container) werden die Dienste nicht aktiviert. Das Skript sagt es.
- Das Skript ist wiederholbar. Eine vorhandene Konfiguration im Datenordner bleibt unberührt.
- Es schaltet **kein** Overlay und keine schreibgeschützte Boot-Partition ein.

**Selbstprüfung auf einem solchen System, Schritt `schutz`:**

- Kein Overlay auf `/` und eine beschreibbare Boot-Partition sind hier erwartet und werden nur als Hinweis (PRÜFEN) gemeldet.
- Der Watchdog (`RuntimeWatchdogSec`) steht erst nach dem nächsten Neustart. Davor meldet der Schritt **FEHLER** („Watchdog … nicht eingestellt“).
- Ist `config.txt` nicht lesbar, meldet der Schritt **FEHLER** („WLAN/Bluetooth nicht prüfbar: config.txt …“). Das trifft Systeme ohne `/boot/firmware/config.txt` oder `/boot/config.txt`, also die meisten PCs. Die Selbstprüfung ist dort in diesem Schritt nicht bestehbar; der Rest lässt sich trotzdem bewerten.
- Die Schalter „WLAN“ und „Bluetooth“ im Menü „Netzwerk“ funktionieren ohne `/boot/firmware/config.txt` nicht: Der Helfer liest und ändert diese Datei (Pfad `-boot-config`, Standard `/boot/firmware/config.txt`) und bricht sonst mit „config.txt nicht lesbar“ ab. Die Einstellung wird dann nicht übernommen. Auf einem PC ohne diese Datei die Funkmodule anders abschalten (BIOS, Betriebssystem).

Nicht geprüft: Die Pakete kommen aus Debian Trixie, nicht aus dem Raspberry-Pi-Archiv. Das Abbild nutzt dort ein neueres `labwc`. Wenn die Fensterregeln unter labwc auf einem solchen System nicht greifen, hilft das Grafikprofil „X11 (openbox)“.

## 12. Offene Punkte nur auf Hardware

Diese Liste ist die Abnahme am Gerät. Alles andere ist ohne Hardware geprüft (Abgleich mit der Spezifikation in [abgleich.md](abgleich.md)). Die Reihenfolge entspricht dem Ablauf der Inbetriebnahme. Pro Punkt ankreuzen und das Ergebnis dahinter notieren. „H“ und die Zahl nennen den Punkt in Abschnitt 14 der Spezifikation, dort stehen auch die Rückfälle. Die Handgriffe stehen in den genannten Abschnitten dieses Handbuchs und in [betrieb.md](betrieb.md).

**Erster Raspi, vor der Einrichtung**

1. [ ] Erster Start vollständig: Partition anlegen, Neustart, Start mit Overlay, Kachelseite erscheint (H8, Abschnitt 4). Dauer des ersten Starts messen und in Abschnitt 4 und im Betriebshandbuch statt der Schätzwerte eintragen. Bei Notfallmodus: `hmi-kiosk-firstboot.log` auf der Bootpartition.
2. [ ] Anmeldung der Grafiksitzung auf tty1 und der Dienst unter `ExecStartPre`: Dienst und Browser laufen ohne Eingriff (H9).
3. [ ] Debian-Version des Geräts entspricht der Annahme Trixie (H1).
4. [ ] Chromium startet mit Sandbox; kein Hinweis „no-sandbox“ oder Sandbox-Fehler im Protokoll (H12).
5. [ ] Mauszeiger bleibt unter labwc auch über Chromium und Remmina unsichtbar (H13).
6. [ ] Anzeige: Auflösung unter System passt zum iiyama, sonst überschreiben (H2, Abschnitt 6).
7. [ ] SSH-Regel der Anlage entscheiden, IP-Adresse im DHCP-Server festlegen (H21, Abschnitt 5 und [betrieb.md](betrieb.md), „SSH“).

**Einrichtung und Selbstprüfung (Abschnitte 6 und 7)**

8. [ ] HMI anlegen, Verbindungsprüfung zeigt Auflösung; die zusätzliche Verbindung der Vorabprüfung stört den VNC-Server des HMI nicht (H7, Abschnitt 6.3).
9. [ ] Remmina-VNC-Plugin unter Wayland/labwc: Bild erscheint, Skalierung scharf genug; sonst Qualität ändern oder TigerVNC (H3, H4, Selbstprüfung `client-bild`).
10. [ ] Fensterregel unter labwc: Leiste oben, Bild darunter, kein Rahmen. Sichtprüfung, da die Selbstprüfung die Lage unter labwc nicht misst (H5, Selbstprüfung `fenster-leiste`). Greift die Regel nicht: Grafikprofil X11. Das Profil X11 ohnehin einmal einschalten und zurückstellen: rootloses Xorg und der Treiber für den Pi 5 laufen (H10).
11. [ ] Touch am iiyama: alle Ziele getroffen, Abweichung höchstens 15 Pixel (H2, Selbstprüfung `touch`).
12. [ ] Zeit von Antippen bis zum Bild etwa 1 bis 2 Sekunden, Eindruck gegenüber noVNC (H23).
13. [ ] Inaktivität: nach der eingestellten Zeit ohne Berührung zurück zu den Kacheln, Berührung setzt die Uhr zurück (H24).
14. [ ] Selbstprüfung ohne FEHLER, PRÜFEN-Zeilen einzeln bewertet (Abschnitt 7).
15. [ ] Chromium während einer Sitzung beenden (zum Beispiel per SSH-Abbild oder nach Abschnitt 8, Alternative): Die Leiste kommt wieder, die VNC-Sitzung bleibt (H17).
16. [ ] Mitschauen und paralleler Zugriff über das PDH am echten HMI, sonst Option aus lassen (H6, Abschnitt 6.3).
17. [ ] Optional, falls ein Gerät mit Tastatur zur Verfügung steht: Chromium-Tastenkürzel und Adresseingabe im Kiosk. Die URL-Sperre per Richtlinie ist im Abbild; geprüft wird, dass Adresseingaben ins Leere laufen und die Kachelseite weiter lädt (H16).

**Netzwerk und Zeit (Abschnitt 6.6, 6.7)**

18. [ ] WLAN und Bluetooth abschalten, neu starten: Die Funkmodule fehlen (Overlay-Namen auf dem Pi 5), Selbstprüfung `schutz` zeigt es (H11).
19. [ ] Uhrzeit von Hand setzen und NTP-Server eintragen; um eine Zeitumstellung herum die mehrdeutige Stunde prüfen (H14).

**Dauerprüfung (Abschnitt 8)**

20. [ ] 24 Stunden außerhalb der Betriebszeit, Ergebnis ohne Leck und ohne Fehler. Fällt der Dienst während der Prüfung aus, ist der Stand der Seite leer; Messwerte bleiben in `soak.jsonl` (H15).
21. [ ] Watchdog: `schutz` meldet „eingestellt“. Ein echter Aussetzer wird nicht erzeugt (H19).

**Betrieb und Ausfall**

22. [ ] Stromausfall mehrfach, auch während einer Sitzung und kurz nach der Ersteinrichtung vor dem Setzen des Admin-Passworts (Abschnitt 10.5, H18; Erholung bei 500 siehe [betrieb.md](betrieb.md)).
23. [ ] Programm-Update einspielen und den automatischen Rückfall nach 60 Sekunden mit einer absichtlich falschen Version erzwingen ([betrieb.md](betrieb.md), Abschnitt 2, H20).

**Zweiter Raspi (Abschnitte 9 und 10)**

24. [ ] Beide Raspis im selben Netz mit gleichem Hostnamen und gleicher PARTUUID ohne Auffälligkeiten (H22).
25. [ ] Übernahme, Sperre, Vorrang, Netztrennung und Stromausfall mit beiden Geräten (Abschnitt 10, H25).

Alle Punkte erledigt: Die Abweichungen in Abschnitt 14 der Spezifikation nachtragen, die alte Karte des ersten Terminals erst danach löschen.
