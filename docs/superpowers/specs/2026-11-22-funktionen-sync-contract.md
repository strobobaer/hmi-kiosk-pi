# Vertrag: Funktionen-Abgleich zwischen PDH-Terminal und PDH-Server

Stand 2026-11-22, Terminal 0.4.3. Dieser Vertrag ist die einzige Quelle für beide Seiten: Das Terminal
(`internal/product/pdhterminal/functions.go`, Repo hmi-kiosk-pi) setzt die Geräteseite um, PDH setzt die
Serverseite nach diesem Text um. Alle Neuerungen sind **hinzufügend**: ältere Terminals und ältere
PDH-Versionen funktionieren weiter (jede Kombination), siehe Abschnitt 8.

## 1. Ziel

Am Terminal (Admin-Seite, Reiter „Funktionen“) und in PDH (Benutzerdetail des Terminal-Benutzers) lassen sich
dieselben Schalter einstellen. **Die jüngere Änderung gewinnt**, je Schalter. Beide Seiten speichern zu jedem
Schalter den Zeitpunkt der letzten Änderung.

## 2. Schalter (Schlüssel)

| Schlüssel      | Anzeige am Terminal                | Grundwert, solange nie geändert |
|----------------|------------------------------------|----------------------------------|
| `stoerung`     | Störung                            | `true`                           |
| `ticket`       | Ticket                             | `true`                           |
| `ruesten`      | Rüsten                             | `true`                           |
| `stillstand`   | Stillstand                         | `true`                           |
| `hmi`          | HMI                                | `true`                           |
| `wartungen`    | Wartungen                          | `true`                           |
| `saver_slides` | Folienschau als Bildschirmschoner  | `false` (Migration 195)          |

`enabled=false` heißt: Die Terminalseite von PDH zeigt die Funktion nicht an (Kachel/Knopf/Route entfällt bzw.
antwortet wie „nicht eingerichtet“). `saver_slides` ist die schon vorhandene Spalte
`terminal_settings.saver_slides`; sie behält ihre Bedeutung. Unbekannte Schlüssel ignoriert jede Seite (ohne
Fehler), damit später Schalter ergänzt werden können.

Die Schalter gehören zum **Terminal-Benutzer** (`terminal_settings`, Schlüssel `user_id`), nicht zum einzelnen
Gerät: Ein Abgleich eines Geräts gilt für alle Geräte dieses Benutzers (wie `saver_slides` heute).

## 3. Zeitstempel und Vergleich

* Format: **RFC 3339, UTC, mit Z**, auf der Leitung vom Terminal immer mit Millisekunden:
  `2026-11-22T10:15:30.123Z`. PDH **muss** jede RFC-3339-Zeit lesen (mit oder ohne Bruchteil bis 9 Stellen,
  mit `Z` oder Offset) und in UTC umrechnen; PDH **sendet** ebenfalls UTC mit `Z` (Millisekunden oder
  Mikrosekunden sind beides zulässig, das Terminal liest bis 9 Stellen).
* Verglichen wird als Zeitpunkt (nicht als Text).
* **Regel je Schalter** (gilt identisch auf beiden Seiten):
  1. Der Wert mit dem **strikt jüngeren** `updated_at` gewinnt.
  2. **Bei Gleichstand gewinnt PDH.** (Das Terminal übernimmt bei gleichem Zeitpunkt den Wert von PDH; PDH
     nimmt bei gleichem Zeitpunkt den Wert des Terminals **nicht** an.)
  3. Die Regel gilt je Schlüssel, nicht für das ganze Paket: Ein Aufruf kann einen Schalter übernehmen und
     einen anderen ablehnen.
* **Bestätigung nach Wert (geändert 2026-11-22):** Antwortet PDH mit demselben `enabled`, aber einem **älteren**
  `updated_at` (etwa wegen der Klemme), übernimmt das Terminal den Zeitpunkt von PDH für diesen Schlüssel und gilt
  als bestätigt; es sendet ihn nicht erneut. Bei anderem `enabled` und älterem PDH-Zeitpunkt bleibt der lokale
  Wert und wird (weiter) gesendet.
