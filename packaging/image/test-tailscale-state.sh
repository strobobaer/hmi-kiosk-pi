#!/bin/sh
# Test von stage-hmi/00-tailscale/files/hmi-kiosk-tailscale-state mit Ersatzpfad. Braucht root (Besitzerprüfung):
#
#   docker run --rm -v "$PWD:/src" -w /src debian:trixie sh packaging/image/test-tailscale-state.sh
#
# Prüft: legt /data/system und /data/system/tailscale an (root, 0755 bzw. 0700), lässt einen vorhandenen
# Zustand stehen und zieht zu weite Rechte nach; endet mit 1 und folgt keinem Verweis, wenn /data/system oder
# der Ordner darin ein Verweis ist, nicht root gehört oder für andere schreibbar ist.
set -u

SCRIPT=$(pwd)/packaging/image/stage-hmi/00-tailscale/files/hmi-kiosk-tailscale-state
fails=0
ok() { echo "OK $1"; }
bad() {
	echo "FEHLER $1"
	fails=$((fails + 1))
}

if [ "$(id -u)" != 0 ]; then
	echo "Der Test braucht root (Besitzerprüfung)"
	exit 2
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
D=$work/data
SYS=$D/system
TS=$SYS/tailscale

reset() {
	rm -rf "$D" "$work/ziel"
	mkdir -p "$D" "$work/ziel"
	chown 1100:1100 "$D"
	chmod 750 "$D"
}
rc=0
run() {
	HMI_TAILSCALE_STATE_DIR=$TS sh "$SCRIPT" >"$work/out" 2>&1
	rc=$?
}
mode() { stat -c '%u:%g %a' "$1"; }

echo "== frisch: legt beide Ordner an"
reset
run
if [ "$rc" = 0 ] && [ "$(mode "$SYS")" = "0:0 755" ] && [ "$(mode "$TS")" = "0:0 700" ]; then ok "angelegt"; else bad "frisch: rc=$rc $(cat "$work/out")"; fi

echo "== vorhandener Zustand bleibt, zu weite Rechte werden enger"
echo zustand >"$TS/tailscaled.state"
chmod 755 "$TS"
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/tailscaled.state")" = zustand ] && [ "$(mode "$TS")" = "0:0 700" ]; then ok "Zustand bleibt, 0700"; else bad "vorhanden: rc=$rc $(mode "$TS")"; fi

echo "== Falle: /data/system ist ein Verweis"
reset
ln -s "$work/ziel" "$SYS"
run
if [ "$rc" = 1 ] && [ ! -e "$work/ziel/tailscale" ]; then ok "Verweis abgelehnt"; else bad "Verweis /data/system: rc=$rc"; fi

echo "== Falle: /data/system gehört dem Dienstbenutzer"
reset
mkdir -m 755 "$SYS"
chown 1100:1100 "$SYS"
run
if [ "$rc" = 1 ] && [ ! -e "$TS" ]; then ok "fremder Besitzer abgelehnt"; else bad "Besitzer: rc=$rc"; fi

echo "== Falle: /data/system für andere schreibbar"
reset
mkdir -m 777 "$SYS"
chmod 777 "$SYS"
run
if [ "$rc" = 1 ] && [ ! -e "$TS" ]; then ok "schreibbar abgelehnt"; else bad "Rechte: rc=$rc"; fi

echo "== Falle: der Zustandsordner ist ein Verweis"
reset
mkdir -m 755 "$SYS"
ln -s "$work/ziel" "$TS"
chmod 755 "$work/ziel"
run
if [ "$rc" = 1 ] && [ "$(stat -c %a "$work/ziel")" = 755 ]; then ok "Verweis abgelehnt, Ziel unverändert"; else bad "Verweis tailscale: rc=$rc"; fi

echo "== Falle: der Zustandsordner ist eine Datei"
reset
mkdir -m 755 "$SYS"
: >"$TS"
run
if [ "$rc" = 1 ]; then ok "Datei abgelehnt"; else bad "Datei: rc=$rc"; fi

echo "== Falle: der Zustandsordner gehört dem Dienstbenutzer"
reset
mkdir -m 755 "$SYS"
mkdir -m 777 "$TS"
chmod 777 "$TS"
chown 1100:1100 "$TS"
run
if [ "$rc" = 1 ] && [ "$(mode "$TS")" = "1100:1100 777" ]; then ok "fremder Zustandsordner abgelehnt, unverändert"; else bad "fremder Zustandsordner: rc=$rc $(mode "$TS")"; fi

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) fehlgeschlagen"
	exit 1
fi
echo "Alle Prüfungen bestanden"
