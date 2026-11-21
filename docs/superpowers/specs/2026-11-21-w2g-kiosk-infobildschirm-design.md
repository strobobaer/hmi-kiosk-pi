# W2+G: Windows-Kioskgerät und globales Terminal (Infobildschirm) (Design)

Stand 2026-11-21. Ein gemeinsames Teilprojekt aus W2 und G der Übersicht
`2026-11-12-plattformen-design.md` (Abschnitt „Reihenfolge ab 2026-11-20“). Der Nutzer wollte beides als eins
und hat auf Rückfragen verzichtet („leg los“). Alles, was nicht unten unter „Entscheidungen des Nutzers“ steht,
ist ein Call des Designers.

## 1. Absicht

Ein Terminal kann künftig zwei Dinge, die zusammengehören:

1. **Globales Terminal (Infobildschirm):** ein Schalter je Terminal in PDH. Ist er an, zeigt das Gerät eine
   Folienschau für Büro und Empfang, getrennt von den Anlagen-Terminals. Es hat keine Anmeldung, keinen Leser und
   keine Benachrichtigung. Läuft auf allen Plattformen, auch auf den vorhandenen Pis.
2. **Windows-Kioskgerät:** Ein Windows-11-PC zeigt nur das Terminal. Das gilt für ein Anlagen-Terminal mit Leser
   und ebenso für einen Infobildschirm. Das Setup richtet das ein; das System bleibt für den normalen Benutzer
   gesperrt, und die Admin-Seite ist über die Ecke oben rechts erreichbar wie am Pi.

**Erfolg:**
- **Pi:** Ein Pi wird in PDH auf „Globales Terminal“ gestellt. Er zeigt binnen Sekunden die Folien, blättert
  weiter, öffnet per Antippen einen Vorgang nur zum Lesen und kehrt nach der Rückkehrzeit zur Folienschau
  zurück.
- **Windows:** Ein frischer Windows-11-Pro-PC wird mit dem Setup in der Betriebsart „Kiosk“ installiert. Nach dem
  Neustart meldet er sich selbst an und zeigt nur das Terminal. Eine 5-Sekunden-Berührung oben rechts öffnet die
  Admin-Seite.

## 2. Entscheidungen des Nutzers (aus früheren Gesprächen)

- **Globales Terminal:**
  - Es ist eine Funktion zum Aktivieren je Terminal.
  - Gezeigt werden Bilder, Texte und Infos wie beim normalen Terminal.
  - Mit der Eingabe kann man nur einen Vorgang öffnen oder weiterblättern.
  - Keine Anmeldung, kein Leser, keine Benachrichtigung.
  - Die Inhalte werden getrennt von allen anderen Terminals eingestellt.
  - Es gilt für alle Plattformen.
- **Folienliste:**
  - Folienarten: Bild, Text und „Vorgänge“ ausgewählter Anlagen.
  - Jede Folie hat eine Dauer; die Liste läuft im Kreis.
  - Ein Vorgang wird nur zum Lesen geöffnet.
  - Es gilt die Rückkehrzeit.
  - Offline läuft die Folienschau aus dem Zwischenspeicher weiter, mit dem Vermerk „Server offline“.
- **Kiosk unter Windows:**
  - Nur Windows 11 Pro, Enterprise oder Education; Home wird nicht unterstützt.
  - Admin über die Ecke oben rechts.
- **W2 und G** sind ein Teilprojekt.

## 3. Ziele, Nicht-Ziele, Anti-Ziele

**Ziele**
- **PDH:**
  - Schalter „Globales Terminal“ am Terminal-Benutzer.
  - Eine Folienliste für alle globalen Terminals mit eigener Verwaltungsseite und eigenem Recht.
  - Abspieler-Seite für globale Terminals.
  - Vorgang-Ansicht nur zum Lesen.
- **Terminal:**
  - Ein globales Terminal liest keine Karten und zeigt keine Benachrichtigung.
  - Offline zeigt es die zuletzt geladene Folienschau aus dem eigenen Zwischenspeicher.
- **Windows:**
  - Das Setup fragt nach der Betriebsart (Zusatz/Kiosk).
  - Kiosk: eigener Kiosk-Benutzer, automatische Anmeldung, Terminal als einzige Oberfläche, Edge im Kioskmodus,
    Admin-Geste oben rechts, Sperren gegen Ausbrechen.

