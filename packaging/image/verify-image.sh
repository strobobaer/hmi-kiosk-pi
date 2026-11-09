#!/bin/sh
# Prüft ein gebautes SD-Abbild, ohne es zu starten: Partitionen, Pakete, Dienste, Benutzer,
# Fensterregeln und Dateirechte. Hängt die Partitionen nur lesend per Loop-Gerät ein.
#
# Aufruf: verify-image.sh [--version <x.y.z>] [--pakete <datei>] <abbild.img|abbild.img.xz>
#   --version  erwartete Programmversion (sonst die aus /etc/hmi-kiosk/image-release)
#   --pakete   schreibt die Paketliste (Name, Version, Architektur) in <datei>
# Umgebung: HMI_ENABLE_SSH=1, wenn das Abbild mit SSH gebaut wurde (sonst muss SSH aus sein).
# Braucht root und sfdisk, losetup, blkid, mount, dpkg-query, stat, lsinitramfs (xz für .img.xz);
# unter Docker mit dem Prüfabbild aus packaging/image/verify.Dockerfile (siehe build-image.sh).
# Exit: 0 alle Prüfungen bestanden, 1 mindestens eine gescheitert, 2 Aufruf oder Umgebung fehlerhaft.
set -eu

KIOSK_UID=1100
REQUIRED_PACKAGES="labwc xwayland wvkbd chromium remmina remmina-plugin-vnc tigervnc-viewer openbox rfkill x11-utils
xserver-xorg-core xserver-xorg-input-libinput xinit xauth x11-xserver-utils overlayroot raspi-config fdisk e2fsprogs
iputils-arping iputils-ping network-manager"
FORBIDDEN_PACKAGES="remmina-plugin-secret gnome-keyring cloud-init"
UNITS="hmi-kiosk.service hmi-kiosk-helper.service hmi-kiosk-session.service hmi-kiosk-update-guard.service hmi-kiosk-firstboot.service hmi-kiosk-grow-data.service hmi-kiosk-identity.service hmi-kiosk-network.service"

usage() {
	echo "Aufruf: verify-image.sh [--version <x.y.z>] [--pakete <datei>] <abbild.img|abbild.img.xz>" >&2
	exit 2
}

want_version=""
package_list=""
while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || usage
		want_version=$2
		shift 2
		;;
	--pakete)
		[ $# -ge 2 ] || usage
		package_list=$2
		shift 2
		;;
	-h | --help) usage ;;
	-*) usage ;;
	*) break ;;
	esac
done
if [ $# -ne 1 ]; then
	echo "Kein Abbild angegeben." >&2
	usage
fi
image=$1
if [ ! -f "$image" ]; then
	echo "Abbild nicht gefunden: $image" >&2
	exit 2
fi
if [ "$(id -u)" != 0 ]; then
	echo "verify-image.sh braucht root für die Loop-Geräte (z. B. im privilegierten Container)." >&2
	exit 2
fi
for tool in sfdisk losetup blkid mount umount dpkg-query stat od lsinitramfs; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "Werkzeug fehlt: $tool" >&2
		exit 2
	fi
done

work=$(mktemp -d)
loops=""
cleanup() {
	for m in "$work/root/boot/firmware" "$work/boot" "$work/root"; do
		if mountpoint -q "$m" 2>/dev/null; then umount "$m" || true; fi
	done
	for l in $loops; do losetup -d "$l" 2>/dev/null || true; done
	rm -rf "$work"
}
trap cleanup EXIT INT TERM

case "$image" in
*.xz)
	command -v xz >/dev/null 2>&1 || {
		echo "Werkzeug fehlt: xz" >&2
		exit 2
	}
	echo "Entpacke $image ..."
	xz -dc "$image" >"$work/image.img"
	img="$work/image.img"
	;;
*) img=$image ;;
esac

fails=0
ok() { echo "OK $1${2:+: $2}"; }
bad() {
	echo "FEHLER $1: $2"
	fails=$((fails + 1))
}

