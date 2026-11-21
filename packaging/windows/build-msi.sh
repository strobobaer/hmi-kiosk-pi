#!/usr/bin/env bash
# Baut den MSI des PDH-Terminals (verwaltete Verteilung: Intune, Gruppenrichtlinie) aus den vorhandenen Programmen.
#
#   packaging/windows/build-msi.sh --version x.y.z
#
# Ergebnis: dist/PDH-Terminal-<version>.msi + .sha256. Erwartet dist/win/pdh-terminal.exe, pdh-terminal-start.exe und
# pdh-terminal-kiosk.exe (von build-setup.sh gebaut, gleiche Version); fehlt eines, bricht das Skript ab.
#
# WiX 3.14 läuft unter Wine im Docker-Abbild aus packaging/windows/msi/Dockerfile (alle Versionen festgelegt; auf dem
# Rechner wird nichts installiert). Der MSI ist nicht signiert. Die ProductCode wird aus der Version abgeleitet (je
# Version fest: für die Erkennung in Intune); die UpgradeCode steht fest in pdh-terminal.wxs.
set -euo pipefail

IMAGE="pdh-msi-wix:wix314"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

die() {
	echo "build-msi.sh: $*" >&2
	exit 1
}

host_path() { echo "$1"; }
case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*)
	host_path() { cygpath -m "$1"; }
	export MSYS_NO_PATHCONV=1
	;;
esac

version=""
while [ $# -gt 0 ]; do
	[ $# -ge 2 ] || die "Option $1 braucht einen Wert."
	case "$1" in
	--version) version="$2" ;;
	*) die "Unbekannte Option $1" ;;
	esac
	shift 2
done
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "--version x.y.z ist nötig (jetzt: \"$version\")."
command -v docker >/dev/null || die "docker fehlt."
for f in pdh-terminal.exe pdh-terminal-start.exe pdh-terminal-kiosk.exe; do
	[ -s "dist/win/$f" ] || die "dist/win/$f fehlt: zuerst packaging/windows/build-setup.sh --version $version."
done

# ProductCode: UUID (Version 5 ähnlich) aus dem SHA-256 von "PDH-Terminal <version>", je Version fest.
h="$(printf 'PDH-Terminal MSI %s' "$version" | sha256sum | cut -c1-32)"
variant="$(printf '%x' $(((0x${h:16:1} & 0x3) | 0x8)))"
pc="$(echo "${h:0:8}-${h:8:4}-5${h:13:3}-${variant}${h:17:3}-${h:20:12}" | tr a-f A-F)"

msi="dist/PDH-Terminal-$version.msi"
mkdir -p dist
rm -f "$msi" "$msi.sha256"

echo "== Abbild $IMAGE"
docker build -q -t "$IMAGE" packaging/windows/msi >/dev/null || die "Das Abbild ließ sich nicht bauen."

echo "== MSI (WiX unter Wine, Version $version, ProductCode {$pc})"
docker run --rm -v "$(host_path "$root"):/work" "$IMAGE" "$version" "$pc"
[ -s "$msi" ] || die "Der MSI wurde nicht gebaut ($msi fehlt)."

(cd dist && sha256sum "$(basename "$msi")" >"$(basename "$msi").sha256")
echo "== Fertig"
echo "ProductCode: {$pc}"
echo "UpgradeCode: {4491D06A-73CE-400E-A1F7-AE1A4539DB19}"
ls -l "$msi" "$msi.sha256"
