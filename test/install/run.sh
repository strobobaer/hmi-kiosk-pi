#!/usr/bin/env bash
# Prüft install.sh in einem frischen debian:trixie-Container ohne Netz.
#
#   bash test/install/run.sh [--product hmi-kiosk|pdh-terminal]
#
# Baut (oder nimmt aus dist/install-test) ein amd64-Bundle mit Version 0.0.1 und startet check.sh im Container.
# HMI_TEST_REUSE=1 verwendet ein vorhandenes Bundle weiter. Braucht Docker und beim Bundle-Bau Internet.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

die() {
	echo "run.sh: $*" >&2
	exit 1
}
. packaging/product-lib.sh

PRODUCT=hmi-kiosk
while [ $# -gt 0 ]; do
	case "$1" in
	--product)
		[ $# -ge 2 ] || die "Option --product braucht einen Wert."
		PRODUCT="$2"
		shift 2
		;;
	*) die "Unbekannte Option $1" ;;
	esac
done
load_product "$PRODUCT"
require_product_cmd

host_path() { echo "$1"; }
case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*)
	host_path() { cygpath -m "$1"; }
	export MSYS_NO_PATHCONV=1
	;;
esac

out=dist/install-test
[ "$PRODUCT_ID" = hmi-kiosk ] || out="dist/install-test-$PRODUCT_ID"
bundle="$out/bundle-trixie-amd64"
if [ "${HMI_TEST_REUSE:-0}" != 1 ] || [ ! -f "$bundle/manifest" ]; then
	# Der Schlüssel ist nur ein Platzhalter; das Testprogramm wird nie ausgeliefert.
	mkdir -p "$out"
	printf '%064d\n' 0 >"$out/dummy.pub"
	HMI_RELEASE_PUB="$out/dummy.pub" packaging/bundle.sh --product "$PRODUCT_ID" --version 0.0.1 --arch amd64 --out "$out"
fi

docker build -q -t hmi-install-test -f test/install/Dockerfile test/install >/dev/null
docker run --rm --network none -e "HMI_TEST_PRODUCT=$PRODUCT_ID" -e "HMI_TEST_BINARY=$PRODUCT_BINARY" \
	-v "$(host_path "$root/$bundle"):/bundle:ro" \
	-v "$(host_path "$root/test/install/check.sh"):/check.sh:ro" \
	hmi-install-test bash /check.sh