# Partitionstabelle: Zeilen "<abbild>N : start= S, size= Z, type=T".
parts=$(sfdisk --dump "$img" | grep "^${img}[0-9]" || true)
nparts=$(printf '%s\n' "$parts" | grep -c . || true)
field() { printf '%s\n' "$parts" | sed -n "${1}p" | sed -n "s/.*$2= *\([0-9a-fA-F]*\).*/\1/p"; }

attach() { # attach <nr> -> Loop-Gerät der Partition, nur lesend
	start=$(field "$1" start)
	size=$(field "$1" size)
	l=$(losetup -f 2>/dev/null)
	l=${l%% *} # "/dev/loopN (lost)", wenn der Knoten fehlt
	# In Containern fehlt der Geräteknoten neuer Loop-Geräte (kein devtmpfs/udev).
	[ -e "$l" ] || mknod "$l" b 7 "${l#/dev/loop}"
	losetup -r -o $((start * 512)) --sizelimit $((size * 512)) "$l" "$img"
	echo "$l"
}

if [ "$nparts" != 2 ]; then
	bad partitionen "$nparts Partitionen statt 2 (Boot, System; /data entsteht beim ersten Start)"
	echo "Abbruch: ohne die erwarteten Partitionen sind keine weiteren Prüfungen möglich."
	exit 1
fi
# attach läuft in einer Unterschale; die Geräte für das Aufräumen hier merken.
boot_dev=$(attach 1)
loops="$loops $boot_dev"
root_dev=$(attach 2)
loops="$loops $root_dev"
boot_type=$(blkid -p -o value -s TYPE "$boot_dev" || true)
root_type=$(blkid -p -o value -s TYPE "$root_dev" || true)
if [ "$boot_type" = vfat ] && [ "$root_type" = ext4 ]; then
	ok partitionen "2 Partitionen: Boot ($boot_type), System ($root_type)"
else
	bad partitionen "Typen Boot=$boot_type System=$root_type, erwartet vfat und ext4"
	exit 1
fi
mkdir -p "$work/root" "$work/boot"
mount -o ro,noload "$root_dev" "$work/root"
mount -o ro "$boot_dev" "$work/boot"
R=$work/root
B=$work/boot
S=$R/etc/systemd/system

installed() { # installed <paket>: Status "ii" im Abbild
	dpkg-query --admindir="$R/var/lib/dpkg" -W -f='${db:Status-Abbrev}' "$1" 2>/dev/null | grep -q '^ii'
}
masked() { [ "$(readlink "$S/$1" 2>/dev/null)" = /dev/null ]; }
enabled_in() { [ -L "$S/$1.wants/$2" ] || [ -L "$S/$1.requires/$2" ]; }
flat() { tr -d '\n\t' <"$1" | tr -s ' '; }
unit_has() { grep -qx "$2" "$S/$1"; }

# Größenänderung der Systempartition
msg=""
if grep -Eq '(^| )resize( |$)' "$B/cmdline.txt"; then msg="$msg; cmdline.txt enthält resize"; fi
if grep -q 'init_resize' "$B/cmdline.txt"; then msg="$msg; cmdline.txt enthält init_resize"; fi
if [ -e "$R/usr/lib/systemd/system/rpi-resize.service" ] && ! masked rpi-resize.service; then
	msg="$msg; rpi-resize.service nicht maskiert"
fi
if grep -q 'x-systemd.growfs' "$R/etc/fstab"; then msg="$msg; fstab enthält x-systemd.growfs"; fi
if installed cloud-init; then msg="$msg; cloud-init installiert"; fi
if [ -z "$msg" ]; then ok groesse-fest "keine Vergrößerung der Systempartition"; else bad groesse-fest "${msg#; }"; fi

# PCIe-Anschluss für NVMe-SSD (Pi 5): die Zeile muss im letzten Abschnitt [all] (oder ohne Abschnitt) stehen
final_all=$(awk '/^\[/ { sec = $0; blk = ""; next } { blk = blk $0 "\n" } END { if (sec == "" || sec == "[all]") printf "%s", blk }' "$B/config.txt")
if printf '%s\n' "$final_all" | grep -Eq '^dtparam=pciex1[[:space:]]*$'; then
	ok pcie-nvme "config.txt enthält dtparam=pciex1 im letzten [all]-Abschnitt"
