# W1: PDH-Terminal unter Windows 11, Betriebsart „Zusatz“ (Design)

Stand 2026-11-20. Erstes Teilprojekt des Wegs „Windows zuerst“ (siehe
`2026-11-12-plattformen-design.md`, Abschnitt „Reihenfolge ab 2026-11-20“). W1 fasst die Teile von
B1, B2, B3 und B5 zusammen, die für ein nutzbares Windows-Setup nötig sind.

## 1. Absicht

Das PDH-Terminal soll auf vorhandenen Windows-11-PCs laufen, an denen nebenher normal gearbeitet wird
(z. B. Excel, HMI-Software). Ein günstiger USB-RFID-Leser am PC meldet Personen an PDH an wie der
PN532 am Pi. Installiert wird per Setup. Ein eigenständiges Windows-Kioskgerät folgt in W2, das globale
Terminal (Infobildschirm ohne Anmeldung) in G.

**Erfolg:** Auf einem frischen Windows 11 (Home oder Pro):

1. Setup per Doppelklick installieren.
2. Das Terminal öffnet sich und lässt sich mit PDH koppeln wie ein Pi.
3. Eine Karte am USB-Leser meldet die Person an, auch wenn Excel vorne ist. In Excel kommt kein
   Zeichen an.
4. Eine Benachrichtigung erscheint in der in PDH für dieses Terminal gewählten Art.
5. Ein Update von der Release-Seite lässt sich per Klick installieren.

## 2. Ziele, Nicht-Ziele, Anti-Ziele

**Ziele**
- `pdh-terminal.exe` (windows/amd64) aus demselben Code wie auf dem Pi, mit Edge als Fenster.
- USB-Tastaturleser (HID, 13,56 MHz) mit Abfangen der Eingaben, Umrechnung des Nummernformats.
- Benachrichtigung bei Kartenanmeldung, während das Terminal nicht vorne ist: Arten A–D, je Terminal in
  PDH wählbar und kombinierbar.
- Setup `PDH-Terminal-Setup-<version>.exe`: Installation, Autostart, Firewall, Startmenü, Deinstallation.
- Signierte Updates über die Release-Seite mit Rückfall bei Fehlstart.

**Nicht-Ziele (später)**
- Kioskbetrieb unter Windows (W2), globales Terminal (G).
- Updates von PDH aus anstoßen, Versionsliste in PDH.
- PC/SC-Leser, PN532 über USB-Seriell (B3-Rest), Linux-Desktop-Installer (B4).
- Windows auf arm64.
- Code-Signatur mit gekauftem Zertifikat (siehe 9).

**Anti-Ziele (wäre ein Fehlschlag, auch wenn es technisch läuft)**
- Kartennummern landen in anderen Programmen, oder normale Tastatureingaben gehen verloren.
- Das Terminal stört die Arbeit am PC ungefragt: Ohne Art D drängt es sich nie nach vorn; geschlossen
  bleibt es geschlossen.
- Pi-Verhalten ändert sich, oder alte Pi-Versionen nehmen Windows-Pakete an.
- Eine bestehende PDH- oder Terminal-Konfiguration wird ungültig (Regel „rückwärtskompatibel“).

## 3. Randbedingungen

- **Rückwärtskompatibel**
  - Nur hinzufügende Migrationen und optionale Felder.
  - Alte `config.json` und alte PDH-Stände arbeiten mit neuen Terminals; neue PDH-Stände arbeiten mit
    alten Terminals.
- **PDH-Migrationen**
  - Nie ändern, nur neue hinzufügen.
  - Neue Tabellen bekommen `id UUID DEFAULT pdh_uuid_v7()`.
  - PDH-Arbeit in einem eigenen Worktree von `main` des PDH-Servers, nicht im Hauptcheckout
    `C:\workplace\pdh-server`.
- **Werkzeuge**
  - Bau mit `scripts/dgo` (Go 1.24 in Docker), `CGO_ENABLED=0`.
  - Keine C-Abhängigkeiten: Win32-Aufrufe über `golang.org/x/sys/windows`.
  - Edge ist auf Windows 11 immer vorhanden; das Setup installiert keinen Browser.
