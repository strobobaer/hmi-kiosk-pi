# Rückfall über das Tailnet – Design

Stand: 24.11.2026 · Terminal-Release 0.5.0 · PDH-Version: nächste freie über main (bei Erstellung 0.82.54)

## Ziel

Ein PDH-Terminal (Raspberry Pi oder Windows) bleibt mit dem PDH-Server verbunden, wenn der normale
Weg (LAN-Adresse des Servers) ausfällt oder gesperrt ist – über eine zweite Adresse im Tailnet
(Tailscale). Tailscale wird auf Pi und Windows mitgeliefert und auf der Admin-Seite des Terminals
eingerichtet.

Erfolg heißt: LAN zum PDH-Server weg → Terminal arbeitet nach spätestens ~1 Minute über das Tailnet
weiter (Startseite, Funktionen, Ereignisse, Karten), zeigt „über Tailnet“ an und kehrt von selbst zum
LAN zurück, sobald es wieder geht. Ohne eingerichtete Ausweichadresse verhält sich das Terminal exakt
wie 0.4.x.

## Ausgangslage (0.4.5)

- Genau eine Serveradresse: `cfg.PDH.URL`, nach der Kopplung zusätzlich `device.json` `URL`;
  „gekoppelt“ verlangt `Device.URL == cfg.PDH.URL` (`start.go`, `admin.go`).
- TLS über die Systemzertifikate mit Namensprüfung, keine Bindung an ein bestimmtes Zertifikat.
- Unerreichbar ⇒ Offline-Seite/Folien, neuer Versuch alle 30 s; kein zweiter Weg.
- Kein Tailscale im Pi-Abbild oder im Windows-Installer.
- PDH prüft fremde Seiten über `Sec-Fetch-Site`, nicht über einen festen Hostnamen; Cookies sind
  Host-gebunden. Ein zweiter Servername ist für PDH grundsätzlich unkritisch.

## Entscheidungen

1. **Ausweichadresse** `pdh.fallback_url` (eine Adresse, keine Liste): reiner Ursprung
   `https://name[:port]`, nur https (auch wenn `allow_http` für die Hauptadresse gesetzt ist).
   Muss sich von der Hauptadresse unterscheiden.
2. **Kopplung bleibt an die Hauptadresse gebunden.** Gerätekennung und Geheimnis gelten für beide
   Adressen (gleicher Server, gleiche Datenbank). Koppeln über die Ausweichadresse ist nicht möglich.
3. **Wechsel nur bei Netzwerkfehlern** (Verbindung abgelehnt, keine Route, DNS-Fehler, Zeitüberschreitung).
   Kein Wechsel bei Zertifikatsfehler (`StateUntrusted`), gesperrtem Gerät (401/403), entkoppeltem Gerät
   oder HTTP-Fehlern des Servers. Ein Zertifikatsfehler auf der Hauptadresse bleibt sichtbar.
4. **Rückkehr:** Solange die Ausweichadresse aktiv ist, wird die Hauptadresse alle 5 Minuten geprüft
   (leichter `GET /api/terminal/v1/device`); gelingt das, sofort zurück. Ein Netzwerkfehler auf der aktiven
   Ausweichadresse stößt die Prüfung sofort an, höchstens einmal je 30 s (toter Rückfall, z. B. Tailscale
   nicht angemeldet, hält das Terminal nicht 5 min fest).
5. **Zertifikat im Tailnet:** Auf dem PDH-Server stellt `tailscale serve` HTTPS unter
   `<server>.<tailnet>.ts.net` bereit (Let's-Encrypt-Zertifikat durch Tailscale, Weiterleitung an den
   PDH-Port). Das Terminal braucht keine Ausnahme bei der Zertifikatsprüfung. `100.x`-Adressen werden
   nicht unterstützt (kein Zertifikat für IPs).