**Nicht-Ziele**
- Updates von PDH anstoßen, Versionen je Gerät (eigener Schritt danach).
- Mehrere getrennte Folienlisten (z. B. je Standort). Es gibt genau eine Liste für alle globalen Terminals.
- Videos, PDF-Folien, Termine aus Kalendern.
- Sicherung und Abbild des Windows-Geräts (bleibt Windows-Sache).
- Zugewiesener Zugriff per MDM/Intune-XML (siehe Ansätze).

**Anti-Ziele**
- Auf einem globalen Terminal gelingt eine Anmeldung, oder eine Karte bewirkt etwas.
- Ein Vorgang lässt sich am globalen Terminal ändern, oder man kommt aus der Vorgang-Ansicht in andere PDH-Seiten.
- Ein Kiosk-PC lässt den normalen Benutzer an Desktop, Explorer, Task-Manager oder andere Programme.
- Ein Administrator kommt nicht mehr an den Kiosk-PC, oder das Gerät ist ohne Neuinstallation nicht mehr zu
  warten.
- Anlagen-Terminals ändern ihr Verhalten, oder alte Terminals und alte PDH-Stände kommen ins Stolpern.

## 4. Randbedingungen

- **Rückwärtskompatibel:** nur hinzufügende Migrationen und optionale Felder. Alte Pis an neuem PDH arbeiten
  weiter; ein altes Terminal mit globalem Schalter zeigt die Folienschau, weil die Seite von PDH kommt, nur ohne
  Offline-Zwischenspeicher.
- **PDH-Migrationen:**
  - Nie ändern, nur hinzufügen, `migrations/checksums.txt` per `PDH_MIGRATIONS_APPEND=1` ergänzen.
  - Neue Tabellen bekommen `id UUID PRIMARY KEY DEFAULT pdh_uuid_v7()`.
  - Vor dem Merge gegen `origin/main` abgleichen; bei Nummernkollision die eigene umnummerieren (AGENTS.md).
- **PDH-Arbeitsort:** Worktree von `origin/main`, nicht der Hauptcheckout. Neue Seiten brauchen ein Handbuch-Kapitel
  (`TestHelpCoversAllPages`); Rechte kommen per Migration; VERSION und CHANGELOG pflegen.
- **Terminal-Arbeitsort:** wie W1, Bau mit `scripts/dgo`, ohne CGO. Linux-Pakete bleiben byteweise gleich.
- **Windows-Systemeinstellungen** (Benutzer, Autologon, Shell, Richtlinien) werden auf dem Entwicklungsrechner
  **nie** angewandt. Geprüft wird über Tests der Skripte und die Geräteprüfliste.

## 5. Ansätze

**Reihenfolge:** G zuerst, dann W2. G ist am Pi sofort nutzbar und testbar, W2 baut auf dem fertigen Terminal
auf (Call des Designers). Verworfen: W2 zuerst, weil es ohne Testgerät länger ungeprüft bliebe.

**Folien-Abspieler:**

| Ansatz | Entscheidung |
|---|---|
| Abspieler als PDH-Seite `/terminal` (Gerätesitzung, Modus `global`), Offline-Kopie im Terminalprogramm | **gewählt**: Inhalte und Rechte liegen in PDH, der Pi braucht kein Update für die Grundfunktion |
| Abspieler im Terminalprogramm, PDH liefert nur JSON | verworfen: jedes Terminal bräuchte ein Update, doppelte Darstellung |

**Kiosk unter Windows:**

| Ansatz | Entscheidung |
|---|---|
| Eigener lokaler Benutzer mit eigener Shell (`HKCU\…\Winlogon\Shell` = Starter), Autologon, Benutzerrichtlinien | **gewählt**: geht auf Pro, Enterprise und Education und lässt sich mit dem Setup (Inno, Admin) einrichten und zurücknehmen |
| Zugewiesener Zugriff (Assigned Access) über die MDM-WMI-Brücke | verworfen: braucht SYSTEM-Kontext, erlaubt im Einzel-App-Modus nur Edge, also keinen Starter, Leser-Hook oder Tray; schwer zurückzunehmen |
| Shell Launcher v2 | verworfen: nur Enterprise/Education |

