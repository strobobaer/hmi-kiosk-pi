#!/usr/bin/env bash
# Prüft hmi-kiosk-session ohne Grafik: Bedienart Touch oder Maus aus config.json.
# labwc und startx werden durch Stubs ersetzt, die ihre Umgebung und Argumente festhalten.
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
chmod +x "$work/bin/labwc" "$work/bin/startx"

# run PROFIL BEDIENART: startet die Sitzung mit dieser Konfiguration und gibt die Stub-Ausgabe aus.
run() {
	local d="$work/$1-$2"
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
if ! grep -q XCURSOR_THEME "$root/packaging/rootfs/etc/hmi-kiosk/labwc/environment"; then ok "environment überschreibt das Zeigerthema nicht"; else bad "XCURSOR_THEME steht noch in environment"; fi

echo
if [ $fails -eq 0 ]; then echo "Alle Prüfungen bestanden."; else echo "$fails Prüfung(en) fehlgeschlagen."; exit 1; fi
