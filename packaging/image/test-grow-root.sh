#!/bin/sh
# Test von hmi-kiosk-grow-root (packaging/rootfs/usr/local/sbin/hmi-kiosk-grow-root): ein dateiweise gesichertes
# Abbild (kleine Partition 2 als Wurzel) auf einer größeren Karte (Loop-Gerät). Läuft im privilegierten
# Container mit Debian Trixie auf dem Host-Prozessor (amd64, aus demselben Grund wie test-grow-data.sh).
#
#   docker run --rm --privileged -v "$PWD:/src" -w /src debian:trixie sh packaging/image/test-grow-root.sh
#
# Prüft: Partition 2 und Dateisystem wachsen online (eingehängt) bis ans Ende, Daten bleiben, zweiter Lauf
# ändert nichts, 10 MiB Rest bleiben, auf dem SD-Abbild (image-release) und bei einer weiteren Partition
# dahinter geschieht nichts, ein gescheitertes resize2fs oder sfdisk wird nur protokolliert (Exit 0) und der
# nächste Lauf holt es nach.
set -eu

GR=$(pwd)/packaging/rootfs/usr/local/sbin/hmi-kiosk-grow-root
fails=0
ok() { echo "OK $1"; }
bad() {
	echo "FEHLER $1"
	fails=$((fails + 1))
}

apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends fdisk e2fsprogs util-linux dosfstools >/dev/null
mount -t devtmpfs devtmpfs /dev # echte Geräteknoten für neue Partitionen

