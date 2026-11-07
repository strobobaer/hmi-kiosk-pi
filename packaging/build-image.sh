#!/usr/bin/env bash
# Baut das SD-Abbild der HMI-Terminals mit dem offiziellen pi-gen in Docker und prüft es.
#
#   VERSION=1.0.0 packaging/build-image.sh
#
# Ergebnis in dist/: hmi-kiosk-<version>.img.xz, hmi-kiosk-<version>-pakete.txt (Paketversionen),
# hmi-kiosk-<version>.img.xz.sha256 und das Protokoll hmi-kiosk-<version>-build.log.
#
# Umgebung:
#   VERSION          Programmversion x.y.z; sonst der Wert aus dem Makefile
#   HMI_RELEASE_PUB  öffentlicher Release-Schlüssel (Standard .secrets/release.pub)
#   HMI_ENABLE_SSH   1: SSH an, nur mit Schlüssel aus HMI_SSH_PUBKEY (Standard 0: SSH aus)
#   HMI_SSH_PUBKEY   öffentlicher SSH-Schlüssel für den Benutzer hmiadmin, Pflicht bei HMI_ENABLE_SSH=1
# Braucht Docker mit privilegierten Containern und arm64-Emulation (binfmt), dazu git und Internet.
set -euo pipefail

# Fester Stand von pi-gen: Tag 2026-10-06-raspios-trixie-arm64 (Zweig arm64), Debian Trixie arm64.
PI_GEN_REPO=https://github.com/RPi-Distro/pi-gen.git
PI_GEN_COMMIT=b2cd98a9ee08a31ef7d0110ed09f2492bbd972ec
CONTAINER_NAME=hmi-kiosk-pigen

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

die() {
	echo "build-image.sh: $*" >&2
	exit 1
}

# Unter Git Bash (Windows) braucht docker.exe Windows-Pfade und keine MSYS-Umschreibung.
host_path() { echo "$1"; }
case "$(uname -s)" in
MINGW* | MSYS* | CYGWIN*)
	host_path() { cygpath -m "$1"; }
	export MSYS_NO_PATHCONV=1
	;;
esac

VERSION="${VERSION:-$(sed -n 's/^VERSION[[:space:]]*?=[[:space:]]*//p' Makefile | head -n 1)}"
if ! [[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
	die "Für ein Abbild ist eine Release-Version x.y.z nötig (jetzt: \"$VERSION\"). Beispiel: VERSION=1.0.0 packaging/build-image.sh"
fi

pub="${HMI_RELEASE_PUB:-.secrets/release.pub}"
"$(dirname "$0")/check-release-pub.sh" "$pub" || exit 1
key="$(cat "$pub")"

ssh="${HMI_ENABLE_SSH:-0}"
case "$ssh" in
0) ;;
1) [ -n "${HMI_SSH_PUBKEY:-}" ] || die "HMI_ENABLE_SSH=1 braucht HMI_SSH_PUBKEY (Anmeldung nur per Schlüssel)" ;;
*) die "HMI_ENABLE_SSH muss 0 oder 1 sein" ;;
esac

command -v docker >/dev/null || die "docker fehlt"
command -v git >/dev/null || die "git fehlt"

mkdir -p dist
work="dist/image-work"
log="dist/hmi-kiosk-$VERSION-build.log"
mkdir -p "$work"
exec > >(tee "$log") 2>&1
echo "Baue hmi-kiosk-$VERSION ($(date '+%Y-%m-%d %H:%M:%S')), pi-gen $PI_GEN_COMMIT, SSH=$ssh"

echo "== Programme (arm64, Version $VERSION)"
rm -rf "${work:?}/bin"
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 scripts/dgo build -trimpath \
	-ldflags "-X hmikiosk/internal/release.ReleasePublicKeyHex=$key -X hmikiosk/internal/release.Version=$VERSION" \
	-o "$work/bin/" ./cmd/hmi-kiosk ./cmd/hmi-kiosk-helper
[ -s "$work/bin/hmi-kiosk" ] && [ -s "$work/bin/hmi-kiosk-helper" ] || die "Programme nicht gebaut"

echo "== pi-gen $PI_GEN_COMMIT"
pg="$work/pi-gen"
if [ ! -d "$pg/.git" ]; then
	git -c core.autocrlf=false clone --quiet "$PI_GEN_REPO" "$pg"
fi
git -C "$pg" -c core.autocrlf=false fetch --quiet origin
git -C "$pg" -c core.autocrlf=false checkout --quiet --force "$PI_GEN_COMMIT"
git -C "$pg" clean -fdxq
[ "$(git -C "$pg" rev-parse HEAD)" = "$PI_GEN_COMMIT" ] || die "pi-gen steht nicht auf $PI_GEN_COMMIT"
if grep -lq $'\r' "$pg/build.sh"; then die "pi-gen mit CRLF ausgecheckt"; fi