- **Windows-Editionen:** Home, Pro, Enterprise, Education (W2 erst schränkt auf Pro+ ein).

## 4. Ansätze (entschieden)

Reihenfolge der Teilprojekte: Der Nutzer hat „Windows zuerst, schlank“ (W1 → W2 → G → PDH-Updates)
gewählt und die Ausführung „was am Token-sparsamsten ist“ freigegeben. Daraus folgt: nacheinander in
einer Sitzung. Die verworfenen Wege:
- **Globales Terminal zuerst:** schneller sichtbar, aber Windows später.
- **B1 in voller Breite vorneweg:** am längsten bis zum ersten Windows-Ergebnis.

Technische Festlegungen (Calls des Designers):

| Frage | Entscheidung | Verworfen, weil |
|---|---|---|
| Fenster | Edge im App-Modus (`msedge --app=… --user-data-dir=…`), wie Chromium am Pi von einem Supervisor gestartet | WebView2: eigene Bindung, für W2-Kiosk ohnehin Edge nötig |
| Prozessmodell | Ein Programm in der Benutzersitzung, Start über Autostart (HKLM `Run`) | Windows-Dienst: Dienste laufen in Sitzung 0 und sehen weder Tastatur noch Bildschirm |
| Leser | Raw Input (Gerät erkennen) + Low-Level-Tastatur-Hook (Tasten des Lesers verschlucken) | Nur Raw Input kann nicht blockieren; Hook allein kennt das Gerät nicht |
| Setup | Inno Setup, gebaut in Docker (Wine), Ausgabe `.exe`, stille Installation mit `/VERYSILENT` | MSI (wixl): keine Dialoge für die Betriebsart (W2), Firewall nur über Umwege; MSI war ein früherer Vorschlag im Gespräch und ist hiermit ersetzt |
| Updates | Versionsordner + Starter statt Symlinks | Symlinks brauchen unter Windows Adminrechte oder Entwicklermodus |

## 5. Was der Nutzer entschieden hat

- Windows-Terminals haben einen günstigen USB-RFID-Leser, der die Karten lesen kann.
- Beide Betriebsarten sollen möglich sein: neben normaler Arbeit (W1) und nur Terminal (W2).
- **Karte, während ein anderes Programm vorne ist:** Es erscheint eine Benachrichtigung.
  - Vier Arten sind wählbar:
    - **A:** Windows-Mitteilung.
    - **B:** großes Fenster in der Mitte.
    - **C:** Leiste am oberen Rand.
    - **D:** Terminal kommt nach vorn.
  - Die Auswahl gilt je Terminal; Kombinationen sind möglich.
- Ein globales Terminal (Infobildschirm ohne Anmeldung) kommt als eigenes Teilprojekt G.

## 6. Design

### 6.1 Programmaufbau

- **`cmd/pdh-terminal`:**
  - `main.go` wird plattformneutral.
  - Die Vorgaben für Datenordner, Laufzeitordner und Helfer kommen aus einer Plattformdatei
    (`defaults_linux.go`, `defaults_windows.go`).
  - **Windows:**
    - Datenordner `%ProgramData%\PDH-Terminal`.
    - Laufzeitordner `%LOCALAPPDATA%\PDH-Terminal\run`.
    - Kein Helfer-Socket.
- **Plattformschnittstelle im Dienst:**
  - **Grundsatz:** Was heute direkt Linux nutzt, kommt hinter eine kleine Schnittstelle in
    `internal/product/pdhterminal`, mit einer Linux- und einer Windows-Umsetzung.
  - **Browser:** starten, beenden, „nach vorn holen“, „ist vorne?“.
  - **Helfer:** unter Windows `nil`. Ohne Helfer blendet die Admin-Seite aus, was ihn braucht:
    Netzwerk, Zeit, Sicherung, Abbild.
  - **Ursprungsabgleich:** unter Windows ohne Wirkung, weil Zusatz die Navigation nicht beschränkt.
  - **Hardwarekennung:** `MachineGuid` aus der Registry.
  - **Systemstand:** gibt es unter Windows nicht. Prüfungen auf den Systemstand gelten nur für Linux.
