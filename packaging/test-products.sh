#!/usr/bin/env bash
# Prüft die Produktbeschreibungen unter packaging/products/ und die Hilfsfunktionen aus product-lib.sh.
#
#   bash packaging/test-products.sh
#
# Läuft ohne Docker und ohne Netz.
set -uo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

fails=0
ok() { echo "OK     $*"; }
bad() {
	echo "FEHLER $*"
	fails=$((fails + 1))
}
die() {
	echo "FEHLER $*" >&2
	return 1
}
check() { # check BESCHREIBUNG BEFEHL...
	local d="$1"
	shift
	if "$@" >/dev/null 2>&1; then ok "$d"; else bad "$d"; fi
}

# shellcheck source=packaging/product-lib.sh
. packaging/product-lib.sh

# Erwartete Werte: ID Name Programm (Quelle der Wahrheit ist internal/release/product.go).
products="hmi-kiosk:HMI-Kiosk:hmi-kiosk pdh-terminal:PDH-Terminal:pdh-terminal"
for entry in $products; do
	IFS=: read -r id name bin <<<"$entry"
	f="packaging/products/$id/product.env"
	if [ ! -f "$f" ]; then
		bad "$f fehlt"
		continue
	fi
	if out=$( (load_product "$id") 2>&1); then ok "$id: product.env vollständig"; else bad "$id: $out"; continue; fi
	(
		load_product "$id" 2>/dev/null
		[ "$PRODUCT_NAME" = "$name" ] && [ "$PRODUCT_BINARY" = "$bin" ] && [ "$IMG_NAME" = "$id" ] && [ "$TARGET_HOSTNAME" = "$id" ]
	) && ok "$id: Name, Programm, Abbildname und Hostname" || bad "$id: Werte weichen ab (erwartet $name, $bin, $id, $id)"
	check "$id: stimmt mit internal/release/product.go überein" \
		grep -Fq "ID: \"$id\", Name: \"$name\", Binary: \"$bin\"" internal/release/product.go
done