else
	bad pcie-nvme "dtparam=pciex1 fehlt im letzten [all]-Abschnitt von config.txt (NVMe-SSD würde nicht erkannt)"
fi

# Erster Start
msg=""
[ -x "$R/usr/local/sbin/hmi-kiosk-firstboot" ] || msg="$msg; /usr/local/sbin/hmi-kiosk-firstboot fehlt"
for u in hmi-kiosk-firstboot.service hmi-kiosk-firstboot-data.service; do
	[ -f "$S/$u" ] || msg="$msg; $u fehlt"
done
enabled_in multi-user.target hmi-kiosk-firstboot.service || msg="$msg; hmi-kiosk-firstboot.service nicht aktiviert"
enabled_in local-fs.target hmi-kiosk-firstboot-data.service || msg="$msg; hmi-kiosk-firstboot-data.service nicht aktiviert"
grep -qx 'ConditionPathExists=!/etc/hmi-kiosk/firstboot-done' "$S/hmi-kiosk-firstboot.service" 2>/dev/null ||
	msg="$msg; Erledigt-Marke /etc/hmi-kiosk/firstboot-done fehlt in der Unit"
grep -qx 'DONE_MARKER=/etc/hmi-kiosk/firstboot-done' "$R/usr/local/sbin/hmi-kiosk-firstboot" 2>/dev/null ||
	msg="$msg; firstboot schreibt die Erledigt-Marke nicht nach /etc/hmi-kiosk/firstboot-done"
if [ -e "$R/etc/hmi-kiosk/firstboot-done" ] || [ -L "$R/etc/hmi-kiosk/firstboot-done" ]; then
	msg="$msg; Erledigt-Marke steht schon im Abbild (der erste Start liefe nie)"
fi
if [ "$(stat -c %u:%g:%a "$R/etc/hmi-kiosk" 2>/dev/null)" != 0:0:755 ]; then
	msg="$msg; /etc/hmi-kiosk nicht root:root 755"
fi
# shellcheck disable=SC2016 # gesucht wird der Text im Skript
grep -q 'safe_dir "$app"' "$R/usr/local/sbin/hmi-kiosk-firstboot" 2>/dev/null ||
	msg="$msg; firstboot prüft /data/app nicht auf symbolische Links"
if [ -z "$msg" ]; then ok erster-start "Skript und Units vorhanden und aktiviert"; else bad erster-start "${msg#; }"; fi

# fstab: /data steht nicht im Abbild; firstboot trägt es ohne nofail und mit Geräte-Frist ein.
fb=$R/usr/local/sbin/hmi-kiosk-firstboot
d=$S/hmi-kiosk-firstboot-data.service
msg=""
if grep -Eq '^[^#]*[[:space:]]/data[[:space:]]' "$R/etc/fstab"; then
	msg="$msg; fstab enthält /data schon im Abbild (wartet beim ersten Start auf eine fehlende Partition)"
fi
grep '^FSTAB_LINE=' "$fb" 2>/dev/null | grep -q '/data .*ext4 .*x-systemd.device-timeout=30s' ||
	msg="$msg; firstboot trägt /data nicht mit x-systemd.device-timeout=30s ein"
if grep '^FSTAB_LINE=' "$fb" 2>/dev/null | grep -q nofail; then msg="$msg; firstboot trägt /data mit nofail ein"; fi
# Feld 6 (fsck-Pass) 2: /data wird beim Start geprüft; fsck.repair=yes (cmdline) repariert ohne Rückfrage.
grep '^FSTAB_LINE=' "$fb" 2>/dev/null | grep -Eq '[[:space:]]0[[:space:]]+2"$' ||
	msg="$msg; firstboot trägt /data nicht mit fsck-Pass 2 ein"
