#!/bin/bash
# check-release-pub.sh <Datei>: prüft den öffentlichen Release-Schlüssel genau im Format von
# "hmi-kiosk-pack keygen": 64 kleingeschriebene Hex-Zeichen, ein Zeilenumbruch, sonst nichts.
# Ein verstümmelter Schlüssel im Programm würde jedes Update als ungültig abweisen.
set -u
f=${1:?Aufruf: check-release-pub.sh <Datei>}
if [ ! -s "$f" ]; then
	echo "Öffentlicher Release-Schlüssel fehlt oder ist leer: $f. Einmalig erzeugen mit: hmi-kiosk-pack keygen -out .secrets (privaten Schlüssel sicher aufbewahren)." >&2
	exit 1
fi
if [ "$(wc -c <"$f" | tr -d ' ')" != 65 ] || [ "$(wc -l <"$f" | tr -d ' ')" != 1 ] || ! grep -Eqx '[0-9a-f]{64}' "$f"; then
	echo "$f hat nicht das Format von \"hmi-kiosk-pack keygen\": erwartet werden genau 64 kleingeschriebene Hex-Zeichen und ein Zeilenumbruch, sonst nichts (kein Windows-Zeilenende, keine Leerzeilen). Datei neu erzeugen oder den Inhalt von release.pub unverändert kopieren." >&2
	exit 1
fi
