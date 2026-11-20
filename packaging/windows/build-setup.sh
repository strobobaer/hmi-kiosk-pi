#!/usr/bin/env bash
# Baut das PDH-Terminal für Windows 11: beide Programme, das Setup und das signierte Update-Paket.
#
#   packaging/windows/build-setup.sh --version x.y.z
#
# Ergebnis in dist/:
#   PDH-Terminal-Setup-<version>.exe                  Setup (Inno Setup)           + .sha256
#   pdh-terminal-<version>-windows-amd64.pkg          signiertes Paket für Updates + .sha256
#   win/pdh-terminal.exe, win/pdh-terminal-start.exe  die gebauten Programme
#
# Programm und Starter werden mit derselben Version und demselben öffentlichen Schlüssel gebaut wie beim
# Linux-Release (Makefile-Ziel "release"): Der Starter nimmt seine Basisversion aus seiner eigenen Version.
#
# Nötig: Docker, ein öffentlicher Schlüssel (HMI_RELEASE_PUB, Standard .secrets/release.pub) und der private
# Schlüssel in HMI_RELEASE_KEY_DIR (Standard .secrets, darin release.key). Der Ordner wird nur lesend in den
# Container eingebunden (-v ...:/keys:ro); der Schlüssel wird nie kopiert.
#
# Inno Setup läuft im Docker-Abbild amake/innosetup, festgelegt über den Digest (Index, linux/amd64 und arm64):
#   amake/innosetup@sha256:e003376ba818547275fe10c95e2a29be0f2d12d45e9eb8f205b6672dc5685bb1
# (Stand 2026-11-20, enthält Inno Setup 6 unter Wine). Das Setup ist nicht Authenticode-signiert; Windows
# SmartScreen zeigt deshalb „Unbekannter Herausgeber“.
set -euo pipefail

INNO_IMAGE="amake/innosetup@sha256:e003376ba818547275fe10c95e2a29be0f2d12d45e9eb8f205b6672dc5685bb1"
GO_IMAGE="golang:1.24-bookworm"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$root"

die() {
	echo "build-setup.sh: $*" >&2
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
# Nur x.y.z: Setup-Dateiname, Tag und Programmversion müssen übereinstimmen (keine Zusätze wie -test).
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "--version x.y.z ist nötig (jetzt: \"$version\")."
command -v docker >/dev/null || die "docker fehlt."

pub="${HMI_RELEASE_PUB:-.secrets/release.pub}"
packaging/check-release-pub.sh "$pub" || exit 1
keydir="${HMI_RELEASE_KEY_DIR:-.secrets}"
[ -s "$keydir/release.key" ] || die "Privater Release-Schlüssel fehlt: $keydir/release.key (Ordner über HMI_RELEASE_KEY_DIR)."
keydir_abs="$(cd "$keydir" && pwd)"

win=dist/win
pkg="dist/pdh-terminal-$version-windows-amd64.pkg"
setup="dist/PDH-Terminal-Setup-$version.exe"
mkdir -p dist
rm -rf "${win:?}"
rm -f "$pkg" "$pkg.sha256" "$setup" "$setup.sha256"

echo "== Programme (windows/amd64, Version $version)"
ldflags="-X hmikiosk/internal/release.ReleasePublicKeyHex=$(tr -d '\r\n' <"$pub") -X hmikiosk/internal/release.Version=$version"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 scripts/dgo build -ldflags "$ldflags" -o "$win/" ./cmd/pdh-terminal ./cmd/pdh-terminal-start
[ -s "$win/pdh-terminal.exe" ] && [ -s "$win/pdh-terminal-start.exe" ] || die "Die Programme wurden nicht gebaut."

echo "== Setup (Inno Setup)"
docker run --rm -v "$(host_path "$root"):/work" "$INNO_IMAGE" "/DAppVersion=$version" packaging/windows/pdh-terminal.iss
[ -s "$setup" ] || die "Das Setup wurde nicht gebaut ($setup fehlt)."

echo "== Update-Paket (signiert)"
# Wie scripts/dgo, dazu der Schlüsselordner nur lesend unter /keys.
docker run --rm -e CGO_ENABLED=0 \
	-v "$(host_path "$root"):/src" -w /src \
	-v "$(host_path "$keydir_abs"):/keys:ro" \
	-v hmi-kiosk-gomod:/go/pkg/mod -v hmi-kiosk-gobuild:/root/.cache/go-build \
	"$GO_IMAGE" go run ./cmd/hmi-kiosk-pack pack -product pdh-terminal -platform windows-amd64 \
	-key /keys/release.key -version "$version" -in "$win" -out "$pkg"
[ -s "$pkg" ] || die "Das Paket wurde nicht erzeugt ($pkg fehlt)."

for f in "$setup" "$pkg"; do
	(cd dist && sha256sum "$(basename "$f")" >"$(basename "$f").sha256")
done

echo "== Fertig"
ls -l "$setup" "$setup.sha256" "$pkg" "$pkg.sha256"
