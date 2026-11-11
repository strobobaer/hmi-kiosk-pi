# Gemeinsame Funktionen für build-image.sh, bundle.sh und die Prüfskripte (wird mit "." eingelesen).
# Erwartet, dass der Aufrufer im Wurzelordner des Repositories steht und die Funktion die() kennt.

# load_product <id>: liest packaging/products/<id>/product.env (PRODUCT_ID, PRODUCT_NAME, PRODUCT_BINARY,
# IMG_NAME, TARGET_HOSTNAME) und prüft sie.
load_product() {
	local id="$1" f k
	[[ "$id" =~ ^[a-z][a-z0-9-]*$ ]] || die "Ungültige Produktkennung „$id“."
	f="packaging/products/$id/product.env"
	[ -f "$f" ] || die "Unbekanntes Produkt „$id“ (es fehlt $f). Bekannt: $(
		cd packaging/products && ls -d -- */ 2>/dev/null | tr -d '/' | paste -sd ' ' -
	)"
	PRODUCT_ID="" PRODUCT_NAME="" PRODUCT_BINARY="" IMG_NAME="" TARGET_HOSTNAME=""
	# shellcheck source=/dev/null
	. "$f"
	for k in PRODUCT_ID PRODUCT_NAME PRODUCT_BINARY IMG_NAME TARGET_HOSTNAME; do
		[ -n "${!k}" ] || die "$f: $k fehlt."
	done
	[ "$PRODUCT_ID" = "$id" ] || die "$f: PRODUCT_ID ist „$PRODUCT_ID“, erwartet „$id“."
	[[ "$PRODUCT_BINARY" =~ ^[a-z][a-z0-9-]*$ ]] || die "$f: PRODUCT_BINARY ungültig."
}

# require_product_cmd: das Programm des Produkts muss als ./cmd/<Programm> im Repository liegen.
require_product_cmd() {
	[ -d "cmd/$PRODUCT_BINARY" ] || die "Das Programm des Produkts „$PRODUCT_ID“ fehlt: ./cmd/$PRODUCT_BINARY existiert nicht. Das Produkt lässt sich erst bauen, wenn es angelegt ist."
}

# stage_product_rootfs <rootfs-Kopie>: legt das Overlay des Produkts über das gemeinsame rootfs
# (packaging/products/<id>/rootfs, falls vorhanden) und schreibt /etc/hmi-kiosk/product.
stage_product_rootfs() {
	local dst="$1"
	if [ -d "packaging/products/$PRODUCT_ID/rootfs" ]; then
		cp -r "packaging/products/$PRODUCT_ID/rootfs/." "$dst/"
	fi
	mkdir -p "$dst/etc/hmi-kiosk"
	printf '%s\n' "$PRODUCT_ID" >"$dst/etc/hmi-kiosk/product"
}