* **Vergangene Zeitpunkte (geändert 2026-11-22):** PDH nimmt `updated_at` in der Vergangenheit **so wie gesendet** an
  (nach der Regel: ist er nicht strikt jünger, verliert die Änderung einfach, ohne Fehler). Ein Terminal mit
  unplausibler Uhr (vor 2025 oder ohne NTP-Abgleich) verliert deshalb lokale Änderungen gegen PDH; die
  Admin-Seite des Terminals zeigt dazu einen Hinweis auf dem Reiter „Funktionen“.
* **Uhrenabweichung:** Ist ein vom Terminal gesendetes `updated_at` mehr als 5 Minuten **nach** der PDH-Zeit, setzt
  PDH es auf die PDH-Zeit (Klemme). Das Terminal stellt seine Zeitpunkte nie zurück: Ein neuer Wert hat immer
  einen größeren Zeitpunkt als der vorige Stand desselben Schalters.
* Ändert eine Person den Schalter in PDH (Benutzerdetail), setzt PDH `updated_at = NOW()` für diesen Schalter,
  nur wenn sich der Wert ändert, und veröffentlicht das Ereignis `settings` (Abschnitt 6).

## 4. Anmeldung (wie die vorhandene Geräte-Schnittstelle)

Alle Aufrufe tragen den Header `Authorization: Bearer <Geräte-Geheimnis>` (das Geheimnis aus
`POST /api/terminal/v1/pair`). PDH prüft ihn wie bei `GET /api/terminal/v1/device` (`terminal_devices`,
`deviceActiveSQL`, `terminalFailLimit`, Antworttexte `msgDeviceInvalid`, `msgPairBlocked`):

* Header fehlt, Geheimnis unbekannt oder Gerät gesperrt: **401** `{"error":"Gerät nicht gekoppelt oder gesperrt"}`
  (nur bei falschem Geheimnis zählt es als Fehlversuch).
* Zu viele Fehlversuche: **429**.

## 5. HTTP-Schnittstelle

**Freiwillige Kopfzeilen des Terminals (geändert 2026-11-22):** Jede Anfrage des Terminals an PDH (Kopplung,
Statusabfrage, `PUT /functions`, `/terminal/events`) trägt `X-Terminal-Version: <Version>` (Build-Version, bei
unbekannter `dev`) und `User-Agent: pdh-terminal/<Version>`. PDH darf sie für Anzeige/Protokoll nutzen und muss
sie nicht auswerten; ihr Fehlen (ältere Terminals) ist kein Fehler.

**Optionale Kopfzeile `X-Terminal-Admin-URL` (geändert 2026-11-23):** Zusätzlich trägt jede Anfrage des Terminals an PDH `X-Terminal-Admin-URL: https://<host>:<port>/admin/`, die Adresse der lokalen Admin-Seite des Geräts. `<host>` ist die lokale IP-Adresse, über die das Gerät den PDH-Server erreicht (IPv6 in eckigen Klammern, z. B. `https://[fd00::7]:8443/admin/`), `<port>` der tatsächlich gebundene Port der Admin-Seite (Standard 8443). Das Terminal ermittelt die Adresse zwischengespeichert (Neubestimmung spätestens alle 5 Minuten). Lässt sie sich nicht bestimmen (oder läuft die Admin-Seite nicht), fehlt die Kopfzeile. Sie enthält nie Zugangsdaten. PDH darf sie für den Zahnrad-Link zur Admin-Seite nutzen; ihr Fehlen ist kein Fehler.

### 5.1 Statusabfrage erweitern: `GET /api/terminal/v1/device`

Die Antwort behält `device_id`, `user`, `name` und bekommt zwei neue **freiwillige** Felder:

```json
{
  "device_id": "0190…",
  "user": "Kasse 1",
  "name": "T1",
  "functions": {
    "stoerung":     {"enabled": true,  "updated_at": "2026-11-22T10:15:30.123Z"},
    "ticket":       {"enabled": false, "updated_at": "2026-11-22T11:00:00.000Z"},
    "ruesten":      {"enabled": true,  "updated_at": "1970-01-01T00:00:00.000Z"},
    "stillstand":   {"enabled": true,  "updated_at": "1970-01-01T00:00:00.000Z"},
    "hmi":          {"enabled": true,  "updated_at": "1970-01-01T00:00:00.000Z"},
    "wartungen":    {"enabled": true,  "updated_at": "1970-01-01T00:00:00.000Z"},
    "saver_slides": {"enabled": false, "updated_at": "2026-11-20T08:00:00.000Z"}
  },
  "theme": {"scheme": "dark"}
}
```

