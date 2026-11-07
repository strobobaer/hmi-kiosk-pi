#!/bin/sh
# Test von "firstboot.sh grow-data": eine kompakte Karte (kleine Partition 3 mit HMIDATA) auf einem
# größeren Träger (Loop-Gerät). Läuft im privilegierten Container mit Debian Trixie, auf dem
# Host-Prozessor (amd64): unter qemu-user (arm64 auf amd64) scheitert der Kernel-Aufruf BLKPG zum
# Vergrößern einer Partition, den partx -u braucht; darum nicht mit test-firstboot.sh zusammen.
#
#   docker run --rm --privileged -v "$PWD:/src" -w /src debian:trixie sh packaging/image/test-grow-data.sh
#
# Prüft: Partition 3 und Dateisystem wachsen online (eingehängt) auf die ganze Karte, Daten, Besitzer
# und Rechte bleiben, fremdes Label und Karte mit höchstens 16 MiB Rest bleiben unberührt, zweiter Lauf
# ändert nichts, ein gescheitertes resize2fs oder sfdisk wird nur protokolliert (Exit 0) und der
# nächste Lauf holt das Dateisystem nach.
set -eu

FB=$(pwd)/packaging/image/firstboot.sh
fails=0
ok() { echo "OK $1"; }
bad() {
	echo "FEHLER $1"
	fails=$((fails + 1))
}

apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends fdisk e2fsprogs >/dev/null
mount -t devtmpfs devtmpfs /dev # echte Geräteknoten für neue Partitionen