Die Übersicht nennt „Zugewiesener Zugriff mit Edge-Kioskmodus“. Das Ziel, ein gesperrtes Gerät nur mit dem
Terminal, bleibt gleich, und die Editionsgrenze (kein Home) bleibt wie vom Nutzer entschieden. Der Weg dorthin
ist die eigene Shell (Call des Designers).

## 6. Design – Teil G: globales Terminal

### 6.1 PDH-Datenmodell (neue Migration)

- **`terminal_settings.global_screen BOOLEAN NOT NULL DEFAULT false`:** der Schalter je Terminal-Benutzer.
- **Neue Tabelle `terminal_slides`:**
  - `id UUID PRIMARY KEY DEFAULT pdh_uuid_v7()`
  - `position INT NOT NULL` (Reihenfolge)
  - `kind TEXT NOT NULL CHECK (kind IN ('image','text','vorgaenge'))`
  - `title TEXT NOT NULL DEFAULT ''`
  - `body TEXT NOT NULL DEFAULT ''` (Text: höchstens 2000 Zeichen, Zeilenumbrüche, kein HTML)
  - `image BYTEA` (Bild: JPEG/PNG/WebP, höchstens 5 MB) plus `image_type TEXT`
  - `infra_ids UUID[] NOT NULL DEFAULT '{}'` (Vorgänge: gewählte Anlagen samt Unteranlagen; leer = alle)
  - `show_faults BOOLEAN NOT NULL DEFAULT true`, `show_tickets BOOLEAN NOT NULL DEFAULT true`
  - `duration_seconds INT NOT NULL DEFAULT 15 CHECK (duration_seconds BETWEEN 5 AND 600)`
  - `active BOOLEAN NOT NULL DEFAULT true`
  - `updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`
- **Ablage der Bilder:** Bilder liegen in der Datenbank. Bild-Uploads gibt es schon (`internal/web/uploads.go`,
  `part_images.go`); der Builder nimmt das Muster, das dort für Bilder gilt, solange es in der Datenbank speichert
  und bei mehreren PDH-Instanzen funktioniert.
- **Neues Recht `terminal.slides.edit`**, Bezeichnung „Infobildschirm bearbeiten“, angelegt per Migration in der
  Kategorie der Terminal-Rechte.

### 6.2 PDH-Verwaltung

- **Schalter:** Am Systembenutzer eines Terminals (bei Anmeldeart und Benachrichtigung) gibt es das Kästchen
  „Globales Terminal (Infobildschirm)“ mit dem Hinweis „Zeigt die Folien aus ‚Infobildschirm‘, ohne Anmeldung und
  ohne Kartenleser“.
  - Recht wie dort (`canManageUsers`).
  - Gespeichert wird nur mit Markerfeld, wie bei `card_notify`.
  - Eine Änderung geht über den Ereigniskanal als neues Feld `global_screen` im Ereignis `settings`.
- **Seite „Infobildschirm“ unter `/terminal-slides`:**
  - Nur mit `terminal.slides.edit`. Ein Menüeintrag liegt dort, wo die Terminal-Verwaltung liegt.
  - Die Liste zeigt die Folien in Reihenfolge mit Vorschaubild oder Titel, Art, Dauer und Aktiv.
  - Knöpfe: Hoch und Runter, Bearbeiten, Löschen mit Rückfrage, „Neue Folie“.
  - Das Formular passt sich der Art an:
    - Bild: Datei, Titel optional.
    - Text: Titel und Text.
    - Vorgänge: Anlagen-Mehrfachauswahl (leer = alle), Störungen ja/nein, Tickets ja/nein.
    - Für alle: Dauer und Aktiv.
  - Jede Änderung meldet allen globalen Terminals „slides“ über den Ereigniskanal, damit sie neu laden.
- **Handbuch-Kapitel** „Infobildschirm“ und Ergänzung im Kapitel zum Anlagenterminal.

### 6.3 PDH-Abspieler

- **Modus:** Eine Gerätesitzung eines Terminal-Benutzers mit `global_screen = true` bekommt unter `/terminal` den
  Modus `global` statt Schoner und Info:
  - keine Anmeldefelder;
  - kein Abo des Kartenstroms (`data-card-events` fehlt);
  - kein Bericht an `/api/card/result`.