* `functions` enthält **immer alle sieben** Schlüssel. Wurde ein Schalter noch nie geändert, ist `enabled` der
  Grundwert aus Abschnitt 2 und `updated_at` die Epoche `1970-01-01T00:00:00.000Z` (so ist jede echte
  Änderung am Terminal jünger). Für `saver_slides` darf PDH bei vorhandener Zeile `terminal_settings.updated_at`
  als Anfangswert nehmen; wichtiger ist, dass der Wert stabil bleibt.
* `theme` ist freiwillig, siehe 5.4. Fehlt es, bleiben die Grundfarben.
* Das Terminal fragt diese Adresse alle 30 Sekunden und beim Start (Statusabfrage) und wendet `functions`
  nach der Regel aus Abschnitt 3 an. **PDH soll diese Adresse nicht teurer machen als bisher**: eine
  Abfrage von `terminal_settings` zusätzlich genügt.

### 5.2 Neu: `PUT /api/terminal/v1/functions`

Das Terminal schickt seine noch nicht bestätigten lokalen Änderungen. Anfrage (`Content-Type: application/json`,
höchstens 4 096 Byte wie `terminalAPIMaxBody`):

```json
{"functions": {"ticket": {"enabled": false, "updated_at": "2026-11-22T11:00:00.123Z"}}}
```

* `functions` enthält einen oder mehrere bekannte Schlüssel; unbekannte Schlüssel werden übergangen.
  Fehlt `functions`, ist es kein Objekt, ist `enabled` kein Boolean oder `updated_at` keine RFC-3339-Zeit:
  **400** `{"error":"…"}` (deutscher Text), es wird nichts übernommen (alles oder nichts bei formalen Fehlern).
* Je Schlüssel gilt die Regel aus Abschnitt 3: ist `updated_at` **strikt jünger** als der gespeicherte Zeitpunkt
  (nach der Klemme), speichert PDH `enabled` und `updated_at` **so wie gesendet** (nicht `NOW()`), sonst
  bleibt der gespeicherte Stand. Ein abgelehnter Schlüssel ist **kein Fehler**.
* Wurde mindestens ein Wert übernommen **und** hat sich der Wert (`enabled`) dadurch geändert, veröffentlicht PDH
  `settings` an alle Ereigniskanäle dieses Terminal-Benutzers (Abschnitt 6). Das gilt auch für den Kanal des
  sendenden Geräts (der Wert ist dann dort schon gleich; die Anwendung ist idempotent).
* Antwort **200** mit dem **vollständigen Stand nach der Verarbeitung**, gleich aufgebaut wie in 5.1:

```json
{"functions": { …alle sieben Schlüssel… }}
```

  Das Terminal übernimmt daraus jeden Schlüssel nach der Regel (so erfährt es, wenn PDH jünger war) und gilt
  als bestätigt, sobald PDH den Schlüssel mit seinem Wert und Zeitpunkt führt.
* Die Anfrage ist **idempotent**: Dieselbe Anfrage zu wiederholen ändert nichts (der Zeitpunkt ist dann nicht
  mehr strikt jünger).
* Fehler: **401** (Abschnitt 4), **413** zu groß, **429**, **500** `{"error":"…"}` bei Datenbankfehlern (das
  Terminal behält den Wert und versucht es später erneut). `GET` auf dieselbe Adresse ist nicht nötig
  (der Stand kommt aus 5.1).
* Eine PDH-Version **ohne** diese Route antwortet 404 (oder 405). Das Terminal behandelt das als „nur lokal“
  (Abschnitt 8), es ist kein Fehlerzustand.

### 5.3 Speicherung (Empfehlung, nur hinzufügend)

Neue Migration, die nur hinzufügt (nie bestehende Migrationen ändern, IDs nie umschreiben):

```sql
ALTER TABLE terminal_settings
    ADD COLUMN IF NOT EXISTS fn_stoerung    BOOLEAN NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS fn_stoerung_at TIMESTAMPTZ,
    … (je Schlüssel: Wert und …_at; für saver_slides nur saver_slides_at ergänzen) …
```

