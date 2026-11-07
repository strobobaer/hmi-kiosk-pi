#!/bin/sh
# hmi-kiosk-identity: Netzwerk-Identität (Maschinen-ID, Rechnername) an das Gerät binden.
# Läuft als root bei jedem Start (hmi-kiosk-identity.service), nach /data und vor dem Netzwerk.
#
# Hintergrund: Wird die SD-Karte auf ein zweites Gerät übertragen, tragen beide dieselbe
# /etc/machine-id (daraus bildet der DHCP-Client seine Kennung) und denselben Rechnernamen; zwei
# Terminals würden sich im Netz um dieselbe Adresse streiten. Das Overlay auf / hält /etc nur im RAM,
# darum steht die Identität dauerhaft in /data/system/identity (Zeilen serial=, machine_id=, hostname=):
#   - keine Datei (oder eine kaputte, root gehörende, etwa nach Stromausfall):
#                            Originalgerät, aktuelle Werte festhalten, nichts ändern (der Rechnername
#                            wird nie angefasst; ist er ungültig, bleibt hostname= in der Datei leer)
#   - gleiche Seriennummer:  Werte aus der Datei setzen, falls abweichend
#   - andere Seriennummer:   Klon, neue Maschinen-ID und Rechnername hmi-kiosk-<Ende der Seriennummer>
# Frühe Verbraucher (PID 1, journald) behalten die alte Maschinen-ID bis zum nächsten Start;
# der DHCP-Client und die Netzwerkdienste starten erst nach dieser Unit und sehen die neue.
# Ob das Terminal als Ersatz oder als neues Terminal weiterläuft, entscheidet der Dienst
# (Wartungsmodus in der Admin-Seite), nicht dieses Skript.
#
# /data gehört dem Dienstbenutzer: /data/system muss ein echter Ordner sein, root gehören und für
# niemanden sonst schreibbar sein, die Datei darin ebenso; Verweise werden nie verfolgt. Im Zweifel
# geschieht nichts, es steht nur im Protokoll. Das Skript endet immer mit 0, damit kein Fehler den
# Start aufhält.
#
# Pfade überschreibbar für den Test (test-identity.sh).
PROC=${HMI_IDENTITY_PROC:-/proc}
SYS=${HMI_IDENTITY_SYS:-/sys}
DIR=${HMI_IDENTITY_DIR:-/data/system}
MACHINE_ID_FILE=${HMI_IDENTITY_MACHINE_ID:-/etc/machine-id}
HOSTNAME_FILE=${HMI_IDENTITY_HOSTNAME:-/etc/hostname}
HOSTS_FILE=${HMI_IDENTITY_HOSTS:-/etc/hosts}
NAME=identity
MAX_BYTES=4096

log() { echo "hmi-kiosk-identity: $*"; }

# read_serial gibt die Hardware-Kennung aus (wie internal/platform.HardwareID): Seriennummer des
# Raspberry Pi, sonst DMI-Produkt-UUID; klein geschrieben, nur [0-9a-z-], höchstens 64 Zeichen.
read_serial() {
	for f in "$PROC/device-tree/serial-number" "$SYS/class/dmi/id/product_uuid"; do
		if [ -L "$f" ] || [ ! -f "$f" ]; then continue; fi
		v=$(head -c 128 "$f" 2>/dev/null | tr -d '\000' | tr 'A-Z' 'a-z' | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
		case $v in
		'' | *[!0-9a-z-]*) continue ;;
		esac
		if [ "${#v}" -gt 64 ] || [ -z "$(printf '%s' "$v" | tr -d '0-')" ]; then continue; fi
		printf '%s' "$v"
		return 0
	done
	return 1
}

valid_machine_id() {
	case $1 in
	*[!0-9a-f]*) return 1 ;;
	esac
	[ "${#1}" -eq 32 ] && [ -n "$(printf '%s' "$1" | tr -d '0')" ]
}

valid_hostname() {
	case $1 in
	'' | -* | *[!0-9a-z-]*) return 1 ;;
	esac
	[ "${#1}" -le 63 ]
}

new_machine_id() {
	od -An -N16 -tx1 /dev/urandom 2>/dev/null | tr -d ' \n'
}