- **`/terminal/slides.json`** (nur Gerätesitzung, nur global) liefert die aktiven Folien in Reihenfolge:
  - `id`, `kind`, `title`, `body`, `duration` und `image_url` (`/terminal/slides/{id}/image`, mit ETag).
  - Für Vorgänge zusätzlich die aktuelle Liste:
    - Störungen und Tickets der gewählten Anlagen samt Unteranlagen.
    - Gefiltert nach den Rechten des Terminal-Benutzers, wie `/terminal/info.json` heute.
    - Höchstens 12 je Art, jeweils mit `id`, Titel, Status, Pfad und Alter.
  - Dazu eine `version` (Hash über Inhalt und `updated_at`), die sich bei jeder Änderung ändert.
- **Ablauf:**
  - Die Seite blendet Folie für Folie ein und hält jede so lange, wie ihre Dauer sagt.
  - Vorgänge-Folien laden ihre Liste beim Erscheinen neu.
  - Gibt es keine aktive Folie, steht dort „Keine Folien eingerichtet“ mit dem PDH-Namen.
- **Eingabe:**
  - Antippen oder Klicken auf freie Fläche oder eine Wischgeste nach links blättert sofort weiter; die Dauer
    beginnt neu.
  - Antippen eines Vorgangs öffnet `/terminal/vorgang/{kind}/{id}`.
  - Andere Eingaben gibt es nicht. Bilder und Text sind nicht verlinkt, Kontextmenü und Markieren sind aus.
- **Vorgang-Ansicht (nur lesen), `/terminal/vorgang/{fault|ticket}/{id}`:**
  - Nur Gerätesitzung, nur globales Terminal.
  - Gezeigt werden Titel, Status, Anlage, Beschreibung, erstellt/zuletzt geändert und die letzten 5
    Verlaufseinträge.
  - Nur wenn der Vorgang zu einer erlaubten Anlage gehört (Prüfung gegen die Anlagen der Vorgänge-Folien und die
    Rechte); sonst 404.
  - Keine Knöpfe außer „Zurück“, keine Links in andere PDH-Bereiche. Anhänge werden nicht gezeigt.
  - Nach der Rückkehrzeit (`terminal.idle_seconds` bzw. Wert des Terminals) und mit „Zurück“ geht es zur
    Folienschau.
- **Änderungen:** Das Ereignis `slides` und eine geänderte `version` laden die Folienliste neu, ohne die Seite neu
  zu laden. `global_screen` aus → die Seite lädt neu und wird zum normalen Terminal.

### 6.4 Terminal (alle Plattformen)

- **Ein globales Terminal liest keine Karten.** Das Terminalprogramm erfährt den Modus nicht aus PDH. Weil die
  PDH-Seite im Modus `global` den Kartenstrom nicht abonniert, kommt keine Karte an. Dazu ist nichts am Programm
  nötig (Call des Designers). Der USB-Leser unter Windows hält trotzdem Tasten an; das bleibt, wie es ist.
  - **Alternative ab dem Offline-Teil:** Das Programm kennt den Modus über den Zwischenspeicher (unten) und
    schaltet dann auch den Leser ab.
- **Offline-Zwischenspeicher:**
  - Die Seite im Modus `global` meldet dem Terminal `POST /api/slides/cache` (Herkunft wie bei `/api/card/result`)
    mit der aktuellen `slides.json` samt Bilddaten.
    - Die Bilder holt die Seite einzeln und schickt sie base64, je Folie höchstens 5 MB, gesamt höchstens 50 MB.
    - Gesendet wird nur, wenn sich die `version` geändert hat.
  - Das Terminal speichert das unter `<Datenordner>/slides/` (atomar, alte Version erst nach erfolgreichem
    Schreiben löschen).
  - Liegt ein Zwischenspeicher vor, gilt das Terminal als globales Terminal. Dann:
    - **Leser:** aus.
    - **Offline-Ersatz:** Die Startseite zeigt bei „PDH nicht erreichbar“ statt der Offline-Meldung eine lokale
      Folienschau unter `/slides` auf dem lokalen Server. Gleicher Ablauf; Vorgänge-Folien zeigen die
      gespeicherte Liste mit dem Vermerk „Stand <Zeit>“, Vorgänge lassen sich offline nicht öffnen. Oben rechts
      steht klein „Server offline“.
    - **Rückkehr:** Alle 30 s prüft die Startseite PDH wie heute; ist es wieder da, geht es zurück zur PDH-Seite.
  - `global_screen` aus: Die PDH-Seite (normales Terminal) schickt `DELETE /api/slides/cache`, das Terminal löscht
    den Zwischenspeicher, und der Leser ist wieder an.