grep -Eq '(^|[[:space:]])fsck\.repair=yes([[:space:]]|$)' "$B/cmdline.txt" ||
	msg="$msg; cmdline.txt ohne fsck.repair=yes (ein beschädigtes /data hielte den Start im Notfallmodus an)"
grep -q '^ensure_fsck_repair' "$fb" 2>/dev/null || msg="$msg; firstboot ergänzt fsck.repair=yes nicht"
grep -q 'nodiscard,lazy_itable_init=1,lazy_journal_init=1' "$fb" 2>/dev/null || msg="$msg; mkfs ohne nodiscard/lazy_init"
for l in TimeoutStartSec=15min Conflicts=shutdown.target "Before=local-fs.target shutdown.target" DefaultDependencies=no \
	"ExecStart=/usr/local/sbin/hmi-kiosk-firstboot prepare-data"; do
	unit_has hmi-kiosk-firstboot-data.service "$l" || msg="$msg; $(basename "$d") ohne $l"
done
[ -d "$R/data" ] || msg="$msg; Einhängepunkt /data fehlt"
if [ -z "$msg" ]; then ok fstab "kein /data-Eintrag im Abbild, firstboot ergänzt ihn ohne nofail mit fsck-Pass 2, fsck.repair=yes"; else bad fstab "${msg#; }"; fi

# Vergrößerung der Datenpartition beim Start (kompakte SD-Sicherung auf größerer Karte)
msg=""
g=hmi-kiosk-grow-data.service
for l in Type=oneshot RequiresMountsFor=/data "Before=hmi-kiosk-helper.service hmi-kiosk.service" \
	"ExecStart=/usr/local/sbin/hmi-kiosk-firstboot grow-data"; do
	unit_has "$g" "$l" || msg="$msg; $g ohne $l"
done
if grep -q '^Condition' "$S/$g" 2>/dev/null; then msg="$msg; $g hat eine Bedingung (muss bei jedem Start laufen)"; fi
grep -q '^grow_data()' "$fb" 2>/dev/null || msg="$msg; firstboot hat keine Funktion grow_data"
grep -q '^grow-data)' "$fb" 2>/dev/null || msg="$msg; firstboot kennt grow-data nicht"
for t in sfdisk resize2fs mkfs.ext4 debugfs blkid blockdev partx findmnt; do
	[ -x "$R/usr/sbin/$t" ] || [ -x "$R/usr/bin/$t" ] || msg="$msg; $t fehlt im Abbild"
done
if [ -z "$msg" ]; then ok datenpartition-waechst "$g läuft bei jedem Start nach data.mount vor Helfer und Dienst, Werkzeuge vorhanden"; else bad datenpartition-waechst "${msg#; }"; fi

# Netzwerk-Identität: eigene Maschinen-ID und eigener Rechnername auf einem geklonten Gerät
msg=""
i=hmi-kiosk-identity.service
for l in Type=oneshot RequiresMountsFor=/data "Before=network-pre.target NetworkManager.service systemd-networkd.service shutdown.target" \
	Wants=network-pre.target DefaultDependencies=no "ExecStart=/usr/local/sbin/hmi-kiosk-identity"; do
	unit_has "$i" "$l" || msg="$msg; $i ohne $l"
done
if grep -q '^Condition' "$S/$i" 2>/dev/null; then msg="$msg; $i hat eine Bedingung (muss bei jedem Start laufen)"; fi
if grep -Eq '^(User|Requires|BindsTo)=' "$S/$i" 2>/dev/null; then msg="$msg; $i nicht als root oder mit harter Abhängigkeit"; fi
id_script=$R/usr/local/sbin/hmi-kiosk-identity
if [ ! -x "$id_script" ] || [ "$(stat -c %u:%g:%a "$id_script" 2>/dev/null)" != 0:0:755 ]; then
	msg="$msg; $id_script fehlt oder nicht root:root 755"
