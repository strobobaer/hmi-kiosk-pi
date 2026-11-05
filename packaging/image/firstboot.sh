#!/bin/sh
# Erster Start des SD-Abbilds, im Abbild als /usr/local/sbin/hmi-kiosk-firstboot.
#
#   hmi-kiosk-firstboot prepare-data  legt /data (ext4, Label HMIDATA) im freien Rest der Karte an,
#                                     trägt es in /etc/fstab ein und hängt es ein. Läuft bei jedem
#                                     Start vor local-fs.target und endet sofort, wenn alles da ist.
#   hmi-kiosk-firstboot finish        richtet /data ein, kopiert die erste Programmversion,
#                                     schaltet Overlay und schreibgeschützte Boot-Partition ein
#                                     und startet neu. Läuft, bis die Erledigt-Marke auf /data steht.
#
# Das Abbild enthält keinen fstab-Eintrag für /data: Ohne nofail müsste die Partition sonst schon
# innerhalb der Gerätefrist da sein, sonst Notfallmodus. Der Eintrag entsteht erst hier, solange
# die Wurzel noch beschreibbar ist (vor dem Overlay).
#
# Jeder Schritt prüft zuerst den Zustand; ein abgebrochener Lauf wird beim nächsten Start fortgesetzt.
# Protokoll: Journal (stdout) und /boot/firmware/hmi-kiosk-firstboot.log.
set -eu

LABEL=HMIDATA
DATA=/data
DONE_MARKER=$DATA/.hmi-kiosk-firstboot-done
RELEASE_FILE=/etc/hmi-kiosk/image-release
INITIAL_DIR=/opt/hmi-kiosk/initial
CMDLINE=/boot/firmware/cmdline.txt
FSTAB=/etc/fstab
FSTAB_LINE="LABEL=HMIDATA  /data           ext4    defaults,noatime,x-systemd.device-timeout=30s  0       2"
BOOT_LOG=${HMI_FIRSTBOOT_LOG:-/boot/firmware/hmi-kiosk-firstboot.log}
KIOSK_USER=hmikiosk

log() {
	echo "$*"
	printf '%s %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$*" >>"$BOOT_LOG" 2>/dev/null || true
}

fail() {
	log "FEHLER: $*"
	exit 1
}

# Partitionsgerät zu Datenträger und Nummer: mmcblk0 -> mmcblk0p3, sda -> sda3.
part_dev() {
	case "$1" in
	*[0-9]) echo "${1}p$2" ;;
	*) echo "$1$2" ;;
	esac
}