# firstboot.sh und verify-image.sh nehmen an, dass die Kennung dem Programmnamen gleicht.
for f in packaging/products/*/product.env; do
	if (
		# shellcheck source=/dev/null
		. "$f"
		[ -n "$PRODUCT_ID" ] && [ "$PRODUCT_ID" = "$PRODUCT_BINARY" ]
	); then ok "$f: PRODUCT_ID == PRODUCT_BINARY"; else bad "$f: PRODUCT_ID und PRODUCT_BINARY weichen ab"; fi
done

# Jede Variable, die die Abbild-Stufe aus build.env verlangt, muss build-image.sh schreiben.
need=$(grep -o '\${HMI_[A-Z_]*:?' packaging/image/stage-hmi/01-hmi/00-run.sh | sed 's/^\${//; s/:?$//' | sort -u)
if [ -z "$need" ]; then bad "00-run.sh verlangt keine HMI_*-Variable aus build.env (Prüfmuster veraltet?)"; fi
for v in $need; do
	check "build.env enthält $v (verlangt von 00-run.sh)" grep -Fq "${v}=%s" packaging/build-image.sh
done

# Overlay PDH-Terminal.
ov=packaging/products/pdh-terminal/rootfs
if [ -d "$ov" ]; then
	outside=$(cd "$ov" && find . -mindepth 1 ! -path './etc' ! -path './etc/*' | head -n 3)
	if [ -z "$outside" ]; then ok "Overlay enthält nur Pfade unter etc/"; else bad "Overlay enthält Pfade außerhalb von etc/: $outside"; fi
else
	bad "Overlay $ov fehlt"
fi
base=packaging/rootfs/etc/systemd/system/hmi-kiosk.service
unit="$ov/etc/systemd/system/hmi-kiosk.service"
if [ -f "$unit" ]; then
	changed=$(diff "$base" "$unit" | grep -c '^>' || true)
	removed=$(diff "$base" "$unit" | grep -c '^<' || true)
	if [ "$changed" = 1 ] && [ "$removed" = 1 ]; then ok "Haupteinheit: genau 1 Zeile geändert"; else bad "Haupteinheit: $changed neue und $removed entfernte Zeilen statt 1/1"; fi
	check "Haupteinheit: geänderte Zeile ist ExecStart mit /data/app/current/pdh-terminal" \
		bash -c "diff '$base' '$unit' | grep '^>' | grep -q '^> ExecStart=/data/app/current/pdh-terminal '"
else
	bad "Haupteinheit im Overlay fehlt"
fi
pol="$ov/etc/chromium/policies/managed/hmi-kiosk.json"
check "Richtlinie: nur die Grundliste ohne Web-Kacheln" bash -c "tr -d '\n\t' <'$pol' | tr -s ' ' | grep -Fq '\"URLAllowlist\": [\"http://127.0.0.1:8080\", \"http://localhost:8080\", \"http://127.0.0.1:8081\", \"http://localhost:8081\"]'"
check "Systemstand-Marker im Overlay ist 1" test "$(cat "$ov/etc/hmi-kiosk/system-level" 2>/dev/null)" = 1

# I²C für den Kartenleser (nur pdh-terminal): Prüffunktion aus verify-i2c.sh gegen Testbäume.
check "modules-load-Datei für i2c-dev im Overlay" test "$(cat "$ov/etc/modules-load.d/pdh-terminal-i2c.conf" 2>/dev/null)" = i2c-dev
check "00-run.sh installiert die modules-load-Datei nur für pdh-terminal" bash -c "grep -B3 'pdh-terminal-i2c.conf\" \"\${R}' packaging/image/stage-hmi/01-hmi/00-run.sh | grep -Fq 'HMI_PRODUCT_ID}\" = pdh-terminal'"
check "00-run.sh legt die Gruppe i2c an" grep -Fq 'getent group i2c >/dev/null || groupadd -r i2c' packaging/image/stage-hmi/01-hmi/00-run.sh
check "00-run.sh legt i2c nur für pdh-terminal an" bash -c "grep -B3 'groupadd -r i2c' packaging/image/stage-hmi/01-hmi/00-run.sh | grep -Fq 'HMI_PRODUCT_ID'"
check "00-run.sh nimmt hmikiosk nur bei pdh-terminal in i2c auf" bash -c "grep -Fq 'HMI_GROUPS=input,video,render,i2c' packaging/image/stage-hmi/01-hmi/00-run.sh && grep -Fq 'HMI_GROUPS=input,video,render' packaging/image/stage-hmi/01-hmi/00-run.sh && ! grep -Eq 'G input,video,render,i2c|G \$\{HMI_GROUPS\},' packaging/image/stage-hmi/01-hmi/00-run.sh"
check "00-run.sh setzt i2c_arm nur für pdh-terminal" bash -c "grep -B3 'dtparam=i2c_arm=on' packaging/image/stage-hmi/01-hmi/00-run.sh | grep -Fq 'HMI_PRODUCT_ID'"
check "install.sh nimmt i2c nur für pdh-terminal in die Gruppen auf" bash -c "grep -B2 'groupadd -r i2c' packaging/install.sh | grep -Fq B_PRODUCT"
check "00-run.sh beginnt mit dem Shebang" bash -c "[ \"\$(head -n 1 packaging/image/stage-hmi/01-hmi/00-run.sh)\" = '#!/bin/bash -e' ]"
check "00-run.sh liest build.env vor dem ersten on_chroot" bash -c "r=packaging/image/stage-hmi/01-hmi/00-run.sh; b=\$(grep -n '^\. files/build.env' \$r | head -n 1 | cut -d: -f1); o=\$(grep -n '^on_chroot' \$r | head -n 1 | cut -d: -f1); [ -n \"\$b\" ] && [ -n \"\$o\" ] && [ \"\$b\" -lt \"\$o\" ]"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
i2c_case() { # i2c_case PRODUKT CONFIG-TEXT GRUPPE(ja/nein) MODULDATEI(ja/nein): gibt die Meldungen der Prüfung aus
	local t
	t=$(mktemp -d "$tmp/i2c.XXXXXX")
	mkdir -p "$t/R/etc/modules-load.d" "$t/B"
	printf '%b' "$2" >"$t/B/config.txt"
	if [ "$3" = ja ]; then echo 'i2c:x:998:hmikiosk' >"$t/R/etc/group"; else echo 'i2c:x:998:' >"$t/R/etc/group"; fi
	if [ "$4" = ja ]; then echo i2c-dev >"$t/R/etc/modules-load.d/pdh-terminal-i2c.conf"; fi
	(
		ok() { echo "OK $1"; }
		bad() { echo "FEHLER $1"; }
		# shellcheck source=packaging/image/verify-i2c.sh
		. packaging/image/verify-i2c.sh
		check_i2c "$1" "$t/R" "$t/B"
	)
}
expect_i2c() { # expect_i2c ok|fehler BESCHREIBUNG PRODUKT CONFIG GRUPPE MODULDATEI
	local want="$1" d="$2" out
	shift 2
	out=$(i2c_case "$@" 2>&1)
	if [ "$want" = ok ] && ! echo "$out" | grep -q '^FEHLER'; then ok "$d"
	elif [ "$want" = fehler ] && echo "$out" | grep -q '^FEHLER'; then ok "$d"
	else bad "$d ($out)"; fi
}
cfg_with='dtparam=audio=on\n[all]\ndtparam=pciex1\ndtparam=i2c_arm=on\n'
cfg_without='dtparam=audio=on\n[all]\ndtparam=pciex1\n'
cfg_early='dtparam=i2c_arm=on\n[pi4]\ndtparam=x\n[all]\ndtparam=pciex1\n'
expect_i2c ok "i2c: pdh-terminal mit i2c_arm, Gruppe und Moduldatei besteht" pdh-terminal "$cfg_with" ja ja
expect_i2c fehler "i2c: pdh-terminal ohne i2c_arm schlägt fehl" pdh-terminal "$cfg_without" ja ja
expect_i2c fehler "i2c: pdh-terminal mit i2c_arm nur im Kopf (nicht im letzten [all]) schlägt fehl" pdh-terminal "$cfg_early" ja ja
expect_i2c fehler "i2c: pdh-terminal ohne Gruppenmitgliedschaft schlägt fehl" pdh-terminal "$cfg_with" nein ja
expect_i2c fehler "i2c: pdh-terminal ohne Moduldatei schlägt fehl" pdh-terminal "$cfg_with" ja nein
expect_i2c ok "i2c: hmi-kiosk braucht weder i2c_arm noch Gruppe noch Moduldatei" hmi-kiosk "$cfg_without" nein nein

# SPI und UART für den Kartenleser (nur pdh-terminal): Prüffunktion aus verify-busses.sh gegen Testbäume.
check "00-run.sh legt die Gruppe spi an" grep -Fq 'getent group spi >/dev/null || groupadd -r spi' packaging/image/stage-hmi/01-hmi/00-run.sh
check "00-run.sh legt spi nur für pdh-terminal an" bash -c "grep -B3 'groupadd -r spi' packaging/image/stage-hmi/01-hmi/00-run.sh | grep -Fq 'HMI_PRODUCT_ID'"
check "00-run.sh nimmt hmikiosk nur bei pdh-terminal in spi und dialout auf" bash -c "grep -Fq 'HMI_GROUPS=input,video,render,i2c,spi,dialout' packaging/image/stage-hmi/01-hmi/00-run.sh && grep -Fq 'HMI_GROUPS=input,video,render' packaging/image/stage-hmi/01-hmi/00-run.sh"
check "00-run.sh setzt spi=on nur für pdh-terminal" bash -c "grep -B3 'dtparam=spi=on' packaging/image/stage-hmi/01-hmi/00-run.sh | grep -Fq 'HMI_PRODUCT_ID'"
check "00-run.sh setzt uart0=on nur für pdh-terminal" bash -c "grep -B3 'dtparam=uart0=on' packaging/image/stage-hmi/01-hmi/00-run.sh | grep -Fq 'HMI_PRODUCT_ID'"
check "install.sh nimmt spi und dialout nur für pdh-terminal in die Gruppen auf" bash -c "grep -B3 'KIOSK_GROUPS,i2c,spi,dialout' packaging/install.sh | grep -Fq B_PRODUCT"
check "install.sh legt die Gruppe spi an" grep -Fq 'getent group spi >/dev/null || groupadd -r spi' packaging/install.sh
check "install.sh schreibt nicht in config.txt" bash -c "! grep -q 'config.txt' packaging/install.sh"
busses_case() { # busses_case PRODUKT CONFIG-TEXT SPI(ja/nein) DIALOUT(ja/nein) CMDLINE-TEXT: gibt die Meldungen der Prüfung aus
	local t g
	t=$(mktemp -d "$tmp/busses.XXXXXX")
	mkdir -p "$t/R/etc" "$t/B"
	printf '%b' "$2" >"$t/B/config.txt"
	printf '%s\n' "$5" >"$t/B/cmdline.txt"
	if [ "$3" = ja ]; then g="spi:x:997:hmikiosk\n"; else g="spi:x:997:\n"; fi
	if [ "$4" = ja ]; then g="${g}dialout:x:20:hmikiosk\n"; else g="${g}dialout:x:20:\n"; fi
	printf '%b' "$g" >"$t/R/etc/group"
	(
		ok() { echo "OK $1"; }
		bad() { echo "FEHLER $1"; }
		# shellcheck source=packaging/image/verify-busses.sh
		. packaging/image/verify-busses.sh
		check_busses "$1" "$t/R" "$t/B"
	)
}
expect_busses() { # expect_busses ok|fehler BESCHREIBUNG PRODUKT CONFIG SPI DIALOUT CMDLINE
	local want="$1" d="$2" out
	shift 2
	out=$(busses_case "$@" 2>&1)
	if [ "$want" = ok ] && ! echo "$out" | grep -q '^FEHLER'; then ok "$d"
	elif [ "$want" = fehler ] && echo "$out" | grep -q '^FEHLER'; then ok "$d"
	else bad "$d ($out)"; fi
}
cfg_bus='dtparam=audio=on\n[all]\ndtparam=pciex1\ndtparam=i2c_arm=on\ndtparam=spi=on\ndtparam=uart0=on\n'
cfg_nospi='dtparam=audio=on\n[all]\ndtparam=pciex1\ndtparam=uart0=on\n'
cfg_nouart='dtparam=audio=on\n[all]\ndtparam=pciex1\ndtparam=spi=on\n'
cfg_busearly='dtparam=spi=on\ndtparam=uart0=on\n[pi4]\ndtparam=x\n[all]\ndtparam=pciex1\n'
cmd_ok='console=tty1 root=PARTUUID=x rootwait'
cmd_ser='console=ttyAMA0,115200 console=tty1 root=PARTUUID=x rootwait'
expect_busses ok "spi/uart: pdh-terminal mit beiden Zeilen, Gruppen und freier Konsole besteht" pdh-terminal "$cfg_bus" ja ja "$cmd_ok"
expect_busses fehler "spi/uart: pdh-terminal ohne dtparam=spi=on schlägt fehl" pdh-terminal "$cfg_nospi" ja ja "$cmd_ok"
expect_busses fehler "spi/uart: pdh-terminal ohne dtparam=uart0=on schlägt fehl" pdh-terminal "$cfg_nouart" ja ja "$cmd_ok"
expect_busses fehler "spi/uart: Zeilen nur im Kopf (nicht im letzten [all]) schlagen fehl" pdh-terminal "$cfg_busearly" ja ja "$cmd_ok"
expect_busses fehler "spi/uart: pdh-terminal ohne Gruppe spi schlägt fehl" pdh-terminal "$cfg_bus" nein ja "$cmd_ok"
expect_busses fehler "spi/uart: pdh-terminal ohne Gruppe dialout schlägt fehl" pdh-terminal "$cfg_bus" ja nein "$cmd_ok"
expect_busses fehler "spi/uart: console=ttyAMA0 in cmdline.txt schlägt fehl" pdh-terminal "$cfg_bus" ja ja "$cmd_ser"
expect_busses fehler "spi/uart: hmi-kiosk mit den Zeilen in config.txt schlägt fehl" hmi-kiosk "$cfg_bus" nein nein "$cmd_ok"
expect_busses ok "spi/uart: hmi-kiosk ohne die Zeilen besteht" hmi-kiosk 'dtparam=audio=on\n[all]\ndtparam=pciex1\n' nein nein "$cmd_ok"

# Hilfsfunktionen.
if out=$( (load_product nope) 2>&1) ; then bad "unbekanntes Produkt wurde angenommen"; elif echo "$out" | grep -q 'Unbekanntes Produkt'; then ok "unbekanntes Produkt: klare Meldung"; else bad "unbekanntes Produkt: Meldung fehlt ($out)"; fi
if out=$( (load_product '../x') 2>&1); then bad "Pfad in Produktkennung wurde angenommen"; else ok "Pfad in Produktkennung abgelehnt"; fi
if out=$( (load_product hmi-kiosk && require_product_cmd) 2>&1); then ok "hmi-kiosk: cmd/hmi-kiosk vorhanden"; else bad "hmi-kiosk: $out"; fi
if out=$( (load_product hmi-kiosk && PRODUCT_BINARY=gibt-es-nicht && require_product_cmd) 2>&1); then bad "fehlendes cmd wurde nicht gemeldet"; elif echo "$out" | grep -q './cmd/gibt-es-nicht existiert nicht'; then ok "fehlendes cmd: klare Meldung"; else bad "fehlendes cmd: Meldung fehlt ($out)"; fi

cp -r packaging/rootfs "$tmp/h"
cp -r packaging/rootfs "$tmp/p"
(load_product hmi-kiosk && stage_product_rootfs "$tmp/h")
(load_product pdh-terminal && stage_product_rootfs "$tmp/p")
check "hmi-kiosk: rootfs bleibt bis auf etc/hmi-kiosk/product unverändert" bash -c "diff -r packaging/rootfs '$tmp/h' | grep -v '^Only in $tmp/h/etc/hmi-kiosk: product\$' | wc -l | grep -qx 0"
check "hmi-kiosk: product enthält die Kennung" test "$(cat "$tmp/h/etc/hmi-kiosk/product")" = hmi-kiosk
check "pdh-terminal: Overlay überschreibt die Haupteinheit" grep -q '^ExecStart=/data/app/current/pdh-terminal ' "$tmp/p/etc/systemd/system/hmi-kiosk.service"
check "pdh-terminal: product enthält die Kennung" test "$(cat "$tmp/p/etc/hmi-kiosk/product")" = pdh-terminal
check "pdh-terminal: übrige Einheiten unverändert" cmp "$tmp/p/etc/systemd/system/hmi-kiosk-helper.service" packaging/rootfs/etc/systemd/system/hmi-kiosk-helper.service

# Die Skripte nehmen --product und lehnen Unbekanntes früh ab.
for s in build-image.sh "bundle.sh --version 1.0.0"; do
	# shellcheck disable=SC2086  # Skriptname und Option sind gewollt getrennt
	if out=$(VERSION=1.0.0 bash packaging/$s --product nope 2>&1); then bad "$s --product nope: kein Fehler"; elif echo "$out" | grep -q 'Unbekanntes Produkt'; then ok "$s --product nope: Abbruch mit klarer Meldung"; else bad "$s --product nope: $out"; fi
done

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) fehlgeschlagen"
	exit 1
fi
echo "Alle Prüfungen bestanden"
