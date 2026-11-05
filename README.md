# hmi-kiosk

Kiosk-Dienst in Go für HMI-Terminals auf dem Raspberry Pi 5. Das Terminal zeigt auf einem Touchbildschirm Kacheln für HMIs. Ein Tipp auf eine Kachel öffnet das HMI über VNC (Remmina oder TigerVNC). Mehrere Terminals stimmen sich direkt miteinander ab, wer ein HMI belegt (Übernahme, Sperre, Vorrang). Die Einrichtung läuft über eine Admin-Seite im Browser.

Der Entwurf steht in `docs/superpowers/specs/2026-11-05-hmi-kiosk-design.md`.

## Handbücher

- [Inbetriebnahme](docs/inbetriebnahme.md): Abbild bauen, Karte schreiben, erster Start, Einrichtung, Selbstprüfung, Dauerprüfung, zweites Terminal, offene Punkte nur auf Hardware, Installation auf anderen Linux-Systemen.
- [Betrieb](docs/betrieb.md): Rückfall, Update, Wartung, Fehlersuche.

## Bauen und testen

Go muss lokal nicht installiert sein. `scripts/dgo` führt das Go-Werkzeug im Container `golang:1.24-bookworm` aus. Nötig sind Docker und eine Bash (unter Windows Git Bash).

```
scripts/dgo test -race ./...          # alle Tests
scripts/dgo vet ./...                 # statische Prüfung
scripts/dgo fmt ./...                 # Formatierung

make test                             # wie die Tests oben
make build                            # alle Programme für linux/arm64
make release VERSION=1.0.0            # Programme mit Release-Schlüssel und Version nach dist/
make image VERSION=1.0.0              # SD-Abbild mit pi-gen in Docker (rund 25 Minuten)

bash test/install/run.sh              # install.sh im frischen debian:trixie-Container ohne Netz
```

Vor einem Release einmalig den Schlüssel erzeugen (Details in der Inbetriebnahme):

```
scripts/dgo run ./cmd/hmi-kiosk-pack keygen -out .secrets
```

Der Integrationstest mit zwei Diensten und einem Test-VNC-Server hat das Build-Tag `integration`:

```
scripts/dgo test -tags integration ./test/integration/
```

## Auslieferung

- **SD-Abbild** für die Raspis: `packaging/build-image.sh` (über `make image`). Geschrieben wird es mit dem Raspberry Pi Imager als „Eigenes Image“ ohne OS-Anpassung.
- **Programm-Update** als signiertes Paket über die Admin-Seite: `hmi-kiosk-pack pack` (siehe Betrieb).
- **Andere Linux-Systeme** (Debian oder Raspberry Pi OS Trixie, arm64 oder amd64): Offline-Bundle mit `packaging/bundle.sh`, Installation mit `install.sh` aus dem Bundle.

## Aufbau des Repositorys

| Pfad | Inhalt |
|---|---|
| `cmd/hmi-kiosk` | Kiosk-Dienst; Unterbefehle `selftest` und `soak` |
| `cmd/hmi-kiosk-helper` | kleiner root-Helfer (Uhrzeit, NTP, WLAN/Bluetooth, Neustart, Update-Aktivierung) und Update-Wächter |
| `cmd/hmi-kiosk-pack` | Release-Schlüssel erzeugen und Update-Pakete signieren |
| `cmd/hmi-kiosk-testvnc`, `cmd/hmi-kiosk-remmina-check` | Hilfsprogramme für Tests und den Remmina-Abgleich |
| `internal/app` | Verdrahtung, Selbstprüfung und Dauerprüfung am Gerät |
| `internal/config`, `internal/secrets` | Konfiguration (atomar, mit Rückfallstand) und verschlüsselte Geheimnisse |
| `internal/session`, `internal/client`, `internal/browser` | Sitzungsablauf, VNC-Clients, Chromium |
| `internal/coord` | Abstimmung zwischen Terminals |
| `internal/rfb` | VNC-Protokoll für Vorabprüfung und Test-Server |
| `internal/web` | Kachelseite und Admin-Seite (Web-Dateien sind eingebettet) |
| `internal/helper`, `internal/helperapi` | Helfer und sein Socket-Protokoll |
| `internal/update`, `internal/release` | Update-Pakete, Aktivierung, Rückfall; Version und öffentlicher Schlüssel |
| `internal/platform`, `internal/selftest` | Anzeige, TCP-Zustand, Touch; Prüfschritte |
| `packaging/` | Abbild-Bau (`build-image.sh`, `image/`, `rootfs/`), `bundle.sh`, `install.sh`, Remmina-Abgleich |
| `test/integration`, `test/install` | Integrationstest mit zwei Diensten; Test von `install.sh` |
| `docs/` | Handbücher, Spezifikation, Bildschirmfotos |
| `scripts/dgo` | Go im Container |
