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

# Hilfsfunktionen.
if out=$( (load_product nope) 2>&1) ; then bad "unbekanntes Produkt wurde angenommen"; elif echo "$out" | grep -q 'Unbekanntes Produkt'; then ok "unbekanntes Produkt: klare Meldung"; else bad "unbekanntes Produkt: Meldung fehlt ($out)"; fi
if out=$( (load_product '../x') 2>&1); then bad "Pfad in Produktkennung wurde angenommen"; else ok "Pfad in Produktkennung abgelehnt"; fi
if out=$( (load_product hmi-kiosk && require_product_cmd) 2>&1); then ok "hmi-kiosk: cmd/hmi-kiosk vorhanden"; else bad "hmi-kiosk: $out"; fi
if out=$( (load_product hmi-kiosk && PRODUCT_BINARY=gibt-es-nicht && require_product_cmd) 2>&1); then bad "fehlendes cmd wurde nicht gemeldet"; elif echo "$out" | grep -q './cmd/gibt-es-nicht existiert nicht'; then ok "fehlendes cmd: klare Meldung"; else bad "fehlendes cmd: Meldung fehlt ($out)"; fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
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
