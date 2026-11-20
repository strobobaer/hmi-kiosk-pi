# Windows-Setup des PDH-Terminals

Inno-Setup-Skript und Bau des Setups sowie des signierten Windows-Update-Pakets.

| Datei | Zweck |
|---|---|
| `pdh-terminal.iss` | Inno-Setup-Skript: Installation für alle Benutzer, Autostart (Starter), Startmenü, Firewall-Regel, Datenordner |
| `build-setup.sh` | baut Programm und Starter, das Setup und das signierte Paket |

## Bauen

```
packaging/windows/build-setup.sh --version x.y.z
```

Nur `x.y.z` ist erlaubt (kein `-test`). Nötig sind Docker, `.secrets/release.pub` (oder `HMI_RELEASE_PUB`) und der private Schlüssel `release.key` im Ordner `HMI_RELEASE_KEY_DIR` (Standard `.secrets`). Der Schlüsselordner wird nur lesend (`:ro`) in den Container eingebunden, nie kopiert.

Ergebnis in `dist/`:

- `PDH-Terminal-Setup-<version>.exe` und `.sha256`
- `pdh-terminal-<version>-windows-amd64.pkg` und `.sha256`
- `win/pdh-terminal.exe`, `win/pdh-terminal-start.exe`

Inno Setup läuft im Docker-Abbild `amake/innosetup`, festgelegt über den Digest im Skript (`INNO_IMAGE`). Zum Wechsel des Abbilds den Digest dort ersetzen und ein Setup bauen.

## Wichtig

- Programm und Starter werden mit **derselben** Version und demselben öffentlichen Schlüssel gebaut wie beim Linux-Release: Der Starter nimmt seine Basisversion aus der eigenen Version.
- Autostart und Startmenü starten `pdh-terminal-start.exe`, nie `pdh-terminal.exe`: Nach einem Update beendet sich das Programm mit Code 4, und nur der Starter startet es neu.
- Die Verknüpfung „PDH-Terminal“ trägt die App-Kennung `PDH.Terminal` (nötig für Windows-Mitteilungen).
- Der Datenordner `%ProgramData%\PDH-Terminal` wird bei der Deinstallation nicht gelöscht.
- Das Setup nicht auf dem Entwicklungsrechner ausführen, um nur zu prüfen: Es installiert wirklich. Prüfung des Baus: `bash packaging/test-products.sh` (prüft die Pflichtteile im `.iss`) und `update.Verify` des Pakets mit dem öffentlichen Schlüssel.
- Der Ablauf des Release steht in `docs/betrieb.md`, Abschnitt 2.