create_partition() {
	if dev=$(blkid -L "$LABEL" 2>/dev/null); then
		log "Datenpartition vorhanden: $dev"
		return 0
	fi
	# HMI_FIRSTBOOT_ROOT nur für den Test im Container (Loop-Gerät statt SD-Karte).
	root=${HMI_FIRSTBOOT_ROOT:-$(findmnt -n -o SOURCE /)}
	case "$root" in
	/dev/*) ;;
	*) fail "Wurzelpartition nicht ermittelbar ($root); Datenpartition fehlt" ;;
	esac
	name=$(basename "$root")
	nr=$(cat "/sys/class/block/$name/partition" 2>/dev/null) || fail "$root ist keine Partition"
	[ "$nr" = 2 ] || fail "Wurzel ist Partition $nr statt 2; Datenpartition wird nicht angelegt"
	disk=/dev/$(basename "$(readlink -f "/sys/class/block/$name/..")")
	data=$(part_dev "$disk" 3)

	if sfdisk --dump "$disk" | grep -q "^${data}[[:space:]]"; then
		log "Partition $data steht schon in der Partitionstabelle"
	else
		# Beginn ausdrücklich hinter der Wurzel (auf 1 MiB gerundet): ohne Angabe nähme sfdisk die
		# erste Lücke, und vor der Boot-Partition ist eine.
		rootend=$(sfdisk --dump "$disk" | sed -n "s|^${root}[[:space:]]*: start= *\([0-9]*\), size= *\([0-9]*\).*|\1 \2|p" |
			awk '{print $1 + $2}')
		[ -n "$rootend" ] || fail "Ende der Wurzelpartition auf $disk nicht ermittelbar"
		start=$(((rootend + 2047) / 2048 * 2048))
		log "Lege $data ab Sektor $start im freien Rest von $disk an"
		echo "start=$start, type=83" | sfdisk --append --no-reread --quiet "$disk" ||
			fail "Partition auf $disk nicht anlegbar"
	fi
	# Die Wurzel ist eingehängt, ein Neueinlesen der ganzen Tabelle scheitert; nur Partition 3 melden.
	if [ ! -e "/sys/class/block/$(basename "$data")" ]; then
		partx --add --nr 3 "$disk" || fail "Kernel kennt $data nicht"
	fi
	if command -v udevadm >/dev/null 2>&1; then
		udevadm settle --timeout=10 || true
	fi
	[ -b "$data" ] || fail "Gerät $data fehlt"

	fstype=$(blkid -p -o value -s TYPE "$data" 2>/dev/null || true)
	if [ -n "$fstype" ]; then
		label=$(blkid -p -o value -s LABEL "$data" 2>/dev/null || true)
		[ "$fstype" = ext4 ] && [ "$label" = "$LABEL" ] && return 0
		fail "$data enthält schon ein Dateisystem ($fstype, Label $label); wird nicht formatiert"
	fi
	log "Formatiere $data als ext4 mit Label $LABEL"
	# nodiscard und verzögerte Initialisierung: sonst löscht mkfs die ganze Karte und dauert Minuten.
	mkfs.ext4 -q -F -L "$LABEL" -m 1 -E nodiscard,lazy_itable_init=1,lazy_journal_init=1 "$data" ||
		fail "mkfs.ext4 auf $data gescheitert"
	log "Datenpartition angelegt: $data"
}

# Trägt /data in die fstab ein (einmal) und hängt es über systemd ein.
add_fstab_entry() {
	if grep -Eq "^[^#]*[[:space:]]${DATA}[[:space:]]" "$FSTAB"; then
		return 0
	fi
	printf '%s\n' "$FSTAB_LINE" >>"$FSTAB"
	sync
	log "fstab-Eintrag für $DATA ergänzt"
	systemctl daemon-reload || fail "systemctl daemon-reload gescheitert"
	systemctl start data.mount || fail "$DATA lässt sich nicht einhängen"
	log "$DATA eingehängt"
}

prepare_data() {
	create_partition
	add_fstab_entry
}

image_version() {
	v=$(sed -n 's/^version=//p' "$RELEASE_FILE" 2>/dev/null | head -n 1)
	[ -n "$v" ] || fail "Keine Version in $RELEASE_FILE"
	echo "$v"
}

install_initial_version() {
	v=$(image_version)
	src=$INITIAL_DIR/$v
	app=$DATA/app
	[ -x "$src/hmi-kiosk" ] || fail "Erste Programmversion fehlt: $src/hmi-kiosk"
	install -d -o "$KIOSK_USER" -g "$KIOSK_USER" -m 0755 "$app"
	if [ -x "$app/$v/hmi-kiosk" ] && cmp -s "$src/hmi-kiosk" "$app/$v/hmi-kiosk"; then
		log "Version $v liegt schon in $app"
	else
		tmp=$app/.tmp-firstboot-$v
		rm -rf "$tmp"
		install -d -o "$KIOSK_USER" -g "$KIOSK_USER" -m 0755 "$tmp"
		install -o "$KIOSK_USER" -g "$KIOSK_USER" -m 0755 "$src/hmi-kiosk" "$tmp/hmi-kiosk"
		printf '%s\n' "$v" >"$tmp/VERSION"
		chown "$KIOSK_USER:$KIOSK_USER" "$tmp/VERSION"
		chmod 0644 "$tmp/VERSION"
		sync
		rm -rf "${app:?}/$v"
		mv "$tmp" "$app/$v"
		log "Version $v nach $app/$v kopiert"
	fi
	if [ -L "$app/current" ]; then
		log "current zeigt schon auf $(readlink "$app/current"), bleibt so"
	else
		ln -sfn "$v" "$app/.current-firstboot"
		chown -h "$KIOSK_USER:$KIOSK_USER" "$app/.current-firstboot"
		mv -T "$app/.current-firstboot" "$app/current"
		log "current zeigt auf $v"
	fi
	sync
}

enable_overlay() {
	if grep -q 'overlayroot=tmpfs:recurse=0' "$CMDLINE"; then
		log "Overlay ist schon eingetragen"
	else
		raspi-config nonint enable_overlayfs || fail "raspi-config enable_overlayfs gescheitert"
		# Ohne recurse=0 legt overlayroot auch /data schreibgeschützt unter ein RAM-Overlay,
		# alle Änderungen dort wären nach dem Neustart weg.
		sed -i 's/overlayroot=tmpfs\([[:space:]]\)/overlayroot=tmpfs:recurse=0\1/' "$CMDLINE"
		grep -q 'overlayroot=tmpfs:recurse=0' "$CMDLINE" ||
			fail "overlayroot=tmpfs:recurse=0 fehlt in $CMDLINE"
		log "Overlay für die Wurzel eingetragen (nur /, /data bleibt beschreibbar)"
	fi
	if grep -E '[[:space:]]/boot/firmware[[:space:]]' "$FSTAB" | grep -q 'defaults,ro'; then
		log "Boot-Partition ist schon schreibgeschützt eingetragen"
	else
		raspi-config nonint enable_bootro || fail "raspi-config enable_bootro gescheitert"
		grep -E '[[:space:]]/boot/firmware[[:space:]]' "$FSTAB" | grep -q 'defaults,ro' ||
			fail "/boot/firmware ist in $FSTAB nicht schreibgeschützt"
		log "Boot-Partition schreibgeschützt eingetragen"
	fi
}

finish() {
	mountpoint -q "$DATA" || fail "$DATA ist nicht eingehängt"
	if [ -e "$DONE_MARKER" ]; then
		log "Erster Start ist schon erledigt"
		return 0
	fi
	log "Erster Start: richte $DATA ein (Abbild $(image_version))"
	chown "$KIOSK_USER:$KIOSK_USER" "$DATA"
	chmod 0750 "$DATA"
	# Der Helfer schreibt hier den NTP-Stand; der Dienst darf ihn nicht ändern.
	install -d -o root -g root -m 0755 "$DATA/system"
	install_initial_version
	enable_overlay
	date '+%Y-%m-%d %H:%M:%S' >"$DONE_MARKER"
	sync
	log "Erster Start erledigt, Neustart mit Overlay"
	systemctl --no-block reboot
}

case "${1:-}" in
prepare-data) prepare_data ;;
finish) finish ;;
*)
	echo "Aufruf: hmi-kiosk-firstboot prepare-data|finish" >&2
	exit 2
	;;
esac