# Stufe stage2 erzeugt kein eigenes Abbild, cloud-init entfällt (würde die Wurzel vergrößern).
touch "$pg/stage2/SKIP_IMAGES" "$pg/stage2/04-cloud-init/SKIP"
cp -r packaging/image/stage-hmi "$pg/stage-hmi"
files="$pg/stage-hmi/01-hmi/files"
mkdir -p "$files/bin"
cp -r packaging/rootfs "$files/rootfs"
cp packaging/image/firstboot.sh "$files/firstboot.sh"
cp packaging/image/identity.sh "$files/identity.sh"
cp "$work/bin/hmi-kiosk" "$work/bin/hmi-kiosk-helper" "$files/bin/"
printf 'HMI_VERSION=%s\nHMI_ENABLE_SSH=%s\n' "$VERSION" "$ssh" >"$files/build.env"
# Unter Windows fehlen Ausführungsrechte; docker build übernimmt sie von hier.
chmod +x "$pg/stage-hmi/prerun.sh" "$pg/stage-hmi/01-hmi/00-run.sh"

# Konfiguration dieses Baus. FIRST_USER_PASS ist ein verworfener Zufallswert: pi-gen verlangt ihn
# ohne Umbenennungs-Assistent, stage-hmi sperrt das Konto danach.
cfg="$pg/config"
trap 'rm -f "$cfg"' EXIT
{
	cat packaging/image/config
	echo "IMG_FILENAME=hmi-kiosk-$VERSION"
	echo "FIRST_USER_PASS='$(head -c 48 /dev/urandom | base64 | tr -dc 'A-Za-z0-9')'"
	echo "ENABLE_SSH=$ssh"
	if [ "$ssh" = 1 ]; then
		echo "PUBKEY_ONLY_SSH=1"
		# %q: die Konfiguration wird von bash eingelesen, der Schlüssel darf sie nicht aufbrechen.
		printf 'PUBKEY_SSH_FIRST_USER=%q\n' "$HMI_SSH_PUBKEY"
	fi
} >"$cfg"

# build-docker.sh ruft "${DOCKER}" auf; unter Windows übersetzt der Wrapper Pfade für docker.exe.
docker_cmd=docker
if [ "$(host_path /)" != / ]; then
	docker_cmd="$root/$work/docker-host-paths"
	cat >"$docker_cmd" <<'EOF'
#!/usr/bin/env bash
# Übersetzt Host-Pfade (auch vor ":" in -v/--volume) für docker.exe unter Git Bash.
args=()
for a in "$@"; do
	p="${a%%:*}"
	if [[ "$a" == /* ]] && [ -e "$p" ]; then
		a="$(cygpath -m "$p")${a#"$p"}"
	fi
	args+=("$a")
done
MSYS_NO_PATHCONV=1 exec docker "${args[@]}"
EOF
	chmod +x "$docker_cmd"
fi

if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
	echo "Entferne alten Baucontainer $CONTAINER_NAME"
	docker rm -v -f "$CONTAINER_NAME" >/dev/null
fi
rm -rf "$pg/deploy"

echo "== pi-gen-Bau (dauert unter Emulation lange)"
(cd "$pg" && DOCKER="$docker_cmd" CONTAINER_NAME="$CONTAINER_NAME" GIT_HASH="$PI_GEN_COMMIT" ./build-docker.sh -c config)
rm -f "$cfg"

img="$pg/deploy/hmi-kiosk-$VERSION.img"
[ -f "$img" ] || die "Abbild fehlt nach dem Bau: $img (siehe $log)"
mv -f "$img" "dist/hmi-kiosk-$VERSION.img"

echo "== Prüfung des Abbilds"
docker build -q -t hmi-kiosk-verify -f "$(host_path "$root/packaging/image/verify.Dockerfile")" "$(host_path "$root/packaging/image")" >/dev/null
# Kopie im Container: Loop-Geräte auf Dateien eines Windows-Bind-Mounts sind nicht verlässlich.
docker run --rm --privileged -e HMI_ENABLE_SSH="$ssh" -v "$(host_path "$root"):/src" -w /src hmi-kiosk-verify \
	sh -c "cp dist/hmi-kiosk-$VERSION.img /tmp/abbild.img && sh packaging/image/verify-image.sh \
		--version $VERSION --pakete dist/hmi-kiosk-$VERSION-pakete.txt /tmp/abbild.img"

echo "== Komprimieren"
docker run --rm -v "$(host_path "$root/dist"):/dist" -w /dist hmi-kiosk-verify \
	sh -c "xz -T0 -6 -f hmi-kiosk-$VERSION.img && sha256sum hmi-kiosk-$VERSION.img.xz >hmi-kiosk-$VERSION.img.xz.sha256"

echo "Fertig: dist/hmi-kiosk-$VERSION.img.xz, dist/hmi-kiosk-$VERSION-pakete.txt"
echo "Nicht auf Hardware geprüft: der erste echte Start ist Teil der Inbetriebnahme (Spec Abschnitt 13)."
