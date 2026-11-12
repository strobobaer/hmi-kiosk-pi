#!/bin/sh
# I²C-Prüfung für das PDH-Terminal (Kartenleser PN532), eingebunden von verify-image.sh und
# von packaging/test-products.sh. Erwartet die Funktionen ok und bad des Aufrufers.
#
#   check_i2c <produkt> <wurzel> <boot>
#     wurzel  eingehängtes Systemdateisystem, boot  Boot-Partition (config.txt)
# hmi-kiosk braucht weder i2c_arm noch die Gruppe noch die Moduldatei und wird nicht geprüft.

check_i2c() {
	[ "$1" = pdh-terminal ] || return 0
	_i2c_msg=""
	_i2c_final=$(awk '/^\[/ { sec = $0; blk = ""; next } { blk = blk $0 "\n" } END { if (sec == "" || sec == "[all]") printf "%s", blk }' "$3/config.txt")
	if ! printf '%s\n' "$_i2c_final" | grep -Eq '^dtparam=i2c_arm=on[[:space:]]*$'; then
		_i2c_msg="$_i2c_msg; dtparam=i2c_arm=on fehlt im letzten [all]-Abschnitt von config.txt"
	fi
	grep -Eq '^i2c:[^:]*:[0-9]+:([^,]*,)*hmikiosk(,|$)' "$2/etc/group" || _i2c_msg="$_i2c_msg; hmikiosk nicht in Gruppe i2c"
	[ "$(cat "$2/etc/modules-load.d/pdh-terminal-i2c.conf" 2>/dev/null)" = i2c-dev ] ||
		_i2c_msg="$_i2c_msg; /etc/modules-load.d/pdh-terminal-i2c.conf (i2c-dev) fehlt"
	if [ -z "$_i2c_msg" ]; then
		ok kartenleser-i2c "i2c_arm im letzten [all], hmikiosk in Gruppe i2c, i2c-dev wird geladen"
	else
		bad kartenleser-i2c "${_i2c_msg#; }"
	fi
}