- **Rückwärtskompatibel:**
  - Ein altes Terminal antwortet auf `/api/slides/cache` mit 404; die Seite ignoriert das still.
  - Ein neues Terminal an altem PDH bekommt nie einen Zwischenspeicher.

## 7. Design – Teil W2: Windows-Kioskgerät

### 7.1 Setup

- **Neue Seite „Betriebsart“:**
  - „Zusatz – neben normaler Arbeit“ (Vorgabe) oder „Kiosk – dieser PC zeigt nur das Terminal“.
  - Still: `/MODE=zusatz` (Vorgabe) oder `/MODE=kiosk`.
  - Kiosk nur auf Pro, Enterprise oder Education (Edition aus `HKLM\SOFTWARE\Microsoft\Windows NT\CurrentVersion\EditionID`).
    Auf Home: Meldung „Der Kioskbetrieb braucht Windows 11 Pro, Enterprise oder Education.“; Kiosk ist nicht
    wählbar, still Abbruch mit Fehlercode.
- **Kiosk-Einrichtung (als Administrator im Setup):**
  1. **Kiosk-Benutzer:** lokaler Standardbenutzer `PDH-Kiosk` (kein Administrator) mit zufälligem 32-Zeichen-Passwort.
     Das Passwort wird nirgends angezeigt oder in Dateien gespeichert, außer im LSA-Geheimnis für Autologon.
  2. **Autologon:**
     - `Winlogon` mit `AutoAdminLogon=1`, `DefaultUserName`, `DefaultDomainName=.`.
     - Das Passwort liegt als LSA-Geheimnis `DefaultPassword` (`LsaStorePrivateData`), nicht im Klartext in der
       Registry. Das macht ein kleiner Helfer `pdh-terminal-kiosk.exe` (Teil des Setups), der auch die anderen
       Schritte ausführt.
  3. **Shell:** Für `PDH-Kiosk` wird das Benutzerprofil angelegt; im geladenen Benutzer-Hive gilt
     `Software\Microsoft\Windows NT\CurrentVersion\Winlogon\Shell` = `"{app}\pdh-terminal-start.exe" -kiosk`. Kein
     Explorer, also auch keine Taskleiste und kein Desktop.
  4. **Sperren für `PDH-Kiosk`** (Benutzerrichtlinien im Hive):
     - `DisableTaskMgr`, `DisableLockWorkstation`, `DisableChangePassword`, `NoRun`, `NoWinKeys`,
       `NoViewContextMenu`.
     - Keine Bildschirmsperre: `ScreenSaveActive=0`, `ScreenSaveIsSecure=0`. Energiesparplan wird nicht geändert.
     - Strg+Alt+Entf bleibt mit „Abmelden“ und „Benutzer wechseln“, damit ein Administrator ans Gerät kommt.
  5. **Maschinenweit:**
     - Benachrichtigungen und Windows-Tipps für den Kiosk-Benutzer aus.
     - Die Edge-Richtlinie aus W1 (`HideFirstRunExperience`) bleibt.
  6. Rückgängig: Die Deinstallation und `pdh-terminal-kiosk.exe -undo` nehmen alles zurück:
     - Autologon aus, LSA-Geheimnis gelöscht.
     - Shell und Richtlinien aus dem Hive entfernt, Benutzer `PDH-Kiosk` samt Profil gelöscht.
     - Die Daten des Terminals bleiben.
- **Wechsel der Betriebsart** durch erneutes Setup mit anderer Wahl (richtet ein bzw. nimmt zurück).
- **Neustart:** Nach Kiosk-Einrichtung bietet das Setup einen Neustart an; still nur mit `/RESTART=yes`.

### 7.2 Terminal im Kiosk

- **Starter mit `-kiosk`:**
  - Er läuft als Shell. Er startet das Programm mit `-kiosk`.
  - Eine Instanz, wie bisher. Endet der Starter, meldet Windows den Benutzer ab; deshalb gibt es im Kiosk kein
    „Beenden“.