- **Bildschirmtastatur (wvkbd), OLED, PN532:** nur Linux. Die Admin-Seite zeigt unter Windows nur
  „kein Leser“ und „USB-Leser (Tastatur)“.
- **Selbstprüfung:** unter Windows die plattformneutralen Schritte plus „Edge gefunden“ und
  „USB-Leser“.
- **Abgrenzung:** Die Zusammenstellung dieser Liste ist Sache des Builders, ebenso welche bestehenden
  Linux-Teile hinter Build-Tags wandern.

### 6.2 Edge-Fenster

- **Start:**
  - Der Supervisor startet `msedge.exe --app=http://127.0.0.1:8080/start
    --user-data-dir=%LOCALAPPDATA%\PDH-Terminal\edge --no-first-run --disable-features=msEdgeSleepingTabs`.
  - Dazu kommen Schalter gegen Hintergrund-Drosselung, damit der Kartenstrom (SSE) und die Anmeldung
    auch im verdeckten Fenster sofort laufen.
  - Den Pfad zu Edge liest der Supervisor aus der Registry (`App Paths\msedge.exe`).
- **Lokales Netz (Call des Designers):**
  - Die PDH-Seite (HTTPS) ruft `http://127.0.0.1:8080` auf. Am Pi erlaubt das die Chromium-Richtlinie
    `LocalNetworkAccessAllowedForUrls`, die der Helfer schreibt.
  - Unter Windows erlaubt der Supervisor das nur für die eigene Edge-Instanz, über Startschalter, die
    die Prüfung für lokale Netzzugriffe abschalten. So ändert sich das normale Edge des Benutzers
    nicht.
  - Reichen Schalter in der vorhandenen Edge-Version nicht, schreibt das Programm dieselbe Richtlinie
    unter HKCU. HKLM geht nicht, weil das Programm ohne Adminrechte läuft und die PDH-Adresse erst nach
    der Installation feststeht.
- **Nur Edge (Call des Designers):** Fehlt Edge oder ist es per Richtlinie gesperrt, bleibt das
  Terminal rot mit Grund (6.8). Einen Ersatzbrowser gibt es nicht.
- **Schließt der Benutzer das Fenster (Entscheidung des Nutzers: „Schließen wirkt wie Minimieren“):**
  - Der Supervisor startet Edge sofort neu, minimiert in der Taskleiste und ohne Fokus. Das neue
    Fenster nimmt dem Benutzer weder Tastatur noch Vordergrund weg.
  - Die Terminalseite ist damit immer geladen. Eine Karte meldet ohne Verzögerung an.
  - Ganz beenden geht nur über das Tray-Symbol („Beenden“).
  - Startet Edge in kurzer Folge mehrmals nicht, gelten die Pausen aus 6.8. Eine Neustartschleife darf
    nicht entstehen.
  - Das Symbol im Infobereich (Tray) bietet „Terminal öffnen“, „Admin-Seite“, „Selbstprüfung“ und
    „Beenden“. „Beenden“ hält das Programm bis zur nächsten Anmeldung an.
- **Fenster erkennen:** Das Programm findet das Fenster über die Prozess-ID des gestarteten Edge,
  genauer seiner Kindprozesse mit sichtbarem Fenster der Klasse `Chrome_WidgetWin_1`. Gebraucht wird das
  für „ist vorne?“ und „nach vorn holen“.

### 6.3 USB-Tastaturleser

- **Bestehender Lesertyp:**
  - Es bleibt `card.type = "usb"`; `usbDevice` wählt das Gerät, `format` die Schreibweise (`hex`,
    `hex-rev`, `dec`).
  - Unter Windows nennt `usbDevice` VID:PID (`vvvv:pppp`) oder einen Teil des Gerätenamens wie unter
    Linux.
