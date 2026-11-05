# Inbetriebnahme

Dieses Handbuch führt vom Bau des Abbilds bis zum zweiten Terminal. Es gilt für die Raspberry Pi 5 mit dem fertigen SD-Abbild. Für andere Linux-Systeme siehe Abschnitt 12.

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
11. Offene Punkte nur auf Hardware
12. Andere Linux-Systeme (install.sh)

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

- Rechnen Sie mit einigen Minuten bis zum Neustart. Die genaue Dauer auf echter Hardware ist noch nicht gemessen (siehe Abschnitt 11).
- Warten Sie bis zu 10 Minuten. Schalten Sie das Gerät in dieser Zeit nicht aus.

Wenn nach 10 Minuten nichts erscheint:

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
4. Beim ersten Aufruf erscheint die Seite „Passwort festlegen“. Das Gerät hat noch kein Admin-Passwort.
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

Das Passwort beim HMI muss ein VNC-Passwort sein. Die Prüfung am HMI zeigt auch, wie der VNC-Server mehrere Clients behandelt (siehe Abschnitt 11).

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

Weitere Einstellungen unter „Kiosk“: Rückkehr zu den Kacheln bei Inaktivität (Minuten, 0 = nie), Kachel-Layout, Leiste ausblenden. Unter „Client“: Profil (Remmina oder TigerVNC), Qualität, Farbtiefe, Skalierung.

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

Die Dauerprüfung startet und beendet wiederholt einen Client gegen den eingebauten Test-VNC-Server und misst dabei den Speicher. Sie muss **mindestens 24 Stunden** laufen, **außerhalb der Betriebszeit**.

### Voraussetzungen

- Daueranzeige auf „Keine“ und keine laufende Sitzung. Sonst verweigert der Befehl den Start.
- **Niemand bedient das Terminal während der Prüfung.** Bedienung verfälscht die Werte.
- Der Befehl braucht eine Eingabeaufforderung auf dem Gerät. Das Standard-Abbild hat keine: SSH ist aus, alle Konten sind gesperrt. Für die Dauerprüfung gibt es zwei Wege:
  - Ein Abbild mit SSH bauen (Schlüsselanmeldung, siehe [betrieb.md](betrieb.md), „SSH einschalten“) und die Dauerprüfung darauf ausführen. Danach nach Konfigurationsexport entscheiden, ob SSH bleibt oder ein Abbild ohne SSH geschrieben wird.
  - Ein System aus `install.sh` (Abschnitt 12) verwenden.

### Start

Auf dem Gerät, zum Beispiel per SSH, als Benutzer `hmikiosk` mit der Umgebung der Grafiksitzung:

```
sudo -u hmikiosk env XDG_RUNTIME_DIR=/run/user/1100 $(cat /run/hmi-kiosk-session/env) \
  /data/app/current/hmi-kiosk soak -duration 24h
```

- Ein Durchgang dauert etwa eine halbe Minute plus Start und Ende des Clients. In 24 Stunden sind das einige tausend Durchgänge.
- Mit `nohup` oder `tmux` starten, damit die Prüfung das Trennen der SSH-Sitzung übersteht.
- Abbruch mit Strg+C ist erlaubt. Die Auswertung bis dahin bleibt gültig.

### Ergebnis lesen

Jeder Durchgang schreibt eine JSON-Zeile auf den Bildschirm und an die Datei `/data/log/soak.jsonl`. Die Datei ist nicht über die Admin-Seite herunterladbar. Felder:

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

Am Ende steht eine Zeile auf der Fehlerausgabe: „Dauerprüfung beendet: N Durchgänge, M Fehler“, bei Speicherzuwachs mit dem Zuwachs in MB je Stunde.

So wertet man aus:

1. Exit-Code 0 bedeutet: keine Fehler und kein Speicherzuwachs. Exit-Code 1: Fehler oder Zuwachs. Exit-Code 2: falscher Aufruf oder Prüfung nicht gestartet.
2. Letzte Zeile ansehen: `tail -n 1 /data/log/soak.jsonl` und `failures` lesen. Es sollte 0 sein.
3. Fehler suchen: `grep '"error"' /data/log/soak.jsonl`.
4. Zuwachs suchen: `grep '"leak":true' /data/log/soak.jsonl`. Ein Zuwachs wird ab 20 Durchgängen gemeldet, wenn er über 1 MB je Stunde liegt.
5. Bei `leak` den Zuwachs von `serviceRss` und `clientRss` vergleichen. Der größere nennt den Verursacher.
6. Auch ohne Meldung die Werte `rssBytes` am Anfang und Ende vergleichen. Sie sollten ähnlich sein.

Bestanden ist die Dauerprüfung bei 0 Fehlern und ohne Speicherzuwachs über mindestens 24 Stunden. Sonst Ergebnis aufheben und melden. Das Terminal bleibt bis zur Klärung nicht im Dauerbetrieb.

