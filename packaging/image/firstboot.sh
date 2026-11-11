#!/bin/sh
# Erster Start des SD-Abbilds, im Abbild als /usr/local/sbin/hmi-kiosk-firstboot.
#
#   hmi-kiosk-firstboot prepare-data  legt /data (ext4, Label HMIDATA) im freien Rest der Karte an,
#                                     trägt es in /etc/fstab ein und hängt es ein. Läuft bei jedem
#                                     Start vor local-fs.target und endet sofort, wenn alles da ist.
#   hmi-kiosk-firstboot finish        richtet /data ein, kopiert die erste Programmversion,
#                                     schaltet Overlay und schreibgeschützte Boot-Partition ein
#                                     und startet neu. Läuft, bis die Erledigt-Marke steht.
#   hmi-kiosk-firstboot grow-data     vergrößert Partition 3 und /data auf die ganze Karte, wenn das
#                                     Abbild von einer kleineren Karte stammt (kompakte SD-Sicherung).
#                                     Läuft bei jedem Start, ändert sonst nichts und endet mit 0.
#
# Die Erledigt-Marke liegt in /etc/hmi-kiosk (root, untere Ebene des Overlays), nicht auf /data:
# /data gehört dem Dienst, und eine dort löschbare Marke ließe finish als root erneut in einem
# Ordner laufen, den der Dienst vorbereitet hat (Symlinks). finish folgt deshalb auch keinem Link.
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
DONE_MARKER=/etc/hmi-kiosk/firstboot-done
RELEASE_FILE=/etc/hmi-kiosk/image-release
PRODUCT_FILE=${HMI_PRODUCT_FILE:-/etc/hmi-kiosk/product}
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

# Hängt das neue Dateisystem probeweise ein, schreibt und liest eine Testdatei. Erst danach darf
# die fstab-Zeile entstehen: Ohne nofail führt ein nicht einhängbares /data sonst bei jedem
# Start in den Notfallmodus.
probe_mount() {
	dev=$(blkid -L "$LABEL" 2>/dev/null) || return 1
	probe=$(mktemp -d /run/hmi-kiosk-firstboot.XXXXXX) || return 1
	ok=1
	if mount -t ext4 "$dev" "$probe"; then
		if printf 'hmi-kiosk\n' >"$probe/.hmi-kiosk-probe" && sync &&
			[ "$(cat "$probe/.hmi-kiosk-probe")" = hmi-kiosk ] && rm -f "$probe/.hmi-kiosk-probe"; then
			ok=0
		fi
		umount "$probe" || ok=1
	fi
	rmdir "$probe" 2>/dev/null || true
	return "$ok"
}

# Entfernt genau die eigene Zeile wieder; die übrige fstab bleibt unverändert (atomar ersetzt).
# Jeder Fehler wird gemeldet: Bliebe die Zeile stehen, endete der nächste Start im Notfallmodus.
remove_fstab_entry() {
	tmp="$FSTAB.hmi-kiosk.$$"
	rc=0
	grep -vxF "$FSTAB_LINE" "$FSTAB" >"$tmp" || rc=$?
	# grep endet mit 1, wenn keine Zeile übrig bleibt; erst ab 2 ist es ein Fehler.
	if [ "$rc" -gt 1 ]; then
		rm -f "$tmp"
		fail "$FSTAB nicht lesbar oder Kopie nicht schreibbar; die Zeile für $DATA steht noch in $FSTAB und muss von Hand entfernt werden"
	fi
	chmod --reference="$FSTAB" "$tmp" 2>/dev/null || chmod 0644 "$tmp"
	sync
	if ! mv -f "$tmp" "$FSTAB"; then
		rm -f "$tmp"
		fail "$FSTAB nicht ersetzbar; die Zeile für $DATA steht noch in $FSTAB und muss von Hand entfernt werden"
	fi
	sync
}

# Trägt /data in die fstab ein (einmal) und hängt es über systemd ein.
add_fstab_entry() {
	if grep -Eq "^[^#]*[[:space:]]${DATA}[[:space:]]" "$FSTAB"; then
		return 0
	fi
	probe_mount || fail "$LABEL lässt sich nicht probeweise einhängen und beschreiben; kein fstab-Eintrag"
	printf '%s\n' "$FSTAB_LINE" >>"$FSTAB"
	sync
	log "fstab-Eintrag für $DATA ergänzt"
	if systemctl daemon-reload && systemctl start data.mount; then
		log "$DATA eingehängt"
		return 0
	fi
	# Ohne diesen Rückbau stünde beim nächsten Start eine Zeile ohne nofail für ein
	# nicht einhängbares /data in der fstab.
	remove_fstab_entry
	systemctl daemon-reload || true
	fail "$DATA lässt sich über data.mount nicht einhängen; fstab-Eintrag wieder entfernt"
}