- **Edge im Kiosk:** `--kiosk <start> --edge-kiosk-type=fullscreen --no-first-run` statt `--app`, eigenes Profil.
  Schließen gibt es nicht. Stürzt Edge ab, startet es sofort neu (normal, nicht minimiert).
- **Tray:** Es gibt keine Taskleiste. Benachrichtigungen sind im Kiosk aus, weil das Terminal immer vorne ist.
- **Admin-Geste:**
  - 5 Sekunden Halten im Feld 80 × 80 px oben rechts, wie am Pi (`cornerConfig`).
  - Unter Windows über einen Low-Level-Maus-Hook (`WH_MOUSE_LL`). Berührung erzeugt Mausereignisse, also gilt die
    Geste für Finger und Maus.
  - Die Geste öffnet im Kiosk-Edge die lokale Admin-Seite (`TargetAdmin`, Admin-Passwort wie bisher).
  - „Zurück“ dort führt zurück zum Terminal.
- **Admin-Seite im Kiosk:** Abschnitt „Kiosk“ mit „Kiosk-Benutzer abmelden“ (Abmeldung, danach meldet sich der
  Kiosk-Benutzer wieder an) und dem Hinweis, wie man mit Strg+Alt+Entf → „Benutzer wechseln“ als Administrator an
  Windows kommt.
- **Datenordner:** Im Kiosk gilt dasselbe Datenverzeichnis wie bei Zusatz (`%ProgramData%\PDH-Terminal`). Das
  Setup gibt im Kiosk nur `PDH-Kiosk` und den Administratoren Schreibrechte statt der Gruppe „Benutzer“. Damit
  ist die Datenschutz-Grenze aus W1 6.8 geschlossen.

### 7.3 Fehler und Grenzen

- **Kiosk-Einrichtung scheitert** an einem Schritt: Das Setup nimmt die schon gemachten Schritte zurück, meldet
  den Schritt und bleibt bei „Zusatz“.
- **Edge fehlt:** Der Kiosk zeigt eine eigene Fehlerseite (Fenster des Programms) „Edge nicht gefunden“ mit
  Admin-Geste.
- **Das Gerät hängt ohne Netz:** wie am Pi; ein globales Terminal läuft aus dem Zwischenspeicher.

## 8. Prüfung

- **PDH:**
  - Migration, Recht, Formular, Seite, `slides.json` und Vorgang-Ansicht mit Integrationstests auf der
    Labor-Datenbank, darunter: nur Gerätesitzung, nur global, fremde Anlage 404, Rechte, Bildgröße und -typ.
  - Der Abspieler bekommt JS-Logik als reine Funktionen, mit Node-Tests wie in Task 11 von W1.
- **Terminal:**
  - Zwischenspeicher (Grenzen, atomar, Löschen) und `/slides` offline per Unit-Tests.
  - Leser aus bei vorhandenem Zwischenspeicher.
  - Kiosk-Helfer: Logik plattformneutral testbar (welche Registry-Werte und Schritte). Die Win32- und
    LSA-Aufrufe werden nur gebaut und mit `vet` geprüft, **nicht** auf dem Entwicklungsrechner ausgeführt.
  - `.iss` mit Prüfungen in `packaging/test-products.sh`.
- **Geräteprüfliste `dist/geraetetest-w2g.md`:**
  - Pi als globales Terminal.
  - Offline.
  - Windows-Kiosk in einer VM oder auf einem Test-PC (Pro).
  - Admin-Geste.
  - Ausbruchversuche (Win-Taste, Strg+Alt+Entf, Alt+Tab, Alt+F4).
  - Rückbau.

## 9. Dem Builder überlassen

- Darstellung der Folienschau (Übergänge, Schriftgrößen), solange auf 1920×1080 und 1280×800 aus 3 m lesbar.
- Bildablage-Muster in PDH (6.1).
- Aufteilung der Zuständigkeiten im Kiosk-Helfer.
- Wie die Seite Bilder für den Zwischenspeicher holt und sendet, im Rahmen der Grenzen.

## 10. Offene Punkte für den Nutzer

- Ob die Wahl „eigene Shell statt Zugewiesener Zugriff“ passt. Sie hält die Editionsgrenze und das Ziel ein, nur
  der Mechanismus ist ein anderer als in der Übersicht notiert.
- Code-Signatur (wie W1).