- **Umsetzung (`card/usb_windows.go`):**
  - **Raw Input:** Ein unsichtbares Nachrichtenfenster registriert sich mit `RIDEV_INPUTSINK` für
    Tastaturen. Jede Taste kommt mit dem Gerätehandle; passt das Gerät zum Leser, geht das Zeichen in
    den bestehenden `keyBuffer` (Enter schließt ab, Pause > 1 s verwirft).
  - **Low-Level-Hook (`WH_KEYBOARD_LL`):**
    - Er verschluckt Tasten, die laut Raw Input vom Leser stammen.
    - Die Zuordnung zwischen Hook-Ereignis und Raw-Input-Ereignis läuft über Reihenfolge und Scancode.
      Das genaue Verfahren legt der Builder fest.
    - Gefordert ist: Keine Lesertaste erreicht ein anderes Programm, keine Taste der echten Tastatur
      geht verloren.
  - **Gerät abgezogen:** Leserzustand „getrennt“ wie unter Linux; Wiederanstecken wird erkannt.
  - **Tastatur-Layout:** Leser tippen nach US-Layout. Die Zuordnung Scancode → Zeichen ist fest
    (unabhängig vom Windows-Layout), wie die `keyChars`-Tabelle unter Linux.
- **Gerät wählen auf der Admin-Seite:**
  - Liste der angeschlossenen Tastaturen mit Name und VID:PID.
  - Knopf „Karte vorhalten“ erkennt das Gerät, das zuletzt eine schnelle Ziffernfolge mit Enter
    geschickt hat, und trägt es ein.
  - Dabei schlägt die Seite ein Format vor: Liefert die Folge genau 8 oder 14 Hex-Zeichen, `hex`; nur
    Ziffern mit 10 Stellen, `dec`. Im Zweifel bleibt `hex`.
  - **Vergleich mit dem Pi:** Die Admin-Seite zeigt die erkannte UID in allen drei Schreibweisen. Wer
    die Karte am Pi kennt, sieht so, welche passt.
- **Risiko Virenscanner:** Ein unsigniertes Programm mit Tastatur-Hook kann als Keylogger gelten.
  - Der Plan beginnt deshalb mit einem Spike: Hook und Raw Input in einer Mini-Exe auf einem
    Windows-11-PC mit eingeschaltetem Defender.
  - Schlägt Defender an, kommt das vor dem weiteren Bau zum Nutzer.

### 6.4 Benachrichtigung bei Kartenanmeldung

- **Ablauf heute:**
  - Die Startseite meldet das Gerät bei PDH an.
  - Die PDH-Terminalseite im Edge-Fenster liest den Kartenstrom `http://127.0.0.1:8080/api/card/events`
    und schickt die UID an `POST /terminal/card`.
  - Das Ergebnis (Person oder Fehler) kennt also nur die PDH-Seite.
- **Neu, lokaler Endpunkt `POST /api/card/result` am Terminal:**
  - Er ist auf dieselbe Herkunft beschränkt wie der Kartenstrom (`cardOrigin`).
  - Die PDH-Seite schickt nach jeder Kartenanmeldung `{"ok":bool,"name":"…","message":"…","notify":["overlay",…]}`.
    `notify` ist die Einstellung des Terminals aus PDH.
  - Ein altes Terminal kennt den Endpunkt nicht. Die PDH-Seite ignoriert den Fehler (404) still.
  - Neue Linux-Versionen nehmen den Endpunkt an und antworten 204, ohne etwas zu tun. Am Pi gibt es
    kein anderes Programm vorne (Call des Designers).
  - PDH kennt Plattform und Betriebsart des Geräts nicht. Das Formular zeigt den Hinweis deshalb immer
    (Call des Designers).
- **Das Terminalprogramm zeigt die Benachrichtigung, wenn:**
  - das Edge-Fenster nicht im Vordergrund oder geschlossen ist, und
  - `notify` nicht leer ist.
  - Ist das Fenster vorne, zeigt die PDH-Seite das Ergebnis selbst, wie bisher.