work=$(mktemp -d)
loop=""
cleanup() {
	umount "$work/root" 2>/dev/null || true
	[ -z "$loop" ] || losetup -d "$loop" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT
mkdir -p "$work/bin" "$work/root"
PATH="$work/bin:$PATH"
export HMI_GROW_ROOT_MOUNT="$work/root"
export HMI_GROW_ROOT_RELEASE="$work/kein-image-release"
export HMI_GROW_ROOT_DISKS="loop*" # sonst nur mmcblk*, nvme*n* und sd*

img=$work/karte.img
truncate -s 600M "$img"
printf 'label: dos\nstart=2048, size=65536, type=c\nstart=67584, size=131072, type=83\n' | sfdisk -q "$img"
loop=$(losetup -f --show -P "$img")
sleep 1
mkfs.vfat -F 32 "${loop}p1" >/dev/null
mkfs.ext4 -q -L rootfs -b 4096 -E resize=134217728 "${loop}p2"
mount "${loop}p2" "$work/root"
head -c 20971520 /dev/urandom >"$work/root/gross"
md5sum "$work/root/gross" >"$work/gross.md5"
echo klein >"$work/root/klein"
chown 1100:1100 "$work/root/klein"
chmod 600 "$work/root/klein"

p2sys() { cat "/sys/class/block/$(basename "${loop}p2")/size"; }
table() { sfdisk --dump "$loop" | grep '^/dev/'; }
fsbytes() { echo $(($(dumpe2fs -h "${loop}p2" 2>/dev/null | sed -n 's/^Block count: *//p') * 4096)); }
devsectors() { blockdev --getsz "$loop"; }
data_ok() {
	(cd "$work/root" && md5sum -c "$work/gross.md5" >/dev/null 2>&1) && [ "$(cat "$work/root/klein")" = klein ] &&
		[ "$(stat -c '%u:%g %a' "$work/root/klein")" = "1100:1100 600" ]
}
grow() { sh "$GR" >"$work/grow.log" 2>&1; }

echo "== SD-Abbild (image-release vorhanden): nichts tun"
touch "$work/image-release"
before=$(table)
HMI_GROW_ROOT_RELEASE="$work/image-release" sh "$GR" >"$work/grow.log" 2>&1
if [ "$(table)" = "$before" ] && grep -q 'SD-Abbild' "$work/grow.log"; then
	ok "SD-Abbild: Tabelle unverändert, Exit 0"
else
	bad "SD-Abbild: $(cat "$work/grow.log")"
fi

echo "== Gerät nicht in der Liste (Standard: mmcblk, nvme, sd): nichts tun"
before=$(table)
if env -u HMI_GROW_ROOT_DISKS sh "$GR" >"$work/grow.log" 2>&1 && [ "$(table)" = "$before" ] && grep -q 'nicht unterstützt' "$work/grow.log" && grep -qF '(nur mmcblk* nvme*n* sd*)' "$work/grow.log"; then
	ok "loop-Gerät mit Standardliste: Tabelle unverändert, Exit 0"
else
	bad "Standardliste: $(cat "$work/grow.log")"
fi

echo "== Wurzel ohne Partition (tmpfs): nichts tun"
mkdir "$work/tmpfs"
mount -t tmpfs tmpfs "$work/tmpfs"
if HMI_GROW_ROOT_MOUNT="$work/tmpfs" sh "$GR" >"$work/grow.log" 2>&1 && [ "$(table)" = "$before" ] && grep -q 'keiner Partition' "$work/grow.log"; then
	ok "tmpfs: keine Änderung, Exit 0"
else
	bad "tmpfs: $(cat "$work/grow.log")"
fi
umount "$work/tmpfs"

echo "== Karte mit 600 MB, Partition 2 klein"
size0=$(p2sys)
fs0=$(fsbytes)
if grow; then
	ok "grow-root endet mit Exit 0"
else
	bad "grow-root: Exit ungleich 0: $(cat "$work/grow.log")"
fi
total=$(devsectors)
p2line=$(table | sed -n 's|^/dev/[a-z0-9]*2 : start= *\([0-9]*\), size= *\([0-9]*\).*|\1 \2|p')
p2start=${p2line% *}
p2size=${p2line#* }
if [ $((p2start + p2size)) -ge $((total - 2048)) ] && [ "$(p2sys)" = "$p2size" ] && [ "$p2size" -gt "$size0" ]; then
	ok "Partition 2 reicht jetzt bis zum Ende ($p2start+$p2size von $total Sektoren), Kernel kennt die Größe"
else
	bad "Partition 2: $p2line von $total, Kernel $(p2sys)"
fi
if [ "$(fsbytes)" -gt "$fs0" ] && [ $(($(fsbytes) + 8 * 1048576)) -ge $((p2size * 512)) ]; then
	ok "Dateisystem wächst online auf die Partition ($(fsbytes) Byte)"
else
	bad "Dateisystem: $(fsbytes) Byte, Partition $((p2size * 512))"
fi
if data_ok && mountpoint -q "$work/root"; then
	ok "Daten, Besitzer und Rechte bleiben, die Wurzel bleibt eingehängt"
else
	bad "Daten nach dem Vergrößern verändert"
fi
if [ "$(blkid -p -o value -s LABEL "${loop}p2")" = rootfs ] && [ "$(blkid -p -o value -s TYPE "${loop}p2")" = ext4 ]; then
	ok "ext4 mit Label rootfs bleibt"
else
	bad "Label oder Typ geändert"
fi
if [ "$(blkid -p -o value -s TYPE "${loop}p1")" = vfat ]; then
	ok "Partition 1 bleibt FAT"
fi

echo "== zweiter Lauf"
after=$(table)
fs1=$(fsbytes)
if grow && [ "$(table)" = "$after" ] && [ "$(fsbytes)" = "$fs1" ] && grep -q 'reicht schon bis zum Ende' "$work/grow.log"; then
	ok "zweiter Lauf ändert nichts"
else
	bad "zweiter Lauf: $(cat "$work/grow.log")"
fi

echo "== nur 10 MiB frei dahinter"
truncate -s 610M "$img"
losetup -c "$loop"
after=$(table)
if grow && [ "$(table)" = "$after" ] && [ "$(devsectors)" -gt "$((p2start + p2size + 16384))" ] && grep -q 'reicht schon bis zum Ende' "$work/grow.log"; then
	ok "10 MiB frei dahinter: Partition bleibt"
else
	bad "10 MiB frei: $(cat "$work/grow.log")"
fi

echo "== resize2fs scheitert"
truncate -s 710M "$img"
losetup -c "$loop"
fs2=$(fsbytes)
printf '#!/bin/sh\nexit 1\n' >"$work/bin/resize2fs"
chmod 755 "$work/bin/resize2fs"
if grow && grep -q 'FEHLER' "$work/grow.log" && [ "$(p2sys)" -gt "$p2size" ] && [ "$(fsbytes)" = "$fs2" ]; then
	ok "Exit 0, FEHLER im Protokoll, Partition vergrößert, Dateisystem noch nicht"
else
	bad "resize2fs-Fehler: $(cat "$work/grow.log")"
fi
rm -f "$work/bin/resize2fs"
if grow && [ "$(fsbytes)" -gt "$fs2" ] && [ $(($(fsbytes) + 8 * 1048576)) -ge $(($(p2sys) * 512)) ] && data_ok; then
	ok "nächster Lauf vergrößert das Dateisystem nach, Daten unverändert"
else
	bad "Nachholen: $(fsbytes) Byte, Partition $(($(p2sys) * 512))"
fi

echo "== sfdisk scheitert"
truncate -s 800M "$img"
losetup -c "$loop"
printf '#!/bin/sh\nexit 1\n' >"$work/bin/sfdisk"
chmod 755 "$work/bin/sfdisk"
before=$(table)
if grow && grep -q 'FEHLER' "$work/grow.log" && [ "$(table)" = "$before" ]; then
	ok "Exit 0, FEHLER im Protokoll, Tabelle unverändert"
else
	bad "sfdisk-Fehler: $(cat "$work/grow.log")"
fi
rm -f "$work/bin/sfdisk"
if grow && data_ok; then
	ok "danach wächst die Karte, Daten unverändert"
else
	bad "Wachstum nach dem sfdisk-Fehler: $(cat "$work/grow.log")"
fi

echo "== weitere Partition hinter der Wurzel: nichts anfassen"
umount "$work/root"
losetup -d "$loop"
printf 'label: dos\nstart=2048, size=65536, type=c\nstart=67584, size=131072, type=83\nstart=198656, size=131072, type=83\n' | sfdisk -q "$img"
loop=$(losetup -f --show -P "$img")
sleep 1
mkfs.ext4 -q -L rootfs -b 4096 "${loop}p2"
mount "${loop}p2" "$work/root"
before=$(table)
if grow && [ "$(table)" = "$before" ] && grep -q 'kein Layout mit genau zwei Partitionen' "$work/grow.log"; then
	ok "weitere Partition dahinter: keine Änderung"
else
	bad "weitere Partition: $(cat "$work/grow.log")"
fi

echo "== Wurzel auf Partition 1 einer Karte mit einer Partition: nichts anfassen"
umount "$work/root"
losetup -d "$loop"
printf 'label: dos
start=2048, size=131072, type=83
' | sfdisk -q "$img"
loop=$(losetup -f --show -P "$img")
sleep 1
mkfs.ext4 -q -L rootfs -b 4096 "${loop}p1"
mount "${loop}p1" "$work/root"
before=$(table)
if grow && [ "$(table)" = "$before" ] && grep -q 'Partition 1 statt 2' "$work/grow.log"; then
	ok "Wurzel auf Partition 1: keine Änderung"
else
	bad "Partition 1: $(cat "$work/grow.log")"
fi

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) gescheitert"
	exit 1
fi
echo "Alle Prüfungen bestanden."