fi
grep -q '^enter_dir()' "$id_script" 2>/dev/null || msg="$msg; Identitätsskript prüft /data/system nicht"
grep -q '^exit 0$' "$id_script" 2>/dev/null || msg="$msg; Identitätsskript endet nicht immer mit 0"
if [ -z "$msg" ]; then ok netzwerk-identitaet "$i läuft bei jedem Start nach /data vor dem Netzwerk, Skript vorhanden"; else bad netzwerk-identitaet "${msg#; }"; fi

# Ethernet-Einstellung: bestätigte feste Adresse aus /data vor NetworkManager als Profil herstellen
msg=""
n=hmi-kiosk-network.service
for l in Type=oneshot RequiresMountsFor=/data "Before=network-pre.target NetworkManager.service shutdown.target" \
	Wants=network-pre.target DefaultDependencies=no "ExecStart=/usr/local/bin/hmi-kiosk-helper net-boot"; do
	unit_has "$n" "$l" || msg="$msg; $n ohne $l"
done
if grep -q '^Condition' "$S/$n" 2>/dev/null; then msg="$msg; $n hat eine Bedingung (muss bei jedem Start laufen)"; fi
if grep -Eq '^(User|Requires|BindsTo)=' "$S/$n" 2>/dev/null; then msg="$msg; $n nicht als root oder mit harter Abhängigkeit"; fi
[ -d "$R/etc/NetworkManager/system-connections" ] || msg="$msg; /etc/NetworkManager/system-connections fehlt"
if [ -e "$R/etc/NetworkManager/system-connections/hmi-kiosk-eth.nmconnection" ]; then
	msg="$msg; Profil hmi-kiosk-eth steht schon im Abbild (jedes Gerät bekäme dieselbe Adresse)"
fi
for t in nmcli arping; do
	[ -x "$R/usr/bin/$t" ] || [ -x "$R/usr/sbin/$t" ] || [ -x "$R/bin/$t" ] || [ -x "$R/sbin/$t" ] || msg="$msg; $t fehlt im Abbild"
done
if [ -z "$msg" ]; then ok ethernet-einstellung "$n läuft bei jedem Start nach /data vor NetworkManager, nmcli und arping vorhanden"; else bad ethernet-einstellung "${msg#; }"; fi

# initramfs des Pi-5-Kernels enthält overlayroot (sonst bliebe das Overlay wirkungslos)
if [ ! -f "$B/initramfs_2712" ]; then
	bad initramfs "initramfs_2712 fehlt auf der Boot-Partition"
elif lsinitramfs "$B/initramfs_2712" 2>/dev/null | grep -q 'scripts/init-bottom/overlayroot$'; then
	ok initramfs "initramfs_2712 enthält scripts/init-bottom/overlayroot"
else
	bad initramfs "initramfs_2712 ohne overlayroot"
fi

# Pakete
missing=""
for p in $REQUIRED_PACKAGES; do installed "$p" || missing="$missing $p"; done
if [ -z "$missing" ]; then ok pakete "alle vorhanden"; else bad pakete "fehlen:$missing"; fi
present=""
for p in $FORBIDDEN_PACKAGES; do if installed "$p"; then present="$present $p"; fi; done
if [ -z "$present" ]; then ok pakete-verboten "$FORBIDDEN_PACKAGES fehlen"; else bad pakete-verboten "installiert:$present"; fi
# Bildschirmtastatur: Der Dienst startet genau dieses Programm (internal/keyboard.Program).
if [ -x "$R/usr/bin/wvkbd-mobintl" ]; then ok bildschirmtastatur "/usr/bin/wvkbd-mobintl vorhanden"; else bad bildschirmtastatur "/usr/bin/wvkbd-mobintl fehlt (Paket wvkbd)"; fi

# Automatische Updates
msg=""
for t in apt-daily.timer apt-daily-upgrade.timer; do
	masked "$t" || msg="$msg; $t nicht maskiert"
	if enabled_in timers.target "$t"; then msg="$msg; $t in timers.target"; fi
done
if installed unattended-upgrades; then
	if enabled_in multi-user.target unattended-upgrades.service; then msg="$msg; unattended-upgrades aktiv"; fi