- **Arten:**
  - **A, Windows-Mitteilung:** Toast über die App-Kennung `PDH.Terminal`, die das Setup mit der
    Startmenü-Verknüpfung registriert. Knopf „Terminal öffnen“.
  - **B, Fenster in der Mitte:** eigenes Fenster, immer obenauf, ohne Fokus.
    - Inhalt: Name, Ergebnis, Uhrzeit.
    - Schließt nach 4 s; ein Klick schließt sofort.
  - **C, Leiste oben:** eigenes Fenster über die volle Breite des Hauptbildschirms, 48 px hoch, ohne
    Fokus. Schließt nach 4 s.
  - **D, Terminal nach vorn:**
    - Das Edge-Fenster wird geöffnet oder nach vorn geholt.
    - Windows erlaubt `SetForegroundWindow` aus dem Hintergrund nur eingeschränkt. Der Builder wählt
      einen dokumentierten Weg, der nicht flackert; reicht keiner, blinkt die Taskleiste.
- **Darstellung von B und C:** Erfolg grün, Fehler rot, Abmeldung grau. Text deutsch: „Angemeldet:
  <Name>“, „Abgemeldet: <Name>“, bei Fehler die Meldung von PDH („Karte nicht registriert“ …).
- **PDH-Seite:**
  - **Neue Migration:** `terminal_settings.card_notify TEXT[] NOT NULL DEFAULT '{}'`, Werte aus
    `toast`, `overlay`, `banner`, `front`, geprüft mit CHECK.
  - **Leere Liste:** bedeutet „Vorgabe“, und die Vorgabe ist `{overlay}`. PDH setzt sie beim Senden
    ein, damit ein späterer Wechsel der Vorgabe ohne Datenänderung geht.
  - **Ganz abschalten:** ein eigener Wert `off`, nicht die leere Liste. `off` schließt andere Werte
    aus.
  - **Formular:** Die Einstellung sitzt beim Terminal neben der Anmeldeart (K1), als vier Kästchen plus
    „Keine Benachrichtigung“.
  - **Hinweis im Formular:** „Wirkt nur an Windows-Terminals in der Betriebsart Zusatz.“
  - **Übermittlung:**
    - Die Einstellung geht wie `manual_login` über den Ereigniskanal `/terminal/events` an die
      Terminalseite.
    - Die Seite gibt sie im `notify`-Feld weiter.
    - Ändert sich die Einstellung, wirkt sie ab der nächsten Karte ohne Neuladen.

### 6.5 Updates und Startregel