6. **Tailscale mitliefern:**
   - Pi-Abbild: offizielles Paket aus der signierten Tailscale-Paketquelle, Dienst installiert, aber
     nicht angemeldet.
   - Windows: Installer installiert Tailscale nur, wenn es fehlt. Weg: offizielles MSI, Version und
     SHA-256 beim Bau festgelegt. Ob das MSI mitgeliefert werden darf oder bei der Installation von
     `pkgs.tailscale.com` geladen wird, entscheidet die Lizenzprüfung (Aufgabe im Bauplan); ohne
     Erlaubnis zur Weitergabe ⇒ Laden mit Prüfsummenprüfung, schlägt das fehl, läuft die
     Terminal-Installation trotzdem durch (Tailnet dann „nicht installiert“).
7. **Auth-Key wird nicht gespeichert.** Eingabe auf der Admin-Seite, einmalig an
   `tailscale up --auth-key=… --hostname=<gerätename>` übergeben (nicht in der Befehlszeile sichtbar
   loggen, nicht in Konfiguration/Protokoll schreiben). Danach hält Tailscale seinen eigenen
   Maschinenschlüssel.

## Bestandteile

### Terminal (hmi-kiosk)

- **Konfiguration** (`internal/config/pdh.go`): Feld `FallbackURL` (`json:"fallback_url"`), Prüfung
  wie oben; `PDHOrigins` enthält die Ausweichadresse, wenn gesetzt.
- **Wegwahl** (neu, z. B. `internal/product/pdhterminal/route.go`): kleiner, nebenläufig sicherer
  Zustand „aktive Basisadresse“ (`primary`/`fallback`, Zeitpunkt des Wechsels, letzter Fehler).
  `Base() string`, `Report(err)` (stuft Fehler ein, wechselt bei Netzwerkfehler auf fallback),
  Rückkehrprüfung alle 5 min. Einstufung der Fehler als eigene, getestete Funktion.
- **Nutzung:** `Client` (Check, FetchRemote, PushFunctions), Ereignisstrom, Startseite
  (`/terminal/geraet`-Formular, CSP `form-action`), Kartenleser-Ursprung (`cardOrigin`) nehmen die
  aktive Basis statt `Device.URL`. Die Gekoppelt-Prüfung `Device.URL == cfg.PDH.URL` bleibt unverändert.
- **Chromium-/Browser-Richtlinie:** Ausweichadresse erlaubt, wenn gesetzt (gleicher Mechanismus wie
  die Hauptadresse).
- **Admin-Seite:**
  - Feld „Ausweichadresse (Tailnet)“ mit Hinweis.
  - Bereich „Tailnet“: Status (nicht installiert / nicht angemeldet / angemeldet als `<name>`,
    `100.x`), Auth-Key-Feld + „Anmelden“, „Abmelden“.
  - Statuszeile „verbunden über: LAN / Tailnet“ mit Zeitpunkt des Wechsels; Tooltip: der Netzwerkfehler auf
    der Hauptadresse, der den Wechsel ausgelöst hat (Fehler der Proben werden nicht festgehalten).
  - Bereich „Tailnet“ zeigt nur die passende Aktion: „Anmelden“ bei nicht angemeldet, „Abmelden“ bei
    angemeldet; ist der Zustand nicht abfragbar, beide.
- **Tailscale-Steuerung** (neu, z. B. `internal/tailnet`): Status über `tailscale status --json`,
  Anmelden/Abmelden über die CLI; plattformabhängiger Pfad (Pi: `/usr/bin/tailscale`, Windows:
  `%ProgramFiles%\Tailscale\tailscale.exe`). Auf dem Pi über den vorhandenen privilegierten Helfer,
  nicht als Kiosk-Benutzer.
- **Pi-Abbild:** Tailscale-Paketquelle + Schlüssel, Paket `tailscale`, `tailscaled` aktiviert;
  `verify-image.sh` prüft Paket und Dienst. Der Zustand von `tailscaled` liegt auf `/data/system/tailscale`
  (Drop-in für `tailscaled.service` mit `BindPaths=/data/system/tailscale:/var/lib/tailscale`, ExecStart des
  Pakets unverändert), weil `/` im Betrieb ein RAM-Overlay ist; sonst wäre die Anmeldung nach jedem Neustart weg.
  Der Zustand ist an die Seriennummer gebunden (`.pdh-serial`): Auf einem anderen Gerät (geklonte Karte) wird er
  vor dem Start verworfen, die Kopie ist „nicht angemeldet“.
