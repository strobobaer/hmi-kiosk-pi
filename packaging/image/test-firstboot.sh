#!/bin/sh
# Test von firstboot.sh auf einer nachgebildeten SD-Karte (Loop-Gerät) in einem privilegierten
# arm64-Container mit Debian Trixie. raspi-config ist das echte Skript (Zweig trixie) als Datei.
#
#   docker run --rm --privileged --platform linux/arm64 -v "$PWD:/src" -w /src debian:trixie \
#     sh packaging/image/test-firstboot.sh <raspi-config-Datei>
#
# Prüft: Datenpartition im freien Rest bei eingehängter Wurzel, fstab-Eintrag einmalig und
# Einhängen über data.mount, Wiederholbarkeit, keine
# Formatierung fremder Dateisysteme, Kopie der ersten Version, current, Rechte, Overlay mit
# recurse=0, fsck.repair=yes, Boot schreibgeschützt in fstab, Erledigt-Marke in /etc/hmi-kiosk und Neustart,
# gemeldetes Scheitern beim Zurücksetzen der fstab und Abbruch ohne Wirkung bei Links unter /data.
set -eu

raspi_config=${1:?Aufruf: test-firstboot.sh <raspi-config-Datei>}
FB=$(pwd)/packaging/image/firstboot.sh
V=1.2.3
fails=0
ok() { echo "OK $1"; }
bad() {
	echo "FEHLER $1"
	fails=$((fails + 1))
}

echo "== Partitionsnamen (part_dev) für SD, NVMe und USB"
eval "$(sed -n '/^part_dev() {/,/^}/p' "$FB")"
for c in "/dev/mmcblk0 3 /dev/mmcblk0p3" "/dev/nvme0n1 3 /dev/nvme0n1p3" "/dev/sda 3 /dev/sda3"; do
	set -- $c
	if [ "$(part_dev "$1" "$2")" = "$3" ]; then ok "part_dev $1 $2 = $3"; else bad "part_dev $1 $2 = $(part_dev "$1" "$2"), erwartet $3"; fi
done

apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends fdisk e2fsprogs dosfstools \
	overlayroot procps psmisc >/dev/null
install -m 755 "$raspi_config" /usr/bin/raspi-config
# Echte Geräteknoten für neue Partitionen, wie auf dem Raspi (devtmpfs).
mount -t devtmpfs devtmpfs /dev

work=$(mktemp -d)
img=$work/karte.img
truncate -s 1200M "$img"
printf 'label: dos\nstart=16384, size=131072, type=c\nstart=147456, size=1048576, type=83\n' | sfdisk -q "$img"
loop=$(losetup -f --show -P "$img")
cleanup() {
	umount /data 2>/dev/null || true
	umount /boot/firmware 2>/dev/null || true
	umount "$work/root" 2>/dev/null || true
	losetup -d "$loop" 2>/dev/null || true
	rm -rf "$work"
}
trap cleanup EXIT
sleep 1
mkfs.vfat -n bootfs "${loop}p1" >/dev/null
mkfs.ext4 -q -L rootfs "${loop}p2"
mkdir -p "$work/root"
mount "${loop}p2" "$work/root" # belegt die Karte wie die laufende Wurzel

export HMI_FIRSTBOOT_ROOT="${loop}p2" HMI_FIRSTBOOT_LOG="$work/firstboot.log"
# fstab wie im Abbild: ohne /data. systemctl wird nur protokolliert.
cat >/etc/fstab <<'EOF'
proc            /proc           proc    defaults          0       0
PARTUUID=abcd-01  /boot/firmware  vfat    defaults          0       2
PARTUUID=abcd-02  /               ext4    defaults,noatime  0       1
EOF
mkdir -p "$work/bin"
# systemctl wird protokolliert; mit $work/fail-start scheitert "start".
# shellcheck disable=SC2016 # $* und $1 gehören in das erzeugte Skript
printf '#!/bin/sh\necho "systemctl $*" >>%s/systemctl.log\nif [ "$1" = start ] && [ -e %s/fail-start ]; then exit 1; fi\n' "$work" "$work" >"$work/bin/systemctl"
chmod 755 "$work/bin/systemctl"
export PATH="$work/bin:$PATH"
cp /etc/fstab "$work/fstab.orig"
data_lines() { grep -Ec '^LABEL=HMIDATA  /data .*ext4 .*x-systemd.device-timeout=30s +0 +2$' /etc/fstab || true; }

echo "== prepare-data, Probe-Einhängen scheitert"
printf '#!/bin/sh\necho "mount $*" >>%s/mount.log\nexit 32\n' "$work" >"$work/bin/mount"
chmod 755 "$work/bin/mount"
if sh "$FB" prepare-data >/dev/null 2>&1; then
	bad "Probe gescheitert, trotzdem Exit 0"
