#!/bin/sh
# Test von stage-hmi/00-tailscale/files/hmi-kiosk-tailscale-state mit Ersatzpfad. Braucht root (Besitzerprüfung):
#
#   docker run --rm -v "$PWD:/src" -w /src debian:trixie sh packaging/image/test-tailscale-state.sh
#
# Prüft: legt /data/system und /data/system/tailscale an (root, 0755 bzw. 0700), lässt einen vorhandenen
# Zustand stehen und zieht zu weite Rechte nach; endet mit 1 und folgt keinem Verweis, wenn /data/system oder
# der Ordner darin ein Verweis ist, nicht root gehört oder für andere schreibbar ist. Bindung an das Gerät:
# frischer Ordner bekommt .pdh-serial, gleiche Seriennummer behält den Zustand, eine andere verwirft ihn (geklonte
# Karte), eine unlesbare Seriennummer löscht nichts.
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
PROC=$work/proc
DMI=$work/sys

# set_serial <wert>: Seriennummer des Geräts (wie /proc/device-tree/serial-number, mit NUL am Ende); ohne Wert: keine.
set_serial() {
	rm -rf "$PROC" "$DMI"
	mkdir -p "$PROC/device-tree" "$DMI/class/dmi/id"
	if [ $# -gt 0 ]; then printf '%s\000' "$1" >"$PROC/device-tree/serial-number"; fi
}
reset() {
	rm -rf "$D" "$work/ziel"
	mkdir -p "$D" "$work/ziel"
	chown 1100:1100 "$D"
	chmod 750 "$D"
	set_serial 10000000ABCDEF01
}
rc=0
run() {
	HMI_TAILSCALE_STATE_DIR=$TS HMI_TAILSCALE_STATE_PROC=$PROC HMI_TAILSCALE_STATE_SYS=$DMI sh "$SCRIPT" >"$work/out" 2>&1
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

echo "== Bindung: frischer Ordner bekommt die Seriennummer"
reset
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/.pdh-serial")" = 10000000abcdef01 ] && [ "$(mode "$TS/.pdh-serial")" = "0:0 600" ]; then
	ok "Seriennummer festgehalten"
else
	bad "frische Bindung: rc=$rc $(cat "$work/out")"
fi

echo "== Bindung: gleiche Seriennummer, Zustand bleibt"
echo zustand >"$TS/tailscaled.state"
mkdir -p "$TS/certs"
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/tailscaled.state")" = zustand ] && [ -d "$TS/certs" ] && ! grep -q verworfen "$work/out"; then
	ok "gleiches Gerät behält den Zustand"
else
	bad "gleiche Seriennummer: rc=$rc $(cat "$work/out")"
fi

echo "== Bindung: andere Seriennummer (geklonte Karte), Zustand wird verworfen"
set_serial 10000000FEDCBA99
ln -s "$work/ziel" "$TS/verweis"
echo bleibt >"$work/ziel/datei"
run
if [ "$rc" = 0 ] && [ ! -e "$TS/tailscaled.state" ] && [ ! -e "$TS/certs" ] && [ ! -L "$TS/verweis" ] &&
	[ "$(cat "$work/ziel/datei")" = bleibt ] && [ "$(cat "$TS/.pdh-serial")" = 10000000fedcba99 ] &&
	[ "$(mode "$TS")" = "0:0 700" ] && [ "$(grep -c 'Tailnet-Zustand verworfen: anderes Gerät (geklonte Karte)' "$work/out")" = 1 ]; then
	ok "Klon verwirft den Zustand, folgt keinem Verweis, eine Protokollzeile"
else
	bad "Klon: rc=$rc $(ls -A "$TS") $(cat "$work/out")"
fi
run
if [ "$rc" = 0 ] && ! grep -q verworfen "$work/out"; then ok "danach stabil"; else bad "zweiter Lauf auf dem Klon: $(cat "$work/out")"; fi

echo "== Bindung: Seriennummer nicht lesbar, nichts wird gelöscht"
echo zustand >"$TS/tailscaled.state"
set_serial
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/tailscaled.state")" = zustand ] && [ "$(cat "$TS/.pdh-serial")" = 10000000fedcba99 ]; then
	ok "fehlende Seriennummer: Zustand und Marke bleiben"
else
	bad "fehlend: rc=$rc $(cat "$work/out")"
fi
set_serial 0000000000000000
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/tailscaled.state")" = zustand ]; then ok "Nullen-Seriennummer: Zustand bleibt"; else bad "Nullen: rc=$rc"; fi
set_serial
echo 10000000AAAAAAAA >"$work/ziel/serial"
ln -s "$work/ziel/serial" "$PROC/device-tree/serial-number"
ln -s /nicht/da "$DMI/class/dmi/id/product_uuid"
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/tailscaled.state")" = zustand ] && [ "$(cat "$TS/.pdh-serial")" = 10000000fedcba99 ]; then
	ok "Seriennummer als Verweis wird nicht gelesen, Zustand bleibt"
else
	bad "Verweis-Seriennummer: rc=$rc $(cat "$work/out")"
fi

echo "== Bindung: DMI-UUID als Rückfall"
reset
rm -f "$PROC/device-tree/serial-number"
echo "4C4C4544-0042-1234-8056-B2C04F333232" >"$DMI/class/dmi/id/product_uuid"
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/.pdh-serial")" = 4c4c4544-0042-1234-8056-b2c04f333232 ]; then ok "DMI-UUID"; else bad "DMI: rc=$rc $(cat "$work/out")"; fi

echo "== Bindung: vorhandener Zustand ohne Marke bleibt und wird gebunden"
reset
mkdir -m 755 "$SYS"
mkdir -m 700 "$TS"
echo zustand >"$TS/tailscaled.state"
run
if [ "$rc" = 0 ] && [ "$(cat "$TS/tailscaled.state")" = zustand ] && [ "$(cat "$TS/.pdh-serial")" = 10000000abcdef01 ]; then
	ok "ohne Marke: Zustand bleibt, gebunden"
else
	bad "ohne Marke: rc=$rc $(cat "$work/out")"
fi

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) fehlgeschlagen"
	exit 1
fi
echo "Alle Prüfungen bestanden"
