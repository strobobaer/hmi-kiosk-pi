#!/usr/bin/env bash
# Baut das Offline-Bundle für install.sh (andere Linux-Systeme als das SD-Abbild).
#
#   packaging/bundle.sh --version 1.0.0 [--arch arm64|amd64] [--bin-dir ORDNER] [--out ORDNER]
#
# Ergebnis: <out>/hmi-kiosk-bundle-<version>-trixie-<arch>.tar.gz (und .sha256), dazu der entpackte Ordner
# <out>/bundle-trixie-<arch>/. Das Bundle enthält install.sh, ein Manifest, die Programme dieser Architektur,
# die Dienstdateien und ein lokales Paketverzeichnis (repo/) mit allen .deb-Paketen samt Abhängigkeiten.
#
# Die Pakete werden in einem Container debian:trixie derselben Architektur geladen (Docker, Internet nötig).
# Liste: packaging/image/stage-hmi/00-packages/00-packages-nr ohne die Punkte, die nur das Abbild braucht
# (raspi-config, overlayroot), dazu libpam-systemd und die Werkzeuge der dateiweisen SD-Sicherung
# (rsync, dosfstools, fdisk mit sfdisk, e2fsprogs, util-linux mit losetup). Die Abhängigkeiten werden gegen ein frisches
# debian:trixie aufgelöst: Alles, was dort schon installiert ist, setzt install.sh auf dem Zielsystem voraus.
#
# Programme: aus --bin-dir (muss hmi-kiosk und hmi-kiosk-helper enthalten) oder, ohne Angabe, frisch gebaut
# mit scripts/dgo; dafür ist der öffentliche Release-Schlüssel nötig (HMI_RELEASE_PUB oder .secrets/release.pub).
set -euo pipefail

CODENAME=trixie
IMAGE=debian:trixie
IMAGE_ONLY_PACKAGES="raspi-config overlayroot"
# rsync, dosfstools, fdisk, e2fsprogs und util-linux braucht die dateiweise SD-Sicherung samt Wachstum der Wurzel
# (im SD-Abbild geht dieser Weg nicht; sie schaden dort nicht).
EXTRA_PACKAGES="libpam-systemd rsync dosfstools fdisk e2fsprogs util-linux"

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

die() {
	echo "bundle.sh: $*" >&2
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
arch=arm64
bindir=""
out=dist
while [ $# -gt 0 ]; do
	[ $# -ge 2 ] || die "Option $1 braucht einen Wert."
	case "$1" in
	--version) version="$2" ;;
	--arch) arch="$2" ;;
	--bin-dir) bindir="$2" ;;
	--out) out="$2" ;;
	*) die "Unbekannte Option $1" ;;
	esac
	shift 2
done

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "--version x.y.z ist nötig (jetzt: \"$version\")."
case "$arch" in
arm64 | amd64) ;;
*) die "--arch muss arm64 oder amd64 sein." ;;
esac
command -v docker >/dev/null || die "docker fehlt."

if [ -z "$bindir" ]; then
	pub="${HMI_RELEASE_PUB:-.secrets/release.pub}"
	[ -s "$pub" ] || die "Öffentlicher Release-Schlüssel fehlt: $pub. Einmalig erzeugen mit: scripts/dgo run ./cmd/hmi-kiosk-pack keygen -out .secrets"
	key="$(tr -d '\r\n' <"$pub")"
	[[ "$key" =~ ^[0-9a-fA-F]{64}$ ]] || die "$pub enthält keinen Ed25519-Schlüssel (64 Hex-Zeichen)."
	bindir="$out/bundle-bin-$arch"
	echo "== Programme ($arch, Version $version)"
	rm -rf "${bindir:?}"
	mkdir -p "$bindir"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" scripts/dgo build -trimpath \
		-ldflags "-X hmikiosk/internal/release.ReleasePublicKeyHex=$key -X hmikiosk/internal/release.Version=$version" \
		-o "$bindir/" ./cmd/hmi-kiosk ./cmd/hmi-kiosk-helper