`…_at IS NULL` heißt „nie geändert“ und wird als Epoche ausgeliefert. Wie PDH das intern speichert
(Spalten, JSONB), ist PDHs Entscheidung; vertraglich zählt nur die Leitung.

### 5.4 Farben: Feld `theme` (freiwillig)

```json
"theme": {"scheme": "dark", "bg": "#0f1117", "bg2": "#1a1d27", "bg3": "#222536",
          "text": "#f1f3fb", "muted": "#9aa3b8", "border": "#2f3348"}
```

* `scheme`: `"dark"` oder `"light"` (wählt die Grundwerte des Terminals, unbekannt = dunkel).
* Alle übrigen Felder sind freiwillig; gültig ist nur `#rrggbb`. Alles andere ignoriert das Terminal.
* Das Terminal setzt damit den Hintergrund seiner eigenen Seiten (Admin-Seite, Startseite/Meldeseiten,
  Offline-Folienschau). Die Grundwerte des Terminals sind die CSS-Werte von PDH: dunkel
  `--bg:#0f1117; --bg2:#1a1d27; --bg3:#222536; --text:#f1f3fb; --muted:#9aa3b8; --border:#2f3348`
  (`web/templates/terminal.gohtml`, die Terminalseite ist immer dunkel); hell
  `--bg:#f3f3f3; --bg2:#ffffff; --bg3:#f9f9f9; --text:#1b1b1b; --muted:#5f5f5f; --border:#e2e2e2`
  (`web/templates/base.gohtml`, `html[data-theme="light"]`). Solange die Terminalseite dunkel bleibt, braucht
  PDH `theme` gar nicht zu senden. Ändert PDH die Terminalfarben, sendet es `theme` in 5.1 und im Ereignis.

## 6. Ereigniskanal: `GET /terminal/events` (Server-Sent Events)

Das vorhandene Ereignis `settings` bekommt dieselben zwei Felder zusätzlich zu den bisherigen
(`manual_login`, `password_button`, `idle`, `card_notify`, `global_screen`, `saver_slides`):

```
event: settings
data: {"manual_login":"card_pin", …, "saver_slides":false,
       "functions":{ …alle sieben Schlüssel wie in 5.1… },
       "theme":{"scheme":"dark"}}
```

* Das Feld `saver_slides` (Boolean) bleibt unverändert bestehen (ältere Terminalseiten lesen es), auch wenn
  es nun zusätzlich unter `functions.saver_slides` steht.
* **Herzschläge (geändert 2026-11-22):** PDH muss auf diesem Kanal keine Herzschläge senden. Das Terminal baut den
  Kanal nach 90 Sekunden ohne Daten neu auf (Lesefrist); die Statusabfrage alle 30 Sekunden bleibt der Rückfall.
* Der Kanal wird **auch mit dem Geräte-Geheimnis** angenommen: Die Terminal-Software liest ihn mit
  `Authorization: Bearer <Geräte-Geheimnis>` (zusätzlich zur Sitzung der Terminalseite im Browser, `cardTerminal`).
  Fehlt dies oder antwortet PDH nicht mit **200** und `Content-Type: text/event-stream`, bleibt es bei der
  Statusabfrage (5.1); das Terminal versucht den Kanal dann nur alle 5 Minuten neu (kein Dauerfeuer).
* Ereignisse werden wie bisher mit `terminalEventHub.publish(userID, "settings")` ausgelöst: nach jeder
  Änderung eines Schalters in PDH und nach jeder durch 5.2 übernommenen Änderung.
* Ein Terminal wendet `functions` und `theme` aus dem Ereignis mit derselben Regel wie aus 5.1 an. Ist lokal
  etwas Jüngeres noch nicht bestätigt, sendet es es sofort per 5.2.

## 7. Verhalten des Terminals (zur Abstimmung)

* Speicherort: `<Datenordner>/pdh/functions.json` mit je Schlüssel `enabled`, `updated_at` und dem Merker
  `pending` für „noch nicht an PDH übertragen“; Farben in `<Datenordner>/pdh/theme.json`.