- **Windows-Installer:** Tailscale-Installation wie Entscheidung 6; Deinstallation des Terminals
  entfernt Tailscale nicht.

### PDH (pdh-server)

- Prüfen und mit Test absichern, dass Kopplungs-/Geräte-API, `/terminal/geraet`, `/terminal/events`
  und Kartenleser-Endpunkte unter einem zweiten Hostnamen funktionieren (keine Bindung an
  `PublicURL`).
- Kiosk-Terminals-Liste: Spalte/Hinweis „Weg“ (LAN/Tailnet). Das Terminal meldet den aktiven Weg im
  Header `X-Terminal-Route: lan|tailnet` (nur hinzufügend; alte Terminals ohne Header ⇒ „LAN“).
  Gespeichert wie die Admin-Adresse (letzter Stand je Gerät), Migration nur hinzufügend falls nötig.

### Server-Einrichtung (Anleitung, kein Code)

- `tailscale serve` auf dem PDH-Server für HTTPS → PDH-Port.
- Tailnet-Richtlinie (ACL): Terminals mit `tag:pdh-terminal`, Auth-Keys mit diesem Tag und
  vorab freigegeben; Zugriff von `tag:pdh-terminal` nur auf den PDH-Server, Port 443.

## Fehlerfälle

| Fall | Verhalten |
|---|---|
| Keine Ausweichadresse gesetzt | wie 0.4.x |
| Hauptadresse: Netzwerkfehler, Ausweichadresse erreichbar | Wechsel, Anzeige „über Tailnet“ |
| Beide unerreichbar | Offline-Seite/Folien wie heute, Versuche weiter im 30-s-Takt am aktiven Weg (steht er noch auf LAN, wechselt ein Netzwerkfehler auf die Ausweichadresse); jeder Netzwerkfehler auf der aktiven Ausweichadresse stößt sofort eine Probe der Hauptadresse an (höchstens eine je 30 s, `routeKickMin`), sonst Probe alle 5 min |
| Hauptadresse: Zertifikatsfehler | kein Wechsel, Meldung „Zertifikat nicht vertrauenswürdig“ wie heute |
| Gerät gesperrt/entkoppelt (über welchen Weg auch immer) | kein Wechsel, Anzeige wie heute |
| Ausweichadresse: Zertifikatsfehler | Der Wechsel geschieht beim Netzwerkfehler der Hauptadresse, ohne die Ausweichadresse vorher zu prüfen. Danach bleibt das Terminal auf der Ausweichadresse und zeigt den Fehler („Zertifikat von PDH wird nicht vertraut“, Startseite und Admin-Seite); der TLS-Fehler stößt keine Probe an, die Probe alle 5 min führt aber zur Hauptadresse zurück, sobald sie antwortet |
| Tailscale nicht angemeldet | Ausweichadresse nicht erreichbar ⇒ Hinweis auf der Admin-Seite |
| Ungültiger/abgelaufener Auth-Key | Fehlermeldung aus `tailscale up`, Key verworfen |

## Tests

- Fehler-Einstufung (Tabelle: Netzwerkfehler ⇒ wechseln; TLS, 401/403, 5xx ⇒ nicht).
- Wegwahl: Wechsel, Rückkehr nach Prüfintervall (Uhr injizierbar), nebenläufiger Zugriff (`-race`).
- Konfig-Prüfung `fallback_url` (nur https, ohne Pfad, ≠ Hauptadresse).
- Startseite/CSP/Karten-Ursprung mit aktiver Ausweichadresse (httptest mit zwei Servern).
- Tailscale-Steuerung mit gefälschter CLI (Status-JSON, Fehlerausgaben, Auth-Key taucht in keinem
  Protokoll auf).
- Abbild: `verify-image.sh` prüft Paket und Dienst; Windows: Installer-Logik ohne Ausführung auf dem
  Entwickler-PC prüfbar (Struktur der WiX-Datei/Custom Action).
- PDH: Handler-Tests mit zweitem Hostnamen; Header `X-Terminal-Route`.

## Nicht enthalten

- Mehr als eine Ausweichadresse, Lastverteilung.
- Automatisches Einrichten von `tailscale serve` auf dem Server.
- Fernzugriff auf Entwickler-PCs (separat, reine Einrichtung).
