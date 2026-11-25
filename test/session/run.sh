#!/usr/bin/env bash
# Prüft hmi-kiosk-session ohne Grafik: Bedienart Touch, Maus oder Automatisch aus config.json.
# labwc, startx und udevadm werden durch Stubs ersetzt: labwc und startx halten Umgebung und Argumente fest,
# udevadm liefert die udev-Eigenschaften gefälschter Eingabegeräte (Automatisch = Maus erkannt).
#
#   bash test/session/run.sh
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
script="$root/packaging/rootfs/usr/local/bin/hmi-kiosk-session"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT
fails=0
ok() { echo "OK     $*"; }
bad() {
	echo "FEHLER $*"
	fails=$((fails + 1))
}

mkdir -p "$work/bin"
cat >"$work/bin/labwc" <<'EOF'
#!/bin/sh
printf 'XCURSOR_THEME=%s\nargs=%s\n' "${XCURSOR_THEME:-}" "$*" >"$STUB_OUT"
EOF
cat >"$work/bin/startx" <<'EOF'
#!/bin/sh
printf 'XCURSOR_THEME=%s\nargs=%s\n' "${XCURSOR_THEME:-}" "$*" >"$STUB_OUT"
EOF
# udevadm-Stub: "info --query=property --name=<Gerät>" gibt den Inhalt von $FAKE_UDEV_DIR/<Gerätename> aus;
# FAKE_UDEV_FAIL=1 lässt jede Abfrage scheitern, "settle" gelingt immer.
cat >"$work/bin/udevadm" <<'EOF'
#!/bin/sh
cmd=$1
shift
[ "$cmd" = settle ] && exit 0
[ "$cmd" = info ] || exit 1
[ -z "${FAKE_UDEV_FAIL:-}" ] || exit 1
for a in "$@"; do
	case "$a" in --name=*) name=${a#--name=} ;; esac
done
cat "$FAKE_UDEV_DIR/$(basename "$name")"
EOF
chmod +x "$work/bin/labwc" "$work/bin/startx" "$work/bin/udevadm"

# devices SZENARIO [GERÄT=EIGENSCHAFTEN ...]: legt gefälschte Eingabegeräte an (Eigenschaften mit \n getrennt)
# und zeigt HMI_KIOSK_INPUT_DIR und FAKE_UDEV_DIR darauf.
devices() {
	local scn="$1" spec
	shift
	mkdir -p "$work/dev-$scn" "$work/udev-$scn"
	for spec in "$@"; do
		: >"$work/dev-$scn/${spec%%=*}"
		printf '%b\n' "${spec#*=}" >"$work/udev-$scn/${spec%%=*}"
	done
	export HMI_KIOSK_INPUT_DIR="$work/dev-$scn" FAKE_UDEV_DIR="$work/udev-$scn"
	unset FAKE_UDEV_FAIL
}
devices keine

# run PROFIL BEDIENART [SZENARIO]: startet die Sitzung mit dieser Konfiguration und gibt die Stub-Ausgabe aus.
run() {
	local d="$work/$1-$2-${3:-ohne}"
	mkdir -p "$d/state"
	if [ -n "$2" ]; then
		printf '{"kiosk":{"inputMode":"%s"},"display":{"graphicsProfile":"%s"}}\n' "$2" "$1" >"$d/config.json"
	else
		printf '{"display":{"graphicsProfile":"%s"}}\n' "$1" >"$d/config.json"
	fi
	export HMI_KIOSK_CONFIG="$d/config.json" HMI_KIOSK_SESSION_DIR="$d/state" XDG_RUNTIME_DIR="$d" STUB_OUT="$d/out"
	PATH="$work/bin:$PATH" sh "$script" prepare >/dev/null
	PATH="$work/bin:$PATH" sh "$script"
	cat "$d/out"
}

out=$(run labwc touch)
if echo "$out" | grep -q '^XCURSOR_THEME=hmi-blank$'; then ok "labwc Touch: Zeiger ausgeblendet"; else bad "labwc Touch: $out"; fi
if echo "$out" | grep -q 'args=-C /etc/hmi-kiosk/labwc'; then ok "labwc: eigener Konfigurationsordner"; else bad "labwc Ordner: $out"; fi
out=$(run labwc mouse)
if echo "$out" | grep -q '^XCURSOR_THEME=$'; then ok "labwc Maus: Zeiger sichtbar"; else bad "labwc Maus: $out"; fi
out=$(run labwc "")
if echo "$out" | grep -q '^XCURSOR_THEME=hmi-blank$'; then ok "ohne Angabe: Touch"; else bad "ohne Angabe: $out"; fi
out=$(run x11 touch)
if echo "$out" | grep -q -- '-nocursor'; then ok "X11 Touch: -nocursor"; else bad "X11 Touch: $out"; fi
out=$(run x11 mouse)
if ! echo "$out" | grep -q -- '-nocursor'; then ok "X11 Maus: ohne -nocursor"; else bad "X11 Maus: $out"; fi

# --- Automatisch: Maus erkannt (udev), Touchscreen und Touchpad ---
MOUSE='ID_INPUT=1\nID_INPUT_MOUSE=1'
TOUCHPAD='ID_INPUT=1\nID_INPUT_TOUCHPAD=1'
TOUCHSCREEN='ID_INPUT=1\nID_INPUT_MOUSE=1\nID_INPUT_TOUCHSCREEN=1'
KEYBOARD='ID_INPUT=1\nID_INPUT_KEY=1\nID_INPUT_KEYBOARD=1'

# check_input TITEL ERWARTET(mouse|touch) PROFIL EINSTELLUNG SZENARIO
check_input() {
	local title="$1" want="$2" profile="$3" setting="$4" scn="$5" out d cursor
	out=$(run "$profile" "$setting" "$scn")
	d="$work/$profile-$setting-$scn"
	if [ "$(cat "$d/state/input" 2>/dev/null)" = "$want" ]; then ok "$title: wirksame Bedienart $want"; else bad "$title: input=$(cat "$d/state/input" 2>/dev/null)"; fi
	if [ -n "$setting" ]; then
		if [ "$(cat "$d/state/input-setting" 2>/dev/null)" = "$setting" ]; then ok "$title: Einstellung $setting vermerkt"; else bad "$title: input-setting=$(cat "$d/state/input-setting" 2>/dev/null)"; fi
	fi
	if [ "$profile" = x11 ]; then
		cursor=visible
		echo "$out" | grep -q -- '-nocursor' && cursor=hidden
	else
		cursor=hidden
		echo "$out" | grep -q '^XCURSOR_THEME=$' && cursor=visible
	fi
	if { [ "$want" = mouse ] && [ "$cursor" = visible ]; } || { [ "$want" = touch ] && [ "$cursor" = hidden ]; }; then ok "$title: Zeiger $cursor"; else bad "$title: Zeiger $cursor, erwartet $want: $out"; fi
}

devices maus "event0=$KEYBOARD" "event1=$MOUSE"
check_input "auto mit Maus (labwc)" mouse labwc auto maus
check_input "auto mit Maus (X11)" mouse x11 auto maus
devices touchpad "event0=$TOUCHPAD"
check_input "auto mit Touchpad" mouse labwc auto touchpad
devices touchscreen "event0=$KEYBOARD" "event1=$TOUCHSCREEN"
check_input "auto nur mit Touchscreen" touch labwc auto touchscreen
check_input "auto nur mit Touchscreen (X11)" touch x11 auto touchscreen
devices beide "event0=$TOUCHSCREEN" "event1=$MOUSE"
check_input "auto mit Touchscreen und Maus" mouse labwc auto beide
devices leer
check_input "auto ohne Eingabegeräte" touch labwc auto leer
devices fehler "event0=$MOUSE"
export FAKE_UDEV_FAIL=1
check_input "auto, udevadm scheitert" touch labwc auto fehler
unset FAKE_UDEV_FAIL
# Ohne udevadm im Suchpfad gilt ebenfalls touch.
devices ohneudev "event0=$MOUSE"
mv "$work/bin/udevadm" "$work/udevadm.weg"
check_input "auto ohne udevadm" touch labwc auto ohneudev
mv "$work/udevadm.weg" "$work/bin/udevadm"

# Feste Einstellungen und bestehende Dateien ändern sich durch eine angeschlossene Maus nicht.
devices fest "event0=$MOUSE"
check_input "touch bleibt touch trotz Maus" touch labwc touch fest
check_input "mouse bleibt mouse" mouse labwc mouse fest
check_input "ohne Angabe (ältere Datei) bleibt touch trotz Maus" touch labwc "" fest
devices fest2 "event0=$KEYBOARD"
check_input "mouse ohne Maus bleibt mouse" mouse labwc mouse fest2

if ! grep -q XCURSOR_THEME "$root/packaging/rootfs/etc/hmi-kiosk/labwc/environment"; then ok "environment überschreibt das Zeigerthema nicht"; else bad "XCURSOR_THEME steht noch in environment"; fi

echo
if [ $fails -eq 0 ]; then echo "Alle Prüfungen bestanden."; else echo "$fails Prüfung(en) fehlgeschlagen."; exit 1; fi
