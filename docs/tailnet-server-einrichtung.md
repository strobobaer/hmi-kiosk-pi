# Tailnet-Rückfall: Server und Tailnet einrichten (ab 0.5.0)

Diese Anleitung beschreibt, was **einmalig auf dem PDH-Server und in der Tailscale-Verwaltung** zu tun ist, damit PDH-Terminals bei Ausfall des LAN-Wegs über das Tailnet weiterarbeiten. Die Einrichtung am Terminal steht in [betrieb.md](betrieb.md) (Abschnitt „Tailnet“ und Abschnitt 4), die Entscheidungen in [entscheidungen.md](entscheidungen.md).

Das Terminal richtet nichts davon selbst ein: `tailscale serve` und die Richtlinie des Tailnets sind Sache des Servers und der Tailscale-Verwaltung.

## Überblick

1. Der PDH-Server ist Mitglied des Tailnets und gibt PDH dort unter `https://<server>.<tailnet>.ts.net` frei (`tailscale serve`). Tailscale beschafft das Zertifikat selbst (Let's Encrypt); das Terminal braucht keine Ausnahme bei der Zertifikatsprüfung.
2. Die Tailnet-Richtlinie (ACL) erlaubt den Terminals nur den Zugriff auf diesen Server, Port 443.
3. Ein Auth-Key mit dem Tag der Terminals meldet jedes Terminal an.
4. Am Terminal wird die `ts.net`-Adresse als **Ausweichadresse** eingetragen.

`100.x`-Adressen als Ausweichadresse funktionieren nicht (für IP-Adressen gibt es kein Zertifikat); das Terminal lehnt IP-Adressen beim Speichern ab. Der Name unter `ts.net` ist der vorgesehene Weg (andere Namen mit gültigem Zertifikat sind erlaubt).

## 1. Voraussetzungen im Tailnet

In der Tailscale-Verwaltung (Admin-Konsole, `https://login.tailscale.com/admin`):

- **MagicDNS** und **HTTPS-Zertifikate** sind eingeschaltet (Reiter „DNS“, Abschnitt „HTTPS Certificates“). Ohne sie gibt `tailscale serve` keinen HTTPS-Zugang frei. Das Einschalten macht die Namen des Tailnets in den öffentlichen Zertifikatsprotokollen sichtbar; das ist bei Tailscale so vorgesehen.
- Der PDH-Server ist im Tailnet angemeldet (`tailscale up` auf dem Server mit Tag, siehe Abschnitt 2, „Server-Tailscale und Tag“).

## 2. `tailscale serve` auf dem PDH-Server

### Server-Tailscale und Tag

`tailscale serve` braucht das **Tailscale des Server-Betriebssystems** (`tailscaled` auf dem Host). PDH hat außerdem einen eigenen, eingebetteten tsnet-Knoten mit eigenen Tags (`tag:pdh`, `tag:pdh-data`, Datennetz). Das ist ein **anderer Knoten** und kann nicht für `tailscale serve` benutzt werden. Der Host muss selbst im Tailnet sein, mit eigenem Tag:

```
sudo tailscale up --advertise-tags=tag:pdh-server
```

Das Tag muss in `tagOwners` stehen (Abschnitt 3), sonst lehnt Tailscale die Anmeldung ab. Einen Namen wählen, der nicht mit `tag:pdh` oder `tag:pdh-data` zusammenfällt, damit die Regeln des PDH-Datennetzes unberührt bleiben. Ist der Host schon ohne Tag angemeldet, das Tag in der Admin-Konsole bei der Maschine setzen; sie gehört danach keinem Benutzer mehr.

### Welcher Port, welches Protokoll

PDH lauscht auf **Port 8090** und spricht dort **unverschlüsseltes HTTP** (`PDH_SERVER_PORT=8090` in `.env.example`, `compose.yaml` veröffentlicht `8090:8090`, `cmd/server/main.go` ruft `ListenAndServe` ohne TLS auf). Das TLS im normalen Betrieb übernimmt ein vorgeschalteter nginx auf Port 443 mit dem Zertifikat des PDH-Namens. Für das Tailnet gilt deshalb:

| PDH-Betrieb | Befehl |
|---|---|
| **Standard (PDH selbst, HTTP auf 8090)** | `sudo tailscale serve --bg --https=443 http://localhost:8090` |
| PDH hinter einem eigenen TLS-Proxy, der auf einem lokalen Port mit **selbstsigniertem** Zertifikat hört | `sudo tailscale serve --bg --https=443 https+insecure://localhost:<Port>` |

Der **erste** Befehl gilt für ein PDH nach der Standardinstallation (Docker, `compose.yaml`). Tailscale beendet TLS selbst und reicht die Anfrage unverschlüsselt an `localhost:8090` weiter; im Tailnet ist die Verbindung zwischen Terminal und Server zusätzlich durch WireGuard verschlüsselt.

Den zweiten Befehl nur verwenden, wenn der Proxy auf `localhost` tatsächlich TLS spricht und den ts.net-Namen kennt (siehe Alternative unten). `https+insecure://` prüft das Zertifikat des lokalen Ziels nicht, was auf `localhost` vertretbar ist. **Nicht** den nginx auf Port 443 als Ziel nehmen: Er wählt die Seite nach dem Namen und kennt den `ts.net`-Namen nicht, die Anfragen landen dann auf der falschen Seite.

**Zu beachten:**

- `PDH_PUBLIC_URL` muss gesetzt bleiben (Hauptadresse). Sonst könnte PDH Links (QR-Codes, Passwort-Rücksetzung) über den ts.net-Weg mit `http://` bauen.
- Hinter `tailscale serve` kommen alle Terminals von derselben Loopback-Adresse bei PDH an, genau wie hinter nginx. Die Begrenzung bei der Kopplung (5 Fehlversuche in 5 Minuten) gilt deshalb für alle Terminals dieses Wegs zusammen.
- Alternative: im nginx des Servers einen eigenen `server_name <name>.ts.net`-Block anlegen (Vorlage: der Block, den der Update-Agent für den PDH-Namen schreibt) und `tailscale serve` auf `https+insecure://localhost:443` zeigen lassen. Der Standardbefehl oben ist einfacher und bleibt gültig.

PDH prüft fremde Seiten über `Sec-Fetch-Site`, nicht über einen festen Hostnamen, und die Cookies gelten je Name. Ein zweiter Name ist für PDH deshalb unkritisch.

Prüfen:

```
tailscale serve status
```

Die Ausgabe zeigt `https://<server>.<tailnet>.ts.net (tailnet only)` und das Ziel `proxy http://localhost:8090`. Ausschalten: `sudo tailscale serve --https=443 off`.

`tailscale serve` (nicht `funnel`) macht PDH **nur im Tailnet** erreichbar, nicht im Internet. Nie `funnel` verwenden.

### Den Namen finden und am Terminal eintragen

```
tailscale status
```

In der Zeile des PDH-Servers steht der Kurzname; der volle Name ist `<kurzname>.<tailnet>.ts.net`. Genau gibt ihn `tailscale status --json` im Feld `Self.DNSName` an (ohne den Punkt am Ende).

Am Terminal: Admin-Seite, Reiter **PDH**, Feld **Ausweichadresse (Tailnet)**, zum Beispiel `https://pdh.example-tailnet.ts.net` (reiner Ursprung: nur `https`, kein Pfad, nicht gleich der Hauptadresse). Speichern.

## 3. Richtlinie des Tailnets (ACL)

Die Terminals tragen das Tag `tag:pdh-terminal` und dürfen **nur** den PDH-Server auf Port 443 erreichen. Ausschnitt der Richtliniendatei (Admin-Konsole, Reiter „Access controls“; HuJSON, also mit Kommentaren und einem Komma am Ende erlaubt). Platzhalter in spitzen Klammern ersetzen:

```jsonc
{
  // Wer die Tags vergeben darf (Personen oder Gruppen, die Auth-Keys erzeugen und Geräte anmelden)
  "tagOwners": {
    "tag:pdh-terminal": ["<admin@example.com>"],
    "tag:pdh-server":   ["<admin@example.com>"],   // Tag des Host-Tailscale des PDH-Servers (Abschnitt 2)
  },

  "acls": [
    // Terminals dürfen nur den PDH-Server erreichen, nur Port 443
    {
      "action": "accept",
      "src":    ["tag:pdh-terminal"],
      "dst":    ["tag:pdh-server:443"],
    },

    // ... bestehende Regeln für Administratoren hier belassen ...
  ],
}
```

Hinweise:

- Tailscale verbietet, was keine Regel erlaubt. Gibt es bereits eine „alle zu allen“-Regel (`"src": ["*"], "dst": ["*:*"]`), dürfen die Terminals trotz der Regel oben alles erreichen. Diese Regel dann einschränken, sonst schützt `tag:pdh-terminal` nichts.
- Die Terminals erreichen nichts außer dem PDH-Server, auch keine anderen Terminals. Der Gegenweg (Server zu Terminal) ist nicht nötig.
- Ein Gerät mit Tag gehört keinem Benutzer; sein Schlüssel läuft nicht mit dem Benutzer ab.
- Mit der Vorschau der Admin-Konsole („Preview rules“) lässt sich prüfen, dass ein Terminal nur den Server sieht.

## 4. Auth-Key erzeugen

Pro Terminal wird einmal ein Auth-Key auf der Admin-Seite des Terminals eingegeben (Abschnitt „Tailnet“). Das Terminal speichert ihn nicht; danach hält Tailscale seinen eigenen Maschinenschlüssel, auf dem Pi auf der Datenpartition (`/data/system/tailscale`), unter Windows in `C:\ProgramData\Tailscale`. Die Anmeldung übersteht Neustart und Stromausfall; ein neuer Key ist nur für ein neues Gerät nötig oder nach dem Abmelden. Auf dem Pi ist der Zustand an die Seriennummer gebunden: Eine kopierte SD-Karte verwirft ihn auf einem anderen Gerät beim Start und braucht dort nur eine neue Anmeldung mit einem Key; auch ein Ersatzgerät wird neu angemeldet, den Eintrag des alten Geräts unter „Machines“ entfernen ([betrieb.md](betrieb.md), „SD-Sicherung auf ein anderes Gerät“, Tailnet-Identität).

In der Admin-Konsole: **Settings, Keys, Generate auth key**:

| Einstellung | Wert | Grund |
|---|---|---|
| **Tags** | `tag:pdh-terminal` | Das Gerät bekommt genau die Rechte der Richtlinie oben und gehört keinem Benutzer. |
| **Pre-approved** | an | In Tailnets mit „Device approval“ muss das Gerät nicht von Hand freigegeben werden. |
| **Reusable** | je nach Zahl der Geräte | Für ein einzelnes Terminal nicht nötig (Key gilt einmal). Für mehrere Terminals einen wiederverwendbaren Key erzeugen, damit nicht für jedes Gerät einer nötig ist. |
| **Ephemeral** | **aus** | Ephemeral-Geräte verschwinden aus dem Tailnet, sobald sie eine Zeit offline sind. Ein festes Terminal soll nach einem Stromausfall mit derselben Identität wieder da sein (der Tailscale-Zustand bleibt auf dem Gerät erhalten, auf dem Pi unter `/data/system/tailscale`). **Für feste Terminals nicht empfohlen.** |
| **Expiration** | kurz | Der Key wird nur bei der Anmeldung gebraucht. |

Hinweise:

- Der Key gilt nur für die **Anmeldung**. Ist das Terminal angemeldet, läuft es auch nach Ablauf des Keys weiter. Bei Geräten mit Tag ist der Ablauf des Geräteschlüssels standardmäßig aus; in der Admin-Konsole unter „Machines“ nachsehen.
- Einen wiederverwendbaren Key nach der Inbetriebnahme **widerrufen** (Settings, Keys). Bereits angemeldete Geräte bleiben angemeldet.
- Den Key nicht in Tickets, Chats oder Dateien ablegen. Das Terminal gibt ihn über eine kurzlebige Datei an `tailscale up` und löscht sie danach; er steht in keinem Protokoll.
- Bei einem widerrufenen oder abgelaufenen Key zeigt die Admin-Seite die Fehlermeldung von `tailscale up`; es wurde nichts angemeldet.

## 5. Prüfen

1. Am Terminal unter „Tailnet“ anmelden. Der Status zeigt „angemeldet als …“ mit Name, DNS-Name und `100.x`-Adresse.
2. Am Terminal (Pi zum Beispiel per SSH): `tailscale ping <pdh-server>` muss antworten.
3. Den Weg zum PDH-Server über die Hauptadresse unterbrechen (Netzwerkkabel oder Firewall) und nach etwa einer Minute prüfen: Admin-Seite, Reiter „PDH“, zeigt „verbunden über: Tailnet seit hh:mm“. Weg wieder freigeben: nach etwa 5 Minuten (Probe alle 5 Minuten plus bis zu 10 Sekunden) steht dort wieder „verbunden über: LAN“.