elif [ "$(data_lines)" = 0 ] && cmp -s /etc/fstab "$work/fstab.orig" && [ ! -s "$work/systemctl.log" ] && [ -s "$work/mount.log" ]; then
	ok "Probe gescheitert: kein fstab-Eintrag, kein systemctl, Exit ungleich 0"
else
	bad "Probe gescheitert, aber fstab/systemctl: $(cat /etc/fstab "$work/systemctl.log")"
fi
rm -f "$work/bin/mount"

echo "== prepare-data, data.mount scheitert"
touch "$work/fail-start"
if sh "$FB" prepare-data >/dev/null 2>&1; then
	bad "data.mount gescheitert, trotzdem Exit 0"
elif cmp -s /etc/fstab "$work/fstab.orig" &&
	[ "$(cat "$work/systemctl.log")" = "$(printf 'systemctl daemon-reload\nsystemctl start data.mount\nsystemctl daemon-reload')" ]; then
	ok "data.mount gescheitert: fstab-Eintrag wieder entfernt, fstab sonst unverändert, daemon-reload"
else
	bad "nach gescheitertem data.mount: $(cat /etc/fstab "$work/systemctl.log")"
fi

echo "== prepare-data, data.mount scheitert und die fstab lässt sich nicht zurücksetzen"
# mv scheitert nur beim Ersetzen der fstab; die Meldung muss im Protokoll stehen.
# shellcheck disable=SC2016 # $1, $2 und $@ gehören in das erzeugte Skript
printf '#!/bin/sh\nif [ "$1" = -f ] && [ "$3" = /etc/fstab ]; then exit 1; fi\nexec /bin/mv "$@"\n' >"$work/bin/mv"
chmod 755 "$work/bin/mv"
: >"$work/systemctl.log"
: >"$work/firstboot.log"
if sh "$FB" prepare-data >/dev/null 2>&1; then
	bad "fstab nicht zurückgesetzt, trotzdem Exit 0"
elif grep -q 'muss von Hand entfernt werden' "$work/firstboot.log" && ! ls /etc/fstab.hmi-kiosk.* >/dev/null 2>&1; then
	ok "gescheitertes Zurücksetzen der fstab wird gemeldet, keine Reste"
else
	bad "gescheitertes Zurücksetzen ohne Meldung: $(cat "$work/firstboot.log")"
fi
rm -f "$work/bin/mv"
cp "$work/fstab.orig" /etc/fstab
rm -f "$work/fail-start"
: >"$work/systemctl.log"
: >"$work/firstboot.log"

echo "== prepare-data"
sh "$FB" prepare-data
if grep -q 'Formatiere' "$work/firstboot.log"; then
	bad "gültiges HMIDATA beim erneuten Versuch neu formatiert"
else
	ok "erneuter Versuch formatiert das vorhandene HMIDATA nicht neu"
fi
if [ "$(blkid -p -o value -s LABEL "${loop}p3")" = HMIDATA ] && [ "$(blkid -p -o value -s TYPE "${loop}p3")" = ext4 ]; then
	ok "Partition 3 ext4 HMIDATA angelegt"
else
	bad "Partition 3 fehlt oder falsch"