- **Ordner**
  - Das Setup installiert die Grundversion nach `%ProgramFiles%\PDH-Terminal\` (`pdh-terminal.exe`,
    Starter).
  - Updates landen in `%ProgramData%\PDH-Terminal\app\<version>\pdh-terminal.exe`.
- **Starter `pdh-terminal-start.exe`, im Setup enthalten, wird vom Autostart aufgerufen:**
  - Er startet die höchste Version, deren Signatur gültig ist und die nicht als „schlecht“ markiert
    ist.
  - Zur Wahl stehen die Grundversion und alle Versionsordner. Das ist die Regel „höhere Version
    gewinnt“ aus der Übersicht.
  - Die Signatur prüft er mit demselben öffentlichen Schlüssel. Dafür steht neben jedem
    Versionsordner die Paketsignatur, oder der Starter prüft den Hash aus dem signierten Paket; die
    Wahl trifft der Builder.
- **Prozessbaum (Call des Designers):**
  - Der Starter bleibt als Elternprozess laufen. Er hält den systemweiten Mutex (eine Instanz je PC)
    und startet das Programm.
  - Stürzt das Programm ab, startet der Starter es neu, mit wachsender Pause: 5 s, 30 s, dann 5 min.
  - „Beenden“ im Tray beendet das Programm mit einem festgelegten Rückgabewert. Der Starter endet dann
    ebenfalls, bis zur nächsten Anmeldung.
- **Gesundmeldung (Call des Designers):** Das Programm meldet sich gesund, sobald Startseite und
  Admin-Seite gebunden sind und die Konfiguration geladen ist.
  - Signal: Es legt `app\<version>\healthy` an; für die Grundversion gilt dasselbe in
    `%ProgramData%\PDH-Terminal\base-healthy-<version>`.
  - Der Starter wartet höchstens 60 s darauf.
- **Prüfung bei jedem Start (Call des Designers):**
  - Im Versionsordner liegt das signierte Paket `.pkg` selbst.
  - Der Starter prüft vor jedem Start die Signatur des Pakets und den Hash der Exe gegen das Paket.
  - Er öffnet die Exe dabei so, dass sie bis zum Start nicht ersetzt werden kann.
  - Ein selbst angelegter Ordner ohne gültiges Paket wird übersprungen. Die Grenze aus 6.8 bleibt:
    Benutzer des PCs gelten in „Zusatz“ als vertrauenswürdig.
  - Die Paketprüfung kommt aus `internal/update`. Aktivieren und Zurückfallen über Symlinks und Helfer
    bleiben Linux-Sache.
  - Unter Windows schreibt das Programm Updates selbst in den Datenordner, ohne erhöhte Rechte.
- **Update-Wächter wie am Pi:**
  - Eine neue Version gilt als „ausstehend“.
  - Meldet sie sich nicht innerhalb von 60 s als gesund, beendet der Starter sie, markiert sie als
    schlecht und startet die vorherige.
  - Ein Setup mit höherer Version gewinnt ebenfalls gegen ältere Versionsordner; die alten Ordner
    räumt das Programm beim nächsten gesunden Start auf (behält eine vorherige).
- **Pakete**
  - Dasselbe Format `.pkg`, dasselbe Produkt `pdh-terminal`, derselbe Schlüssel.
  - Neues optionales Manifestfeld `platform`, z. B. `windows-amd64`. Fehlt es, gilt `linux-arm64`.
  - Neue Versionen lehnen Pakete einer anderen Plattform ab.
  - Release-Datei: `pdh-terminal-<version>-windows-amd64.pkg` am selben Release `pdh-terminal-v<version>`.
  - Die Updateprüfung unter Windows sucht genau diesen Namen. Der Pi sucht weiter
    `pdh-terminal-<version>.pkg` und findet die Windows-Datei deshalb nicht.
  - **Altes Pi und manuell hochgeladenes Windows-Paket:**
    - Ein altes Pi kennt `platform` nicht und würde ein von Hand hochgeladenes Windows-Paket
      installieren.
    - Das Programm startet dann nicht, und der Update-Wächter schaltet nach 60 s zurück.
    - Das ist hinnehmbar und kommt in `docs/betrieb.md`.
- **Systemstand:** gilt unter Windows nicht. Ein Windows-Paket trägt keinen Systemstand.

### 6.6 Setup

- **Bauweise:** Inno Setup in Docker. Das Skript liegt in `packaging/windows/`, Ausgabe
  `dist/PDH-Terminal-Setup-<version>.exe`.
- **Verhalten:**
  - Installation für alle Benutzer (Adminrechte, einmalige UAC-Abfrage).
  - In W1 fragt das Setup nach keiner Betriebsart, es installiert immer „Zusatz“. Die Seite
    „Betriebsart“ kommt mit W2.
  - **Dateien:** Programm und Starter nach `%ProgramFiles%\PDH-Terminal`.
  - **Datenordner `%ProgramData%\PDH-Terminal`:** Vollzugriff für SYSTEM und Administratoren, Ändern
    für die Gruppe „Benutzer“, weil das Programm als angemeldeter Benutzer läuft.
  - **Autostart:** `HKLM\Software\Microsoft\Windows\CurrentVersion\Run`, Starter für jeden Benutzer.
  - **Startmenü:** „PDH-Terminal“ (öffnet bzw. startet) und „PDH-Terminal Admin“. Die Verknüpfung trägt
    die App-Kennung `PDH.Terminal` für Mitteilungen.
  - **Firewall:** eingehende Regel für TCP 8443 (Admin-Seite aus dem Netz) im Profil Domäne/Privat,
    nicht Öffentlich.
  - **Edge-Richtlinien:** nur das Nötigste unter HKLM (kein Erststart-Assistent). Nichts, was das
    normale Edge des Benutzers verändert.
  - **Deinstallation:**
    - Entfernt Programm, Autostart, Firewall-Regel und Verknüpfungen.
    - Der Datenordner bleibt, mit Hinweis; die Kopplung bleibt so bei einer Neuinstallation erhalten.
  - **Stille Installation:** `/VERYSILENT /SUPPRESSMSGBOXES` für die Verteilung.
- **Mehrere Benutzer am selben PC:**
  - Es läuft genau eine Instanz je PC. Ein zweiter angemeldeter Benutzer (schneller Benutzerwechsel)
    bekommt keine zweite Instanz; der Starter erkennt die laufende über einen systemweiten Mutex.
  - Der Starter eines zweiten Benutzers endet still, ohne Tray-Symbol (Call des Designers).
    Dokumentieren: „Ein Terminal je PC, es gehört der zuerst angemeldeten Sitzung.“

### 6.7 Admin-Seite unter Windows

- Erreichbar über das Startmenü, das Tray-Symbol und https://<PC>:8443 aus dem Netz, mit demselben
  Admin-Passwort wie am Pi.
- Abschnitte, die den Helfer brauchen (Netzwerk, Zeit, Sicherung, Abbild), sind unter Windows
  ausgeblendet. Der Text verweist dafür auf Windows.

### 6.8 Fehler und Grenzen

- **Edge fehlt oder startet nicht:** Tray-Symbol rot, Selbstprüfung rot mit Grund, erneuter Versuch
  alle 30 s.
- **Leser nicht gefunden:** Leserzustand „getrennt“, wie unter Linux. PDH sieht das über den
  bestehenden Leserstatus.
- **Port 8080 oder 8443 belegt:** Meldung im Tray und in der Selbstprüfung. Kein Absturz in Schleife.
- **Datenschutz-Grenze:** Jeder Benutzer am PC kann den Datenordner lesen, also auch das
  Gerätegeheimnis. In der Betriebsart Zusatz gelten die Benutzer des PCs als vertrauenswürdig.
  Dokumentieren; W2 (Kiosk) schließt das ab.

## 7. Prüfung

- **Unit-Tests:** wie bisher mit `scripts/dgo test -race ./...`.
  - Windows-Teile mit `GOOS=windows` bauen und `go vet` laufen lassen.
  - Tests für Windows-Logik, die ohne Win32 auskommt (Starter-Auswahl, Formaterkennung, Plattformfeld
    im Paket), plattformneutral schreiben.
- **Kreuzbau:** `GOOS=windows GOARCH=amd64` für `./cmd/pdh-terminal` und den Starter.
  `GOOS=linux GOARCH=arm64` bleibt grün. `packaging/test-products.sh` bleibt grün.
- **PDH:** Migration und Formular mit Integrationstest wie bei K1. Der Ereigniskanal-Test prüft das
  neue Feld, ein alter Terminalstand ohne `/api/card/result` bleibt fehlerfrei.
- **Geräteprüfliste `dist/geraetetest-w1.md`** (nicht im Repo), Ablauf auf einem echten Windows-11-PC:
  1. Installieren, koppeln.
  2. Leser wählen, Karte vor Excel.
  3. A–D einzeln und kombiniert.
  4. Fenster schließen: Es kommt minimiert zurück und nimmt nicht den Fokus. Karte vorhalten: Die
     Anmeldung läuft, die Benachrichtigung erscheint.
  5. Update und absichtlicher Fehlstart.
  6. Deinstallieren.

## 8. Dem Builder überlassen

- Paketaufteilung der Plattformschnittstelle und welche Linux-Dateien Build-Tags bekommen.
- Zuordnungsverfahren zwischen Raw Input und Hook (6.3), solange die Anforderung gilt.
- Zeichnen der Fenster B/C (GDI oder anderes, ohne CGO).
- Weg für „nach vorn holen“ (6.4 D).
- Wie der Starter die Signatur je Versionsordner prüft (6.5).
- Inno-Setup-Docker-Abbild und Skriptaufbau.
- Reihenfolge der Plan-Aufgaben. Der Spike zum Virenscanner (6.3) steht am Anfang.

## 9. Offene Punkte

- **Code-Signatur:** Ohne gekauftes Zertifikat zeigt Windows beim Setup „Unbekannter Herausgeber“
  (SmartScreen). Ein Zertifikat kostet etwa 200–400 € im Jahr. W1 kommt ohne aus; die Entscheidung
  liegt beim Nutzer.