# clone_hostname: hmi-kiosk-<die letzten 6 Zeichen der Kennung ohne Bindestriche>
clone_hostname() {
	s=$(printf '%s' "$1" | tr -d '-')
	if [ "${#s}" -gt 6 ]; then s=${s#"${s%??????}"}; fi
	printf 'hmi-kiosk-%s' "$s"
}

# enter_dir wechselt in den Ordner und prüft ihn dort: Alle späteren Zugriffe gelten dem
# geöffneten Ordner, nicht mehr dem Pfad, den der Dienstbenutzer tauschen könnte.
enter_dir() {
	if [ -L "$DIR" ]; then
		log "$DIR ist ein Verweis, nichts getan"
		return 1
	fi
	if [ ! -e "$DIR" ]; then
		mkdir -m 755 "$DIR" 2>/dev/null || {
			log "$DIR nicht anlegbar, nichts getan"
			return 1
		}
	fi
	parent=$(cd -P "$(dirname "$DIR")" 2>/dev/null && pwd -P) || {
		log "Elternordner von $DIR nicht erreichbar, nichts getan"
		return 1
	}
	cd -P "$DIR" 2>/dev/null || {
		log "$DIR kein Ordner, nichts getan"
		return 1
	}
	if [ "$(pwd -P)" != "$parent/$(basename "$DIR")" ]; then
		log "$DIR ist kein echter Ordner (Verweis), nichts getan"
		return 1
	fi
	if [ "$(stat -c %u .)" != 0 ] || [ $((0$(stat -c %a .) & 0022)) -ne 0 ]; then
		log "$DIR gehört nicht root oder ist für andere schreibbar, nichts getan"
		return 1
	fi
}

# read_identity liest serial, machine_id und hostname aus der Datei. 0: gültig, 1: fehlt, 2: unsicher
# (Verweis, fremder Besitzer, Rechte, Größe: nichts tun), 3: root-eigen, aber Inhalt kaputt (wie fehlend).
read_identity() {
	if [ -L "$NAME" ]; then
		log "$DIR/$NAME ist ein Verweis, nichts getan"
		return 2
	fi
	[ -e "$NAME" ] || return 1
	if [ ! -f "$NAME" ] || [ "$(stat -c %u "$NAME")" != 0 ] || [ $((0$(stat -c %a "$NAME") & 0022)) -ne 0 ] ||
		[ "$(stat -c %s "$NAME")" -gt "$MAX_BYTES" ]; then
		log "$DIR/$NAME ist keine passende Datei (Art, Besitzer, Rechte oder Größe), nichts getan"
		return 2
	fi
	f_serial=$(sed -n 's/^serial=//p' "$NAME" | head -n 1)
	f_mid=$(sed -n 's/^machine_id=//p' "$NAME" | head -n 1)
	f_host=$(sed -n 's/^hostname=//p' "$NAME" | head -n 1)
	if [ -z "$f_serial" ] || ! valid_machine_id "$f_mid" || { [ -n "$f_host" ] && ! valid_hostname "$f_host"; }; then
		log "$DIR/$NAME ist unvollständig oder ungültig, wird wie beim ersten Start neu geschrieben"
		return 3
	fi
	return 0
}

# write_identity ersetzt die Datei atomar im geprüften Ordner.
write_identity() {
	tmp=.$NAME.tmp$$
	(
		umask 022
		set -C
		printf 'serial=%s\nmachine_id=%s\nhostname=%s\n' "$1" "$2" "$3" >"$tmp"
	) && chmod 644 "$tmp" && { sync "$tmp" 2>/dev/null || sync; } && mv -f "$tmp" "$NAME" || {
		rm -f "$tmp"
		log "$DIR/$NAME nicht geschrieben"
		return 1
	}
}

current_hostname() {
	h=$(head -n 1 "$HOSTNAME_FILE" 2>/dev/null)
	if ! valid_hostname "$h"; then h=$(hostname 2>/dev/null); fi
	printf '%s' "$h"
}

# apply setzt Maschinen-ID und Rechnername, soweit sie abweichen.
apply() {
	if [ "$(head -n 1 "$MACHINE_ID_FILE" 2>/dev/null)" != "$1" ]; then
		if printf '%s\n' "$1" >"$MACHINE_ID_FILE" 2>/dev/null; then
			log "Maschinen-ID gesetzt"
		else
			log "$MACHINE_ID_FILE nicht beschreibbar"
		fi
	fi
	if [ -n "$2" ] && [ "$(head -n 1 "$HOSTNAME_FILE" 2>/dev/null)" != "$2" ]; then
		old=$(current_hostname)
		if printf '%s\n' "$2" >"$HOSTNAME_FILE" 2>/dev/null; then
			if [ -n "$old" ] && [ -f "$HOSTS_FILE" ] && ! [ -L "$HOSTS_FILE" ]; then
				sed -i "s/^\\(127\\.0\\.1\\.1[[:space:]][[:space:]]*\\).*/\\1$2/" "$HOSTS_FILE" 2>/dev/null || true
			fi
		else
			log "$HOSTNAME_FILE nicht beschreibbar"
		fi
	fi
	if [ -n "$2" ] && [ "$(hostname 2>/dev/null)" != "$2" ]; then
		hostname "$2" 2>/dev/null || log "Rechnername nicht gesetzt"
	fi
	return 0
}

main() {
	serial=$(read_serial) || {
		log "Keine Hardware-Kennung lesbar, nichts getan"
		return 0
	}
	enter_dir || return 0
	read_identity
	case $? in
	2) return 0 ;;
	1 | 3)
		mid=$(head -n 1 "$MACHINE_ID_FILE" 2>/dev/null)
		if ! valid_machine_id "$mid"; then mid=$(new_machine_id); fi
		host=$(current_hostname)
		if ! valid_hostname "$host"; then host=; fi
		if ! valid_machine_id "$mid"; then
			log "Keine Maschinen-ID erzeugbar, nichts getan"
			return 0
		fi
		write_identity "$serial" "$mid" "$host" || return 0
		log "Identität festgehalten (${host:-ohne Rechnername})"
		apply "$mid" ""
		;;
	0)
		if [ "$f_serial" = "$serial" ]; then
			apply "$f_mid" "$f_host"
			return 0
		fi
		mid=$(new_machine_id)
		if ! valid_machine_id "$mid"; then
			log "Keine Maschinen-ID erzeugbar, nichts getan"
			return 0
		fi
		host=$(clone_hostname "$serial")
		log "Anderes Gerät als eingerichtet (Klon): neue Maschinen-ID, Rechnername $host"
		write_identity "$serial" "$mid" "$host" || return 0
		apply "$mid" "$host"
		;;
	esac
	return 0
}

main
exit 0
