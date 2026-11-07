#!/usr/bin/env bash
# Echter Lauf der dateiweisen SD-Sicherung mit Loop-Geräten (privilegierter Container, Debian Bookworm mit Go).
#
#   bash test/backup-files/run.sh
#
# Baut aus einem nachgebauten Quellsystem (Loop-"Karte" mit FAT und ext4, Pi-OS-artige cmdline.txt und fstab mit
# PARTUUID) ein Abbild mit dem Helfer-Code und prüft es: zwei Partitionen, neue Disk-ID, Verweise angepasst,
# Dateien, Besitzer, Rechte, feste Verweise und Attribute erhalten, Ausschlüsse fehlen, gzip lesbar, Abbruch
# und Absturzreste werden aufgeräumt. Braucht Docker (privilegiert) und Netz für apt.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*)
	root="$(cygpath -m "$root")"
	export MSYS_NO_PATHCONV=1
	;;
esac
exec docker run --rm --privileged -e HMI_LOOP_TEST=1 \
	-v "$root:/src" -w /src \
	-v hmi-kiosk-gomod:/go/pkg/mod -v hmi-kiosk-gobuild:/root/.cache/go-build \
	golang:1.24-bookworm bash -eu -c '
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends rsync dosfstools fdisk e2fsprogs util-linux attr >/dev/null
mount -t devtmpfs devtmpfs /dev 2>/dev/null || true
go test -count=1 -v -run "TestFilesLoop" ./internal/helper/
'