fi
if [ -z "$msg" ]; then ok updates-aus "apt-daily-Timer maskiert, kein unattended-upgrades"; else bad updates-aus "${msg#; }"; fi

# Dienstbenutzer
pw=$(grep '^hmikiosk:' "$R/etc/passwd" || true)
uid=$(printf '%s' "$pw" | cut -d: -f3)
msg=""
[ -n "$pw" ] || msg="Benutzer hmikiosk fehlt"
[ -z "$pw" ] || [ "$uid" = "$KIOSK_UID" ] || msg="$msg; UID $uid statt $KIOSK_UID (fest in den Units)"
[ "$uid" != 0 ] || msg="$msg; UID 0"
for g in input video render; do
	grep -Eq "^$g:[^:]*:[0-9]+:([^,]*,)*hmikiosk(,|$)" "$R/etc/group" || msg="$msg; nicht in Gruppe $g"
done
grep -Eq '^hmikiosk:[!*]' "$R/etc/shadow" || msg="$msg; Passwort nicht gesperrt"
if [ -z "$msg" ]; then ok benutzer "hmikiosk UID $uid, Gruppen input video render, ohne Passwort"; else bad benutzer "${msg#; }"; fi

# Fensterregeln: Remmina ohne Rahmen bei 0,48, darunter maximiert (Rand oben 48)
lw=$(flat "$R/etc/hmi-kiosk/labwc/rc.xml" 2>/dev/null || true)
if printf '%s' "$lw" | grep -q '<margin top="48" */>' &&
	printf '%s' "$lw" | grep -Eq '<windowRule identifier="org\.remmina\.Remmina"[^>]*serverDecoration="no"[^>]*> *<action name="MoveTo" x="0" y="48" */> *<action name="Maximize" */>'; then
	ok rc-labwc "Remmina ohne Rahmen bei 0,48, maximiert unter dem Rand von 48"
else
	bad rc-labwc "Regel für org.remmina.Remmina (0,48, ohne Rahmen, Rand 48) fehlt in /etc/hmi-kiosk/labwc/rc.xml"
fi
ob=$(flat "$R/etc/xdg/openbox/rc.xml" 2>/dev/null || true)
if printf '%s' "$ob" | grep -q '<margins> *<top>48</top>' &&
	printf '%s' "$ob" | grep -Eq '<application class="org\.remmina\.Remmina"[^>]*> *<decor>no</decor> *<position force="yes"> *<x>0</x> *<y>48</y> *</position> *<maximized>yes</maximized>'; then
	ok rc-openbox "Remmina ohne Rahmen bei 0,48, maximiert unter dem Rand von 48"
else
	bad rc-openbox "Regel für org.remmina.Remmina (0,48, ohne Rahmen, Rand 48) fehlt in /etc/xdg/openbox/rc.xml"
fi

if printf '%s' "$lw" | grep -Eq '<windowRule identifier="chrome-\*-hmi-kiosk-web"[^>]*serverDecoration="no"[^>]*> *<action name="MoveTo" x="0" y="48" */> *<action name="Maximize" */>' &&
	printf '%s' "$ob" | grep -Eq '<application class="hmi-kiosk-web"[^>]*> *<decor>no</decor> *<position force="yes"> *<x>0</x> *<y>48</y> *</position> *<maximized>yes</maximized>'; then
	ok rc-web "Web-Kachel (app_id chrome-*-hmi-kiosk-web, WM_CLASS hmi-kiosk-web) ohne Rahmen bei 0,48, maximiert"
else
	bad rc-web "Fensterregel für Web-Kacheln fehlt (labwc chrome-*-hmi-kiosk-web, openbox class hmi-kiosk-web)"
fi