work=$(mktemp -d)
loop=""
cleanup() {
	umount /data 2>/dev/null || true
	[ -z "$loop" ] || losetup -d "$loop" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT
export HMI_FIRSTBOOT_LOG="$work/firstboot.log"
mkdir -p "$work/bin" /data
PATH="$work/bin:$PATH"

img=$work/karte.img
truncate -s 600M "$img"
printf 'label: dos\nstart=2048, size=65536, type=c\nstart=67584, size=131072, type=83\nstart=198656, size=131072, type=83\n' | sfdisk -q "$img"
loop=$(losetup -f --show -P "$img")
sleep 1
mkfs.ext4 -q -L HMIDATA -b 4096 -E resize=134217728 "${loop}p3"
mount "${loop}p3" /data
head -c 20971520 /dev/urandom >/data/gross
md5sum /data/gross >"$work/gross.md5"
echo klein >/data/klein
chown 1100:1100 /data/klein
chmod 600 /data/klein

p3sys() { cat "/sys/class/block/$(basename "${loop}p3")/size"; }
table() { sfdisk --dump "$loop" | grep '^/dev/'; }
fsbytes() { echo $(($(dumpe2fs -h "${loop}p3" 2>/dev/null | sed -n 's/^Block count: *//p') * 4096)); }
devsectors() { blockdev --getsz "$loop"; }
data_ok() {
	(cd /data && md5sum -c "$work/gross.md5" >/dev/null 2>&1) && [ "$(cat /data/klein)" = klein ] &&
		[ "$(stat -c '%u:%g %a' /data/klein)" = "1100:1100 600" ] && mountpoint -q /data
}
grow() { sh "$FB" grow-data >/dev/null 2>&1; }
logreset() { : >"$work/firstboot.log"; }

echo "== fremdes Label"
before=$(table)
size0=$(p3sys)
fs0=$(fsbytes)
tune2fs -L ANDERES "${loop}p3" >/dev/null
logreset
if grow && [ "$(table)" = "$before" ] && [ "$(p3sys)" = "$size0" ] && grep -q 'ist nicht HMIDATA' "$work/firstboot.log"; then
	ok "fremdes Label: Tabelle unverändert, Exit 0"
else
	bad "fremdes Label: $(table) $(cat "$work/firstboot.log")"
fi
tune2fs -L HMIDATA "${loop}p3" >/dev/null

echo "== Karte mit 600 MB, Partition 3 klein"
if grow; then
	ok "grow-data endet mit Exit 0"
else
	bad "grow-data: Exit ungleich 0"
fi
total=$(devsectors)
p3line=$(table | sed -n 's|^/dev/[a-z0-9]*3 : start= *\([0-9]*\), size= *\([0-9]*\).*|\1 \2|p')
p3start=${p3line% *}
p3size=${p3line#* }
if [ $((p3start + p3size)) -ge $((total - 2048)) ] && [ "$(p3sys)" = "$p3size" ] && [ "$p3size" -gt "$size0" ]; then
	ok "Partition 3 reicht jetzt bis zum Ende ($p3start+$p3size von $total Sektoren), Kernel kennt die Größe"
else
	bad "Partition 3: $p3line von $total, Kernel $(p3sys)"
fi
if [ "$(fsbytes)" -gt "$fs0" ] && [ $(($(fsbytes) + 8 * 1048576)) -ge $((p3size * 512)) ]; then
	ok "Dateisystem wächst online auf die Partition ($(fsbytes) Byte)"
else
	bad "Dateisystem: $(fsbytes) Byte, Partition $((p3size * 512))"
fi
if data_ok; then
	ok "Daten, Besitzer und Rechte bleiben, /data bleibt eingehängt"
else
	bad "Daten nach dem Vergrößern verändert"
fi
if [ "$(blkid -p -o value -s LABEL "${loop}p3")" = HMIDATA ] && [ "$(blkid -p -o value -s TYPE "${loop}p3")" = ext4 ]; then
	ok "ext4 mit Label HMIDATA bleibt"
else
	bad "Label oder Typ geändert"
fi

echo "== zweiter Lauf"
after=$(table)
fs1=$(fsbytes)
logreset
if grow && [ "$(table)" = "$after" ] && [ "$(fsbytes)" = "$fs1" ] && grep -q 'reicht schon bis zum Ende' "$work/firstboot.log"; then
	ok "zweiter Lauf ändert nichts"
else
	bad "zweiter Lauf: $(cat "$work/firstboot.log")"
fi

echo "== nur 10 MiB frei dahinter"
truncate -s 610M "$img"
losetup -c "$loop"
after=$(table)
logreset
if grow && [ "$(table)" = "$after" ] && [ "$(devsectors)" -gt "$((p3start + p3size + 16384))" ] && grep -q 'reicht schon bis zum Ende' "$work/firstboot.log"; then
	ok "10 MiB frei dahinter: Partition bleibt"
else
	bad "10 MiB frei: $(cat "$work/firstboot.log")"
fi

echo "== resize2fs scheitert"
truncate -s 710M "$img"
losetup -c "$loop"
fs2=$(fsbytes)
printf '#!/bin/sh\nexit 1\n' >"$work/bin/resize2fs"
chmod 755 "$work/bin/resize2fs"
logreset
if grow && grep -q 'FEHLER' "$work/firstboot.log" && [ "$(p3sys)" -gt "$p3size" ] && [ "$(fsbytes)" = "$fs2" ]; then
	ok "Exit 0, FEHLER im Protokoll, Partition vergrößert, Dateisystem noch nicht"
else
	bad "resize2fs-Fehler: $(cat "$work/firstboot.log")"
fi
rm -f "$work/bin/resize2fs"
# Die Partition steht schon am Ende; der nächste Lauf holt das Dateisystem trotzdem nach.
if grow && [ "$(fsbytes)" -gt "$fs2" ] && [ $(($(fsbytes) + 8 * 1048576)) -ge $(($(p3sys) * 512)) ] && data_ok; then
	ok "nächster Lauf vergrößert das Dateisystem nach, Daten unverändert"
else
	bad "Nachholen: $(fsbytes) Byte, Partition $(($(p3sys) * 512))"
fi

echo "== sfdisk scheitert"
truncate -s 800M "$img"
losetup -c "$loop"
printf '#!/bin/sh\nexit 1\n' >"$work/bin/sfdisk"
chmod 755 "$work/bin/sfdisk"
before=$(table)
logreset
if grow && grep -q 'FEHLER' "$work/firstboot.log" && [ "$(table)" = "$before" ]; then
	ok "Exit 0, FEHLER im Protokoll, Tabelle unverändert"
else
	bad "sfdisk-Fehler: $(cat "$work/firstboot.log")"
fi
rm -f "$work/bin/sfdisk"
if grow && data_ok; then
	ok "danach wächst die Karte, Daten unverändert"
else
	bad "Wachstum nach dem sfdisk-Fehler: $(cat "$work/firstboot.log")"
fi

echo "== Partition 3 ist nicht die Datenpartition (Nummer 2)"
# Die Datenpartition liegt auf einer anderen Nummer: nichts anfassen.
umount /data
losetup -d "$loop"
printf 'label: dos\nstart=2048, size=65536, type=c\nstart=67584, size=131072, type=83\n' | sfdisk -q "$img"
loop=$(losetup -f --show -P "$img")
sleep 1
mkfs.ext4 -q -L HMIDATA "${loop}p2"
mount "${loop}p2" /data
before=$(table)
logreset
if grow && [ "$(table)" = "$before" ] && grep -q 'Partition 2 statt 3' "$work/firstboot.log"; then
	ok "HMIDATA auf Partition 2: keine Änderung"
else
	bad "Partition 2: $(cat "$work/firstboot.log")"
fi

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) gescheitert"
	exit 1
fi
echo "Alle Prüfungen bestanden."