prepare_data() {
	create_partition
	add_fstab_entry
}

# Bei einer Karte, die größer ist als die der Sicherung (kompaktes Abbild, kleine Partition 3):
# Partition 3 (HMIDATA) bis zum Ende des Geräts vergrößern und das Dateisystem online anpassen.
# Läuft bei jedem Start vor dem Helfer und dem Dienst; ist alles schon groß genug, ändert sich
# nichts. Fehler werden nur protokolliert, das Gerät startet trotzdem. resize2fs läuft auch dann,
# wenn die Partition schon am Ende steht: so holt der nächste Start einen gescheiterten Schritt nach.
grow_data() {
	# Das Gerät kommt aus dem Einhängepunkt (die Unit hängt an data.mount), nicht aus blkid -L:
	# dessen Zwischenspeicher kann eine alte Beschriftung melden. blkid -p liest das Gerät selbst.
	dev=$(findmnt -n -o SOURCE "$DATA" 2>/dev/null) || dev=""
	case "$dev" in
	/dev/*) ;;
	*)
		log "Keine Datenpartition unter $DATA eingehängt; nichts zu vergrößern"
		return 0
		;;
	esac
	fstype=$(blkid -p -o value -s TYPE "$dev" 2>/dev/null || true)
	label=$(blkid -p -o value -s LABEL "$dev" 2>/dev/null || true)
	[ "$fstype" = ext4 ] && [ "$label" = "$LABEL" ] || {
		log "$dev ist nicht $LABEL (ext4, Label $label); nichts zu vergrößern"
		return 0
	}
	name=$(basename "$(readlink -f "$dev")")
	nr=$(cat "/sys/class/block/$name/partition" 2>/dev/null) || fail "$dev ist keine Partition"
	[ "$nr" = 3 ] || {
		log "$dev ist Partition $nr statt 3; keine Vergrößerung"
		return 0
	}
	disk=/dev/$(basename "$(readlink -f "/sys/class/block/$name/..")")
	total=$(blockdev --getsz "$disk") || fail "Größe von $disk nicht ermittelbar"
	start=$(cat "/sys/class/block/$name/start")
	size=$(cat "/sys/class/block/$name/size")
	# Sektoren zu 512 Byte; höchstens 16 MiB frei dahinter gelten als "am Ende".
	if [ $((total - start - size)) -le 32768 ]; then
		log "Partition 3 reicht schon bis zum Ende von $disk"
	else
		log "Vergrößere $dev ($size Sektoren) bis zum Ende von $disk ($total Sektoren)"
		echo ', +' | sfdisk -N 3 --no-reread --quiet "$disk" || fail "Partition 3 auf $disk nicht vergrößerbar"
		partx -u -n 3 "$disk" || fail "Kernel kennt die neue Größe von $dev nicht"
		if command -v udevadm >/dev/null 2>&1; then
			udevadm settle --timeout=10 || true
		fi
	fi
	resize2fs "$dev" || fail "resize2fs auf $dev gescheitert"
	log "Datenpartition $dev: $(cat "/sys/class/block/$name/size") Sektoren"
}

image_version() {
	v=$(sed -n 's/^version=//p' "$RELEASE_FILE" 2>/dev/null | head -n 1)
	[ -n "$v" ] || fail "Keine Version in $RELEASE_FILE"
	echo "$v"
}

# safe_dir <pfad> <besitzer...>: Abbruch, wenn der Pfad ein symbolischer Link ist oder, falls
# vorhanden, kein Ordner eines der genannten Besitzer. finish läuft als root in /data, das dem
# Dienst gehört; ein vorbereiteter Link darf nie verfolgt werden (chown, install, printf).
safe_dir() {
	p=$1
	shift
	[ ! -L "$p" ] || fail "$p ist ein symbolischer Link; Erststart abgebrochen, es wird nichts verändert"
	[ -e "$p" ] || return 0
	[ -d "$p" ] || fail "$p ist kein Ordner; Erststart abgebrochen"
	o=$(stat -c %U "$p")
	for want in "$@"; do
		[ "$o" != "$want" ] || return 0
	done
	fail "$p gehört $o statt $*; Erststart abgebrochen"
}

# Programmname des Produkts: Inhalt von /etc/hmi-kiosk/product (gleich der Produkt-ID, auch gleich dem
# Programmnamen); fehlt die Datei (ältere Abbilder), gilt hmi-kiosk.
product_binary() {
	bin=$(head -n 1 "$PRODUCT_FILE" 2>/dev/null || true)
	case "$bin" in
	"") bin=hmi-kiosk ;;
	*[!a-z0-9-]* | -*) fail "Ungültiges Produkt in $PRODUCT_FILE: $bin" ;;
	esac
}

install_initial_version() {
	v=$(image_version)
	product_binary
	src=$INITIAL_DIR/$v
	app=$DATA/app
	[ -x "$src/$bin" ] || fail "Erste Programmversion fehlt: $src/$bin"
	safe_dir "$app" "$KIOSK_USER"
	[ -d "$app" ] || install -d -o "$KIOSK_USER" -g "$KIOSK_USER" -m 0755 "$app"
	safe_dir "$app" "$KIOSK_USER"
	safe_dir "$app/$v" "$KIOSK_USER"
	if [ -x "$app/$v/$bin" ] && [ ! -L "$app/$v/$bin" ] && cmp -s "$src/$bin" "$app/$v/$bin"; then
		log "Version $v liegt schon in $app"
	else
		tmp=$app/.tmp-firstboot-$v
		safe_dir "$tmp" root "$KIOSK_USER"
		rm -rf "$tmp"
		# Erst als root befüllen, dann übergeben: Solange root hineinschreibt, gehört der
		# Ordner nicht dem Dienst. chown -h ändert nur den Eintrag selbst.
		install -d -o root -g root -m 0755 "$tmp"
		safe_dir "$tmp" root
		install -o root -g root -m 0755 "$src/$bin" "$tmp/$bin"
		printf '%s\n' "$v" >"$tmp/VERSION"
		chmod 0644 "$tmp/VERSION"
		chown -h "$KIOSK_USER:$KIOSK_USER" "$tmp/$bin" "$tmp/VERSION" "$tmp"
		sync
		rm -rf "${app:?}/$v"
		mv -T "$tmp" "$app/$v"
		log "Version $v nach $app/$v kopiert"
	fi
	if [ -L "$app/current" ]; then
		log "current zeigt schon auf $(readlink "$app/current"), bleibt so"
	else
		[ ! -e "$app/current" ] || fail "$app/current ist kein symbolischer Link; Erststart abgebrochen"
		ln -sfn "$v" "$app/.current-firstboot"
		chown -h "$KIOSK_USER:$KIOSK_USER" "$app/.current-firstboot"
		mv -T "$app/.current-firstboot" "$app/current"
		log "current zeigt auf $v"
	fi
	sync
}

# fsck.repair=yes: systemd-fsck repariert /data (Pass 2) beim Start ohne Rückfrage; ohne diese
# Angabe bliebe ein beschädigtes Dateisystem im Notfallmodus stehen, an den niemand herankommt.
ensure_fsck_repair() {
	if grep -Eq '(^|[[:space:]])fsck\.repair=yes([[:space:]]|$)' "$CMDLINE"; then
		return 0
	fi
	sed -i 's/[[:space:]]*fsck\.repair=[^[:space:]]*//g; s/$/ fsck.repair=yes/' "$CMDLINE"
	grep -Eq '(^|[[:space:]])fsck\.repair=yes([[:space:]]|$)' "$CMDLINE" || fail "fsck.repair=yes fehlt in $CMDLINE"
	log "fsck.repair=yes in $CMDLINE ergänzt"
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
	[ ! -L "$DATA" ] || fail "$DATA ist ein symbolischer Link; Erststart abgebrochen"
	chown "$KIOSK_USER:$KIOSK_USER" "$DATA"
	chmod 0750 "$DATA"
	# Der Helfer schreibt hier den NTP-Stand; der Dienst darf ihn nicht ändern.
	safe_dir "$DATA/system" root
	[ -d "$DATA/system" ] || install -d -o root -g root -m 0755 "$DATA/system"
	safe_dir "$DATA/system" root
	install_initial_version
	ensure_fsck_repair
	enable_overlay
	# Die Marke entsteht in der unteren Ebene, solange die Wurzel noch beschreibbar ist; mit
	# dem Overlay ab dem nächsten Start kann sie niemand mehr dauerhaft löschen.
	install -d -o root -g root -m 0755 "$(dirname "$DONE_MARKER")"
	date '+%Y-%m-%d %H:%M:%S' >"$DONE_MARKER.tmp"
	mv -f "$DONE_MARKER.tmp" "$DONE_MARKER"
	sync
	log "Erster Start erledigt, Neustart mit Overlay"
	systemctl --no-block reboot
}

case "${1:-}" in
prepare-data) prepare_data ;;
finish) finish ;;
grow-data)
	# Jeder Fehler endet hier: das Gerät soll mit der kleinen Partition trotzdem starten.
	(grow_data) || log "Die Datenpartition wurde nicht vergrößert; das Gerät startet trotzdem"
	;;
*)
	echo "Aufruf: hmi-kiosk-firstboot prepare-data|finish|grow-data" >&2
	exit 2
	;;
esac