# Navigationssperre: Chromium-Richtlinie sperrt alles außer der Kachelseite und der Admin-Seite am Terminal (Loopback)
pol="$R/etc/chromium/policies/managed/hmi-kiosk.json"
pf=$(flat "$pol" 2>/dev/null || true)
if printf '%s' "$pf" | grep -Fq '"URLBlocklist": ["*"]' &&
	printf '%s' "$pf" | grep -Fq '"URLAllowlist": ["http://127.0.0.1:8080", "http://localhost:8080", "http://127.0.0.1:8081", "http://localhost:8081"]' &&
	printf '%s' "$pf" | grep -Fq '"PasswordManagerEnabled": false,' &&
	printf '%s' "$pf" | grep -Fq '"DownloadRestrictions": 3' &&
	[ "$(stat -c %u:%g:%a "$pol")" = 0:0:644 ]; then
	ok chromium-richtlinie "URLBlocklist * und URLAllowlist nur 127.0.0.1/localhost:8080 (Kacheln) und :8081 (Admin am Terminal), kein Passwortmanager, keine Downloads, root 644 (Web-Kacheln ergänzt der Helfer beim Start)"
else
	bad chromium-richtlinie "/etc/chromium/policies/managed/hmi-kiosk.json fehlt oder weicht ab (URLBlocklist [\"*\"], URLAllowlist nur Kachelseite und Admin-Seite am Terminal auf Loopback)"
fi

# Dienste
msg=""
for u in $UNITS; do
	[ -f "$S/$u" ] || msg="$msg; $u fehlt"
	enabled_in multi-user.target "$u" || msg="$msg; $u nicht aktiviert"
done
k=hmi-kiosk.service
for l in User=hmikiosk Type=notify WatchdogSec=30s Restart=always RestartSec=2 StartLimitIntervalSec=0 \
	RequiresMountsFor=/data RuntimeDirectory=hmi-kiosk RuntimeDirectoryPreserve=yes KillMode=control-group \
	"ExecStart=/data/app/current/hmi-kiosk -data /data -run /run/hmi-kiosk -helper-socket /run/hmi-kiosk-helper/helper.sock" \
	"Environment=XDG_RUNTIME_DIR=/run/user/$KIOSK_UID"; do
	unit_has "$k" "$l" || msg="$msg; $k ohne $l"
done
if grep -Eq '^(PartOf|BindsTo)=.*hmi-kiosk-helper' "$S/$k"; then msg="$msg; $k an den Helfer gebunden"; fi
h=hmi-kiosk-helper.service
if grep -q '^User=' "$S/$h"; then msg="$msg; $h nicht als root"; fi
if grep -Eq '^(PartOf|BindsTo)=.*hmi-kiosk\.service' "$S/$h"; then msg="$msg; $h an hmi-kiosk.service gebunden"; fi
unit_has "$h" "ExecStart=/usr/local/bin/hmi-kiosk-helper -uid $KIOSK_UID -gid $KIOSK_UID -socket /run/hmi-kiosk-helper/helper.sock" ||
	msg="$msg; $h ohne -uid/-gid $KIOSK_UID"
unit_has "$h" RuntimeDirectoryMode=0750 || msg="$msg; $h ohne RuntimeDirectoryMode=0750"
unit_has "$h" "ExecStartPre=-/usr/local/bin/hmi-kiosk-helper web-policy" || msg="$msg; $h schreibt die Web-Richtlinie nicht vor dem Dienst (ExecStartPre web-policy)"
unit_has hmi-kiosk-update-guard.service Type=oneshot || msg="$msg; Update-Wächter nicht oneshot"
masked getty@tty1.service || msg="$msg; getty@tty1.service nicht maskiert"
[ -x "$R/usr/local/bin/hmi-kiosk-session" ] || msg="$msg; /usr/local/bin/hmi-kiosk-session fehlt"
[ -f "$R/etc/pam.d/hmi-kiosk-session" ] || msg="$msg; /etc/pam.d/hmi-kiosk-session fehlt"
[ -e "$R/var/lib/systemd/linger/hmikiosk" ] || msg="$msg; linger für hmikiosk fehlt"
[ -f "$R/usr/share/icons/hmi-blank/cursors/default" ] || msg="$msg; Zeigerthema hmi-blank fehlt"
if [ -z "$msg" ]; then ok dienste "$UNITS vorhanden und aktiviert; Dienst als hmikiosk, Helfer als root"; else bad dienste "${msg#; }"; fi