## 9. Zweiten Raspi umstellen

1. Auf dem ersten Terminal: Menü „System“, „Konfiguration sichern“.
2. Ein Passwort für die Datei vergeben (mindestens 10 Zeichen) und wiederholen, „Exportieren“.
3. Die Datei (`.hkx`) speichern. Das Passwort für die Datei gut aufbewahren: Ohne es ist die Datei nicht wiederherstellbar. Die Datei enthält VNC-Passwörter und den gemeinsamen Schlüssel.
4. Den zweiten Raspi wie ab Abschnitt 3 vorbereiten: alte Karte beschriften und aufbewahren, neue Karte schreiben, starten, Admin-Seite öffnen, Admin-Passwort festlegen.
5. Auf dem zweiten Terminal: Menü „System“, „Konfiguration wiederherstellen“. Passwort der Datei eingeben, Datei auswählen, „Importieren“.
6. Was der Import ändert und was nicht:
   - Übernommen werden HMIs, Client-, Kiosk- und Anzeige-Einstellungen, die Terminal-Liste und der gemeinsame Schlüssel.
   - Name, Kennung und Rolle dieses Terminals, das Admin-Passwort und die Netzwerk-Einstellungen (WLAN, Bluetooth, NTP) bleiben unverändert.
   - Ein Eintrag mit dem eigenen Namen wird aus der Terminal-Liste entfernt.
7. Auf dem zweiten Terminal nacharbeiten:
   - Menü „Terminals“: eigenen Namen und die Rolle setzen.
   - Das erste Terminal (Name und Adresse) in „Andere Terminals“ eintragen, falls es fehlt. Der Export des ersten Terminals enthält es nicht.
   - Unter „Gemeinsamer Schlüssel“ muss der Status „gesetzt“ sein.
   - Unter „Netzwerk“: WLAN und Bluetooth abschalten, NTP eintragen (diese Werte gelten je Gerät).
   - Eine Daueranzeige nach Bedarf eintragen.
8. Auf dem ersten Terminal prüfen, dass das zweite in „Andere Terminals“ steht.
9. Auf dem zweiten Terminal die Selbstprüfung (Abschnitt 7) und die Dauerprüfung (Abschnitt 8) ausführen.

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

## 11. Offene Punkte nur auf Hardware

Diese Punkte sind nur am echten Gerät zu klären. Aufgabe 19 ergänzt und schließt die Liste. Pro Punkt ankreuzen und das Ergebnis notieren. Der Rückfall steht rechts.

### Aus der Spezifikation (Abschnitt 14)

- [ ] Debian-Version der Raspis. Beim Bau Trixie angenommen. Rückfall: Das Abbild ersetzt das System ohnehin.
- [ ] Display-Auflösung und Touch-Eingabe am iiyama. Prüfung: Selbstprüfung `touch`, Anzeige unter System. Rückfall: Auflösung in der Admin-Seite überschreiben.
- [ ] HMI-Auflösung und Schärfe der Skalierung. Prüfung: Selbstprüfung und Betrieb. Rückfall: Qualität ändern oder TigerVNC.
- [ ] Remmina-VNC-Plugin unter Wayland/labwc. Prüfung: Selbstprüfung. Rückfall: Grafikprofil X11.
- [ ] labwc-Regeln für Platzierung und Größe (die Regel greift auf der Hardware, kein Rahmen). Prüfung: Selbstprüfung `fenster-leiste`. Rückfall: Grafikprofil X11 mit openbox.
- [ ] Geteilter Zugriff bei „Mitschauen“ und bei parallelem Zugriff über das PDH. Prüfung am HMI. Rückfall: Option aus lassen.
- [ ] VNC-Server der HMIs: Verhalten bei mehreren Clients. Prüfung: Inbetriebnahme. Kein Rückfall.

### Aus dem Bau des Abbilds (Bericht zu Aufgabe 17)