* Admin-Seite (geändert 2026-11-22): Das Formular sendet je Schalter den angezeigten Stand (`was_<Schlüssel>`);
  nur Schalter, die die Person gegenüber diesem Stand geändert hat, gelten als lokale Änderung (kein
  Überschreiben von PDH-Änderungen, die nach dem Öffnen der Seite kamen).
* Lokale Änderung: Wert und `updated_at = jetzt` (UTC, Millisekunden) speichern, `pending` setzen, sofort
  Abgleich: erst Stand holen (5.1, anwenden), dann alle `pending`-Schlüssel per 5.2 senden, die Antwort
  anwenden.
* PDH nicht erreichbar, Zertifikat nicht vertraut, 5xx, Zeitüberschreitung: der lokale Wert bleibt in Kraft,
  `pending` bleibt, neuer Versuch spätestens nach 30 Sekunden (feste Abfrageperiode, kein engerer Takt).
* 401 (gesperrt): 10 Minuten Ruhe (PDH zählt Fehlversuche je Quelladresse).
* Der Abgleich läuft nur bei bestehender Kopplung.

## 8. Rückwärtskompatibilität

| Terminal | PDH | Ergebnis |
|----------|-----|----------|
| neu | alt (kein `functions`, keine Route) | `GET /device` ohne `functions`: nichts anzuwenden. `PUT` → 404: Schalter gelten **nur am Terminal**, die Admin-Seite sagt das; neuer Versuch erst nach einer Stunde, kein Wiederholungssturm. Terminalseiten von PDH laufen unverändert. |
| alt | neu | Das Terminal ignoriert die neuen Felder (`functions`, `theme` sind für alte Terminals unbekannt, die Antworten bleiben gültiges JSON). |
| neu | neu | wie beschrieben |

Beide Seiten ignorieren unbekannte Felder und unbekannte Schlüssel. Ein 404 auf `PUT` ist **keine** Störung
und wird weder als „gesperrt“ noch als „nicht erreichbar“ gemeldet.

## 9. Bildschirmtastatur (Ergänzung, nur Terminalseite in PDH)

Seit Terminal 0.4.3 bietet das Gerät auf dem Pi für die PDH-Seiten die Tastatur auf Anforderung an:

* `POST http://127.0.0.1:8080/api/keyboard/show` und `…/hide` (derselbe Ursprung wie `CARD_ORIGIN` in
  `terminal.js`; nur Anfragen mit `Origin` = PDH-Adresse, CORS und Private-Network-Preflight wie bei
  `/api/card/result`; Antwort 204, 503 wenn es keine Tastatur gibt, 404 für andere Aktionen).
* **PDH-Aufgabe:** `terminal.js` ruft `show` beim `focusin` auf ein Texteingabefeld (`input` vom Typ text,
  password, number, search, email, tel, url sowie `textarea`, nicht deaktiviert/readonly) und `hide` beim
  `focusout` (150 ms verzögert, wenn nicht gleich das nächste Feld den Fokus bekommt) sowie beim Absenden und in
  `pagehide`. Fehler ignorieren (`fetch(...).catch(function(){})`), nur im Gerätemodus (wenn `CARD_ORIGIN`
  erreichbar ist). Unter Windows antwortet das Gerät mit 503 und die Windows-Bildschirmtastatur erscheint von
  selbst beim Antippen eines Feldes; dort ist nichts zu tun.

## 10. Prüfliste für PDH

1. `GET /api/terminal/v1/device` liefert `functions` (alle sieben, Epoche für „nie“), `theme` optional.
2. `PUT /api/terminal/v1/functions` nach 5.2 mit Regel „strikt jünger, Gleichstand PDH“, Klemme +5 Minuten,
   Antwort mit vollständigem Stand, 400/401/413/429.
3. `settings`-Ereignis mit `functions` (und `saver_slides` unverändert); Kanal nimmt Bearer-Geheimnis an.
4. Änderung in PDH setzt `updated_at = NOW()` nur bei geänderter Wert und löst `settings` aus.
5. Terminalseiten blenden ausgeschaltete Funktionen aus.
6. Migration nur hinzufügend; ältere Terminals bleiben funktionsfähig.
7. Tests: jünger/älter/gleich je Schlüssel, gemischtes Paket, 400 bei kaputtem Inhalt, 401 gesperrt, Klemme,
   Idempotenz, Ereignis nach Änderung.
