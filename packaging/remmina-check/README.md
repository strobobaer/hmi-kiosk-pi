# Remmina-Abgleich

Prüft das echte Remmina aus Debian Trixie unter Xvfb gegen die Annahmen aus `internal/client/remmina.go`.
Die Ergebnisse stehen in Abschnitt 14 der Spec.

Build-Kontext ist die Repo-Wurzel:

```bash
docker build --platform linux/arm64 -f packaging/remmina-check/Dockerfile -t hmi-remmina-check .
docker run --rm --platform linux/arm64 hmi-remmina-check
```

Auf einem amd64-Host braucht das arm64-Abbild Emulation (binfmt/QEMU, bei Docker Desktop vorhanden).

Jeder Punkt ergibt eine Zeile `OK <punkt>: <detail>` oder `ABWEICHUNG <punkt>: <detail>`. Der Exit-Code ist
nur ungleich 0, wenn die Prüfumgebung selbst versagt. Ein vollständiger Lauf dauert etwa fünf Minuten.

Die Fensterpunkte für `viewmode` 0, 3 und 4 melden eine Abweichung von der Fensterregel; das ist das erwartete
Verhalten dieser Werte, die der Dienst nicht verwendet. Auch `falsches-passwort` meldet eine bekannte Abweichung.

Optionen des Prüfprogramms (hinter dem Abbildnamen):

- `-only <regexp>`: nur passende Punkte, z. B. `-only '^drop-clients$'`
- `-shots <verzeichnis>`: Bildschirmfotos ablegen, z. B. mit `-v "$PWD/out:/shots" ... -shots /shots`
- `-v`: Ausgaben von Remmina auf stderr zeigen

`openbox-rc.xml` bildet die spätere labwc-Regel nach. Die Größen darin müssen zu `run.sh` und zu `target` in
`cmd/hmi-kiosk-remmina-check/main.go` passen.