- [ ] Erster Start insgesamt: Partition anlegen, solange die Wurzel läuft (bisher nur mit einem Loop-Gerät getestet), 30-Sekunden-Frist der Geräteerkennung, Neustart, Start mit Overlay. Bei Notfallmodus hilft ein Neustart; das Protokoll liegt in `hmi-kiosk-firstboot.log` auf der Boot-Partition.
- [ ] Dauer des ersten Starts messen und in Abschnitt 4 eintragen.
- [ ] Anmeldung der Grafiksitzung auf tty1 über logind (labwc), rootloses Xorg im Profil X11 und der Treiber für den Pi 5 im Profil X11.
- [ ] Chromium-Sandbox (braucht User-Namespaces; das Paket `chromium-sandbox` ist nicht installiert).
- [ ] Mauszeiger bleibt unter labwc auch über Chromium und Remmina unsichtbar.
- [ ] SSH: Die Regel der Anlage ist unbekannt. Das Abbild hat SSH aus. Entscheidung treffen (siehe [betrieb.md](betrieb.md)).
- [ ] IP-Konfiguration: Das Abbild nutzt DHCP. Feste Adressen im DHCP-Server eintragen oder vor Ort klären.
- [ ] WLAN und Bluetooth: Wirkung der Overlays `disable-wifi` und `disable-bt` auf dem Pi 5 nach dem Neustart bestätigen (Selbstprüfung `schutz`, PRÜFEN).
- [ ] Beide Raspis mit gleichem Hostnamen und gleicher PARTUUID im selben Netz: keine Auffälligkeiten.
- [ ] Watchdog: `schutz` meldet „eingestellt“; ein echter Aussetzer wird nicht erzeugt.
- [ ] Stromausfall mehrfach (Abschnitt 10.5), auch während einer Sitzung.
- [ ] Update einspielen und den automatischen Rückfall nach 60 Sekunden prüfen (siehe [betrieb.md](betrieb.md)).
- [ ] Zeitumstellung: `set-time` bei mehrdeutiger Zeit zur Sommerzeitumstellung am Gerät prüfen.
- [ ] Die Vorabprüfung belegt kurz eine zusätzliche VNC-Verbindung je Start. Am HMI beobachten, dass das den VNC-Server nicht stört.

## 12. Andere Linux-Systeme (install.sh)

Für ein System ohne das fertige SD-Abbild gibt es ein Offline-Bundle und `install.sh`. Es gilt für Debian oder Raspberry Pi OS **Trixie** auf arm64 oder amd64. Es gibt keinen Schreibschutz und keine eigene Datenpartition. Das Abbild bleibt der empfohlene Weg.

### Bundle bauen

Auf dem Entwicklungsrechner, mit Docker und Internet:

```
packaging/bundle.sh --version 1.0.0 --arch arm64
```

Für ein PC-System `--arch amd64`. Das Programm wird dabei mit `.secrets/release.pub` gebaut (Abschnitt 1). Das Ergebnis liegt in `dist/`: `hmi-kiosk-bundle-1.0.0-trixie-arm64.tar.gz` und die Prüfsumme dazu. Das Bundle enthält die Programme, die Dienstdateien und alle Pakete samt Abhängigkeiten.

### Installieren

1. Bundle auf das Zielsystem kopieren und Prüfsumme kontrollieren (`sha256sum -c`).
2. Entpacken: `tar xzf hmi-kiosk-bundle-1.0.0-trixie-arm64.tar.gz`
3. Als root ausführen:

   ```
   cd hmi-kiosk-bundle-1.0.0-trixie-arm64
   sudo ./install.sh
   ```

4. Das Skript braucht kein Netz. Es prüft vor allem anderen:
   - `VERSION_CODENAME` in `/etc/os-release` gleich `trixie`
   - die dpkg-Architektur gleich der des Bundles
   - die Prüfsummen des Bundles
5. Bei einer Abweichung bricht es mit einer klaren Meldung ab und verändert nichts.

Optionen:

| Option | Wirkung |
|---|---|
| `--no-start` | Nur einrichten, Dienste weder aktivieren noch starten (für Tests) |
| `--online` | Pakete aus den Paketquellen des Systems holen statt aus dem Bundle |
| `--data-dir ORDNER` | Datenordner, Standard `/var/lib/hmi-kiosk` (im Abbild `/data`) |

### Was das Skript einrichtet

- Pakete aus dem Bundle (ohne die Punkte, die nur das Abbild braucht: `raspi-config`, `overlayroot`, `fdisk`).
- Benutzer `hmikiosk` (Kennung 1100, wenn frei) in den Gruppen `input`, `video`, `render`, ohne Passwort.
- Der Helfer unter `/usr/local/bin/hmi-kiosk-helper`, der Dienst unter `<Datenordner>/app/<Version>/hmi-kiosk` mit dem Verweis `current`.
- Die vier Dienste (`hmi-kiosk`, `hmi-kiosk-helper`, `hmi-kiosk-session`, `hmi-kiosk-update-guard`) in `/etc/systemd/system`, mit einer Ergänzungsdatei `10-install.conf` für Datenordner und Benutzerkennung.
- Ohne systemd (zum Beispiel in einem Container) werden die Dienste nicht aktiviert. Das Skript sagt es.
- Das Skript ist wiederholbar. Eine vorhandene Konfiguration im Datenordner bleibt unberührt.
- Es schaltet **kein** Overlay und keine schreibgeschützte Boot-Partition ein.

Auf einem solchen System meldet die Selbstprüfung beim Schutz (`schutz`) fehlendes Overlay nur als Hinweis (PRÜFEN), nicht als Fehler.

Nicht geprüft: Die Pakete kommen aus Debian Trixie, nicht aus dem Raspberry-Pi-Archiv. Das Abbild nutzt dort ein neueres `labwc`. Wenn die Fensterregeln unter labwc auf einem solchen System nicht greifen, hilft das Grafikprofil „X11 (openbox)“.