fi
[ -s "$bindir/hmi-kiosk" ] && [ -s "$bindir/hmi-kiosk-helper" ] || die "In $bindir fehlen hmi-kiosk und hmi-kiosk-helper."

# Paketliste ohne Kommentare und ohne Abbild-Punkte.
pkgs=""
while IFS= read -r line; do
	line="${line%%#*}"
	for p in $line; do
		case " $IMAGE_ONLY_PACKAGES " in
		*" $p "*) ;;
		*) pkgs="$pkgs $p" ;;
		esac
	done
done <packaging/image/stage-hmi/00-packages/00-packages-nr
pkgs="${pkgs# } $EXTRA_PACKAGES"

bundle="$out/bundle-$CODENAME-$arch"
rm -rf "${bundle:?}"
mkdir -p "$bundle/repo" "$bundle/bin"

echo "== Pakete laden ($IMAGE, linux/$arch)"
docker run --rm --platform "linux/$arch" -e "PKGS=$pkgs" -e "ARCH=$arch" \
	-v "$(host_path "$root/$bundle/repo"):/out" "$IMAGE" bash -eu -c '
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
resolved=""
for p in $PKGS; do
	if [ "$p" = chromium ] && ! apt-cache show chromium >/dev/null 2>&1; then
		p=chromium-browser
	fi
	resolved="$resolved $p"
done
resolved="${resolved# }"
echo "$resolved" >/out/.packages
# Leerer Zwischenspeicher: geladen wird alles, was dem frischen Container fehlt.
apt-get -o Dir::Cache::archives=/out -o APT::Keep-Downloaded-Packages=true \
	install --download-only -y --no-install-recommends $resolved
rm -rf /out/partial /out/lock
apt-get install -y -qq --no-install-recommends dpkg-dev apt-utils >/dev/null
cd /out
dpkg-scanpackages -m . >Packages 2>/dev/null
gzip -kf Packages
apt-ftparchive release . >Release
for f in *.deb; do dpkg-deb -f "$f" Package Version Architecture | paste -sd " " ; done | sort >/out/.pakete.txt
'
[ -s "$bundle/repo/Packages" ] || die "Das Paketverzeichnis wurde nicht erzeugt."
resolved="$(cat "$bundle/repo/.packages")"
mv "$bundle/repo/.pakete.txt" "$bundle/pakete.txt"
rm -f "$bundle/repo/.packages"

cp "$bindir/hmi-kiosk" "$bindir/hmi-kiosk-helper" "$bundle/bin/"
cp -r packaging/rootfs "$bundle/rootfs"
# Dieselbe Netzwerk-Kennung wie im SD-Abbild (packaging/image/identity.sh).
install -D -m 755 packaging/image/identity.sh "$bundle/rootfs/usr/local/sbin/hmi-kiosk-identity"
cp packaging/install.sh "$bundle/install.sh"
chmod 755 "$bundle/install.sh" "$bundle/bin/"* "$bundle/rootfs/usr/local/bin/hmi-kiosk-session" "$bundle/rootfs/usr/local/sbin/hmi-kiosk-grow-root"
printf 'version=%s\ncodename=%s\narch=%s\npackages=%s\n' "$version" "$CODENAME" "$arch" "$resolved" >"$bundle/manifest"
(cd "$bundle" && find . -type f ! -name SHA256SUMS -print0 | sort -z | xargs -0 sha256sum --text >SHA256SUMS)

tarball="$out/hmi-kiosk-bundle-$version-$CODENAME-$arch.tar.gz"
tar -C "$bundle" --transform "s|^\./|hmi-kiosk-bundle-$version-$CODENAME-$arch/|" -czf "$tarball" .
(cd "$out" && sha256sum "$(basename "$tarball")" >"$(basename "$tarball").sha256")
echo "== Fertig: $tarball ($(du -h "$tarball" | cut -f1)), $(wc -l <"$bundle/pakete.txt") Pakete"