# Hardware-Watchdog
if grep -qx 'RuntimeWatchdogSec=15s' "$R/etc/systemd/system.conf.d/watchdog.conf" 2>/dev/null; then
	ok watchdog "RuntimeWatchdogSec=15s"
else
	bad watchdog "RuntimeWatchdogSec=15s fehlt in system.conf.d/watchdog.conf"
fi

# SSH nur, wenn beim Bau verlangt
ssh_on=0
if enabled_in multi-user.target ssh.service || enabled_in multi-user.target sshd.service; then ssh_on=1; fi
if [ "$ssh_on" = "${HMI_ENABLE_SSH:-0}" ]; then
	ok ssh "$([ "$ssh_on" = 1 ] && echo an || echo aus)"
else
	bad ssh "SSH ist $([ "$ssh_on" = 1 ] && echo an || echo aus), erwartet HMI_ENABLE_SSH=${HMI_ENABLE_SSH:-0}"
fi

is_arm64_elf() { [ "$(od -A n -t x1 -j 18 -N 2 "$1" 2>/dev/null | tr -d ' ')" = b700 ]; }

# Helfer: root gehört er, nur root darf schreiben
hp=$R/usr/local/bin/hmi-kiosk-helper
if [ -f "$hp" ]; then
	owner=$(stat -c %u:%g "$hp")
	mode=$(stat -c %a "$hp")
	if [ "$owner" = 0:0 ] && [ $((0$mode & 022)) = 0 ] && [ -x "$hp" ] && is_arm64_elf "$hp"; then
		ok helfer "/usr/local/bin/hmi-kiosk-helper root:root $mode, arm64"
	else
		bad helfer "Besitzer $owner, Modus $mode (erwartet root:root, für andere nicht schreibbar, arm64)"
	fi
else
	bad helfer "/usr/local/bin/hmi-kiosk-helper fehlt"
fi

# Kennzeichen und erste Programmversion
rf=$R/etc/hmi-kiosk/image-release
version=""
if [ -f "$rf" ]; then
	version=$(sed -n 's/^version=//p' "$rf")
	lines=$(grep -c . "$rf")
	if [ "$(stat -c %u:%g:%a "$rf")" = 0:0:644 ] && [ "$lines" = 1 ] && [ -n "$version" ] &&
		{ [ -z "$want_version" ] || [ "$version" = "$want_version" ]; }; then
		ok image-release "version=$version, root:root 644"
	else
		bad image-release "Inhalt oder Rechte falsch: $(stat -c %u:%g:%a "$rf"), $(tr '\n' ' ' <"$rf")${want_version:+(erwartet version=$want_version)}"
	fi
else
	bad image-release "/etc/hmi-kiosk/image-release fehlt"
fi
iv=$R/opt/hmi-kiosk/initial/$version
if [ -n "$version" ] && [ -x "$iv/hmi-kiosk" ] && is_arm64_elf "$iv/hmi-kiosk" &&
	[ "$(cat "$iv/VERSION" 2>/dev/null)" = "$version" ]; then
	ok erste-version "/opt/hmi-kiosk/initial/$version/hmi-kiosk (arm64)"
else
	bad erste-version "/opt/hmi-kiosk/initial/$version/hmi-kiosk oder VERSION fehlt oder passt nicht"
fi

if [ -n "$package_list" ]; then
	dpkg-query --admindir="$R/var/lib/dpkg" -W -f='${db:Status-Abbrev}\t${Package}\t${Version}\t${Architecture}\n' |
		awk -F'\t' '$1 ~ /^ii/ {print $2 "\t" $3 "\t" $4}' | sort >"$package_list"
	echo "Paketliste: $package_list ($(wc -l <"$package_list") Pakete)"
fi

if [ "$fails" -gt 0 ]; then
	echo "$fails Prüfung(en) gescheitert."
	exit 1
fi
echo "Alle Prüfungen bestanden."