fi
p2end=$(sfdisk --dump "$img" | sed -n "s|^$img""2 : start= *\([0-9]*\), size= *\([0-9]*\).*|\1 \2|p" | awk '{print $1+$2}')
p3=$(sfdisk --dump "$img" | sed -n "s|^$img""3 : start= *\([0-9]*\), size= *\([0-9]*\).*|\1 \2|p")
p3start=${p3% *}
p3size=${p3#* }
total=$(($(stat -c %s "$img") / 512))
if [ "$p3start" -ge "$p2end" ] && [ $((p3start + p3size)) -le "$total" ] && [ $((total - p3start - p3size)) -lt 4096 ]; then
	ok "Partition 3 füllt den Rest ($p3start+$p3size von $total Sektoren)"
else
	bad "Partition 3 an falscher Stelle: $p3, Ende p2 $p2end, gesamt $total"
fi
if [ "$(data_lines)" = 1 ] && ! grep -q nofail /etc/fstab; then
	ok "fstab-Eintrag /data ergänzt, ohne nofail"
else
	bad "fstab: $(grep data /etc/fstab)"
fi
if [ "$(cat "$work/systemctl.log")" = "$(printf 'systemctl daemon-reload\nsystemctl start data.mount')" ]; then
	ok "daemon-reload, dann data.mount gestartet"
else
	bad "systemctl: $(cat "$work/systemctl.log")"
fi
: >"$work/systemctl.log"

echo "== prepare-data erneut"
out=$(sh "$FB" prepare-data)
if printf '%s' "$out" | grep -q 'Datenpartition vorhanden' && [ "$(sfdisk --dump "$img" | grep -c "^${img}[0-9]")" = 3 ]; then
	ok "zweiter Lauf ändert nichts"
	if [ "$(data_lines)" = 1 ] && [ ! -s "$work/systemctl.log" ]; then
		ok "fstab-Eintrag bleibt einmalig, kein erneutes Einhängen"
	else
		bad "fstab doppelt oder erneut eingehängt: $(grep -c data /etc/fstab)"
	fi
else
	bad "zweiter Lauf: $out"
fi

echo "== fremdes Dateisystem auf Partition 3"
mkfs.ext4 -q -F -L ANDERES "${loop}p3"
if sh "$FB" prepare-data >/dev/null 2>&1; then
	bad "fremdes Dateisystem wurde nicht abgelehnt"
elif [ "$(blkid -p -o value -s LABEL "${loop}p3")" = ANDERES ]; then
	ok "fremdes Dateisystem bleibt unberührt, Exit ungleich 0"
else
	bad "fremdes Dateisystem verändert"
fi
wipefs -q -a "${loop}p3"
sh "$FB" prepare-data >/dev/null
if [ "$(blkid -p -o value -s LABEL "${loop}p3")" = HMIDATA ]; then
	ok "leere Partition 3 wird formatiert"
else
	bad "leere Partition 3 nicht formatiert"
fi

echo "== finish"
useradd -u 1100 -U -M -s /usr/sbin/nologin hmikiosk
mkdir -p /data /boot/firmware /etc/hmi-kiosk "/opt/hmi-kiosk/initial/$V"
mount "${loop}p1" /boot/firmware
mount "${loop}p3" /data
: >/boot/firmware/config.txt # raspi-config erkennt /boot/firmware daran
echo 'console=serial0,115200 console=tty1 root=PARTUUID=abcd-02 rootfstype=ext4 rootwait consoleblank=0' >/boot/firmware/cmdline.txt
printf 'version=%s\n' "$V" >/etc/hmi-kiosk/image-release
printf '#!/bin/sh\necho kiosk\n' >"/opt/hmi-kiosk/initial/$V/hmi-kiosk"
chmod 755 "/opt/hmi-kiosk/initial/$V/hmi-kiosk"
: >"$work/systemctl.log"
sh "$FB" finish

if [ "$(readlink /data/app/current)" = "$V" ]; then
	ok "current -> $V"
else
	bad "current: $(readlink /data/app/current)"
fi
if cmp -s "/opt/hmi-kiosk/initial/$V/hmi-kiosk" "/data/app/$V/hmi-kiosk"; then
	ok "Programm kopiert"
else
	bad "Programm fehlt"
fi
if [ "$(cat "/data/app/$V/VERSION")" = "$V" ]; then
	ok "VERSION $V"
else
	bad "VERSION falsch"
fi
own=$(stat -c '%U:%G %a' /data /data/app "/data/app/$V" "/data/app/$V/hmi-kiosk" | tr '\n' ' ')
if [ "$own" = "hmikiosk:hmikiosk 750 hmikiosk:hmikiosk 755 hmikiosk:hmikiosk 755 hmikiosk:hmikiosk 755 " ]; then
	ok "Besitzer und Rechte: $own"
else
	bad "Besitzer und Rechte: $own"
fi
if [ "$(stat -c '%U:%G %a' /data/system)" = "root:root 755" ]; then
	ok "/data/system root:root 755"
else
	bad "/data/system: $(stat -c '%U:%G %a' /data/system)"
fi
if grep -q '^overlayroot=tmpfs:recurse=0 console=' /boot/firmware/cmdline.txt; then
	ok "cmdline: $(cat /boot/firmware/cmdline.txt)"
else
	bad "cmdline: $(cat /boot/firmware/cmdline.txt)"
fi
if [ "$(grep -o 'fsck\.repair=[^ ]*' /boot/firmware/cmdline.txt)" = fsck.repair=yes ]; then
	ok "fsck.repair=yes ergänzt"
else
	bad "fsck.repair: $(cat /boot/firmware/cmdline.txt)"
fi
if grep -q '^PARTUUID=abcd-01  /boot/firmware  vfat    defaults,ro ' /etc/fstab; then
	ok "Boot schreibgeschützt in fstab"
else
	bad "fstab: $(grep firmware /etc/fstab)"
fi
if [ "$(data_lines)" = 1 ]; then
	ok "fstab-Eintrag /data unverändert"
else
	bad "fstab /data verändert"
fi
if [ -f /etc/hmi-kiosk/firstboot-done ] && [ "$(stat -c '%U:%G' /etc/hmi-kiosk/firstboot-done)" = root:root ] && [ ! -e /data/.hmi-kiosk-firstboot-done ]; then
	ok "Erledigt-Marke in /etc/hmi-kiosk (root), nicht auf /data"
else
	bad "Erledigt-Marke fehlt"
fi
if grep -q 'systemctl --no-block reboot' "$work/systemctl.log"; then
	ok "Neustart angefordert"
else
	bad "kein Neustart"
fi
if grep -q 'Erster Start erledigt' "$work/firstboot.log"; then
	ok "Protokolldatei geschrieben"
else
	bad "Protokolldatei fehlt"
fi

echo "== finish erneut"
: >"$work/systemctl.log"
if sh "$FB" finish | grep -q 'schon erledigt' && [ ! -s "$work/systemctl.log" ]; then
	ok "zweiter Lauf endet ohne Änderung und ohne Neustart"
else
	bad "zweiter Lauf nicht ruhig"
fi
rm /etc/hmi-kiosk/firstboot-done
sh "$FB" finish >/dev/null
if [ "$(grep -o 'overlayroot=[^ ]*' /boot/firmware/cmdline.txt | wc -l)" = 1 ] && [ "$(grep -c 'defaults,ro' /etc/fstab)" = 1 ] &&
	[ "$(grep -o 'fsck\.repair=[^ ]*' /boot/firmware/cmdline.txt | wc -l)" = 1 ]; then
	ok "abgebrochener Lauf wiederholt ohne doppelte Einträge"
else
	bad "Einträge doppelt: $(cat /boot/firmware/cmdline.txt)"
fi

# Ein Angreifer mit den Rechten des Dienstes (hmikiosk) ersetzt Ordner unter /data durch Links
# auf Systemordner. finish darf als root keinem davon folgen und bricht ab.
victim=/opt/hmi-victim
planted() { # planted <beschreibung> <pfad>: <pfad> ist schon ein Link auf $victim
	rm -f /etc/hmi-kiosk/firstboot-done
	: >"$work/systemctl.log"
	: >"$work/firstboot.log"
	if sh "$FB" finish >/dev/null 2>&1; then
		bad "$1: finish endete mit Exit 0"
	elif [ "$(stat -c '%U:%G %a' "$victim" "$victim/datei" | tr '\n' ' ')" = "root:root 700 root:root 600 " ] &&
		[ "$(cat "$victim/datei")" = unberührt ] && [ "$(find "$victim" | wc -l)" = 2 ] &&
		[ ! -e /etc/hmi-kiosk/firstboot-done ] && ! grep -q reboot "$work/systemctl.log" &&
		grep -q 'symbolischer Link' "$work/firstboot.log"; then
		ok "$1: Ziel unberührt, keine Marke, kein Neustart, Meldung im Protokoll"
	else
		bad "$1: $(stat -c '%U:%G %a %n' "$victim" "$victim"/* 2>&1 | tr '\n' ' ') $(cat "$work/firstboot.log")"
	fi
}
mkdir -m 700 "$victim"
echo unberührt >"$victim/datei"
chmod 600 "$victim/datei"
mv /data/app /data/app.echt
ln -s "$victim" /data/app
chown -h hmikiosk:hmikiosk /data/app
planted "/data/app als Link" /data/app
rm /data/app
mv /data/app.echt /data/app

mv "/data/app/$V" "/data/app/$V.echt"
ln -s "$victim" "/data/app/$V"
chown -h hmikiosk:hmikiosk "/data/app/$V"
planted "/data/app/$V als Link" "/data/app/$V"
rm "/data/app/$V"

ln -s "$victim" "/data/app/.tmp-firstboot-$V"
chown -h hmikiosk:hmikiosk "/data/app/.tmp-firstboot-$V"
planted "Arbeitsordner als Link" "/data/app/.tmp-firstboot-$V"
rm "/data/app/.tmp-firstboot-$V"

mv /data/system /data/system.echt
ln -s "$victim" /data/system
planted "/data/system als Link" /data/system
rm /data/system
mv /data/system.echt /data/system

mkdir "/data/app/$V"
chown hmikiosk:hmikiosk "/data/app/$V"
rm -f /etc/hmi-kiosk/firstboot-done
if sh "$FB" finish >/dev/null 2>&1 && cmp -s "/opt/hmi-kiosk/initial/$V/hmi-kiosk" "/data/app/$V/hmi-kiosk" &&
	[ -f /etc/hmi-kiosk/firstboot-done ]; then
	ok "nach Entfernen der Links läuft finish wieder durch"
else
	bad "finish nach Entfernen der Links: $(cat "$work/firstboot.log")"
fi
rm -rf "/data/app/$V.echt"

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) gescheitert"
	exit 1
fi
echo "Alle Prüfungen bestanden."
